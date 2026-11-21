package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestHelp(t *testing.T) {
	// Help must work without a repository, even for commands that need a
	// task number, an editor, or a Pi session.
	cwd := filepath.Join(t.TempDir(), "missing")
	readHelp := func(t *testing.T, args ...string) string {
		t.Helper()
		out, stderr, err := invoke(cwd, "", args...)
		if err != nil || stderr != "" {
			t.Fatalf("%v: %v; stderr: %q", args, err, stderr)
		}
		if !strings.HasSuffix(out, "\n") {
			t.Errorf("missing final newline: %q", out)
		}
		for _, line := range strings.Split(out, "\n") {
			if utf8.RuneCountInString(line) > 80 {
				t.Errorf("help exceeds 80 columns: %q", line)
			}
		}
		return out
	}

	root := readHelp(t, "--help")
	if short := readHelp(t, "-h"); short != root {
		t.Errorf("top-level -h differs from --help: %q", short)
	}
	if alias := readHelp(t, "help"); alias != root {
		t.Errorf("help differs from top-level --help: %q", alias)
	}
	if !strings.HasPrefix(root, "Usage: tasks [COMMAND]\n") {
		t.Errorf("missing top-level usage: %q", root)
	}
	if !strings.Contains(root, "\n  --version   Print the version and exit.\n") {
		t.Errorf("missing --version option in top-level help: %q", root)
	}
	for _, example := range []string{
		"`tasks go \"Task title\"`",
		"`tasks create \"Task title\"`",
		"`tasks run NUMBER`",
		"`tasks run NUMBER --no-merge`",
		"`tasks merge NUMBER`",
		"`tasks help COMMAND` or `tasks COMMAND --help`",
	} {
		if !strings.Contains(root, example) {
			t.Errorf("missing delimited example %s in top-level help: %q", example, root)
		}
	}
	for name, spec := range commands {
		if spec.hidden {
			continue
		}
		t.Run(name, func(t *testing.T) {
			out := readHelp(t, name, "--help")
			if short := readHelp(t, name, "-h"); short != out {
				t.Errorf("-h differs from --help: %q", short)
			}
			if alias := readHelp(t, "help", name); alias != out {
				t.Errorf("help %s differs from %s --help: %q", name, name, alias)
			}

			usage, _, _ := strings.Cut(out, "\n")
			if usage != "Usage: tasks "+strings.TrimSpace(name+" "+spec.usage) {
				t.Errorf("unexpected usage: %q", usage)
			}
			if row := fmt.Sprintf("  %-10s %s\n", name, spec.summary); !strings.Contains(root, row) {
				t.Errorf("missing command and summary in top-level help: %q", row)
			}
			if !strings.Contains(out, "\n\n"+spec.summary+"\n") {
				t.Errorf("missing summary in command help: %q", out)
			}

			tokens := strings.Fields(strings.NewReplacer("[", " ", "]", " ", "|", " ").Replace(usage))
			for option := range spec.options {
				if !slices.Contains(tokens, option) {
					t.Errorf("missing %s from usage: %q", option, usage)
				}
			}
			if len(spec.options) > 0 && !strings.Contains(out, "\nOptions:\n") {
				t.Error("missing Options section")
			}
		})
	}
}

func TestHelpUsageErrors(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "missing")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"unknown"}, "unknown command: unknown"},
		{[]string{"create", "list"}, "`tasks help` expects 1 positional argument(s)"},
	} {
		args := append([]string{"help"}, tc.args...)
		out, _, err := invoke(cwd, "", args...)
		if out != "" || err == nil || ExitCode(err) != 2 || err.Error() != tc.want {
			t.Errorf("%v: got output %q and error %v, want usage error %q and no output", args, out, err, tc.want)
		}
	}
}
