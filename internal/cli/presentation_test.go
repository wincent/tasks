package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/testutil"
)

func TestMergeHeadlinesUseDestinationWidth(t *testing.T) {
	t.Parallel()
	wf := taskWorkflowFixture(t)
	run(t, wf.root, "create", "界界界 Review the feature", "--body", "requirements")
	if out, err := wf.invokePi(context.Background(), wf.root, func(_ context.Context, child process.Command) process.Result {
		commitImplementation(t, piWorktree(t, child), "feature.txt", "done\n")
		return process.Result{}
	}, "run", "1", "--no-merge"); err != nil {
		t.Fatalf("prepare merge: %v\n%s", err, out)
	}

	out, readOut := sizedTerminalOutput(t, 50)
	diagnostic, readDiagnostic := sizedTerminalOutput(t, 47)
	if err := Run(context.Background(), wf.root, []string{"merge", "1"}, IO{Out: out, Err: diagnostic, Env: withEnv(wf.env, "TERM=xterm", "NO_COLOR=")}); err != nil {
		t.Fatal(err)
	}
	wantProgress := "🔄 \x1b[1;36mMerging\x1b[0m \x1b[4mEXAMPLE-1\x1b[0m (\x1b[3m界界…\x1b[0m) into local main...\n"
	if got := readDiagnostic(); got != wantProgress {
		t.Errorf("merge progress = %q; want %q", got, wantProgress)
	}
	wantResult := "✅ \x1b[4mEXAMPLE-1\x1b[0m (\x1b[3m界界界 Rev…\x1b[0m) \x1b[1;32mmerged into local main.\x1b[0m\n"
	if got := readOut(); got != wantResult {
		t.Errorf("merge result = %q; want %q", got, wantResult)
	}
}

func TestMergeConflictPresentation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, term, noColor string
		terminal, styled    bool
	}{
		{"styled terminal", "xterm", "", true, true},
		{"no color", "xterm", "1", true, false},
		{"dumb terminal", "dumb", "", true, false},
		{"non-terminal", "xterm", "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wf := taskWorkflowFixture(t)
			commitImplementation(t, wf.root, "conflict", "base\n")
			run(t, wf.root, "create", "Conflict", "--body", "requirements")
			var worktree string
			if out, err := wf.invokePi(context.Background(), wf.root, func(_ context.Context, child process.Command) process.Result {
				worktree = piWorktree(t, child)
				commitImplementation(t, worktree, "conflict", "task\n")
				commitImplementation(t, wf.root, "conflict", "main\n")
				return process.Result{}
			}, "run", "1", "--no-merge"); err != nil {
				t.Fatalf("prepare conflict: %v\n%s", err, out)
			}

			env := withEnv(wf.env, "TERM="+tc.term, "NO_COLOR="+tc.noColor)
			var gotOut, gotErr string
			var err error
			if tc.terminal {
				out, readOut := sizedTerminalOutput(t, 120)
				diagnostic, readDiagnostic := sizedTerminalOutput(t, 120)
				err = Run(context.Background(), wf.root, []string{"merge", "1"}, IO{Out: out, Err: diagnostic, Env: env})
				gotOut, gotErr = readOut(), readDiagnostic()
			} else {
				var out, diagnostic bytes.Buffer
				err = Run(context.Background(), wf.root, []string{"merge", "1"}, IO{Out: &out, Err: &diagnostic, Env: env})
				gotOut, gotErr = out.String(), diagnostic.String()
			}
			if err == nil || ExitCode(err) != 1 {
				t.Fatalf("merge error = %v (code %d), want exit 1", err, ExitCode(err))
			}
			if gotOut != "" {
				t.Errorf("stdout = %q; want empty", gotOut)
			}
			var wantProgress, wantConflict string
			if tc.styled {
				wantProgress = "🔄 \x1b[1;36mMerging\x1b[0m \x1b[4mEXAMPLE-1\x1b[0m (\x1b[3mConflict\x1b[0m) into local main...\n"
				wantConflict = fmt.Sprintf("🚫 EXAMPLE-1 needs attention: \x1b[4mmerge conflict; main is unchanged\x1b[0m. In %s, merge main, resolve and commit, then retry with `tasks merge 1`.\n", worktree)
			} else {
				wantProgress = "Merging EXAMPLE-1 (Conflict) into local main...\n"
				wantConflict = fmt.Sprintf("EXAMPLE-1 needs attention: merge conflict; main is unchanged. In %s, merge main, resolve and commit, then retry with `tasks merge 1`.\n", worktree)
			}
			if want := wantProgress + wantConflict; gotErr != want {
				t.Errorf("stderr =\n%q\nwant:\n%q", gotErr, want)
			}
			var extra bytes.Buffer
			WriteError(&extra, err)
			if extra.Len() != 0 {
				t.Errorf("WriteError wrote %q; want empty", extra.String())
			}
		})
	}
}

func sizedTerminalOutput(t *testing.T, columns uint16) (*os.File, func() string) {
	t.Helper()
	master, slave, err := testutil.OpenPTYWithWidth(columns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.ClosePTY(master, slave) })
	if err := master.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(master); done <- data }()
	return slave, func() string {
		// Close the slave to let the reader finish draining the master.
		slave.Close()
		return strings.ReplaceAll(string(<-done), "\r\n", "\n")
	}
}

func TestEllipsizeTaskTitle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		title string
		width int
		want  string
	}{
		{"Title", 0, ""},
		{"Title", 1, "…"},
		{"Title", 5, "Title"},
		{"One two", 5, "One…"},
		{"👩‍💻 test", 2, "…"},
		{"👩‍💻 test", 3, "👩‍💻…"},
		{"e\u0301 test", 2, "e\u0301…"},
	} {
		if got := ellipsize(tc.title, tc.width); got != tc.want {
			t.Errorf("ellipsize(%q, %d) = %q; want %q", tc.title, tc.width, got, tc.want)
		}
	}
}

func TestCreateConfirmationOnTerminal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, term, noColor, want string
	}{
		{"styled", "xterm", "", "✅ \x1b[1;32mCreated:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
		{"no color", "xterm", "1", "Created: EXAMPLE-1 Task title\n"},
		{"dumb terminal", "dumb", "", "Created: EXAMPLE-1 Task title\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, root := fixture(t)
			env := withEnv(nil, "TERM="+tc.term, "NO_COLOR="+tc.noColor)
			got, diagnostic, err := outputTerminalEnv(t, root, env, "create", "Task title", "--body", "details")
			if err != nil || diagnostic != "" || got != tc.want {
				t.Fatalf("create = %q, %q, %v; want %q", got, diagnostic, err, tc.want)
			}

			// A script gets a usable path, not terminal presentation.
			got = run(t, root, "create", "Scripted", "--body", "details")
			path := strings.TrimSuffix(got, "\n")
			if !filepath.IsAbs(path) || filepath.Base(path) != "EXAMPLE-2 Scripted.md" {
				t.Fatalf("create did not print an absolute task path: %q", got)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInteractiveCreateDoesNotPrintPreEditorBanner(t *testing.T) {
	t.Parallel()
	ef := editorFixture(t)
	ef.setEnv("TASK_TEST_EDITOR_MODE=write", "TERM=xterm", "NO_COLOR=")
	got, diagnostic, err := captureTerminalEnv(t, ef.root, true, ef.env, "create", "From editor")
	want := "editor output\n✅ \x1b[1;32mCreated:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mFrom editor\x1b[0m\n"
	if err != nil || diagnostic != "" || got != want {
		t.Fatalf("create = %q, %q, %v; want %q", got, diagnostic, err, want)
	}
}

func TestTaskMutationConfirmations(t *testing.T) {
	t.Parallel()
	ef := editorFixture(t)
	run(t, ef.root, "create", "Task title", "--body", "description")
	ef.setEnv("TASK_TEST_EDITOR_MODE=empty", "TERM=xterm", "NO_COLOR=")
	for _, tc := range []struct {
		args         []string
		confirmation string
	}{
		{[]string{"edit", "1"}, "ℹ️ \x1b[1;36mUnchanged:\x1b[0m \x1b[4;36mEXAMPLE-1\x1b[0m \x1b[3;36mTask title\x1b[0m\n"},
		{[]string{"comment", "1", "--body", "note"}, "✅ \x1b[1;32mCommented on:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
		{[]string{"close", "1", "--body", "done"}, "✅ \x1b[1;32mClosed:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
	} {
		got, diagnostic, err := captureTerminalEnv(t, ef.root, true, ef.env, tc.args...)
		if err != nil || !strings.HasSuffix(got, tc.confirmation) {
			t.Fatalf("%v = %q, %q, %v; want confirmation %q", tc.args, got, diagnostic, err, tc.confirmation)
		}
	}
}

func TestTaskStatusPresentation(t *testing.T) {
	t.Parallel()
	_, root := fixture(t)
	path := strings.TrimSuffix(run(t, root, "create", "Needs input", "--body", "details"), "\n")
	content := strings.Replace(run(t, root, "show", "1", "--raw"), "Status: open", "Status: blocked", 1)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	env := withEnv(nil, "TERM=xterm", "NO_COLOR=")

	want := "\x1b[4mEXAMPLE-1\x1b[0m  \x1b[33mblocked\x1b[0m  \x1b[3mNeeds input\x1b[0m\n"
	wantJSON := run(t, root, "status", "--json")
	for _, name := range []string{"status", "st"} {
		t.Run(name, func(t *testing.T) {
			got, diagnostic, err := outputTerminalEnv(t, root, env, name)
			if err != nil || diagnostic != "" || got != want {
				t.Fatalf("terminal output = %q, %q, %v; want %q", got, diagnostic, err, want)
			}
			if got := run(t, root, name); got != "EXAMPLE-1  blocked  Needs input\n" {
				t.Fatalf("plain output = %q", got)
			}
			got, diagnostic, err = outputTerminalEnv(t, root, env, name, "--json")
			if err != nil || diagnostic != "" || got != wantJSON {
				t.Fatalf("terminal JSON = %q, %q, %v; want %q", got, diagnostic, err, wantJSON)
			}
			if got := run(t, root, name, "--json"); got != wantJSON {
				t.Fatalf("plain JSON = %q; want %q", got, wantJSON)
			}
		})
	}
}
