package cli

import (
	"context"
	"errors"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "list",
		min:  0, max: 1,
		options: map[string]optionKind{
			"--json": flagOption, "--verbose": flagOption,
			"--status": valueOption, "--include-archived": flagOption, "--query": valueOption,
		},
		validate: func(a arguments) error {
			if len(a.positional) > 0 && len(a.values["--status"]) > 0 {
				return errors.New("positional status and --status are mutually exclusive")
			}
			status := a.value("--status")
			if len(a.positional) > 0 {
				status = a.positional[0]
			}
			return validateStatus(status)
		},
		usage:   "[S|--status S] [--query Q --include-archived --json --verbose]",
		summary: "List tasks by status.",
		details: `By default, show open, active and blocked tasks in task number order.
Status (S): open, active, blocked, done, merged, closed, or all.
Archived tickets are hidden unless --include-archived is passed.

Options:
  --status STATUS     Filter by status. You can also pass STATUS on its own.
  --include-archived  Include archived tickets in the status filter.
  -q, --query EXPR    Narrow these results. Use 'list all' for all statuses.
  --json              Print the results as JSON.
  -v, --verbose       Include saved checkout and Pi session details.

` + queryHelp + listingHelp,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			status := a.value("--status")
			if len(a.positional) > 0 {
				status = a.positional[0]
			}
			return listTasks(ctx, a, streams, invocation, tasks, listingOptions{status: status})
		}),
	})
}
