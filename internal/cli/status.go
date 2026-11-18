package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/wincent/tasks/internal/store"
)

type taskActivity struct {
	Number   int    `json:"number"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Worktree string `json:"worktree,omitempty"`
	Session  string `json:"session,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func describeActivity(directory string, prefix store.Prefix, task store.Task) (taskActivity, *taskSession, error) {
	result := taskActivity{Number: task.Number, Title: task.Title, Status: task.Status}
	record, err := readTaskSession(directory, task.Number)
	if err != nil {
		return result, nil, err
	}
	live, err := running(directory, prefix, task.Number, record)
	if err != nil {
		return result, nil, err
	}
	if record != nil {
		result.Worktree, result.Session, result.Reason = record.Worktree, record.SessionFile, record.Reason
	}
	switch {
	case live:
		result.Status = "active"
	case result.Status == "active":
		result.Status, result.Reason = "blocked", "previous run stopped; run this task again to continue"
	}
	return result, record, nil
}

func statusTask(directory string, tasks *store.Store, a arguments, streams IO) error {
	scanned, err := tasks.Scan()
	result := struct {
		Tasks   []taskActivity `json:"tasks"`
		Merging bool           `json:"merging"`
	}{Tasks: []taskActivity{}}
	result.Merging, err = joinMergeStatus(directory, err)
	for _, task := range scanned {
		activity, _, e := describeActivity(directory, tasks.Prefix, task)
		if e != nil {
			err = errors.Join(err, e)
		} else if activity.Status == "active" || activity.Status == "blocked" {
			result.Tasks = append(result.Tasks, activity)
		}
	}
	if a.flags["--json"] {
		return errors.Join(err, encode(streams.Out, result))
	}
	if result.Merging {
		fmt.Fprintln(streams.Out, "Merging into local main.")
	}
	for _, task := range result.Tasks {
		fmt.Fprintf(streams.Out, "%s  %s  %s\n", tasks.Prefix.ID(task.Number), task.Status, task.Title)
		if task.Worktree != "" {
			fmt.Fprintln(streams.Out, "  Checkout:", task.Worktree)
		}
		if task.Session != "" {
			fmt.Fprintln(streams.Out, "  Session:", task.Session)
		}
		if task.Reason != "" {
			fmt.Fprintln(streams.Out, "  "+task.Reason)
		}
	}
	if err == nil && !result.Merging && len(result.Tasks) == 0 {
		fmt.Fprintln(streams.Out, "No active runs, merges or blocked tasks.")
	}
	return err
}

func joinMergeStatus(directory string, err error) (bool, error) {
	held, e := lockHeld(filepath.Join(directory, "merge.lock"))
	return held, errors.Join(err, e)
}
