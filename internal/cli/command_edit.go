package cli

import (
	"context"

	"github.com/wincent/tasks/internal/git"
)

func init() {
	commands.register(commandSpec{
		name: "edit", autoCorrect: allowCorrection,
		min: 1, max: 1,
		numbered: true,
		usage:    "NUMBER",
		summary:  "Edit a task's Markdown in your editor.",
		details: `Requires an interactive terminal. Uses $VISUAL, then $EDITOR, then vi.
Save and quit to commit the task edit. Leave it unchanged to cancel.
If saving fails, the command prints the path to the saved draft.`,
		run: func(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
			return editTask(ctx, cwd, repo, a.prefix, a.defaultTarget, a.number(), streams)
		},
	})
}
