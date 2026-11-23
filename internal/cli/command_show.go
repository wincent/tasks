package cli

import (
	"context"
	"fmt"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name:     "show",
		numbered: true,
		min:      0, max: 1,
		options: map[string]optionKind{"--raw": flagOption},
		usage:   "[NUMBER] [--raw]",
		summary: "Show the Markdown for a task.",
		details: `Omit NUMBER to show the highest-numbered task outside the archive.
Pass NUMBER to show a specific task, even if archived. On a terminal, use
Glow if available; otherwise print Markdown.

Options:
  --raw  Print the original Markdown even on a terminal.`,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			var task store.Task
			var err error
			if len(a.positional) == 0 {
				task, err = tasks.ReadLatest()
			} else {
				task, err = tasks.Read(a.number())
			}
			if err != nil {
				return err
			}
			content := task.Content
			if task.Archived {
				notice := task.ID() + " is archived."
				if a.flags["--raw"] {
					fmt.Fprintln(streams.Err, notice)
				} else {
					content = "> " + notice + "\n\n" + content
				}
			}
			return renderMarkdown(ctx, cwd, content, a.flags["--raw"], streams)
		}),
	})
}
