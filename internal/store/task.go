package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wincent/tasks/internal/git"
)

var ticketName = regexp.MustCompile(`^([A-Z][A-Z0-9]*)-([1-9][0-9]*) (.+)\.md$`)
var ticketNumber = regexp.MustCompile(`^(?:([A-Za-z][A-Za-z0-9]*)-)?([1-9][0-9]*)$`)
var statusLine = regexp.MustCompile(`(?m)^Status: (.*)$`)

// ParseNumber validates the syntax; the CLI checks the optional prefix against
// the repository setting before dispatching the command.
func ParseNumber(value string) (Prefix, int, error) {
	match := ticketNumber.FindStringSubmatch(value)
	if match == nil {
		return "", 0, errors.New("expected a positive ticket number (for example, 1 or PREFIX-1)")
	}
	n, err := decimalNumber(match[2])
	return Prefix(strings.ToUpper(match[1])), n, err
}

func decimalNumber(digits string) (int, error) {
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, fmt.Errorf("ticket number exceeds supported integer range: %w", err)
	}
	return n, nil
}

func (store *Store) matchTicket(name string) []string {
	match := ticketName.FindStringSubmatch(name)
	if match == nil || match[1] != string(store.Prefix) {
		return nil
	}
	return match
}

// ID returns the task identifier as recorded in its ticket filename.
func (task Task) ID() string {
	id, _, _ := strings.Cut(filepath.Base(task.Path), " ")
	return id
}

func ValidTitle(title string) bool {
	if title == "" || strings.TrimSpace(title) != title || !utf8.ValidString(title) {
		return false
	}
	for _, r := range title {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

func ValidStatus(status string) bool {
	switch status {
	case "open", "active", "blocked", "done", "merged", "closed":
		return true
	}
	return false
}

func Status(content string) (string, error) {
	if hasMergeConflict(content) {
		return "", errors.New("unresolved merge conflict markers; resolve the task record before editing it")
	}
	header, _ := taskHeader(content)
	matches := statusLine.FindAllStringSubmatch(header, -1)
	if len(matches) != 1 || !ValidStatus(matches[0][1]) {
		return "", errors.New("expected exactly one Status: open, active, blocked, done, merged, or closed line")
	}
	return matches[0][1], nil
}

func WithStatus(content, status string) (string, error) {
	if !ValidStatus(status) {
		return "", errors.New("invalid target status")
	}
	if _, err := Status(content); err != nil {
		return "", err
	}
	header, body := taskHeader(content)
	return statusLine.ReplaceAllString(header, "Status: "+status) + body, nil
}

// Task status is metadata in the preamble, not a restriction on Markdown notes.
func taskHeader(content string) (string, string) {
	if i := strings.Index(content, "\n## "); i >= 0 {
		return content[:i], content[i:]
	}
	return content, ""
}

type Activity struct {
	Worktree    string  `json:"worktree"`
	SessionID   *string `json:"session_id"`
	SessionFile string  `json:"session,omitempty"`
}

type Task struct {
	Number   int        `json:"number"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Path     string     `json:"path"`
	Content  string     `json:"-"`
	Activity []Activity `json:"activity,omitempty"`
}

// Read locates one task without requiring valid status, so show remains
// available for diagnosis. It never follows symlinks or opens special files.
func (store *Store) Read(number int) (Task, error) {
	if store.prospective {
		return store.readProspective(number)
	}
	entries, err := os.ReadDir(store.Path)
	if err != nil {
		return Task{}, err
	}
	paths := []string{}
	for _, entry := range entries {
		match := store.matchTicket(entry.Name())
		if match == nil {
			continue
		}
		n, err := decimalNumber(match[2])
		if err != nil {
			return Task{}, err
		}
		if n == number {
			paths = append(paths, filepath.Join(store.Path, entry.Name()))
		}
	}
	if len(paths) != 1 {
		return Task{}, fmt.Errorf("expected one %s task in the shared store; found %d", store.Prefix.ID(number), len(paths))
	}
	data, err := regularContents(paths[0])
	if err != nil {
		return Task{}, err
	}
	if data == nil {
		return Task{}, errors.New("task disappeared while reading")
	}
	if !utf8.Valid(data) {
		return Task{}, fmt.Errorf("%s: task must be UTF-8", paths[0])
	}
	title := store.matchTicket(filepath.Base(paths[0]))[3]
	return Task{Number: number, Title: title, Path: paths[0], Content: string(data)}, nil
}

// ReadLatest reads the highest-numbered task with the same diagnostic and
// file-safety behavior as Read, without requiring valid task status.
func (store *Store) ReadLatest() (Task, error) {
	entries, err := store.taskEntries()
	if err != nil {
		return Task{}, err
	}
	latest := 0
	for _, entry := range entries {
		match := store.matchTicket(entry.name)
		if match == nil {
			continue
		}
		n, err := decimalNumber(match[2])
		if err != nil {
			return Task{}, err
		}
		if n > latest {
			latest = n
		}
	}
	if latest == 0 {
		return Task{}, errors.New("no tasks in the shared store")
	}
	return store.Read(latest)
}

func (store *Store) Scan() ([]Task, error) {
	entries, err := store.taskEntries()
	if err != nil {
		return nil, err
	}
	tasks := []Task{}
	var failures []error
	seen := map[int]string{}
	duplicates := map[int]bool{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.name, ".") || (entry.name == artifactStorage && entry.directory) {
			continue
		}
		path := filepath.Join(store.Path, entry.name)
		match := store.matchTicket(entry.name)
		if match == nil {
			failures = append(failures, fmt.Errorf("%s: expected '%s-<positive number> <title>.md'", path, store.Prefix))
			continue
		}
		n, err := decimalNumber(match[2])
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if previous, ok := seen[n]; ok {
			duplicates[n] = true
			failures = append(failures, fmt.Errorf("%s: duplicate %s (also %s)", path, store.Prefix.ID(n), previous))
			continue
		}
		seen[n] = path
		if !ValidTitle(match[3]) {
			failures = append(failures, fmt.Errorf("%s: invalid task title", path))
			continue
		}
		data, err := store.readEntry(entry)
		if err == nil && data == nil {
			err = errors.New("task disappeared while scanning")
		}
		if err == nil && !utf8.Valid(data) {
			err = errors.New("task must be UTF-8")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		status, err := Status(string(data))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		tasks = append(tasks, Task{Number: n, Title: match[3], Status: status, Path: path, Content: string(data)})
	}
	// Neither side of an ambiguous ticket number is authoritative, including
	// when a caller elects to display partial results alongside scan errors.
	valid := tasks[:0]
	for _, task := range tasks {
		if !duplicates[task.Number] {
			valid = append(valid, task)
		}
	}
	tasks = valid
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].Number < tasks[j].Number })
	return tasks, errors.Join(failures...)
}

type taskEntry struct {
	name      string
	tree      git.TreeEntry
	directory bool
}

func (store *Store) taskEntries() ([]taskEntry, error) {
	var result []taskEntry
	if store.prospective {
		entries, err := store.Repository.Tree(store.revision)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			result = append(result, taskEntry{name: entry.Name, tree: entry, directory: entry.Mode == 0040000})
		}
	} else {
		entries, err := os.ReadDir(store.Path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			result = append(result, taskEntry{name: entry.Name(), directory: entry.IsDir()})
		}
	}
	return result, nil
}

func (store *Store) readEntry(entry taskEntry) ([]byte, error) {
	if !store.prospective {
		return regularContents(filepath.Join(store.Path, entry.name))
	}
	if entry.tree.Mode != 0100644 && entry.tree.Mode != 0100755 {
		return nil, errors.New("task must be a regular file")
	}
	return store.Repository.Blob(entry.tree.ID)
}
