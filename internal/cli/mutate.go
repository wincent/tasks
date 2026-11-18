package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func bodyOptions() map[string]optionKind {
	return map[string]optionKind{"--body": valueOption, "--body-file": valueOption}
}

func addNote(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
	target, title := "", "Comment"
	if a.action == "close" {
		lock, err := taskLock(invocation.session.Directory, a.prefix, a.number())
		if err != nil {
			return err
		}
		defer lock.Close()
		target, title = "closed", "Closed"
	}
	path, err := updateTask(invocation.session, a.prefix, a.number(), target, title, invocation.body, a.action)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(streams.Out, path)
	return err
}

func updateTask(session *store.Session, prefix store.Prefix, n int, status, heading, note, action string) (string, error) {
	tasks, err := session.Preview(prefix)
	if err != nil {
		return "", err
	}
	defer tasks.Close()
	task, err := tasks.Read(n)
	if err != nil {
		return "", err
	}
	content := task.Content
	if status != "" {
		content, err = store.WithStatus(content, status)
		if err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(note) != "" {
		content = strings.TrimSpace(content) + "\n\n## " + heading + " (" + time.Now().UTC().Format(time.RFC3339) + ")\n\n" + strings.TrimSpace(note) + "\n"
	}
	if content == task.Content {
		return task.Path, nil
	}
	return writeTask(session, prefix, filepath.Base(task.Path), content, action+" "+task.ID())
}

func writeTask(session *store.Session, prefix store.Prefix, name, content, message string) (string, error) {
	tasks, err := session.Preview(prefix)
	if err != nil {
		return "", err
	}
	defer tasks.Close()
	if err := tasks.RequireClean(name); err != nil {
		return "", err
	}
	head, err := tasks.Head()
	if err != nil {
		return "", err
	}
	changes := map[string][]byte{name: []byte(content)}
	if err := tasks.ValidateContents(changes); err != nil {
		return "", err
	}
	opened, err := session.Ensure(git.Signature{})
	if err != nil {
		return "", err
	}
	opened.Close()
	if _, err := session.CommitFiles(changes, store.CommitOptions{
		FromEnvironment: true, Message: "chore: " + message, ExpectedRefs: map[string]git.ID{store.Branch: head.ID},
	}); err != nil {
		return "", err
	}
	return filepath.Join(tasks.Path, name), nil
}
