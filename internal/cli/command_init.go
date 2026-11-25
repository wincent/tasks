package cli

import (
	"context"
	"fmt"

	"github.com/wincent/tasks/internal/git"
)

func init() {
	commands.register(commandSpec{
		name: "init",
		min:  0, max: 0,
		summary: "Set up the local tasks checkout.",
		details: `Use the existing local tasks branch. If it is missing, follow the setup
instructions printed by init or see:
  ` + setupGuideURL + `

This command does not fetch, push, or reset the branch.`,
		run: withTaskStore(storeRequirements{}, func(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation) error {
			tasks, err := invocation.session.Ensure(git.Signature{})
			if err != nil {
				return err
			}
			defer tasks.Close()
			_, err = fmt.Fprintln(streams.Out, tasks.Path)
			return err
		}),
	})
}
