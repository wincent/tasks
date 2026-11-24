package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func TestCommandCorrectionDispatch(t *testing.T) {
	_, root := fixture(t)
	run(t, root, "create", "Example task", "--body", "Details")

	for _, tc := range []struct {
		typo, command string
		trailing      []string
	}{
		{"lsti", "list", []string{"--json"}},
		{"lst", "list", nil}, // Deletion, not an insertion into the "st" alias.
		{"listt", "list", nil},
		{"l1st", "list", nil},
		{"sttaus", "status", []string{"--json"}},
		{"lsti", "list", []string{"--help"}},
		{"hepl", "help", []string{"list"}},
		{"cleanpu", "cleanup", nil},
		{"sotre", "store", []string{"1"}},
	} {
		args := append([]string{tc.typo}, tc.trailing...)
		want := run(t, root, append([]string{tc.command}, tc.trailing...)...)
		out, diagnostic, err := invoke(root, "", args...)
		warning := fmt.Sprintf("warning: subcommand `%s` does not exist; using `%s` instead.\n", tc.typo, tc.command)
		if err != nil || out != want || diagnostic != warning {
			t.Errorf("%v: stdout = %q, stderr = %q, err = %v; want %q, %q, nil", args, out, diagnostic, err, want, warning)
		}
	}
	out, diagnostic, err := invoke(root, "", "st", "--json")
	if want := run(t, root, "status", "--json"); err != nil || out != want || diagnostic != "" {
		t.Errorf("exact alias: stdout = %q, stderr = %q, err = %v; want %q, no warning", out, diagnostic, err, want)
	}
}

func TestCommandCorrectionRejectsUnsafeAndUnknown(t *testing.T) {
	// Usage errors must be reported without opening a repository.
	cwd := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"og", "Example task"}, "subcommand `og` does not exist; did you mean `go`?"},
		{[]string{"hs", "1", "--", "git", "status"}, "subcommand `hs` does not exist; did you mean `sh`?"},
		{[]string{"cretae", "Example task", "--body="}, "subcommand `cretae` does not exist; did you mean `create`?"},
		{[]string{"cretae", "Example task", "--body-file", "-"}, "subcommand `cretae` does not exist; did you mean `create`?"},
		{[]string{"clsoe", "1", "--body", "Note"}, "subcommand `clsoe` does not exist; did you mean `close`?"},
		{[]string{"commnet", "1", "--body-file=-"}, "subcommand `commnet` does not exist; did you mean `comment`?"},
		{[]string{"cleanpu", "--apply"}, "subcommand `cleanpu` does not exist; did you mean `cleanup`?"},
		{[]string{"sotre", "1", "--", "--help"}, "subcommand `sotre` does not exist; did you mean `store`?"},
		{[]string{"so"}, "unknown command: so"}, // One edit from both "go" and "sh".
		{[]string{"ts"}, "unknown command: ts"}, // Aliases are exact only.
		{[]string{"not-a-command"}, "unknown command: not-a-command"},
		{[]string{"--lsti"}, "unknown command: --lsti"},
		{[]string{"list", "--josn"}, "unrecognized argument: --josn"},
		{[]string{"help", "lsti"}, "unknown command: lsti"},
	} {
		out, diagnostic, err := invoke(cwd, "", tc.args...)
		if err == nil || ExitCode(err) != 2 || err.Error() != tc.want || out != "" || diagnostic != "" {
			t.Errorf("%v: stdout = %q, stderr = %q, err = %v; want usage error %q only", tc.args, out, diagnostic, err, tc.want)
		}
	}
}

func TestCommandCorrectionArchiveConfirmation(t *testing.T) {
	repo, root := fixture(t)
	run(t, root, "create", "Example task", "--body", "Details")
	head := func() git.ID { return fixtureRevision(t, repo, store.Branch) }
	before := head()
	const warning = "warning: subcommand `arcihve` does not exist; using `archive` instead.\n"

	out, diagnostic, err := invoke(root, "yes\n", "arcihve", "1")
	if err == nil || !strings.Contains(err.Error(), "confirmation requires a terminal") || out != "" || diagnostic != warning || head() != before {
		t.Fatalf("non-terminal archive: stdout = %q, stderr = %q, err = %v", out, diagnostic, err)
	}
	out, diagnostic, err = invoke(root, "yes\n", "arcihve", "-q", "status:open")
	if err != nil || !strings.Contains(out, "EXAMPLE-1") || !strings.Contains(out, "No changes made") || diagnostic != warning || head() != before {
		t.Fatalf("non-terminal query archive: stdout = %q, stderr = %q, err = %v", out, diagnostic, err)
	}

	for _, answer := range []string{"no\n", "yes\n"} {
		out, err := archiveAtPrompt(t, root, func(_ context.CancelFunc, input io.Writer) {
			if _, err := io.WriteString(input, answer); err != nil {
				t.Fatal(err)
			}
		}, "arcihve", "1")
		if err != nil || !strings.HasPrefix(out, "Archive EXAMPLE-1? Type yes to confirm: ") {
			t.Fatalf("answer %q: stdout = %q, err = %v", answer, out, err)
		}
		if answer == "no\n" && (!strings.Contains(out, "Archive cancelled") || head() != before) {
			t.Fatal("declining confirmation changed the tasks ref")
		}
	}
	if out := run(t, root, "list", "--include-archived"); !strings.Contains(out, "Example task (archived)") || head() == before {
		t.Fatalf("confirmed archive did not move the task: %q", out)
	}
}

func TestCommandCorrectionCreateUsesEditor(t *testing.T) {
	repo, root, _ := editorFixture(t)
	t.Setenv("TASK_TEST_EDITOR_MODE", "fail")
	before := fixtureRevision(t, repo, store.Branch)
	out, err := runTerminal(t, root, "cretae", "Example task")
	if err == nil || !strings.Contains(err.Error(), "creation cancelled") || out != "" || fixtureRevision(t, repo, store.Branch) != before {
		t.Fatalf("failed editor: stdout = %q, err = %v", out, err)
	}

	t.Setenv("TASK_TEST_EDITOR_MODE", "")
	out, err = runTerminal(t, root, "cretae", "Example task")
	if err != nil || !strings.HasSuffix(strings.TrimSpace(out), "EXAMPLE-1 Example task.md") {
		t.Fatalf("successful editor: stdout = %q, err = %v", out, err)
	}
	if out := run(t, root, "show", "1"); !strings.Contains(out, "Description from editor.") {
		t.Fatal(out)
	}
}
