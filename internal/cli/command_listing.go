package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

type listingOptions struct {
	status, query string
	recent        bool
}

func listTasks(a arguments, streams IO, invocation storeInvocation, tasks *store.Store, listing listingOptions) (err error) {
	directory, root := invocation.directory, invocation.root
	scanned, scanErr := tasks.Scan()
	// Preserve the normal error/exit-status contract while making valid results
	// available on stdout. The executable reports errors on stderr, not in JSON.
	defer func() {
		if scanErr != nil {
			err = errors.Join(err, fmt.Errorf("incomplete task results: %w", scanErr))
		}
	}()
	status, query := listing.status, listing.query
	if status != "" && status != "all" && !store.ValidStatus(status) {
		return fmt.Errorf("invalid status: %s", status)
	}
	selected := []store.Task{}
	verbose := a.flags["--verbose"] || a.flags["-v"]
	for _, task := range scanned {
		if query != "" && !strings.Contains(strings.ToLower(task.Title), query) {
			continue
		}
		info, record, err := describeActivity(directory, tasks.Prefix, task)
		if err != nil {
			return err
		}
		task.Status = info.Status
		if verbose && record != nil {
			relative, err := filepath.Rel(root, record.Worktree)
			if err != nil {
				return err
			}
			task.Activity = []store.Activity{{Worktree: relative, SessionID: record.SessionID, SessionFile: record.SessionFile}}
		}
		if listing.recent || status == "all" || status == task.Status || (status == "" && (task.Status == "open" || task.Status == "active" || task.Status == "blocked")) {
			selected = append(selected, task)
		}
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
	for _, task := range selected {
		suffix := ""
		if marker := markers[filepath.Base(task.Path)]; marker != "" {
			suffix = " (" + p.changeMarker(marker) + ")"
		}
		fmt.Fprintf(streams.Out, "%s\t%s\t%s%s\n", p.paint(task.ID(), "4"), p.status(task.Status), p.paint(task.Title, "3"), suffix)
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
		if query != "" {
			fmt.Fprintln(streams.Err, "No tasks match the search query and status filter.")
		} else if listing.recent {
			fmt.Fprintln(streams.Out, "No tasks yet. Create one with: tasks create \"Task title\"")
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
				fmt.Fprintln(streams.Err, "There are no uncompleted tasks.\nRun `tasks list --status done` to see completed tasks.")
			}
		}
	}
	if listing.recent {
		fmt.Fprintln(streams.Out)
		return help("", streams.Out)
	}
	return nil
}
