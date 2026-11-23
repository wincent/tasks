package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "run", min: 1, max: 1, numbered: true,
		options: map[string]optionKind{"--worktree": valueOption, "--no-merge": flagOption, "--prompt": valueOption},
		usage:   "NUMBER [--no-merge] [--prompt TEXT] [--worktree PATH]",
		summary: "Run Pi non-interactively and merge into the recorded topic.",
		details: "Uses a separate checkout and resumes the saved Pi session when available.\n" +
			"If interrupted, run the same task again to continue. Pi must be on PATH.\n" +
			"Fresh checkouts start from the recorded topic (local main by default).\n" +
			"A successful run merges clean, committed work there without pushing.\n\n" +
			"Options:\n" +
			"  --no-merge       Keep the work for review. Use `tasks merge NUMBER` later.\n" +
			promptHelp +
			worktreeHelp,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			return launchTask(ctx, cwd, a, streams)
		},
	})
}
