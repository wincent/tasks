package store

import (
	"errors"
	"os"

	"github.com/wincent/tasks/internal/git"
)

// MetadataSignatures is read-only, including when the checkout does not exist.
// Callers must preflight before Ensure, ledger allocation, or claim mutation;
// commitFiles resolves again in the actual commit repository before writing.
func MetadataSignatures(repository *git.Repository) (git.Signatures, error) {
	paths, worktrees, err := repository.PathsAndWorktrees()
	if err != nil {
		return git.Signatures{}, err
	}
	path, common, err := locationFromPaths(paths)
	if err != nil {
		return git.Signatures{}, err
	}
	if _, err := os.Lstat(path); err == nil {
		tasks, err := openWith(path, common, worktrees)
		if err != nil {
			return git.Signatures{}, err
		}
		defer tasks.Close()
		return tasks.Repository.Signatures()
	} else if !errors.Is(err, os.ErrNotExist) {
		return git.Signatures{}, err
	}
	return repository.ProspectiveWorktreeSignatures("tasks", path, Branch)
}
