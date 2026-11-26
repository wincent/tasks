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

func locationFromPaths(paths git.Paths) (path, common string, err error) {
	common, err = canonical(paths.CommonDir)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(filepath.Dir(common), "tasks"), common, nil
}

func Open(repository *git.Repository) (*Store, error) {
	paths, worktrees, err := repository.PathsAndWorktrees()
	if err != nil {
		return nil, err
	}
	path, common, err := locationFromPaths(paths)
	if err != nil {
		return nil, err
	}
	return openWith(path, common, worktrees)
}

func openWith(path, originalCommon string, worktrees []git.Worktree) (*Store, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task checkout must be a real directory at %s; preserve obstructions and run `tasks init`", path)
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
	tasks, summary, err := git.OpenCheckout(path)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			tasks.Close()
		}
	}()
	common, err := canonical(summary.Paths.CommonDir)
	if err != nil {
		return nil, err
	}
	if common != originalCommon {
		return nil, fmt.Errorf("task checkout belongs to another repository: %s", path)
	}
	if summary.HeadErr != nil {
		return nil, summary.HeadErr
	}
	head := summary.Head
	if head.Branch != Branch || head.ID == (git.ID{}) {
		return nil, fmt.Errorf("%s must have the committed tasks branch checked out; preserve its edits before switching branches", path)
	}
	targetBranch, err := targetBranchAt(tasks, head.ID)
	if err != nil {
		return nil, err
	}
	if err := checkHistoryIndependence(tasks, head.ID, targetBranch); err != nil {
		return nil, err
	}
	ok = true
	return &Store{Path: path, Repository: tasks, Paths: summary.Paths}, nil
}

func checkHistoryIndependence(repo *git.Repository, tasksID git.ID, targetBranch string) error {
	ref, err := repo.Reference("refs/heads/" + targetBranch)
	if git.HasCode(err, git.NotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ref.Symbolic != "" {
		return fmt.Errorf("%s must be a direct branch to verify task history independence", targetBranch)
	}
	bases, err := repo.MergeBases(tasksID, ref.ID)
	if err != nil {
		return fmt.Errorf("cannot verify task history independence: %w", err)
	}
	if len(bases) != 0 {
		return fmt.Errorf("tasks shares implementation history with %s; reconcile onto independent orphan history without discarding existing task history", targetBranch)
	}
	return nil
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
