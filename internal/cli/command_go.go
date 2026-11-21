package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "go", min: 1, max: 1,
		usage:   "TITLE",
		summary: "Create and run a task from its title alone.",
		details: `Quote a title that contains spaces. Create a task with "None provided." as its
description, then run Pi non-interactively and merge successful work into local
main. Pi must be on PATH. To retry a failed run, use 'tasks run NUMBER'.`,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			n, err := createTask(ctx, cwd, a, streams, false)
			if err != nil {
				return err
			}
			// Creation has released the metadata lock that launchTask needs.
			return launchTask(ctx, cwd, arguments{action: "run", prefix: a.prefix, taskNumber: n}, streams)
		},
	})
}
