package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/terminal"
	"github.com/wincent/tasks/internal/testutil"
)

func TestEditorProcess(t *testing.T) {
	if os.Getenv("TASK_TEST_EDITOR_HELPER") != "1" {
		return
	}
	draft := os.Args[len(os.Args)-1]
	original, err := os.ReadFile(draft)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	capture := map[string]any{"draft": draft, "initial": string(original), "stdin_tty": terminal.IsTerminal(os.Stdin), "stdout_tty": terminal.IsTerminal(os.Stdout), "args": os.Args}
	data, _ := json.Marshal(capture)
	if err := os.WriteFile(os.Getenv("TASK_TEST_CAPTURE"), data, 0600); err != nil {
		os.Exit(4)
	}
	switch os.Getenv("TASK_TEST_EDITOR_MODE") {
	case "fail":
		os.Exit(7)
	case "interrupt":
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {}
	case "manual":
		if err := os.WriteFile(os.Getenv("TASK_TEST_TASK_FILE"), []byte("Concurrent manual edit\n"), 0600); err != nil {
			os.Exit(6)
		}
	case "empty":
		os.Exit(0)
	case "create":
		cwd, _ := os.Getwd()
		if err := Run(context.Background(), cwd, []string{"create", "Concurrent", "--body", "editor still open"}, IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(5)
		}
	case "concurrent":
		cwd, _ := os.Getwd()
		if err := Run(context.Background(), cwd, []string{"comment", "1", "--body", "Concurrent comment"}, IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(6)
		}
	}
	if err := os.WriteFile(draft, []byte(os.Getenv("TASK_TEST_CONTENT")), 0600); err != nil {
		os.Exit(8)
	}
	fmt.Fprintln(os.Stdout, "editor output")
	os.Exit(0)
}

func editorFixture(t *testing.T) (*git.Repository, string, string) {
	t.Helper()
	repo, root := fixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "capture.json")
	t.Setenv("TASK_TEST_EDITOR_HELPER", "1")
	t.Setenv("TASK_TEST_CAPTURE", capture)
	t.Setenv("VISUAL", "'"+strings.ReplaceAll(binary, "'", "'\\''")+"' -test.run=TestEditorProcess -- --flag 'argument with spaces'")
	t.Setenv("EDITOR", "/nonexistent/editor")
	t.Setenv("TASK_TEST_CONTENT", "Description from editor.\n\n- Criterion\n")
	return repo, root, capture
}

func runTerminal(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	master, slave, err := testutil.OpenPTY()
	if err != nil {
		t.Fatal(err)
	}
	// Close the master first: Darwin may wait for undrained output when the
	// last slave is closed while its master remains open.
	defer func() { master.Close(); slave.Close() }()
	var out bytes.Buffer
	err = Run(context.Background(), root, args, IO{In: slave, Out: &out, Err: slave})
	return out.String(), err
}
func captured(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEditorQuoting(t *testing.T) {
	for _, test := range []struct {
		input string
		want  []string
	}{
		{`'fake editor' --flag "argument with spaces"`, []string{"fake editor", "--flag", "argument with spaces"}},
		{`vi a\ b '' ""`, []string{"vi", "a b", "", ""}},
		{`vi "$(literal)" '$HOME'`, []string{"vi", "$(literal)", "$HOME"}},
		{`vi "a\qb"`, []string{"vi", `a\qb`}},
	} {
		got, err := splitEditor(test.input)
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("%s: %q %v", test.input, got, err)
		}
	}
	for _, input := range []string{"", `''`, `'unterminated`, `vi \`} {
		if _, err := splitEditor(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestEditorBodyPTYDoesNotHoldStoreLock(t *testing.T) {
	_, root, capture := editorFixture(t)
	t.Setenv("TASK_TEST_EDITOR_MODE", "create")
	out, err := runTerminal(t, root, "create", "Interactive")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "EXAMPLE-2 Interactive.md") {
		t.Fatal(out)
	}
	result := captured(t, capture)
	if result["initial"] != "" || result["stdin_tty"] != true || result["stdout_tty"] != true {
		t.Fatal(result)
	}
	if _, err := os.Stat(result["draft"].(string)); !os.IsNotExist(err) {
		t.Fatalf("draft retained %v", err)
	}
	if content := run(t, root, "show", "2"); !strings.Contains(content, "Description from editor.") {
		t.Fatal(content)
	}
}

func TestEditorCancellationDoesNotAllocate(t *testing.T) {
	for _, mode := range []string{"empty", "fail"} {
		t.Run(mode, func(t *testing.T) {
			repo, root, capture := editorFixture(t)
			t.Setenv("TASK_TEST_EDITOR_MODE", mode)
			if out, err := runTerminal(t, root, "create", "Cancel"); err == nil || out != "" {
				t.Fatalf("%q %v", out, err)
			}
			result := captured(t, capture)
			if _, err := os.Stat(result["draft"].(string)); !os.IsNotExist(err) {
				t.Fatalf("body draft retained %v", err)
			}
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(paths.CommonDir, "tasks", "ledger.json")); !os.IsNotExist(err) {
				t.Fatalf("ledger changed %v", err)
			}
			if got := run(t, root, "create", "First", "--body", "works"); !strings.HasSuffix(got, "EXAMPLE-1 First.md\n") {
				t.Fatal(got)
			}
		})
	}
}

func TestEditPTYAndConcurrentDraftPreservation(t *testing.T) {
	for _, mode := range []string{"write", "empty", "fail", "concurrent", "status"} {
		t.Run(mode, func(t *testing.T) {
			repo, root, capture := editorFixture(t)
			path := strings.TrimSpace(run(t, root, "create", "Edit", "--body", "original"))
			initial := run(t, root, "show", "1")
			updated := strings.Replace(initial, "original", "edited", 1)
			if mode == "status" {
				updated = strings.Replace(initial, "Status: open", "Status: active", 1)
			}
			t.Setenv("TASK_TEST_CONTENT", updated)
			t.Setenv("TASK_TEST_EDITOR_MODE", mode)
			tasks, err := store.Open(repo)
			if err != nil {
				t.Fatal(err)
			}
			defer tasks.Close()
			before, err := tasks.Repository.Head()
			if err != nil {
				t.Fatal(err)
			}
			out, err := runTerminal(t, root, "edit", "1")
			success := mode == "write" || mode == "empty" || mode == "status"
			if success != (err == nil) {
				t.Fatalf("%q %v", out, err)
			}
			result := captured(t, capture)
			draft := result["draft"].(string)
			if success {
				if out != path+"\n" {
					t.Fatal(out)
				}
				if _, err := os.Stat(draft); !os.IsNotExist(err) {
					t.Fatalf("successful draft retained: %v", err)
				}
			} else {
				t.Cleanup(func() { os.RemoveAll(filepath.Dir(draft)) })
				if _, err := os.Stat(draft); err != nil {
					t.Fatalf("failed draft lost: %v", err)
				}
			}
			actual := run(t, root, "show", "1")
			switch mode {
			case "write", "status":
				if actual != updated {
					t.Fatal(actual)
				}
			case "concurrent":
				if !strings.Contains(actual, "Concurrent comment") || strings.Contains(actual, "edited") {
					t.Fatal(actual)
				}
			default:
				if actual != initial {
					t.Fatal(actual)
				}
				after, err := tasks.Repository.Head()
				if err != nil || after != before {
					t.Fatalf("unexpected commit: %+v %v", after, err)
				}
			}
		})
	}
}
