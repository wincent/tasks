package cli

import (
	"context"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "",
		min:  0, max: 0,
		hidden: true,
		run: withTasks(func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
			return listTasks(ctx, a, streams, invocation, tasks, listingOptions{recent: true})
		}),
	})
}
