package cli

import (
	"fmt"
	"io"
	"strings"
)

const bodyUsage = "[--body TEXT | --body-file FILE]"

const bodyHelp = `Options:
  --body TEXT       Use the given text.
  --body-file FILE  Read text from FILE, or use - to read from standard input.

Without either option, open your editor in an interactive terminal.`

const worktreeHelp = "  --worktree PATH  Select an existing checkout for this task."

const listingHelp = `In text output, (M) marks modified tasks and (?) marks untracked tasks.
If any task files are invalid, valid results still appear on standard output.
Errors go to standard error and the command exits with status 1.`

type usageError struct{ err error }

func (err *usageError) Error() string { return err.err.Error() }
func (err *usageError) Unwrap() error { return err.err }

func help(subject string, out io.Writer) error {
	var text strings.Builder
	if spec, ok := commands[subject]; ok && !spec.hidden {
		fmt.Fprintf(&text, "Usage: tasks %s", spec.name)
		if spec.usage != "" {
			fmt.Fprintf(&text, " %s", spec.usage)
		}
		fmt.Fprintf(&text, "\n\n%s\n", spec.summary)
		if spec.details != "" {
			fmt.Fprintf(&text, "\n%s\n", spec.details)
		}
	} else {
		fmt.Fprint(&text, "Usage: tasks [COMMAND]\n\nCommands:\n")
		for _, spec := range commands.visible() {
			fmt.Fprintf(&text, "  %-10s %s\n", spec.name, spec.summary)
		}
		fmt.Fprint(&text, `
Without a command, show recent tasks. Use 123 or PREFIX-123. The prefix
comes from .tasks.yml on local main.

Start with 'tasks create "Task title"'. Use the number in the printed
path with 'tasks run NUMBER'. To review first, add --no-merge, then
use 'tasks merge NUMBER' when ready.

Use 'tasks help COMMAND' or 'tasks COMMAND --help' for options.
For setup, see README.md in the github.com/wincent/tasks source repository.
`)
	}
	_, err := io.WriteString(out, text.String())
	return err
}
