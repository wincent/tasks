package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func init() {
	options := bodyOptions()
	options["--topic"] = valueOption
	commands.register(commandSpec{
		name: "create", autoCorrect: usesBodyEditor, min: 1, max: 1, options: options,
		usage:    "TITLE " + topicUsage + " " + bodyUsage,
		validate: validateTopicOption,
		summary:  "Create a task.",
		details: `Quote a title that contains spaces. On a terminal, print a creation
confirmation; when stdout is redirected, print the task's file path.

` + "Options:\n" + topicHelp + "\n" + bodyOptionsHelp,
		run: func(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
			_, err := createTask(ctx, cwd, repo, a, streams, true)
			return err
		},
	})
}

func createTask(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO, withBody bool) (int, error) {
	var number int
	create := withTaskStore(storeRequirements{mutation: true, body: withBody, prepare: func(a arguments) error {
		if !store.ValidTitle(strings.TrimSpace(a.positional[0])) {
			return errors.New("title must be nonempty, single-line text without path separators")
		}
		return nil
	}}, func(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
		session := invocation.session
		tasks, err := session.Preview(a.prefix)
		if err != nil {
			return err
		}
		defer tasks.Close()
		head, err := tasks.Head()
		if err != nil {
			return err
		}
		n, err := session.NextTaskNumber(a.prefix)
		if err != nil {
			return err
		}
		title := strings.TrimSpace(a.positional[0])
		id := a.prefix.ID(n)
		name := fmt.Sprintf("%s %s.md", id, title)
		description := invocation.body
		if !withBody {
			description = "None provided."
		}
		var topicLine string
		if topic := a.value("--topic"); topic != "" && destination(topic) != a.defaultTarget {
			topicLine = "\nTopic: " + topic
		}
		content := fmt.Sprintf("# %s: %s\n\nStatus: open%s\n\n## Description\n\n%s\n", id, title, topicLine, description)
		path, err := writeTask(session, tasks, head.ID, name, content, "create "+id)
		if err != nil {
			return err
		}
		number = n
		return taskResult(streams, "Created", id, title, filepath.Clean(path))
	})
	err := create(ctx, cwd, repo, a, streams)
	return number, err
}
