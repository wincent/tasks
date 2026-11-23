package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
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

func TestShUsageErrors(t *testing.T) {
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
