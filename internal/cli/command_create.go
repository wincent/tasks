package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "create", min: 1, max: 1, options: bodyOptions(),
		usage:   "TITLE " + bodyUsage,
		summary: "Create a task and print its file path.",
		details: `Quote a title that contains spaces.

` + bodyHelp,
		run: withTaskStore(storeRequirements{mutation: true, body: true, prepare: func(a arguments) error {
			if !store.ValidTitle(strings.TrimSpace(a.positional[0])) {
				return errors.New("title must be nonempty, single-line text without path separators")
			}
			return nil
		}}, createTask),
	})
}

func createTask(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
	session := invocation.session
	n, err := session.NextTaskNumber(a.prefix)
	if err != nil {
		return err
	}
	title := strings.TrimSpace(a.positional[0])
	id := a.prefix.ID(n)
	name := fmt.Sprintf("%s %s.md", id, title)
	content := fmt.Sprintf("# %s: %s\n\nStatus: open\n\n## Description\n\n%s\n", id, title, invocation.body)
	path, err := writeTask(session, a.prefix, name, content, "create "+id)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, filepath.Clean(path))
	return err
}
