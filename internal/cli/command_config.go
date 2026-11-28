package cli

import (
	"context"
	"io"

	"github.com/wincent/tasks/internal/git"
)

func init() {
	commands.register(commandSpec{
		name: "config", autoCorrect: allowCorrection,
		min: 0, max: 0,
		summary: "Show parsed user configuration.",
		details: `Reads $XDG_CONFIG_HOME/tasks/config.yml when XDG_CONFIG_HOME is an
absolute path, or ~/.config/tasks/config.yml otherwise. Prints nothing if
the file is missing or has no configured settings, and exits with an error
if it is invalid.`,
		run: func(_ context.Context, _ string, _ *git.Repository, _ arguments, streams IO) error {
			cfg, err := loadUserConfig(streams)
			if err != nil {
				return err
			}
			formatted := formatUserConfig(cfg)
			if formatted == "" {
				return nil
			}
			_, err = io.WriteString(streams.Out, formatted)
			return err
		},
	})
}
