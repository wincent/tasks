package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "search",
		min:  1, max: 1,
		options: map[string]optionKind{"--json": flagOption, "--verbose": flagOption, "-v": flagOption, "--status": valueOption},
		validate: func(a arguments) error {
			if err := validateStatus(a.value("--status")); err != nil {
				return err
			}
			if a.positional[0] == "" {
				return errors.New("search requires a nonempty query")
			}
			return nil
		},
		usage:   "QUERY [--status STATUS] [--json] [-v | --verbose]",
		summary: "Search task titles.",
		details: `Matches part of a title, ignoring case. Does not search descriptions.
Quote a query that contains spaces. For a query that begins with a dash,
put -- before it (for example, tasks search -- "-draft").

Options:
  --status STATUS  Filter by status (see 'tasks list --help' for values).
                   The default is all statuses.
  --json           Print the results as JSON.
  -v, --verbose    Include saved checkout and Pi session details if available.

If nothing matches, the command succeeds with no results (or [] in JSON).

` + listingHelp,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			status := a.value("--status")
			if status == "" {
				status = "all"
			}
			return listTasks(a, streams, invocation, tasks, listingOptions{status: status, query: strings.ToLower(a.positional[0])})
		}),
	})
}
