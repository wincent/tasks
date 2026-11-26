package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/state"
)

// Ensure never resets an existing branch or replaces a filesystem obstruction.
// Initialization is local-only: users supply task history through their own Git workflow.
func (s *Session) Ensure() (*Store, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	paths, worktrees, err := s.Repository.PathsAndWorktrees()
	if err != nil {
		return nil, err
	}
	path, common, err := locationFromPaths(paths)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		store, err := openWith(path, common, worktrees)
		if err != nil {
			return nil, err
		}
		if err := s.ignoreCheckoutAt(paths.CommonDir); err != nil {
			store.Close()
			return nil, err
		}
		return store, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	preview, err := inspectWith(s.Repository, path, common, worktrees)
	if err != nil {
		return nil, err
	}
	preview.Close()
	if err := s.ignoreCheckoutAt(paths.CommonDir); err != nil {
		return nil, err
	}
	if err := s.Repository.AddWorktree("tasks", path, Branch); err != nil {
		return nil, err
	}
	return Open(s.Repository)
}

func (s *Session) ignoreCheckoutAt(commonDir string) error {
	directory := filepath.Join(commonDir, "info")
	if err := os.Mkdir(directory, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Git info directory must be a real directory")
	}
	path := filepath.Join(directory, "exclude")
	data, err := regularContents(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "/tasks/" {
			return nil
		}
	}
	suffix := "/tasks/\n"
	if len(data) > 0 && data[len(data)-1] != '\n' {
		suffix = "\n" + suffix
	}
	// Append preserves preexisting exclusion rules and file permissions.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(suffix); err != nil {
		return err
	}
	return state.SyncFile(file)
}
