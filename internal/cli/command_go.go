package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
		return errors.New("tasks go expects a title, not a task reference; use 'tasks run NUMBER' with a valid positive task number")
	}
	return fmt.Errorf("tasks go expects a title, not a task reference; use 'tasks run %d' instead", n)
}

func init() {
	commands.register(commandSpec{
		name: "go", min: 1, max: 1,
		usage:   "TITLE",
		summary: "Create and run a task from its title alone.",
		details: `Quote a title that contains spaces. Create a task with "None provided." as its
description, then run Pi non-interactively and merge successful work into local
main. Pi must be on PATH. To run an existing task, use 'tasks run NUMBER'.`,
		validate: validateGo,
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
