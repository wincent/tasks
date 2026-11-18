// Package process executes explicit non-Git adapters (editors, Pi, hooks and
// configured validators). Git operations never route through this package.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type Command struct {
	Args             []string
	Directory        string
	Environment      []string
	In               io.Reader
	Out, Err         io.Writer
	ExtraFiles       []*os.File
	Group            bool
	RequireGroupExit bool
}

type Result struct {
	Code int
	Err  error
}

type Runner interface {
	Run(context.Context, Command) Result
}
type Native struct{}

func (Native) Run(ctx context.Context, command Command) Result {
	if len(command.Args) == 0 {
		return Result{127, errors.New("empty external command")}
	}
	if err := ctx.Err(); err != nil {
		return Result{-int(syscall.SIGTERM), err}
	}
	child := exec.Command(command.Args[0], command.Args[1:]...)
	child.Dir = command.Directory
	child.Env = command.Environment
	child.Stdin = command.In
	child.Stdout = command.Out
	child.Stderr = command.Err
	child.ExtraFiles = command.ExtraFiles
	child.WaitDelay = 2 * time.Second
	if command.Group {
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	if err := child.Start(); err != nil {
		return Result{127, err}
	}
	done, watched := make(chan struct{}), make(chan struct{})
	signal := func(value syscall.Signal) {
		if command.Group {
			_ = syscall.Kill(-child.Process.Pid, value)
		} else {
			_ = child.Process.Signal(value)
		}
	}
	go func() {
		defer close(watched)
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		signal(cancellationSignal(ctx))
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-done:
			if command.Group {
				signal(syscall.SIGKILL)
			}
		case <-timer.C:
			signal(syscall.SIGKILL)
		}
	}()
	err := child.Wait()
	close(done)
	<-watched
	code := 0
	if err != nil {
		code = 1
		if child.ProcessState != nil {
			if status, ok := child.ProcessState.Sys().(syscall.WaitStatus); ok {
				if status.Signaled() {
					code = -int(status.Signal())
				} else {
					code = status.ExitStatus()
				}
			}
		}
		if code == 0 {
			code = 1
		}
	}
	if command.Group && command.RequireGroupExit {
		probe := syscall.Kill(-child.Process.Pid, 0)
		if !errors.Is(probe, syscall.ESRCH) {
			if probe == nil {
				probe = errors.New("external command exited but its process group still has running tools; stop them before continuing")
			}
			return Result{1, probe}
		}
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	return Result{code, err}
}

func Quote(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"\\$`;&|<>()*?![]{}~") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
func Display(args []string) string {
	values := make([]string, len(args))
	for i, arg := range args {
		values[i] = Quote(arg)
	}
	return strings.Join(values, " ")
}
func Failure(command []string, result Result, stdout, stderr string) error {
	parts := []string{fmt.Sprintf("Command failed (exit %d): %s", result.Code, Display(command))}
	for i, value := range []string{stdout, stderr} {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, []string{"stdout:\n", "stderr:\n"}[i]+strings.TrimSpace(value))
		}
	}
	if len(parts) == 1 {
		if result.Err != nil {
			parts = append(parts, result.Err.Error())
		} else {
			parts = append(parts, "No stdout or stderr diagnostics were produced.")
		}
	}
	return errors.New(strings.Join(parts, "\n"))
}
