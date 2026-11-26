package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func assertTailArgs(t *testing.T, cwd, wantOut, wantErr string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	transcript := &cancelOnOutput{cancel: cancel}
	var diagnostic bytes.Buffer
	err := Run(ctx, cwd, args, IO{Out: transcript, Err: &diagnostic})
	if !errors.Is(err, context.Canceled) || !strings.Contains(transcript.String(), wantOut) {
		t.Fatalf("%v: expected to tail %q: %v\nstdout:\n%s\nstderr:\n%s", args, wantOut, err, transcript, diagnostic.String())
	}
	if wantErr != "" && !strings.Contains(diagnostic.String(), wantErr) {
		t.Fatalf("%v: expected stderr containing %q, got:\n%s", args, wantErr, diagnostic.String())
	}
}

func writeTestSession(t *testing.T, directory string, n int, worktree, branch, text string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	sessionID := fmt.Sprintf("11111111-2222-4333-a444-%012d", n)
	sessionFile := filepath.Join(t.TempDir(), sessionID+".jsonl")
	header, err := json.Marshal(map[string]any{
		"type":      "session",
		"version":   3,
		"id":        sessionID,
		"cwd":       worktree,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := json.Marshal(map[string]any{
		"type": "message",
		"message": map[string]any{
			"role":    "assistant",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, sessionFile, string(header)+"\n"+string(message)+"\n")
	if err := writeTaskSession(directory, n, taskSession{
		Worktree:    worktree,
		Branch:      branch,
		SessionID:   &sessionID,
		SessionFile: sessionFile,
	}); err != nil {
		t.Fatal(err)
	}
	return sessionFile
}

func TestTailInfersSingleActiveTask(t *testing.T) {
	t.Parallel()
	wf := taskWorkflowFixture(t)
	repo, root := wf.repo, wf.root
	run(t, root, "create", "First open task", "--body", "requirements")
	run(t, root, "create", "Active task", "--body", "requirements")
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	linked, err := createCheckout(repo, "tail-observer-", head.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	out, err := wf.invokePi(context.Background(), root, func(_ context.Context, child process.Command) process.Result {
		checkoutPath, sessionPath := piWorktree(t, child), child.Args[2]
		commitImplementation(t, checkoutPath, "work.txt", "done\n")
		f, err := os.OpenFile(sessionPath, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(f, `{"type":"message","message":{"role":"assistant","content":[{"type":"text","text":"Inferred output."}]}}`)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		for _, cwd := range []string{root, linked.Paths.WorkDir} {
			assertTailArgs(t, cwd, "Inferred output.", "EXAMPLE-2: Active task", "tail")
		}
		assertTailArgs(t, root, "Inferred output.", "warning: subcommand `tial` does not exist; using `tail` instead.", "tial")
		return process.Result{}
	}, "run", "2")
	if err != nil || !strings.Contains(out, "EXAMPLE-2 (Active task) merged into local main.\n") {
		t.Fatalf("%v\n%s", err, out)
	}

	// Once the run completes, there is no active task to infer, while explicit tail still works.
	fails(t, root, "no active tasks; use `tasks tail NUMBER`", "tail")
	assertTailArgs(t, root, "Inferred output.", "EXAMPLE-2: Active task", "tail", "2")
}

func TestTailInferEdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("empty-store", func(t *testing.T) {
		t.Parallel()
		_, root := fixture(t)
		out, diagnostic, err := invoke(root, "", "tail")
		if err == nil || ExitCode(err) != 1 || out != "" || diagnostic != "" || !strings.Contains(err.Error(), "no active tasks; use `tasks tail NUMBER`") {
			t.Fatalf("empty store: out=%q stderr=%q err=%v", out, diagnostic, err)
		}
	})

	t.Run("stale-active-status-ignored", func(t *testing.T) {
		t.Parallel()
		repo, root := fixture(t)
		stalePath := strings.TrimSpace(run(t, root, "create", "Stale run", "--body", "requirements"))
		commitTaskText(t, repo, stalePath, "# EXAMPLE-1: Stale run\n\nStatus: active\n")
		run(t, root, "create", "Live run", "--body", "requirements")
		paths, err := repo.Paths()
		if err != nil {
			t.Fatal(err)
		}
		directory := stateDirectory(paths)
		writeTestSession(t, directory, 1, root, "refs/heads/task/example-1-stale", "Stale transcript.")
		writeTestSession(t, directory, 2, root, "refs/heads/task/example-2-live", "Live transcript.")

		// Without a live run lock, Status: active is treated as blocked, not active.
		fails(t, root, "no active tasks; use `tasks tail NUMBER`", "tail")

		live, err := state.TryLock(runLockPath(directory, 2))
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()

		// With only task 2 holding its run lock, tail infers task 2 despite task 1's stale Status: active.
		assertTailArgs(t, root, "Live transcript.", "EXAMPLE-2: Live run", "tail")
	})

	t.Run("multiple-active-tasks", func(t *testing.T) {
		t.Parallel()
		repo, root := fixture(t)
		run(t, root, "create", "First active", "--body", "requirements")
		run(t, root, "create", "Second active", "--body", "requirements")
		paths, err := repo.Paths()
		if err != nil {
			t.Fatal(err)
		}
		directory := stateDirectory(paths)
		writeTestSession(t, directory, 1, root, "refs/heads/task/example-1-a", "First transcript.")
		writeTestSession(t, directory, 2, root, "refs/heads/task/example-2-b", "Second transcript.")
		lock1, err := state.TryLock(runLockPath(directory, 1))
		if err != nil {
			t.Fatal(err)
		}
		defer lock1.Close()
		lock2, err := state.TryLock(runLockPath(directory, 2))
		if err != nil {
			t.Fatal(err)
		}
		defer lock2.Close()

		out, diagnostic, err := invoke(root, "", "tail")
		if err == nil || ExitCode(err) != 1 || out != "" || diagnostic != "" || err.Error() != "multiple active tasks (EXAMPLE-1, EXAMPLE-2); use `tasks tail NUMBER`" {
			t.Fatalf("multiple active: out=%q stderr=%q err=%v", out, diagnostic, err)
		}
		assertTailArgs(t, root, "First transcript.", "EXAMPLE-1: First active", "tail", "1")
		assertTailArgs(t, root, "Second transcript.", "EXAMPLE-2: Second active", "tail", "2")
	})

	t.Run("invalid-record-blocks-inference", func(t *testing.T) {
		t.Parallel()
		repo, root := fixture(t)
		path := strings.TrimSpace(run(t, root, "create", "Active task", "--body", "requirements"))
		paths, err := repo.Paths()
		if err != nil {
			t.Fatal(err)
		}
		directory := stateDirectory(paths)
		writeTestSession(t, directory, 1, root, "refs/heads/task/example-1-a", "Active transcript.")
		live, err := state.TryLock(runLockPath(directory, 1))
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()

		broken := filepath.Join(filepath.Dir(path), "EXAMPLE-2 Broken.md")
		writeSafetyFile(t, broken, "Status: invalid\n")

		out, diagnostic, err := invoke(root, "", "tail")
		if err == nil || ExitCode(err) != 1 || out != "" || diagnostic != "" ||
			!strings.Contains(err.Error(), "cannot infer active task; use `tasks tail NUMBER`") ||
			!strings.Contains(err.Error(), broken) {
			t.Fatalf("broken task store: out=%q stderr=%q err=%v", out, diagnostic, err)
		}
		// Explicit task selection remains available even when another task file is malformed.
		assertTailArgs(t, root, "Active transcript.", "EXAMPLE-1: Active task", "tail", "1")
	})

	t.Run("corrupt-session-record-blocks-inference", func(t *testing.T) {
		t.Parallel()
		repo, root := fixture(t)
		run(t, root, "create", "Active task", "--body", "requirements")
		run(t, root, "create", "Corrupt session", "--body", "requirements")
		paths, err := repo.Paths()
		if err != nil {
			t.Fatal(err)
		}
		directory := stateDirectory(paths)
		writeTestSession(t, directory, 1, root, "refs/heads/task/example-1-a", "Active transcript.")
		writeSafetyFile(t, taskSessionPath(directory, 2), "{not json")
		live, err := state.TryLock(runLockPath(directory, 1))
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()

		out, diagnostic, err := invoke(root, "", "tail")
		if err == nil || ExitCode(err) != 1 || out != "" || diagnostic != "" ||
			!strings.Contains(err.Error(), "cannot infer active task; use `tasks tail NUMBER`") {
			t.Fatalf("corrupt claim: out=%q stderr=%q err=%v", out, diagnostic, err)
		}
		assertTailArgs(t, root, "Active transcript.", "EXAMPLE-1: Active task", "tail", "1")
	})

	t.Run("legacy-active-task", func(t *testing.T) {
		t.Parallel()
		repo, root := fixture(t)
		run(t, root, "create", "Legacy active", "--body", "requirements")
		paths, err := repo.Paths()
		if err != nil {
			t.Fatal(err)
		}
		directory := stateDirectory(paths)
		writeTestSession(t, directory, 1, root, "", "Legacy transcript.")
		lock, err := state.TryLock(taskLockPath(directory, store.Prefix("EXAMPLE"), 1))
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()

		assertTailArgs(t, root, "Legacy transcript.", "EXAMPLE-1: Legacy active", "tail")
	})

	t.Run("argument-validation-and-help", func(t *testing.T) {
		t.Parallel()
		_, root := fixture(t)
		for _, tc := range []struct {
			args []string
			want string
		}{
			{[]string{"tail", "1", "2"}, "expects 1 positional"},
			{[]string{"tail", "0"}, "positive ticket number"},
			{[]string{"tail", "-1"}, "positive ticket number"},
			{[]string{"tail", "invalid"}, "positive ticket number"},
			{[]string{"tail", "OTHER-1"}, "unexpected task prefix"},
		} {
			out, diagnostic, err := invoke(root, "", tc.args...)
			if err == nil || ExitCode(err) != 2 || out != "" || diagnostic != "" || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v: out=%q stderr=%q err=%v (code %d); want usage error %q", tc.args, out, diagnostic, err, ExitCode(err), tc.want)
			}
		}
		help := run(t, root, "tail", "--help")
		if !strings.Contains(help, "Usage: tasks tail [NUMBER]") || !strings.Contains(help, "Omit NUMBER") {
			t.Fatal(help)
		}
	})
}
