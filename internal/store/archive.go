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

// MoveTask publishes one archive or unarchive commit. A caller archiving a
// ticket must hold its task-lifetime lock before acquiring this session.
func (s *Session) MoveTask(prefix Prefix, number int, archive bool) (Task, error) {
	tasks, err := s.Preview(prefix)
	if err != nil {
		return Task{}, err
	}
	defer tasks.Close()
	task, err := tasks.Read(number)
	if err != nil {
		return Task{}, err
	}
	if task.Archived == archive {
		if archive {
			return Task{}, fmt.Errorf("%s is already archived", task.ID())
		}
		return Task{}, fmt.Errorf("%s is not archived", task.ID())
	}
	destination := filepath.Base(task.Path)
	verb := "unarchive"
	if archive {
		destination = filepath.Join(archiveBucket(number), destination)
		verb = "archive"
	}
	source := tasks.RelativePath(task)
	move := &archiveMove{prefix: prefix, number: number, source: source, destination: destination}
	head, err := tasks.Head()
	if err != nil {
		return Task{}, err
	}
	changes := map[string][]byte{source: nil, destination: []byte(task.Content)}
	if tasks.Prospective() {
		if err := tasks.withProspectiveView(func(view *git.Repository) error {
			_, err := move.treeEdits(view, head.ID, changes[destination])
			return err
		}); err != nil {
			return Task{}, err
		}
		opened, err := s.Ensure(git.Signature{})
		if err != nil {
			return Task{}, err
		}
		opened.Close()
	}
	if _, err := s.CommitFiles(changes, CommitOptions{
		FromEnvironment: true, Message: "chore: " + verb + " " + task.ID(),
		ExpectedRefs: map[string]git.ID{Branch: head.ID}, move: move,
	}); err != nil {
		return Task{}, err
	}
	task.Path, task.Archived = filepath.Join(tasks.Path, destination), archive
	return task, nil
}

func (move *archiveMove) check(store *Store, head git.ID) error {
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
	entries, err := store.Repository.IndexEntries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Path != move.source && inTicketLocation(entry.Path, move.number) {
			match := store.matchTicket(filepath.Base(entry.Path))
			if match != nil && match[2] == strconv.Itoa(move.number) {
				return fmt.Errorf("ambiguous %s: duplicate path in the task index: %s", task.ID(), entry.Path)
			}
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
