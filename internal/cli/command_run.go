package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "run", min: 1, max: 1, numbered: true,
		options: map[string]optionKind{"--worktree": valueOption, "--no-merge": flagOption},
		usage:   "NUMBER [--no-merge] [--worktree PATH]",
		summary: "Run Pi non-interactively and merge into local main.",
		details: `Uses a separate checkout and resumes the saved Pi session when available.
If interrupted, run the same task again to continue. Pi must be on PATH.
A successful run merges clean, committed work without pushing.

Options:
  --no-merge       Keep the work for review. Use 'tasks merge NUMBER' later.
` + worktreeHelp,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			return launchTask(ctx, cwd, a, streams)
		},
	})
}
