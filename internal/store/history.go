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
	trees   map[git.ID]git.ID
	deltas  map[historyEdge]historyDelta

	// Full indexes are reserved for the head. Dense edges use point
	// lookups rather than keeping or repeatedly building whole indexes.
	headIndex map[string]map[int]git.TreeEntry
}

type historyEdge struct{ child, parent git.ID }

type historyDelta struct {
	changes  map[int]recordChange
	fallback bool
}

// Deltas run backwards from a child whose record was already validated.
// If no matching child entry changed, any added parent entry is a duplicate,
// even if the existing record was in a different directory.
type recordChange struct {
	prior     git.TreeEntry
	replaces  bool // The child's (known unique) record is in the delta.
	duplicate bool // Multiple matching records were added going backwards.
}

// Bound retained data per edge, even for a commit that touches every ticket.
const historyDeltaLimit = 128

func (store *Store) TaskHistory(head git.ID) *TaskHistory {
	return &TaskHistory{
		store: store, head: head,
		commits: make(map[git.ID]git.CommitInfo),
		trees:   make(map[git.ID]git.ID),
		deltas:  make(map[historyEdge]historyDelta),
	}
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
			prior, err := h.parentRecord(historyEdge{revision, parent}, number, current)
			if err != nil {
				return TaskUpdate{}, err
			}
			// An archive move or mode change is not a record edit. At a
			// merge, follow the first parent that already had the record;
			// if none did, the merge introduced this version.
			if current.Name == prior.Name && current.ID == prior.ID {
				same, current = parent, prior
				break
			}
		}
		if same == (git.ID{}) {
			return TaskUpdate{Commit: revision, Time: info.Time}, nil
		}
		revision = same
	}
}

func (h *TaskHistory) parentRecord(edge historyEdge, number int, current git.TreeEntry) (git.TreeEntry, error) {
	delta, ok := h.deltas[edge]
	if !ok {
		delta = h.readDelta(edge)
		h.deltas[edge] = delta
	}
	if delta.fallback {
		return h.record(edge.parent, number)
	}
	change := delta.changes[number]
	if change.duplicate || change.prior.Name != "" && (!change.replaces || !regularRecord(change.prior)) {
		// Use the directory lookup for errors, so ambiguity and mode checks
		// keep their original ordering (root before the archive bucket).
		return h.record(edge.parent, number)
	}
	if change.replaces {
		return change.prior, nil
	}
	return current, nil
}

func (h *TaskHistory) readDelta(edge historyEdge) historyDelta {
	fallback := historyDelta{fallback: true}
	before, err := h.rootTree(edge.parent)
	if err != nil {
		return fallback
	}
	after, err := h.rootTree(edge.child)
	if err != nil {
		return fallback
	}
	remaining := historyDeltaLimit
	compare := func(before, after git.ID) ([]git.TreeChange, bool) {
		changes, complete, err := h.store.Repository.TreeChanges(before, after, remaining)
		remaining -= len(changes)
		return changes, complete && err == nil
	}
	root, ok := compare(before, after)
	if !ok {
		return fallback
	}

	var delta historyDelta
	add := func(entry git.TreeEntry, dir string, prior bool) {
		number, ok := h.ticketNumber(entry.Name)
		if !ok || dir != "" && dir != archiveBucket(number) {
			return
		}
		if delta.changes == nil {
			delta.changes = make(map[int]recordChange)
		}
		change := delta.changes[number]
		if prior {
			change.duplicate = change.prior.Name != ""
			change.prior = entry
		} else {
			change.replaces = true
		}
		delta.changes[number] = change
	}

	// Tree IDs prune unchanged archive directories. Never descend into
	// unrelated root directories or noncanonical archive buckets.
	var oldArchive, newArchive git.TreeEntry
	for _, change := range root {
		add(change.Before, "", true)
		add(change.After, "", false)
		if change.Before.Name == archiveDirectory {
			oldArchive = change.Before
		}
		if change.After.Name == archiveDirectory {
			newArchive = change.After
		}
	}
	if !directoryOrAbsent(oldArchive) || !directoryOrAbsent(newArchive) {
		return fallback
	}
	buckets, ok := compare(oldArchive.ID, newArchive.ID)
	if !ok {
		return fallback
	}
	for _, bucket := range buckets {
		name := bucket.Before.Name
		if name == "" {
			name = bucket.After.Name
		}
		dir := archiveDirectory + "/" + name
		if !validArchiveBucket(dir) {
			continue
		}
		if !directoryOrAbsent(bucket.Before) || !directoryOrAbsent(bucket.After) {
			return fallback
		}
		entries, ok := compare(bucket.Before.ID, bucket.After.ID)
		if !ok {
			// The failure might be in a bucket unrelated to the requested
			// ticket. Let a literal lookup decide whether to report it.
			return fallback
		}
		for _, change := range entries {
			add(change.Before, dir, true)
			add(change.After, dir, false)
		}
	}
	return delta
}

func (h *TaskHistory) rootTree(revision git.ID) (git.ID, error) {
	if tree, ok := h.trees[revision]; ok {
		return tree, nil
	}
	tree, err := h.store.Repository.RootTreeID(revision)
	if err == nil {
		h.trees[revision] = tree
	}
	return tree, err
}

func directoryOrAbsent(entry git.TreeEntry) bool {
	return entry.Name == "" || entry.Mode == 0040000
}

func regularRecord(entry git.TreeEntry) bool {
	return entry.Mode == 0100644 || entry.Mode == 0100755
}

func (h *TaskHistory) ticketNumber(name string) (int, bool) {
	match := h.store.matchTicket(name)
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[2])
	return number, err == nil
}

func (h *TaskHistory) record(revision git.ID, number int) (git.TreeEntry, error) {
	if revision != h.head {
		root, err := h.rootTree(revision)
		if err != nil {
			return git.TreeEntry{}, err
		}
		entry, err := h.store.Repository.HistoryRecord(root, archiveBucket(number), h.store.Prefix.ID(number)+" ")
		if err != nil {
			return entry, fmt.Errorf("at %s: %w", revision, err)
		}
		return entry, nil
	}
	if h.headIndex == nil {
		h.headIndex = make(map[string]map[int]git.TreeEntry)
	}
	var found git.TreeEntry
	for _, dir := range []string{"", archiveBucket(number)} {
		index, ok := h.headIndex[dir]
		if !ok {
			entries, err := h.store.entries(dir, revision)
			if err != nil {
				return found, err
			}
			index = make(map[int]git.TreeEntry)
			for _, entry := range entries {
				base := filepath.Base(entry.name)
				n, ok := h.ticketNumber(base)
				if !ok {
					continue
				}
				if _, duplicate := index[n]; duplicate {
					index[n] = git.TreeEntry{} // Mark duplicate numbers in this directory.
				} else {
					index[n] = entry.tree
				}
			}
			h.headIndex[dir] = index
		}
		if entry, exists := index[number]; exists {
			if entry.Name == "" || found.Name != "" {
				return found, fmt.Errorf("ambiguous committed record at %s", revision)
			}
			if !regularRecord(entry) {
				return found, fmt.Errorf("not a regular committed record at %s", revision)
			}
			found = entry
		}
	}
	return found, nil
}
