package cli

import (
	"context"
	"fmt"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "check",
		min:  0, max: 0,
		summary: "Check task files for errors.",
		details: `Checks filenames, task numbers, file types and statuses. Does not edit
task files or run application tests.`,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			scanned, err := tasks.Scan()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(streams.Out, "Checked %d task(s): OK\n", len(scanned))
			return err
		}),
	})
}
