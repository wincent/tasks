package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wincent/tasks/internal/git"
)

type TaskUpdate struct {
	Commit git.ID
	Time   time.Time
}

// TaskHistory resolves last changes against one committed snapshot. It uses
// ticket numbers, not working filenames or Git's rename heuristics.
type TaskHistory struct {
	store   *Store
	head    git.ID
	commits map[git.ID]git.CommitInfo
	dirs    map[historyDir]map[int]git.TreeEntry
}

type historyDir struct {
	revision git.ID
	dir      string
}

func (store *Store) TaskHistory(head git.ID) *TaskHistory {
	return &TaskHistory{store: store, head: head, commits: make(map[git.ID]git.CommitInfo), dirs: make(map[historyDir]map[int]git.TreeEntry)}
}

func (h *TaskHistory) LastUpdate(ctx context.Context, number int) (TaskUpdate, error) {
	update, err := h.lastUpdate(ctx, number)
	if err != nil {
		return TaskUpdate{}, fmt.Errorf("%s: updated history unavailable: %w", h.store.Prefix.ID(number), err)
	}
	return update, nil
}

func (h *TaskHistory) lastUpdate(ctx context.Context, number int) (TaskUpdate, error) {
	revision := h.head
	current, err := h.record(revision, number)
	if err != nil {
		return TaskUpdate{}, err
	}
	if current.Name == "" {
		return TaskUpdate{}, errors.New("no committed task record")
	}
	for {
		if err := ctx.Err(); err != nil {
			return TaskUpdate{}, err
		}
		info, ok := h.commits[revision]
		if !ok {
			info, err = h.store.Repository.CommitInfo(revision)
			if err != nil {
				return TaskUpdate{}, err
			}
			h.commits[revision] = info
		}
		var same git.ID
		for _, parent := range info.Parents {
			prior, err := h.record(parent, number)
			if err != nil {
				return TaskUpdate{}, err
			}
			// An archive move or mode change is not a record edit. At a
			// merge, follow the first parent that already had the record;
			// if none did, the merge introduced this version.
			if current.Name == prior.Name && current.ID == prior.ID {
				same = parent
				break
			}
		}
		if same == (git.ID{}) {
			return TaskUpdate{Commit: revision, Time: info.Time}, nil
		}
		revision = same
	}
}

func (h *TaskHistory) record(revision git.ID, number int) (git.TreeEntry, error) {
	var found git.TreeEntry
	for _, dir := range []string{"", archiveBucket(number)} {
		key := historyDir{revision, dir}
		index, ok := h.dirs[key]
		if !ok {
			entries, err := h.store.entries(dir, revision)
			if err != nil {
				return found, err
			}
			index = make(map[int]git.TreeEntry)
			for _, entry := range entries {
				base := filepath.Base(entry.name)
				match := h.store.matchTicket(base)
				if match == nil {
					continue
				}
				n, err := strconv.Atoi(match[2])
				if err != nil {
					continue // Not a supported ticket number.
				}
				if _, duplicate := index[n]; duplicate {
					index[n] = git.TreeEntry{} // Mark duplicate numbers in this directory.
				} else {
					index[n] = entry.tree
				}
			}
			h.dirs[key] = index
		}
		if entry, exists := index[number]; exists {
			if entry.Name == "" || found.Name != "" {
				return found, fmt.Errorf("ambiguous committed record at %s", revision)
			}
			if entry.Mode != 0100644 && entry.Mode != 0100755 {
				return found, fmt.Errorf("not a regular committed record at %s", revision)
			}
			found = entry
		}
	}
	return found, nil
}
