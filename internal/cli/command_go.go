package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/wincent/tasks/internal/git"
)

// Reserve reference-shaped titles even if the number is out of range.
var goTaskReference = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9]*-)?([0-9]+)$`)

func validateGo(a arguments) error {
	match := goTaskReference.FindStringSubmatch(strings.TrimSpace(a.positional[0]))
	if match == nil {
		return nil
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n == 0 {
		return errors.New("`tasks go` expects a title, not a task reference; use `tasks run NUMBER` with a valid positive task number")
	}
	return fmt.Errorf("`tasks go` expects a title, not a task reference; use `tasks run %d` instead", n)
}

func init() {
	commands.register(commandSpec{
		name: "go", min: 1, max: 1,
		options: map[string]optionKind{"--topic": valueOption, "--model": valueOption},
		usage:   "TITLE [--model MODEL] " + topicUsage,
		summary: "Create and run a task from its title alone.",
		details: "Quote a title that contains spaces. Create a task with \"None provided.\" as its\n" +
			"description, then run Pi non-interactively and merge successful work into the\n" +
			"recorded topic (local main by default). Pi must be on PATH.\n" +
			"To run an existing task, use `tasks run NUMBER`.\n\nOptions:\n" + modelHelp + topicHelp,
		validate: func(a arguments) error {
			if err := validateGo(a); err != nil {
				return err
			}
			return validateTopicOption(a)
		},
		run: func(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
			model, hasModel, err := resolveModelOption(a, streams)
			if err != nil {
				return err
			}
			n, err := createTask(ctx, cwd, repo, a, streams, false)
			if err != nil {
				return err
			}
			// Creation has released the metadata lock that launchTask needs.
			return launchTaskWithModel(ctx, cwd, repo, arguments{
				action: "run",
				prefix: a.prefix,
				tasks:  []taskArgument{{number: n}},
			}, model, hasModel, streams)
		},
	})
}
