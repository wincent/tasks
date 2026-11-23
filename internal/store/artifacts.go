package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/wincent/tasks/internal/git"
)

const artifactStorage = "storage"

func (task Task) artifactDirectory() string { return artifactStorage + "/" + task.ID() }

func (store *Store) ArtifactPath(task Task, name string) string {
	return filepath.Join(store.Path, task.artifactDirectory(), name)
}

func validArtifactName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "/\\\x00\n\r") && !strings.EqualFold(name, ".git") &&
		!strings.EqualFold(name, ".gitattributes")
}

// Artifacts lists committed filenames, not untracked or staged worktree edits.
func (store *Store) Artifacts(task Task) ([]string, error) {
	head, err := store.Head()
	if err != nil {
		return nil, err
	}
	entries, err := store.Repository.TreeAt(head.ID, task.artifactDirectory())
	if git.HasCode(err, git.NotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	slices.Sort(names)
	return names, nil
}

// ArtifactPresent also detects filesystem aliases of a committed filename.
// Uncommitted entries are obstructions, not stored items.
func (store *Store) ArtifactPresent(task Task, name string, existing []string) (bool, error) {
	if slices.Contains(existing, name) {
		return true, nil
	}
	if store.prospective || !validArtifactName(name) {
		return false, nil
	}
	if err := metadataParents(store.Path, task.artifactDirectory(), "artifact", nil); err != nil {
		return false, err
	}
	info, err := os.Lstat(filepath.Join(store.Path, task.artifactDirectory(), name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, stored := range existing {
		other, err := os.Lstat(filepath.Join(store.Path, task.artifactDirectory(), stored))
		if err == nil && os.SameFile(info, other) {
			return true, nil
		}
	}
	return false, nil
}

func (s *Session) CommitArtifacts(preview *Store, task Task, files map[string][]byte) error {
	if err := s.check(); err != nil {
		return err
	}
	directory := task.artifactDirectory()
	changes := make(map[string][]byte, len(files))
	for name, content := range files {
		if !validArtifactName(name) {
			return fmt.Errorf("invalid artifact filename: %q", name)
		}
		changes[directory+"/"+name] = content
	}
	head, err := preview.Head()
	if err != nil {
		return err
	}
	if preview.Prospective() {
		if err := preview.ValidateContents(changes); err != nil {
			return err
		}
		opened, err := s.Ensure(git.Signature{})
		if err != nil {
			return err
		}
		opened.Close()
	}
	_, err = s.CommitFiles(changes, CommitOptions{
		FromEnvironment: true, Message: "chore: store artifacts for " + task.ID(),
		ExpectedRefs: map[string]git.ID{Branch: head.ID}, artifactDirectory: directory,
	})
	return err
}

// metadataParents refuses symlinks and file obstructions. If creating,
// record the directories a failed publication can remove.
func metadataParents(root, directory, kind string, created *[]string) error {
	if directory == "" {
		return nil
	}
	path := root
	for _, part := range strings.Split(directory, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if created == nil {
				return nil
			}
			if err := os.Mkdir(path, 0755); err != nil {
				return err
			}
			*created = append(*created, path)
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s directory is obstructed: %s", kind, path)
		}
	}
	return nil
}

// The index and HEAD must not contain a file/directory conflict with any new
// path, even if the corresponding file has been removed from the worktree.
func (store *Store) requireNewPaths(directory, kind string, names []string, head git.ID) error {
	if err := metadataParents(store.Path, directory, kind, nil); err != nil {
		return err
	}
	for parent := directory; parent != "" && parent != "."; parent = filepath.Dir(parent) {
		entry, err := store.Repository.Entry(head, parent)
		if err == nil && entry.Mode != 0040000 {
			return fmt.Errorf("%s directory is obstructed in tasks: %s", kind, parent)
		}
		if err != nil && !git.HasCode(err, git.NotFound) {
			return err
		}
	}
	index, err := store.Repository.IndexEntries()
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := store.Repository.Entry(head, name); !git.HasCode(err, git.NotFound) {
			if err == nil {
				return fmt.Errorf("%s path already committed: %s", kind, name)
			}
			return err
		}
		for _, entry := range index {
			if entry.Path == name || strings.HasPrefix(entry.Path, name+"/") || strings.HasPrefix(name, entry.Path+"/") {
				return fmt.Errorf("%s path is obstructed in the task index: %s", kind, entry.Path)
			}
		}
		if _, err := os.Lstat(filepath.Join(store.Path, name)); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return fmt.Errorf("%s path already exists; preserve it before retrying: %s", kind, name)
			}
			return err
		}
	}
	return nil
}
