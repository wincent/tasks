package cli

import "context"

func init() {
	commands.register(commandSpec{
		name: "help", max: 1,
		usage:   "[COMMAND]",
		summary: "Show help for tasks or a specific command.",
		validate: func(a arguments) error {
			if len(a.positional) == 1 {
				name := a.positional[0]
				if spec, ok := commands[name]; !ok || spec.hidden {
					return unknownCommand(name)
				}
			}
			return nil
		},
		run: func(_ context.Context, _ string, a arguments, streams IO) error {
			if len(a.positional) == 1 {
				return help(a.positional[0], streams.Out)
			}
			return help("", streams.Out)
		},
	})
}
