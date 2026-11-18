package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"syscall"

	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/tail"
	"github.com/wincent/tasks/internal/terminal"
)

func init() {
	commands.register(commandSpec{
		name: "tail", min: 1, max: 1, numbered: true,
		usage:   "NUMBER",
		summary: "Follow a task's saved Pi session.",
		details: `Works after the task finishes. Press Ctrl-C to stop viewing without
interrupting the task.`,
		run: withTasks(tailTask),
	})
}

func tailTask(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
	n, directory := a.number(), invocation.directory
	task, err := tasks.Read(n)
	if err != nil {
		return err
	}
	record, err := readTaskSession(directory, n)
	if err != nil {
		return err
	}
	if record == nil {
		source, err := findCheckout(invocation.repository, cwd, a.prefix, n, "", nil)
		if err != nil {
			return err
		}
		record = &taskSession{Worktree: source.Paths.WorkDir}
	}
	if record.SessionFile == "" && record.Worktree != "" {
		record.SessionFile, err = legacySession(directory, record.Worktree, n)
		if err != nil {
			return err
		}
	}
	if record.SessionFile == "" {
		return fmt.Errorf("no saved Pi session for %s", task.ID())
	}
	file, err := openSession(*record)
	if err != nil {
		return fmt.Errorf("open saved Pi session %s: %w", filepath.Base(record.SessionFile), err)
	}
	defer file.Close()
	id := ""
	if record.SessionID != nil {
		id = *record.SessionID
	}
	fmt.Fprintln(streams.Err, tail.Safe(fmt.Sprintf("%s: %s\nFollowing %s (Ctrl-C stops the viewer)", task.ID(), task.Title, record.SessionFile)))
	err = tail.Follow(ctx, file, record.Worktree, id, terminal.Color(streams.Out), streams.Out, streams.Err)
	if errors.Is(err, syscall.EPIPE) {
		return nil
	}
	return err
}
