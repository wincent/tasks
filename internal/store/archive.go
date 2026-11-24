package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
)

const archiveDirectory = "archive"
const archiveBucketSize = 1000

func archiveBucket(number int) string {
	return archiveDirectory + "/" + strconv.Itoa(number-number%archiveBucketSize)
}

// inTicketLocation accepts root tickets and tickets in their canonical
// archive bucket, but not lookalikes under storage or other directories.
func inTicketLocation(path string, number int) bool {
	dir := filepath.Dir(path)
	return dir == "." || dir == archiveBucket(number)
}

func validArchiveBucket(dir string) bool {
	n, err := strconv.Atoi(filepath.Base(dir))
	return err == nil && n >= 0 && dir == archiveBucket(n)
}

func (store *Store) archiveEntries(revision git.ID) ([]taskEntry, error) {
	buckets, err := store.entries(archiveDirectory, revision)
	if err != nil {
		return nil, err
	}
	var result []taskEntry
	var failures []error
	for _, bucket := range buckets {
		base := filepath.Base(bucket.name)
		if strings.HasPrefix(base, ".") {
			continue
		}
		if !validArchiveBucket(bucket.name) || !bucket.directory {
			failures = append(failures, fmt.Errorf("%s: expected an archive bucket directory", filepath.Join(store.Path, bucket.name)))
			continue
		}
		entries, err := store.entries(bucket.name, revision)
		result = append(result, entries...)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return result, errors.Join(failures...)
}

type archiveMove struct {
	prefix      Prefix
	number      int
	source      string
	destination string
}

func (move *archiveMove) directory() string {
	if dir := filepath.Dir(move.destination); dir != "." {
		return dir
	}
	return ""
}

// MoveTasks publishes one archive or unarchive commit for distinct ticket
// numbers. A caller archiving tickets must hold all their task-lifetime locks
// before acquiring this session.
func (s *Session) MoveTasks(prefix Prefix, numbers []int, archive bool) ([]Task, error) {
	if len(numbers) == 0 {
		return nil, nil
	}
	tasks, err := s.Preview(prefix)
	if err != nil {
		return nil, err
	}
	defer tasks.Close()
	head, err := tasks.Head()
	if err != nil {
		return nil, err
	}
	selected := make([]Task, 0, len(numbers))
	for _, number := range numbers {
		task, err := tasks.Read(number)
		if err != nil {
			return nil, err
		}
		if task.Archived == archive {
			if archive {
				return nil, fmt.Errorf("%s is already archived", task.ID())
			}
			return nil, fmt.Errorf("%s is not archived", task.ID())
		}
		selected = append(selected, task)
	}
	verb := "archive"
	if !archive {
		verb = "unarchive"
	}
	target := selected[0].ID()
	if len(selected) > 1 {
		target = fmt.Sprintf("%d selected tasks", len(selected))
	}
	return s.moveTasks(tasks, head.ID, selected, archive, "chore: "+verb+" "+target)
}

func (s *Session) moveTasks(tasks *Store, head git.ID, selected []Task, archive bool, message string) ([]Task, error) {
	moves := make([]archiveMove, 0, len(selected))
	changes := make(map[string][]byte, 2*len(selected))
	moved := make([]Task, 0, len(selected))
	for _, task := range selected {
		source, destination := tasks.RelativePath(task), filepath.Base(task.Path)
		if archive {
			destination = filepath.Join(archiveBucket(task.Number), destination)
		}
		moves = append(moves, archiveMove{prefix: tasks.Prefix, number: task.Number, source: source, destination: destination})
		changes[source], changes[destination] = nil, []byte(task.Content)
		task.Path, task.Archived = filepath.Join(tasks.Path, destination), archive
		moved = append(moved, task)
	}
	if tasks.Prospective() {
		if err := tasks.withProspectiveView(func(view *git.Repository) error {
			for _, move := range moves {
				if _, err := move.treeEdits(view, head, changes[move.destination]); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
		opened, err := s.Ensure(git.Signature{})
		if err != nil {
			return nil, err
		}
		opened.Close()
	}
	if _, err := s.CommitFiles(changes, CommitOptions{
		FromEnvironment: true, Message: message,
		ExpectedRefs: map[string]git.ID{Branch: head}, moves: moves,
	}); err != nil {
		return nil, err
	}
	return moved, nil
}

type ticketKey struct {
	prefix Prefix
	number int
}

func indexTicketPaths(entries []git.IndexEntry) map[ticketKey][]string {
	result := make(map[ticketKey][]string)
	for _, entry := range entries {
		match := ticketName.FindStringSubmatch(filepath.Base(entry.Path))
		if match == nil {
			continue
		}
		number, err := strconv.Atoi(match[2])
		if err != nil || !inTicketLocation(entry.Path, number) {
			continue
		}
		key := ticketKey{prefix: Prefix(match[1]), number: number}
		result[key] = append(result[key], entry.Path)
	}
	return result
}

func (move *archiveMove) check(store *Store, head git.ID, indexEntries *[]git.IndexEntry, indexedTickets *map[ticketKey][]string) error {
	if err := store.requireNewPaths(move.directory(), "archive move destination", []string{move.destination}, head); err != nil {
		return err
	}
	store.Prefix = move.prefix
	task, err := store.Read(move.number)
	if err != nil {
		return err
	}
	if store.RelativePath(task) != move.source {
		return fmt.Errorf("%s moved during archive planning; retry", task.ID())
	}
	if *indexedTickets == nil {
		entries, err := store.Repository.IndexEntries()
		if err != nil {
			return err
		}
		*indexEntries = entries
		*indexedTickets = indexTicketPaths(entries)
	}
	for _, path := range (*indexedTickets)[ticketKey{prefix: move.prefix, number: move.number}] {
		if path != move.source {
			return fmt.Errorf("ambiguous %s: duplicate path in the task index: %s", task.ID(), path)
		}
	}
	return nil
}

// The tree move uses the original blob and mode, not freshly encoded bytes.
// Reject attributes that would make the moved working file dirty.
func (move *archiveMove) treeEdits(repo *git.Repository, head git.ID, data []byte) ([]git.TreeEdit, error) {
	entry, err := repo.Entry(head, move.source)
	if err != nil {
		return nil, err
	}
	if entry.Mode != 0100644 && entry.Mode != 0100755 {
		return nil, fmt.Errorf("task must be a regular file: %s", move.source)
	}
	for _, name := range []string{move.source, move.destination} {
		id, err := repo.WorktreeBlobID(name, data)
		if err != nil {
			return nil, err
		}
		if id != entry.ID {
			return nil, fmt.Errorf("%s would not preserve the committed task contents; check for local edits or conflicting Git attributes", name)
		}
	}
	return []git.TreeEdit{{Path: move.source}, {Path: move.destination, ID: entry.ID, Mode: entry.Mode}}, nil
}

// A no-replace link keeps the ticket's exact bytes and mode. Rollback will
// restore that same file rather than overwrite anything created meanwhile.
func (move *archiveMove) install(root string) (rollback func() error, err error) {
	source, destination := filepath.Join(root, move.source), filepath.Join(root, move.destination)
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("task must be a regular file: %s", source)
	}
	if err := os.Link(source, destination); err != nil {
		return nil, err
	}
	removed := false
	rollback = func() error {
		current, err := os.Lstat(destination)
		if err != nil {
			return err
		}
		if !os.SameFile(info, current) {
			return fmt.Errorf("archive move destination changed: %s", destination)
		}
		if removed {
			if err := os.Link(destination, source); err != nil {
				return err
			}
			if err := state.SyncDirectory(filepath.Dir(source)); err != nil {
				return err
			}
		}
		if err := os.Remove(destination); err != nil {
			return err
		}
		return state.SyncDirectory(filepath.Dir(destination))
	}
	if err := state.SyncDirectory(filepath.Dir(destination)); err != nil {
		return rollback, err
	}
	current, err := os.Lstat(source)
	if err != nil {
		return rollback, err
	}
	if !os.SameFile(info, current) {
		return rollback, fmt.Errorf("archive move source changed: %s", source)
	}
	if err := os.Remove(source); err != nil {
		return rollback, err
	}
	removed = true
	return rollback, state.SyncDirectory(filepath.Dir(source))
}
