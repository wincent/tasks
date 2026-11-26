package cli

import (
	"context"
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
	target, heading, action := "", "Comment", "Commented on"
	if a.action == "close" {
		lock, err := taskLock(invocation.session.Directory, a.prefix, a.number())
		if err != nil {
			return err
		}
		defer lock.Close()
		target, heading, action = "closed", "Closed", "Closed"
	}
	task, err := updateTask(invocation.session, a.prefix, a.defaultTarget, a.number(), target, heading, invocation.body, a.action, "")
	if err != nil {
		return err
	}
	return taskResult(streams, action, task.ID(), task.Title, task.Path)
}

func updateTask(session *store.Session, prefix store.Prefix, defaultTarget destination, n int, status, heading, note, action string, expectedTopic destination) (store.Task, error) {
	tasks, err := session.Preview(prefix)
	if err != nil {
		return store.Task{}, err
	}
	defer tasks.Close()
	// Bind the read and write to the same revision. A direct Git commit can
	// change the task even while we hold the metadata session lock.
	head, err := tasks.Head()
	if err != nil {
		return store.Task{}, err
	}
	task, err := tasks.ReadWorking(n)
	if err != nil {
		return store.Task{}, err
	}
	if expectedTopic != "" {
		if err := requireTaskDestination(task, defaultTarget, expectedTopic); err != nil {
			return store.Task{}, err
		}
	}
	content := task.Content
	if status != "" {
		content, err = store.WithStatus(content, status)
		if err != nil {
			return store.Task{}, err
		}
	}
	if strings.TrimSpace(note) != "" {
		content = strings.TrimSpace(content) + "\n\n## " + heading + " (" + time.Now().UTC().Format(time.RFC3339) + ")\n\n" + strings.TrimSpace(note) + "\n"
	}
	if content != task.Content {
		path, err := writeTask(session, tasks, head.ID, filepath.Base(task.Path), content, action+" "+task.ID())
		if err != nil {
			return store.Task{}, err
		}
		task.Path, task.Content, task.Status = path, content, status
	}
	return task, nil
}

func writeTask(session *store.Session, tasks *store.Store, expected git.ID, name, content, message string) (string, error) {
	if err := tasks.RequireClean(name); err != nil {
		return "", err
	}
	changes := map[string][]byte{name: []byte(content)}
	if err := tasks.ValidateContents(changes); err != nil {
		return "", err
	}
	opened, err := session.Ensure()
	if err != nil {
		return "", err
	}
	opened.Close()
	if _, err := session.CommitFiles(changes, store.CommitOptions{
		FromEnvironment: true, Message: "chore: " + message, ExpectedRefs: map[string]git.ID{store.Branch: expected},
	}); err != nil {
		return "", err
	}
	return filepath.Join(tasks.Path, name), nil
}
