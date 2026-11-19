package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateConfirmationOnTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, term, noColor, want string
	}{
		{"styled", "xterm", "", "✅ \x1b[1;32mCreated:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
		{"no color", "xterm", "1", "Created: EXAMPLE-1 Task title\n"},
		{"dumb terminal", "dumb", "", "Created: EXAMPLE-1 Task title\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, root := fixture(t)
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", tc.noColor)
			got, diagnostic, err := outputTerminal(t, root, "create", "Task title", "--body", "details")
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
	_, root, _ := editorFixture(t)
	t.Setenv("TASK_TEST_EDITOR_MODE", "write")
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	got, diagnostic, err := captureTerminal(t, root, true, "create", "From editor")
	want := "editor output\n✅ \x1b[1;32mCreated:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mFrom editor\x1b[0m\n"
	if err != nil || diagnostic != "" || got != want {
		t.Fatalf("create = %q, %q, %v; want %q", got, diagnostic, err, want)
	}
}

func TestTaskMutationConfirmations(t *testing.T) {
	_, root, _ := editorFixture(t)
	run(t, root, "create", "Task title", "--body", "description")
	t.Setenv("TASK_TEST_EDITOR_MODE", "empty")
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")
	for _, tc := range []struct {
		args         []string
		confirmation string
	}{
		{[]string{"edit", "1"}, "ℹ️ \x1b[1;36mUnchanged:\x1b[0m \x1b[4;36mEXAMPLE-1\x1b[0m \x1b[3;36mTask title\x1b[0m\n"},
		{[]string{"comment", "1", "--body", "note"}, "✅ \x1b[1;32mCommented on:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
		{[]string{"close", "1", "--body", "done"}, "✅ \x1b[1;32mClosed:\x1b[0m \x1b[4;32mEXAMPLE-1\x1b[0m \x1b[3;32mTask title\x1b[0m\n"},
	} {
		got, diagnostic, err := captureTerminal(t, root, true, tc.args...)
		if err != nil || !strings.HasSuffix(got, tc.confirmation) {
			t.Fatalf("%v = %q, %q, %v; want confirmation %q", tc.args, got, diagnostic, err, tc.confirmation)
		}
	}
}

func TestTaskStatusPresentation(t *testing.T) {
	_, root := fixture(t)
	path := strings.TrimSuffix(run(t, root, "create", "Needs input", "--body", "details"), "\n")
	content := strings.Replace(run(t, root, "show", "1", "--raw"), "Status: open", "Status: blocked", 1)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TERM", "xterm")
	t.Setenv("NO_COLOR", "")

	want := "\x1b[4mEXAMPLE-1\x1b[0m  \x1b[33mblocked\x1b[0m  \x1b[3mNeeds input\x1b[0m\n"
	got, diagnostic, err := outputTerminal(t, root, "status")
	if err != nil || diagnostic != "" || got != want {
		t.Fatalf("status = %q, %q, %v; want %q", got, diagnostic, err, want)
	}
	if got := run(t, root, "status"); got != "EXAMPLE-1  blocked  Needs input\n" {
		t.Fatalf("plain status = %q", got)
	}
	got, diagnostic, err = outputTerminal(t, root, "status", "--json")
	if piped := run(t, root, "status", "--json"); err != nil || diagnostic != "" || got != piped {
		t.Fatalf("JSON status = %q, %q, %v; want %q", got, diagnostic, err, piped)
	}
}
