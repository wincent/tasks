package store

import (
	"errors"
	"fmt"
	"os"

	"github.com/wincent/tasks/internal/git"
)

// Preview validates the existing checkout or describes its committed future
// state without registration, ignore-file edits, or object/index writes. The
// caller can retain the metadata lock across inspection and Ensure.
func (s *Session) Preview(prefix Prefix) (*Store, error) {
	if err := s.check(); err != nil {
		return nil, err
	}
	return Inspect(s.Repository, prefix)
}

// Inspect reads the registered checkout or the local task branch without
// creating state or waiting on a writer.
func Inspect(repo *git.Repository, prefix Prefix) (*Store, error) {
	tasks, err := inspect(repo)
	if err != nil {
		return nil, err
	}
	tasks.Prefix = prefix
	return tasks, nil
}

func inspect(repo *git.Repository) (*Store, error) {
	paths, trees, err := repo.PathsAndWorktrees()
	if err != nil {
		return nil, err
	}
	path, common, err := locationFromPaths(paths)
	if err != nil {
		return nil, err
	}
	return inspectWith(repo, path, common, trees)
}

func inspectWith(repo *git.Repository, path, originalCommon string, trees []git.Worktree) (*Store, error) {
	if _, err := os.Lstat(path); err == nil {
		return openWith(repo, path, originalCommon, trees)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, tree := range trees {
		// Invalid registrations are not permission to create a replacement checkout.
		if tree.InvalidReason != "" {
			return nil, fmt.Errorf("inspect invalid worktree %s before bootstrapping tasks: %s", tree.Path, tree.InvalidReason)
		}
		other, summary, err := git.OpenCheckout(tree.Path, "")
		if err != nil {
			return nil, err
		}
		other.Close()
		if summary.HeadErr != nil {
			return nil, summary.HeadErr
		}
		if summary.Head.Branch == Branch {
			return nil, fmt.Errorf("tasks is checked out at %s; relocate it to %s without resetting it", tree.Path, path)
		}
	}
	ref, err := repo.Reference(Branch)
	if err != nil && !git.HasCode(err, git.NotFound) {
		return nil, err
	}
	if git.HasCode(err, git.NotFound) {
		return nil, ErrMissingLocalHistory
	} else if ref.Symbolic != "" {
		return nil, fmt.Errorf("%s must be a direct branch", Branch)
	}
	targetBranch, err := targetBranchAt(repo, ref.ID)
	if err != nil {
		return nil, err
	}
	if err := checkHistoryIndependence(repo, ref.ID, targetBranch); err != nil {
		return nil, err
	}

	return &Store{Path: path, Repository: repo, prospective: true, revision: ref.ID}, nil
}

func (store *Store) Head() (git.Head, error) {
	if store.prospective {
		return git.Head{Branch: Branch, ID: store.revision}, nil
	}
	return store.Repository.Head()
}

// ValidateContents retains the same selected bytes on either side of Ensure.
// Configuration admission is not a lease over independently edited config.
func (store *Store) ValidateContents(contents map[string][]byte) error {
	if store.prospective {
		ref, err := store.Repository.Reference(Branch)
		if err != nil {
			return err
		}
		if ref.ID != store.revision || ref.Symbolic != "" {
			return errors.New("tasks changed during mutation planning; retry")
		}
		return store.Repository.ValidateProspectiveWorktreeContents("tasks", store.Path, Branch, contents)
	}
	return store.Repository.ValidateWorktreeContents(contents)
}

func (store *Store) withProspectiveView(inspect func(*git.Repository) error) error {
	return store.Repository.WithProspectiveWorktree("tasks", store.Path, Branch, func(view *git.Repository) error {
		ref, err := view.Reference(Branch)
		if err != nil {
			return err
		}
		if ref.ID != store.revision || ref.Symbolic != "" {
			return errors.New("tasks changed during mutation planning; retry")
		}
		return inspect(view)
	})
}

// Prospective distinguishes missing-checkout snapshots from filesystem reads.
func (store *Store) Prospective() bool { return store.prospective }
