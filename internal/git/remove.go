package git

/*
#include <git2.h>
static int task_prune_worktree(git_worktree *tree) {
 git_worktree_prune_options opts = GIT_WORKTREE_PRUNE_OPTIONS_INIT;
 opts.flags = GIT_WORKTREE_PRUNE_VALID | GIT_WORKTREE_PRUNE_WORKING_TREE;
 return git_worktree_prune(tree, &opts);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RemoveCleanWorktree refuses locked, dirty (including ignored), changed or
// invalid resources. The caller holds workflow locks and must stop arbitrary
// filesystem/Git writers: native worktree pruning is not an atomic filesystem
// transaction. No locked-worktree override is ever passed to libgit2.
func (r *Repository) RemoveCleanWorktree(name string, expected Paths, head Head) error {
	return r.RemoveWorktree(name, expected, head, false)
}

// RemoveWorktree retains all clean-removal safeguards except that an explicit
// discardIgnored authorization permits deleting ignored local content too.
func (r *Repository) RemoveWorktree(name string, expected Paths, head Head, discardIgnored bool) error {
	trees, err := r.Worktrees()
	if err != nil {
		return err
	}
	found := false
	for _, tree := range trees {
		if tree.Name == name {
			found = true
			if tree.Locked || tree.InvalidReason != "" {
				return fmt.Errorf("worktree is locked or invalid: %s", tree.Path)
			}
			actual, err := filepath.EvalSymlinks(tree.Path)
			if err != nil {
				return err
			}
			want, err := filepath.EvalSymlinks(expected.WorkDir)
			if err != nil {
				return err
			}
			if actual != want {
				return errors.New("worktree registration path changed")
			}
		}
	}
	if !found {
		return errors.New("worktree registration disappeared")
	}
	info, err := os.Lstat(expected.WorkDir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("worktree path is not a real directory")
	}
	repo, err := Open(expected.WorkDir)
	if err != nil {
		return err
	}
	defer repo.Close()
	paths, err := repo.Paths()
	if err != nil {
		return err
	}
	if paths != expected {
		return errors.New("worktree Git-directory identity changed")
	}
	current, err := repo.Head()
	if err != nil {
		return err
	}
	if current != head {
		return errors.New("worktree HEAD changed")
	}
	entries, err := repo.IndexEntries()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.AssumeValid || entry.SkipWorktree {
			return errors.New("worktree has assume-unchanged or skip-worktree index entries")
		}
	}
	changes, err := repo.Status(!discardIgnored)
	if err != nil {
		return err
	}
	if len(changes) > 0 {
		return fmt.Errorf("worktree has %s; preserve local changes before cleanup", DescribeChanges(changes))
	}
	if err := repo.RequireClean(); err != nil {
		return err
	}
	actual, err := os.Lstat(expected.WorkDir)
	if err != nil {
		return err
	}
	if !os.SameFile(info, actual) {
		return errors.New("worktree directory identity changed")
	}
	return r.call("remove worktree", func(native *C.git_repository) error {
		n, err := cstring(name)
		if err != nil {
			return err
		}
		defer free(n)
		var tree *C.git_worktree
		if err := nativeError("lookup worktree", C.git_worktree_lookup(&tree, native, n)); err != nil {
			return err
		}
		defer C.git_worktree_free(tree)
		var reason C.git_buf
		locked := C.git_worktree_is_locked(&reason, tree)
		C.git_buf_dispose(&reason)
		if locked < 0 {
			return nativeError("check worktree lock", locked)
		}
		if locked != 0 {
			return errors.New("worktree became locked")
		}
		return nativeError("remove clean worktree", C.task_prune_worktree(tree))
	})
}
