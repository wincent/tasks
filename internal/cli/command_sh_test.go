package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/state"
)

func TestShRunsCommandInTaskCheckout(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "create", "Inspect the checkout", "--body", "Fixture")
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := createCheckout(repo, "example-1-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	worktree := source.Paths.WorkDir
	script := filepath.Join(worktree, "print args")
	if err := os.WriteFile(script, []byte(`#!/bin/sh
pwd -P
printf '%s\n' "$SH_TEST_VALUE"
IFS= read -r line
printf 'input: %s\n' "$line" >&2
printf '<%s>\n' "$@"
`), 0700); err != nil {
		t.Fatal(err)
	}

	// Inspection must work while an agent holds the task lock.
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := taskLock(directory, "EXAMPLE", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	t.Setenv("SH_TEST_VALUE", "inherited")
	argv := []string{"./print args", "--help", "--worktree", "--", "", "a b", "$HOME; pwd"}
	want := worktree + "\ninherited\n<--help>\n<--worktree>\n<-->\n<>\n<a b>\n<$HOME; pwd>\n"
	for _, args := range [][]string{{"sh", "1", "--"}, {"sh", "example-1"}} {
		out, diagnostic, err := invoke(root, "from stdin\n", append(args, argv...)...)
		if err != nil || out != want || diagnostic != "input: from stdin\n" {
			t.Errorf("%v: stdout %q, stderr %q, error %v", args, out, diagnostic, err)
		}
	}

	for _, tc := range []struct {
		command []string
		code    int
	}{
		{[]string{"/bin/sh", "-c", "exit 19"}, 19},
		{[]string{"/bin/sh", "-c", `kill -TERM "$$"`}, 128 + int(syscall.SIGTERM)},
		{[]string{"./missing-executable"}, 127},
	} {
		_, _, err := invoke(root, "", append([]string{"sh", "1"}, tc.command...)...)
		if err == nil || ExitCode(err) != tc.code {
			t.Errorf("%v: want exit %d, got %v (status %d)", tc.command, tc.code, err, ExitCode(err))
		}
	}
}

func TestShSelectsExistingCheckout(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t)
	run(t, root, "create", "Inspect worktrees", "--body", "Fixture")
	fails(t, root, "no checkout for EXAMPLE-1", "sh", "1", "/bin/pwd")
	fails(t, root, "expected one EXAMPLE-2 task", "sh", "2", "/bin/pwd")
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	first, err := createCheckout(repo, "example-1-first-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := createCheckout(repo, "example-1-second-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	fails(t, root, "multiple checkouts for EXAMPLE-1", "sh", "1", "/bin/pwd")
	if out := run(t, first.Paths.WorkDir, "sh", "1", "/bin/pwd", "-P"); out != first.Paths.WorkDir+"\n" {
		t.Errorf("caller checkout: got %q", out)
	}
	relative, err := filepath.Rel(root, second.Paths.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	if out := run(t, root, "sh", "--worktree", relative, "1", "--", "/bin/pwd", "-P"); out != second.Paths.WorkDir+"\n" {
		t.Errorf("explicit checkout: got %q", out)
	}
}

func TestShKeepsCheckoutDuringCleanup(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t)
	run(t, root, "create", "Keep checkout", "--body", "Fixture")
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := createCheckout(repo, "example-1-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	nested := false
	err = Run(context.Background(), root, []string{"sh", "1", "/bin/pwd"}, IO{
		Out: io.Discard, Err: io.Discard,
		Runner: piFixture(func(ctx context.Context, child process.Command) process.Result {
			for _, args := range [][]string{{"cleanup", "1"}, {"cleanup", "1", "--apply"}} {
				if out := run(t, root, args...); !strings.Contains(out, "checkout is in use by tasks sh") ||
					strings.Contains(out, "Would remove") || strings.Contains(out, "Removed") {
					t.Fatalf("%v ignored the lease: %s", args, out)
				}
			}
			err := Run(ctx, root, []string{"sh", "1", "/bin/pwd"}, IO{
				Out: io.Discard, Err: io.Discard,
				Runner: piFixture(func(_ context.Context, child process.Command) process.Result {
					nested = child.Directory == source.Paths.WorkDir
					return process.Result{}
				}),
			})
			if err != nil {
				t.Fatalf("second shell could not share the lease: %v", err)
			}
			if _, err := os.Stat(child.Directory); err != nil {
				t.Fatalf("checkout disappeared during shell: %v", err)
			}
			return process.Result{}
		}),
	})
	if err != nil || !nested {
		t.Fatalf("shells did not both run: %v (nested: %v)", err, nested)
	}
	if out := run(t, root, "cleanup", "1", "--apply"); !strings.Contains(out, "Removed "+source.Paths.WorkDir) {
		t.Fatalf("checkout kept after shells exited: %s", out)
	}
}

func TestShLeaseSurvivesLauncherDeath(t *testing.T) {
	const helper = "TASKS_TEST_SH_LAUNCHER"
	if os.Getenv(helper) == "1" {
		cwd, err := os.Getwd()
		if err == nil {
			err = Run(context.Background(), cwd, []string{"sh", "1", "/bin/sh", "-c",
				"echo ready; IFS= read -r line && pwd -P"}, IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	t.Parallel()

	repo, root := fixture(t)
	run(t, root, "create", "Keep checkout", "--body", "Fixture")
	base, err := repo.ResolveCommit(mainRef)
	if err != nil {
		t.Fatal(err)
	}
	source, err := createCheckout(repo, "example-1-", base, "")
	if err != nil {
		t.Fatal(err)
	}
	in, send, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer send.Close()
	out, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	defer write.Close()
	if err := out.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	launcher := exec.Command(os.Args[0], "-test.run=^TestShLeaseSurvivesLauncherDeath$")
	launcher.Dir = root
	launcher.Env = append(os.Environ(), helper+"=1")
	launcher.Stdin, launcher.Stdout, launcher.Stderr = in, write, os.Stderr
	if err := launcher.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if launcher.ProcessState == nil {
			_ = launcher.Process.Kill()
			_ = launcher.Wait()
		}
	}()
	in.Close()
	write.Close()
	lines := bufio.NewReader(out)
	if got, err := lines.ReadString('\n'); got != "ready\n" || err != nil {
		t.Fatalf("foreground child did not start: %q, %v", got, err)
	}
	if err := launcher.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := launcher.Wait(); err == nil || launcher.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("launcher was not killed: %v", err)
	}
	if output := run(t, root, "cleanup", "1", "--apply"); !strings.Contains(output, "checkout is in use by tasks sh") {
		t.Fatalf("cleanup ignored surviving child: %s", output)
	}
	if _, err := send.WriteString("exit now\n"); err != nil {
		t.Fatal(err)
	}
	if got, err := lines.ReadString('\n'); got != source.Paths.WorkDir+"\n" || err != nil {
		t.Fatalf("child did not survive at its checkout: %q, %v", got, err)
	}
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lock, err := state.Acquire(ctx, checkoutUseLockPath(directory, "EXAMPLE", 1))
	if err != nil {
		t.Fatalf("child did not release lease: %v", err)
	}
	lock.Close()
	if output := run(t, root, "cleanup", "1", "--apply"); !strings.Contains(output, "Removed "+source.Paths.WorkDir) {
		t.Fatalf("cleanup kept checkout after last user exited: %s", output)
	}
}

func TestShUsageErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "expects a task number and a command"},
		{[]string{"1", "--"}, "expects a task number and a command"},
		{[]string{"1", ""}, "command must not be empty"},
		{[]string{"bogus", "/bin/pwd"}, "positive ticket number"},
		{[]string{"--worktree=", "1", "/bin/pwd"}, "--worktree requires a nonempty path"},
	} {
		_, _, err := invoke(t.TempDir(), "", append([]string{"sh"}, tc.args...)...)
		if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: want usage error containing %q, got %v", tc.args, tc.want, err)
		}
	}
}
