package cli

import (
	"context"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	status := commandSpec{
		name: "status", min: 0, max: 0, options: map[string]optionKind{"--json": flagOption},
		usage:   "[--json]",
		summary: "Show running agents, merges and tasks that need attention.",
		details: `Options:
  --json  Print the status report as JSON.`,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			return statusTask(invocation.directory, tasks, a, streams)
		}),
	}
	commands.register(status)

	status.name = "st"
	status.summary = "Alias for `tasks status`."
	commands.register(status)
}
