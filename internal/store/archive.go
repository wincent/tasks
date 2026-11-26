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
		opened, err := s.Ensure()
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

type archiveLink struct {
	source, destination string
	info                os.FileInfo
	removed             bool
}

// A no-replace link keeps the ticket's exact bytes and mode.
func (move *archiveMove) link(root string) (archiveLink, error) {
	source, destination := filepath.Join(root, move.source), filepath.Join(root, move.destination)
	info, err := os.Lstat(source)
	if err != nil {
		return archiveLink{}, err
	}
	if !info.Mode().IsRegular() {
		return archiveLink{}, fmt.Errorf("task must be a regular file: %s", source)
	}
	if err := os.Link(source, destination); err != nil {
		return archiveLink{}, err
	}
	return archiveLink{source: source, destination: destination, info: info}, nil
}

func (link *archiveLink) checkDestination() error {
	current, err := os.Lstat(link.destination)
	if err != nil {
		return err
	}
	if !os.SameFile(link.info, current) {
		return fmt.Errorf("archive move destination changed: %s", link.destination)
	}
	return nil
}

func (link *archiveLink) unlinkSource() error {
	if err := link.checkDestination(); err != nil {
		return err
	}
	current, err := os.Lstat(link.source)
	if err != nil {
		return err
	}
	if !os.SameFile(link.info, current) {
		return fmt.Errorf("archive move source changed: %s", link.source)
	}
	if err := os.Remove(link.source); err != nil {
		return err
	}
	link.removed = true
	return nil
}

// Restore the original inode before dropping the destination link. If a
// replacement source prevents restoration, leave the destination intact.
func (link *archiveLink) rollback() error {
	if err := link.checkDestination(); err != nil {
		return err
	}
	if link.removed {
		if err := os.Link(link.destination, link.source); err != nil {
			return err
		}
		if err := state.SyncDirectory(filepath.Dir(link.source)); err != nil {
			return err
		}
	}
	if err := os.Remove(link.destination); err != nil {
		return err
	}
	return state.SyncDirectory(filepath.Dir(link.destination))
}

// installArchiveMoves returns all installed links, even on error, for the
// caller to roll back before publication. It makes the destination links
// durable before removing any sources, then flushes the source removals.
func installArchiveMoves(root string, moves []archiveMove, created []string, syncDirectory func(string) error) ([]archiveLink, error) {
	links := make([]archiveLink, 0, len(moves))
	directories := make([]string, 0, len(moves))
	for _, move := range moves {
		link, err := move.link(root)
		if err != nil {
			return links, err
		}
		links = append(links, link)
		directories = append(directories, filepath.Dir(link.destination))
	}
	// Flush the new directory entries too, from the leaves up to the root.
	for i := len(created) - 1; i >= 0; i-- {
		directories = append(directories, filepath.Dir(created[i]))
	}
	if err := syncArchiveDirectories(directories, syncDirectory); err != nil {
		return links, err
	}
	directories = directories[:0]
	for i := range links {
		if err := links[i].unlinkSource(); err != nil {
			return links, err
		}
		directories = append(directories, filepath.Dir(links[i].source))
	}
	return links, syncArchiveDirectories(directories, syncDirectory)
}

func syncArchiveDirectories(directories []string, syncDirectory func(string) error) error {
	seen := make(map[string]bool)
	for _, dir := range directories {
		if seen[dir] {
			continue
		}
		if err := syncDirectory(dir); err != nil {
			return fmt.Errorf("sync archive move directory %s: %w", dir, err)
		}
		seen[dir] = true
	}
	return nil
}
