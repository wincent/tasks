package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "pi", min: 1, max: 1, numbered: true,
		options: map[string]optionKind{"--worktree": valueOption},
		usage:   "NUMBER [--worktree PATH]",
		summary: "Open a task in interactive Pi without merging.",
		details: "Requires a terminal and Pi on PATH. Resumes the saved session if available.\n" +
			"When ready, use `tasks merge NUMBER` to merge committed work.\n\n" +
			"Options:\n" + worktreeHelp,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			return launchTask(ctx, cwd, a, streams)
		},
	})
}
