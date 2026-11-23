package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/wincent/tasks/internal/cli"
	"github.com/wincent/tasks/internal/process"
)

func main() {
	// A viewer piped into an early-closing consumer must observe EPIPE and
	// finish normally, rather than die in the runtime's stdout SIGPIPE path.
	signal.Ignore(syscall.SIGPIPE)
	args := os.Args[1:]
	if len(args) == 1 && args[0] == "--version" {
		args = []string{"version"}
	}

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	var received atomic.Int32
	go func() {
		select {
		case value := <-signals:
			received.Store(int32(value.(syscall.Signal)))
			cancel()
		case <-base.Done():
		}
	}()
	ctx := process.WithCancellationSignal(base, func() syscall.Signal { return syscall.Signal(received.Load()) })
	var cwd string
	var err error
	// Version reporting does not need a working directory.
	if len(args) != 1 || args[0] != "version" {
		cwd, err = os.Getwd()
	}
	if err == nil {
		err = cli.Run(ctx, cwd, args, cli.IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
	}
	if received.Load() != 0 {
		value := syscall.Signal(received.Load())
		signal.Reset(value)
		if killErr := syscall.Kill(os.Getpid(), value); killErr != nil {
			os.Exit(128 + int(value))
		}
		select {} // Default signal disposition terminates the process.
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tasks: %v\n", err)
		os.Exit(cli.ExitCode(err))
	}
}
