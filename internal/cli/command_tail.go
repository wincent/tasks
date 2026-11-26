package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/tail"
)

func init() {
	commands.register(commandSpec{
		name: "tail", autoCorrect: allowCorrection, min: 0, max: 1, numbered: true,
		usage:   "[NUMBER]",
		summary: "Follow a task's saved Pi session.",
		details: `Omit NUMBER to follow the only active task. Works after the task finishes.
Press Ctrl-C to stop viewing without interrupting the task.`,
		run: withTasks(tailTask),
	})
}

func inferActiveTask(directory string, tasks *store.Store) (int, error) {
	scanned, err := tasks.Scan(false)
	if err != nil {
		return 0, fmt.Errorf("cannot infer active task; use `tasks tail NUMBER`: %w", err)
	}
	var active []store.Task
	for _, task := range scanned {
		activity, _, err := describeActivity(directory, tasks.Prefix, task)
		if err != nil {
			return 0, fmt.Errorf("cannot infer active task; use `tasks tail NUMBER`: %w", err)
		}
		if activity.Status == "active" {
			active = append(active, task)
		}
	}
	switch len(active) {
	case 0:
		return 0, errors.New("no active tasks; use `tasks tail NUMBER`")
	case 1:
		return active[0].Number, nil
	default:
		ids := make([]string, len(active))
		for i, task := range active {
			ids[i] = task.ID()
		}
		return 0, fmt.Errorf("multiple active tasks (%s); use `tasks tail NUMBER`", strings.Join(ids, ", "))
	}
}

func tailTask(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
	n, directory := a.number(), invocation.directory
	if n == 0 {
		var err error
		n, err = inferActiveTask(directory, tasks)
		if err != nil {
			return err
		}
	}
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
	err = tail.Follow(ctx, file, record.Worktree, id, streams.color(streams.Out), streams.Out, streams.Err)
	if errors.Is(err, syscall.EPIPE) {
		return nil
	}
	return err
}
