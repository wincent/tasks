package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/process"
)

func TestGoCreatesAndRunsNewTask(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	run(t, root, "create", "Existing task", "--body", "Leave this alone.")
	before := run(t, root, "show", "1", "--raw")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var launches int
	var prompt string
	out, err := invokePi(ctx, root, func(_ context.Context, child process.Command) process.Result {
		launches++
		prompt = child.Args[len(child.Args)-1]
		const want = "# EXAMPLE-2: Quick win\n\nStatus: active\n\n## Description\n\n\n"
		if got := run(t, root, "show", "2", "--raw"); got != want {
			t.Fatalf("task at launch: got %q, want %q", got, want)
		}
		commitImplementation(t, piWorktree(t, child), "quick.txt", "done\n")
		return process.Result{}
	}, "go", "  Quick win  ")
	if err != nil || launches != 1 || !strings.Contains(out, "merged into local main") {
		t.Fatalf("go ran Pi %d times: %v\n%s", launches, err, out)
	}
	path, _, _ := strings.Cut(out, "\n")
	if filepath.Base(path) != "EXAMPLE-2 Quick win.md" || !strings.Contains(prompt, path) {
		t.Fatalf("creation output %q and Pi prompt %q should name the new task", path, prompt)
	}
	if got := run(t, root, "show", "1", "--raw"); got != before {
		t.Fatalf("existing task changed: %q", got)
	}
	if got := run(t, root, "show", "2", "--raw"); !strings.Contains(got, "\nStatus: merged\n") {
		t.Fatalf("new task not marked merged: %q", got)
	}
	if got := systemGit(t, root, "show", "main:quick.txt"); got != "done" {
		t.Fatalf("implementation not merged: %q", got)
	}
}

func TestGoKeepsTaskWhenRunFails(t *testing.T) {
	_, root := taskWorkflowFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := invokePi(ctx, root, func(context.Context, process.Command) process.Result {
		return process.Result{Code: 17}
	}, "go", "Fix later")
	if err == nil || ExitCode(err) != 17 || !strings.Contains(err.Error(), "Continue with tasks run 1") {
		t.Fatalf("expected Pi failure and retry instructions: %v\n%s", err, out)
	}
	assertStatus(t, root, "blocked")
}

func TestGoRejectsInvalidArgumentsWithoutCreatingTask(t *testing.T) {
	_, root := fixture(t)
	before := systemGit(t, root, "rev-parse", "refs/heads/tasks")
	fails(t, root, "go expects 1 positional argument(s)", "go")
	fails(t, root, "go expects 1 positional argument(s)", "go", "Two", "arguments")
	fails(t, root, "unrecognized argument: --body", "go", "Title", "--body", "text")
	fails(t, root, "title must be nonempty", "go", " ")
	fails(t, root, "without path separators", "go", "bad/title")
	fails(t, root, "task text must not be empty", "create", "Title", "--body", "")
	if after := systemGit(t, root, "rev-parse", "refs/heads/tasks"); after != before {
		t.Fatal("invalid arguments changed task history")
	}
}
