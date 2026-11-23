package cli

import (
	"fmt"
	"io"
	"strings"
)

const bodyUsage = "[--body TEXT | --body-file FILE]"

const bodyHelp = "Options:\n" + bodyOptionsHelp

const bodyOptionsHelp = `  --body TEXT       Use the given text.
  --body-file FILE  Read text from FILE, or use - to read from standard input.

Without either option, open your editor in an interactive terminal.
Leave the draft empty to cancel.`

const topicUsage = "[--topic BRANCH]"

const topicHelp = "  --topic BRANCH    Record a local branch to start from and merge into.\n" +
	"                    If missing, create it from local main on first launch."

const promptHelp = "  --prompt TEXT    Append instructions to Pi for this run only.\n"

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
		fmt.Fprint(&text, "\nWithout a command, show recent tasks. Use 123 or PREFIX-123. The prefix\n"+
			"comes from .tasks.yml on local main.\n\n"+
			"For a quick task, use `tasks go \"Task title\"` to create and run it.\n"+
			"To write a description first, use `tasks create \"Task title\"`, then\n"+
			"`tasks run NUMBER`. To review before merging, use `tasks run NUMBER --no-merge`,\n"+
			"then use `tasks merge NUMBER` when ready.\n"+
			"Use `--topic BRANCH` with create or go to target a branch other than main.\n\n"+
			"Options:\n"+
			"  -h, --help  Show this help.\n"+
			"  --version   Print the version and exit.\n\n"+
			"Use `tasks help COMMAND` or `tasks COMMAND --help` for options.\n"+
			"For setup, see README.md in the github.com/wincent/tasks source repository.\n")
	}
	_, err := io.WriteString(out, text.String())
	return err
}
