package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/cli"
	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/testutil"
)

func TestMain(m *testing.M) {
	if os.Getenv("TASK_TEST_MAIN_HELPER") == "1" {
		if os.Getenv("TASK_TEST_DENY_EXEC") == "1" {
			if err := testutil.DenyExec(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(77)
			}
			// Positive control: an executable at an absolute path must now fail.
			if err := exec.Command("/bin/sh", "-c", "exit 0").Run(); err == nil {
				fmt.Fprintln(os.Stderr, "exec denial positive control failed")
				os.Exit(78)
			}
		}
		if filepath.Base(os.Args[0]) == "pi" {
			signals := make(chan os.Signal, 1)
			signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
			if err := os.WriteFile(os.Getenv("TASK_TEST_MAIN_PID"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				panic(err)
			}
			fmt.Println(`{"type":"tool_execution_start","toolName":"fixture-ready"}`)
			received := <-signals
			if err := os.WriteFile(os.Getenv("TASK_TEST_MAIN_SIGNAL"), []byte(strconv.Itoa(int(received.(syscall.Signal)))), 0600); err != nil {
				panic(err)
			}
			os.Exit(0)
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestExecutableForwardsAndPreservesSignalsAfterChildCleanup(t *testing.T) {
	for _, value := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(value.String(), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repo")
			repo, err := git.Init(root, "", false)
			if err != nil {
				t.Fatal(err)
			}
			defer repo.Close()
			seedMainConfig(t, repo)
			seedLocalTaskHistory(t, repo)
			for key, value := range map[string]string{"user.name": "Fixture", "user.email": "fixture@example.com"} {
				if err := repo.SetLocalConfig(key, value); err != nil {
					t.Fatal(err)
				}
			}
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			root = paths.WorkDir
			if err := os.WriteFile(filepath.Join(paths.CommonDir, "info", "exclude"), []byte("/tasks/\n/.agent-worktrees/\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := cli.Run(context.Background(), root, []string{"create", "Signal fixture", "--body", "fixture"}, cli.IO{In: strings.NewReader(""), Out: &output, Err: &output}); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			tools := t.TempDir()
			if err := os.Symlink(executable, filepath.Join(tools, "pi")); err != nil {
				t.Fatal(err)
			}
			pidPath, signalPath := filepath.Join(tools, "pid"), filepath.Join(tools, "signal")
			t.Setenv("TASK_TEST_MAIN_HELPER", "1")
			t.Setenv("TASK_TEST_MAIN_PID", pidPath)
			t.Setenv("TASK_TEST_MAIN_SIGNAL", signalPath)
			t.Setenv("PATH", tools)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "run", "1")
			command.Dir = root
			command.Stderr = &output
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = command.Process.Kill()
				data, _ := os.ReadFile(pidPath)
				pid, _ := strconv.Atoi(string(data))
				if pid > 0 {
					_ = syscall.Kill(-pid, syscall.SIGKILL)
				}
			}()
			line, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || strings.TrimSpace(line) != "Running fixture-ready..." {
				_ = command.Wait()
				t.Fatalf("%q %v\n%s", line, err, output.String())
			}
			if err := command.Process.Signal(value); err != nil {
				t.Fatal(err)
			}
			err = command.Wait()
			if err == nil {
				t.Fatal("signal was discarded")
			}
			status := command.ProcessState.Sys().(syscall.WaitStatus)
			if !status.Signaled() || status.Signal() != value {
				t.Fatalf("status %v\n%s", status, output.String())
			}
			data, err := os.ReadFile(signalPath)
			if err != nil || string(data) != strconv.Itoa(int(value)) {
				t.Fatalf("child signal %q %v", data, err)
			}
			lock, err := state.TryLock(filepath.Join(paths.CommonDir, "tasks", "example-1.lock"))
			if err != nil {
				t.Fatal("child or lifetime lock survived cancellation", err)
			}
			lock.Close()
		})
	}
}
