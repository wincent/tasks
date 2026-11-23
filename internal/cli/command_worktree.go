package cli

import (
	"context"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "worktree", autoCorrect: allowCorrection,
		min: 1, max: 1,
		numbered: true,
		usage:    "NUMBER",
		summary:  "Print the path to a task's checkout.",
		details: `The path is relative to the current directory. If no checkout exists,
nothing is printed. This command does not create or switch checkouts.`,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			if _, err := tasks.Read(a.number()); err != nil {
				return err
			}
			return printTaskWorktree(invocation.repository, cwd, invocation.directory, a.prefix, a.number(), streams.Out)
		}),
	})
}
