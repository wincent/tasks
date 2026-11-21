// Package store implements shared orphan task metadata, independently of the
// application, CLI rendering, process launcher, and validation policy.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wincent/tasks/internal/git"
)

const Branch = "refs/heads/tasks"

// Store owns its repository handle except for a read-only prospective store,
// which borrows its session's source handle. Open validates fresh registration/
// history; callers must reopen at lock boundaries rather than cache validity.
type Store struct {
	Prefix      Prefix
	Path        string
	Repository  *git.Repository
	Paths       git.Paths
	prospective bool
	revision    git.ID
}

func canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// Location is shared by all linked checkouts, including bare-in-.git layouts.
func Location(repository *git.Repository) (string, error) {
	paths, err := repository.Paths()
	if err != nil {
		return "", err
	}
	common, err := canonical(paths.CommonDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(common), "tasks"), nil
}

func Open(repository *git.Repository) (*Store, error) {
	path, err := Location(repository)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task checkout must be a real directory at %s; preserve obstructions and run `tasks init`", path)
	}
	worktrees, err := repository.Worktrees()
	if err != nil {
		return nil, err
	}
	registered := false
	for _, worktree := range worktrees {
		resolved, resolveErr := canonical(worktree.Path)
		if resolveErr == nil && resolved == path && worktree.InvalidReason == "" {
			registered = true
		}
	}
	if !registered {
		return nil, fmt.Errorf("expected a registered tasks worktree at %s; restore its tasks branch without resetting it", path)
	}
	tasks, err := git.Open(path)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			tasks.Close()
		}
	}()
	paths, err := tasks.Paths()
	if err != nil {
		return nil, err
	}
	common, err := canonical(paths.CommonDir)
	if err != nil {
		return nil, err
	}
	original, err := repository.Paths()
	if err != nil {
		return nil, err
	}
	originalCommon, err := canonical(original.CommonDir)
	if err != nil {
		return nil, err
	}
	if common != originalCommon {
		return nil, fmt.Errorf("task checkout belongs to another repository: %s", path)
	}
	head, err := tasks.Head()
	if err != nil {
		return nil, err
	}
	if head.Branch != Branch || head.ID == (git.ID{}) {
		return nil, fmt.Errorf("%s must have the committed tasks branch checked out; preserve its edits before switching branches", path)
	}
	main, err := tasks.Reference("refs/heads/main")
	if err != nil && !git.HasCode(err, git.NotFound) {
		return nil, err
	}
	if err == nil {
		if main.Symbolic != "" {
			return nil, errors.New("main must be a direct branch to verify task history independence")
		}
		bases, err := tasks.MergeBases(head.ID, main.ID)
		if err != nil {
			return nil, fmt.Errorf("cannot verify task history independence: %w", err)
		}
		if len(bases) != 0 {
			return nil, errors.New("tasks shares implementation history with main; reconcile onto independent orphan history without discarding existing task history")
		}
	}
	ok = true
	return &Store{Path: path, Repository: tasks, Paths: paths}, nil
}

func (store *Store) Close() {
	if !store.prospective {
		store.Repository.Close()
	}
}

func (store *Store) noOperation() error {
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_START"} {
		_, err := os.Lstat(filepath.Join(store.Paths.GitDir, name))
		if err == nil {
			return fmt.Errorf("Git operation in progress in task checkout: %s; finish it before editing tasks", name)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
