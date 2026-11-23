package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/wincent/tasks/internal/git"
)

var ticketName = regexp.MustCompile(`^([A-Z][A-Z0-9]*)-([1-9][0-9]*) (.+)\.md$`)
var ticketNumber = regexp.MustCompile(`^(?:([A-Za-z][A-Za-z0-9]*)-)?([1-9][0-9]*)$`)
var statusLine = regexp.MustCompile(`(?m)^Status: (.*)$`)

// ErrTaskNotFound means no task record exists for the requested number.
var ErrTaskNotFound = errors.New("task not found")

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

// ValidTopic accepts local branch names other than the shared metadata branch.
func ValidTopic(name string) bool {
	return utf8.ValidString(name) && !strings.HasPrefix(name, "refs/") && !strings.EqualFold("refs/heads/"+name, Branch) && git.ValidBranchName(name)
}

// Topic reads the optional destination from the task preamble. An empty result
// means the default, main.
func Topic(content string) (string, error) {
	if hasMergeConflict(content) {
		return "", errors.New("unresolved merge conflict markers; resolve the task record first")
	}
	header, _ := taskHeader(content)
	var topic string
	for line := range strings.SplitSeq(header, "\n") {
		value, found := strings.CutPrefix(line, "Topic:")
		if !found {
			continue
		}
		name, spaced := strings.CutPrefix(value, " ")
		if topic != "" || !spaced || !ValidTopic(name) {
			return "", errors.New("expected at most one Topic: BRANCH line naming a local implementation branch")
		}
		topic = name
	}
	if topic == "main" {
		return "", nil
	}
	return topic, nil
}

// Task metadata is in the preamble, not in Markdown notes.
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
	Archived bool       `json:"archived"`
	Content  string     `json:"-"`
	Activity []Activity `json:"activity,omitempty"`
}

// Read locates an explicit task number, including archived records, without
// requiring valid status. It never follows symlinks or opens special files.
func (store *Store) Read(number int) (Task, error) {
	return store.read(number, true)
}

func (store *Store) read(number int, includeArchived bool) (Task, error) {
	head, err := store.Head()
	if err != nil {
		return Task{}, err
	}
	dirs := []string{""}
	if includeArchived {
		dirs = append(dirs, archiveBucket(number))
	}
	var selected []taskEntry
	for _, dir := range dirs {
		entries, err := store.entries(dir, store.revision)
		if err != nil {
			return Task{}, err
		}
		selected = append(selected, store.matchNumber(entries, number)...)
	}
	missing := ""
	if !store.prospective {
		// A committed record missing locally is not a legacy task with no
		// record. In particular, cleanup must not fall back to main.
		for _, dir := range dirs {
			entries, err := store.entries(dir, head.ID)
			if err != nil {
				return Task{}, err
			}
			for _, entry := range store.matchNumber(entries, number) {
				if !slices.ContainsFunc(selected, func(other taskEntry) bool { return other.name == entry.name }) {
					missing = entry.name
				}
			}
		}
	}
	switch {
	case len(selected) > 1:
		return Task{}, fmt.Errorf("expected one %s task in the shared store; found %d", store.Prefix.ID(number), len(selected))
	case len(selected) != 0 && missing != "":
		return Task{}, fmt.Errorf("ambiguous %s: %s is committed but missing from the shared checkout", store.Prefix.ID(number), missing)
	case missing != "":
		return Task{}, fmt.Errorf("%s is missing from the shared checkout; restore it before retrying", missing)
	case len(selected) == 0:
		return Task{}, fmt.Errorf("expected one %s task in the shared store; found 0: %w", store.Prefix.ID(number), ErrTaskNotFound)
	}
	entry := selected[0]
	path := filepath.Join(store.Path, entry.name)
	data, err := store.readTaskEntry(entry, true)
	if err != nil {
		return Task{}, fmt.Errorf("%s: %w", path, err)
	}
	match := store.matchTicket(filepath.Base(entry.name))
	return Task{Number: number, Title: match[3], Path: path, Content: string(data), Archived: filepath.Dir(entry.name) != "."}, nil
}

func (store *Store) matchNumber(entries []taskEntry, number int) []taskEntry {
	var matches []taskEntry
	for _, entry := range entries {
		if match := store.matchTicket(filepath.Base(entry.name)); match != nil && match[2] == strconv.Itoa(number) {
			matches = append(matches, entry)
		}
	}
	return matches
}

// ReadWorking resolves a task for execution or mutation, not just inspection.
func (store *Store) ReadWorking(number int) (Task, error) {
	task, err := store.Read(number)
	if err == nil && task.Archived {
		return Task{}, fmt.Errorf("%s is archived; run 'tasks unarchive %s' first", task.ID(), task.ID())
	}
	return task, err
}

// RelativePath is the literal ticket path in the metadata checkout.
func (store *Store) RelativePath(task Task) string {
	path, _ := filepath.Rel(store.Path, task.Path)
	return path
}

// ReadLatest reads the highest-numbered unarchived task without requiring
// valid task status.
func (store *Store) ReadLatest() (Task, error) {
	entries, err := store.entries("", store.revision)
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
		latest = max(latest, n)
	}
	if latest == 0 {
		return Task{}, errors.New("no tasks in the shared store")
	}
	return store.read(latest, false)
}

func (store *Store) Scan(includeArchived bool) ([]Task, error) {
	entries, err := store.entries("", store.revision)
	if err != nil {
		return nil, err
	}
	tasks := []Task{}
	var failures []error
	if includeArchived {
		archived, err := store.archiveEntries(store.revision)
		entries = append(entries, archived...)
		if err != nil {
			failures = append(failures, err)
		}
	}
	seen := map[int]string{}
	duplicates := map[int]bool{}
	for _, entry := range entries {
		base := filepath.Base(entry.name)
		if strings.HasPrefix(base, ".") || (entry.directory && (entry.name == artifactStorage || entry.name == archiveDirectory)) {
			continue
		}
		path := filepath.Join(store.Path, entry.name)
		match := store.matchTicket(base)
		if match == nil {
			failures = append(failures, fmt.Errorf("%s: expected '%s-<positive number> <title>.md'", path, store.Prefix))
			continue
		}
		n, err := decimalNumber(match[2])
		if err != nil {
			failures = append(failures, err)
			continue
		}
		archived := entry.name != base
		if !inTicketLocation(entry.name, n) {
			failures = append(failures, fmt.Errorf("%s: expected archive bucket %s", path, archiveBucket(n)))
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
		data, err := store.readTaskEntry(entry, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		status, err := Status(string(data))
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		if _, err := Topic(string(data)); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", path, err))
			continue
		}
		tasks = append(tasks, Task{Number: n, Title: match[3], Status: status, Path: path, Content: string(data), Archived: archived})
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

// entries reads one literal directory in the filesystem (zero revision)
// or the given commit. A missing archive directory is an empty result.
func (store *Store) entries(dir string, revision git.ID) ([]taskEntry, error) {
	var result []taskEntry
	if revision != (git.ID{}) {
		var entries []git.TreeEntry
		var err error
		if dir == "" {
			entries, err = store.Repository.Tree(revision)
		} else {
			entries, err = store.Repository.TreeAt(revision, dir)
			if git.HasCode(err, git.NotFound) {
				if parent := filepath.Dir(dir); parent != "." {
					entry, err := store.Repository.Entry(revision, parent)
					if err != nil && !git.HasCode(err, git.NotFound) {
						return nil, err
					}
					if err == nil && entry.Mode != 0040000 {
						return nil, fmt.Errorf("archive directory is obstructed in tasks: %s", parent)
					}
				}
				return nil, nil
			}
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			result = append(result, taskEntry{name: filepath.Join(dir, entry.Name), tree: entry, directory: entry.Mode == 0040000})
		}
	} else {
		if dir != "" {
			if err := metadataParents(store.Path, dir, "archive", nil); err != nil {
				return nil, err
			}
		}
		entries, err := os.ReadDir(filepath.Join(store.Path, dir))
		if dir != "" && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			result = append(result, taskEntry{name: filepath.Join(dir, entry.Name()), directory: entry.IsDir()})
		}
	}
	return result, nil
}

func (store *Store) readTaskEntry(entry taskEntry, smudge bool) ([]byte, error) {
	var data []byte
	var err error
	if !store.prospective {
		data, err = regularContents(filepath.Join(store.Path, entry.name))
	} else if entry.tree.Mode != 0100644 && entry.tree.Mode != 0100755 {
		return nil, errors.New("task must be a regular file")
	} else if smudge {
		err = store.withProspectiveView(func(view *git.Repository) error {
			data, err = view.WorktreeBlob(entry.name, entry.tree.ID)
			return err
		})
	} else {
		data, err = store.Repository.Blob(entry.tree.ID)
	}
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errors.New("task disappeared while reading")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("task must be UTF-8")
	}
	return data, nil
}
