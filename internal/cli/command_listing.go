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

type listingOptions struct {
	status, pattern string
	recent          bool
}

func listTasks(ctx context.Context, a arguments, streams IO, invocation storeInvocation, tasks *store.Store, listing listingOptions) (err error) {
	directory, root := invocation.directory, invocation.root
	matcher := queryMatcher{expr: a.query}
	if a.query.NeedsUpdated() {
		head, err := tasks.Head()
		if err != nil {
			return err
		}
		matcher.history = tasks.TaskHistory(head.ID)
	}
	scanned, scanErr := tasks.Scan(a.flags["--include-archived"])
	// Preserve the normal error/exit-status contract while making valid results
	// available on stdout. The executable reports errors on stderr, not in JSON.
	defer func() {
		if scanErr != nil {
			err = errors.Join(err, fmt.Errorf("incomplete task results: %w", scanErr))
		}
	}()
	status, pattern := listing.status, listing.pattern
	if status != "" && status != "all" && !store.ValidStatus(status) {
		return fmt.Errorf("invalid status: %s", status)
	}
	selected := []store.Task{}
	verbose := a.flags["--verbose"]
	for _, task := range scanned {
		if pattern != "" && !strings.Contains(strings.ToLower(task.Title), pattern) {
			continue
		}
		info, record, err := describeActivity(directory, tasks.Prefix, task)
		if err != nil {
			return err
		}
		if !listing.recent && status != "all" && status != info.Status &&
			!(status == "" && (info.Status == "open" || info.Status == "active" || info.Status == "blocked")) {
			continue
		}
		match, _, err := matcher.match(ctx, task)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		task.Status = info.Status
		if verbose && record != nil {
			relative, err := filepath.Rel(root, record.Worktree)
			if err != nil {
				return err
			}
			task.Activity = []store.Activity{{Worktree: relative, SessionID: record.SessionID, SessionFile: record.SessionFile}}
		}
		selected = append(selected, task)
	}
	if a.flags["--json"] {
		return encode(streams.Out, selected)
	}
	p := terminalPresentation(streams.Out)
	if listing.recent {
		fmt.Fprintln(streams.Out, p.paint("Recent tasks:", "1"))
		if len(selected) > 10 {
			selected = selected[len(selected)-10:]
		}
		for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
			selected[i], selected[j] = selected[j], selected[i]
		}
	}
	markers := map[string]string{}
	if !tasks.Prospective() {
		changes, err := tasks.Repository.Status(true)
		if err != nil {
			return err
		}
		for _, change := range changes {
			marker := "M"
			if change.Status&(git.WorktreeNew|git.Ignored) != 0 {
				marker = "?"
			}
			markers[change.Path] = marker
		}
	}
	// IDs and statuses are ASCII. Measure the visible rows before adding
	// styling, and keep tab separators when the output is redirected.
	var idWidth, statusWidth int
	if p.interactive {
		for _, task := range selected {
			idWidth = max(idWidth, len(task.ID()))
			statusWidth = max(statusWidth, len(task.Status))
		}
	}
	for _, task := range selected {
		id := task.ID()
		afterID, afterStatus := "\t", "\t"
		if p.interactive {
			afterID = strings.Repeat(" ", idWidth-len(id)+2)
			afterStatus = strings.Repeat(" ", statusWidth-len(task.Status)+2)
		}
		suffix := ""
		if task.Archived {
			suffix = " (archived)"
		}
		if marker := markers[tasks.RelativePath(task)]; marker != "" {
			suffix += " (" + p.changeMarker(marker) + ")"
		}
		fmt.Fprintf(streams.Out, "%s%s%s%s%s%s\n", p.paint(id, "4"), afterID, p.status(task.Status), afterStatus, p.paint(task.Title, "3"), suffix)
		for _, activity := range task.Activity {
			suffix := ""
			if activity.SessionFile != "" {
				suffix = "; Pi session: " + activity.SessionFile
			} else if activity.SessionID != nil {
				suffix = "; Pi session: " + *activity.SessionID
			}
			fmt.Fprintf(streams.Out, "  Worktree: %s%s\n", activity.Worktree, suffix)
		}
	}
	if len(selected) == 0 && scanErr == nil {
		if a.query != nil || pattern != "" {
			fmt.Fprintln(streams.Err, "No tasks match the specified filters.")
		} else if listing.recent {
			fmt.Fprintln(streams.Out, "No tasks yet. Create one with: `tasks create \"Task title\"`")
		} else if status == "" || status == "open" {
			active, blocked := false, false
			for _, task := range scanned {
				active = active || task.Status == "active"
				blocked = blocked || task.Status == "blocked"
			}
			if active || blocked {
				fmt.Fprintln(streams.Err, "There are no available open tasks.")
				if active {
					fmt.Fprintln(streams.Err, "Run `tasks list active --verbose` to see active work.")
				}
				if blocked {
					fmt.Fprintln(streams.Err, "Run `tasks list blocked` to see tasks waiting for a decision or other input.")
				}
			} else {
				fmt.Fprintln(streams.Err, "There are no uncompleted tasks.\nRun `tasks list done` or `tasks list merged` to see completed tasks.")
			}
		}
	}
	if listing.recent {
		fmt.Fprintln(streams.Out)
		return help("", streams.Out)
	}
	return nil
}
