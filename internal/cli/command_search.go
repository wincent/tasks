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
		positionalError: "`tasks search` requires one literal title pattern (PATTERN)",
		options:         map[string]optionKind{"--json": flagOption, "--verbose": flagOption, "--status": valueOption, "--query": valueOption},
		validate: func(a arguments) error {
			if err := validateStatus(a.value("--status")); err != nil {
				return err
			}
			if a.positional[0] == "" {
				return errors.New("`tasks search` requires a nonempty literal title pattern (PATTERN)")
			}
			return nil
		},
		usage:   "PATTERN [--status S] [--query EXPR] [--json] [--verbose]",
		summary: "Search task titles.",
		details: "Matches part of a title, ignoring case. Does not search descriptions.\n" +
			"PATTERN is literal text. Quote it if it contains spaces. For a leading dash,\n" +
			"put `--` before it (for example, `tasks search -- \"-draft\"`).\n\n" +
			"Options:\n" +
			"  --status STATUS  Filter by status (see `tasks list --help` for values).\n" +
			"                   The default is all statuses.\n" +
			"  -q, --query EXPR Narrow results that match PATTERN and the status filter.\n" +
			"  --json           Print the results as JSON.\n" +
			"  -v, --verbose    Include saved checkout and Pi session details if available.\n\n" +
			"If nothing matches, the command succeeds with no results (or [] in JSON).\n\n" +
			queryHelp + listingHelp,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			status := a.value("--status")
			if status == "" {
				status = "all"
			}
			return listTasks(ctx, a, streams, invocation, tasks, listingOptions{status: status, pattern: strings.ToLower(a.positional[0])})
		}),
	})
}
