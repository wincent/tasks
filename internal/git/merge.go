package git

/*
#include <git2.h>

static int task_merge_head_tree(git_tree **out, git_repository *repo) {
 git_reference *head = NULL;
 git_object *object = NULL;
 int result = git_repository_head(&head, repo);
 if (!result) result = git_reference_peel(&object, head, GIT_OBJECT_TREE);
 git_reference_free(head);
 if (!result) *out = (git_tree *)object;
 else git_object_free(object);
 return result;
}

static int task_merge(git_repository *repo, const git_oid *tip, const char *driver) {
 git_annotated_commit *commit = NULL;
 git_merge_options merge = GIT_MERGE_OPTIONS_INIT;
 merge.default_driver = driver;
 git_checkout_options checkout = GIT_CHECKOUT_OPTIONS_INIT;
 checkout.checkout_strategy = GIT_CHECKOUT_NONE | GIT_CHECKOUT_DRY_RUN | GIT_CHECKOUT_ALLOW_CONFLICTS | GIT_CHECKOUT_DONT_WRITE_INDEX | GIT_CHECKOUT_NO_REFRESH;
 int result = git_annotated_commit_lookup(&commit, repo, tip);
 if (result < 0) return result;
 const git_annotated_commit *heads[] = { commit };
 result = git_merge(repo, heads, 1, &merge, &checkout);
 git_annotated_commit_free(commit);
 return result;
}

static int task_checkout_implementation(git_repository *repo, git_tree *base, git_tree *target) {
 git_checkout_options opts = GIT_CHECKOUT_OPTIONS_INIT;
 opts.checkout_strategy = GIT_CHECKOUT_SAFE | GIT_CHECKOUT_DONT_OVERWRITE_IGNORED;
 opts.baseline = base;
 return git_checkout_tree(repo, (git_object *)target, &opts);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MergeIntoIndex leaves conflicts in the index and worktree for resolution.
func (r *Repository) MergeIntoIndex(tip ID) (ID, error) {
	return r.mergeIntoIndex(tip, false)
}

var ErrMergeConflict = errors.New("merge conflicts")

// MergeCleanIntoIndex refuses conflicts before writing the index or worktree.
func (r *Repository) MergeCleanIntoIndex(tip ID) (ID, error) {
	return r.mergeIntoIndex(tip, true)
}

func (r *Repository) mergeIntoIndex(tip ID, cleanOnly bool) (ID, error) {
	if err := r.validateIndex(); err != nil {
		return ID{}, err
	}
	mergeDriverRegistry.Lock()
	defer mergeDriverRegistry.Unlock()
	var tree ID
	err := r.call("merge candidate", func(repo *C.git_repository) error {
		restore := encodingComparison(repo)
		defer restore()
		return withOperationConfig(repo, func() (err error) {
			defaultDriver, err := configuredCommand(repo, "merge.default")
			if err != nil {
				return err
			}
			cleanup, err := guardExternalMergeDrivers(repo, defaultDriver)
			defer func() { err = errors.Join(err, cleanup()) }()
			if err != nil {
				return err
			}
			var driver *C.char
			if defaultDriver != "" {
				driver, err = cstring(defaultDriver)
				if err != nil {
					return err
				}
				defer free(driver)
			}
			if err := nativeError("refresh merge attributes", C.git_attr_cache_flush(repo)); err != nil {
				return err
			}
			if err := withSparseMergePlan(repo, tip, driver, func(planned *C.git_index, plan sparsePlan) error {
				if cleanOnly && C.git_index_has_conflicts(planned) != 0 {
					return ErrMergeConflict
				}
				var baseline *C.git_tree
				if err := nativeError("read merge checkout baseline", C.task_merge_head_tree(&baseline, repo)); err != nil {
					return err
				}
				defer C.git_tree_free(baseline)
				var current *C.git_index
				if err := nativeError("read merge checkout index", C.git_repository_index(&current, repo)); err != nil {
					return err
				}
				defer C.git_index_free(current)
				if err := nativeError("refresh merge checkout index", C.git_index_read(current, 1)); err != nil {
					return err
				}
				oid := nativeID(tip)
				if err := withSparseMergeFlags(repo, current, func() error { return nativeError("merge candidate", C.task_merge(repo, &oid, driver)) }); err != nil {
					return err
				}
				// Detach the plan from its quarantine handle before checkout; the
				// real merge has now generated the same result objects.
				result, err := copySparseIndex(planned)
				if err != nil {
					return err
				}
				defer C.git_index_free(result)
				return applySparseIndex(repo, baseline, result, current, plan)
			}); err != nil {
				return err
			}
			var index *C.git_index
			if err := nativeError("read merged index", C.git_repository_index(&index, repo)); err != nil {
				return err
			}
			defer C.git_index_free(index)
			if C.git_index_has_conflicts(index) != 0 {
				return fmt.Errorf("%w: index and worktree retained for inspection", ErrMergeConflict)
			}
			var result C.git_oid
			if err := nativeError("write merged tree", C.git_index_write_tree(&result, index)); err != nil {
				return err
			}
			tree = objectID(&result)
			return nil
		})
	})
	return tree, err
}

// IndexTree snapshots the fresh real index after configured merge hooks.
func (r *Repository) IndexTree() (ID, error) {
	if err := r.validateIndex(); err != nil {
		return ID{}, err
	}
	var result ID
	err := r.call("write index tree", func(repo *C.git_repository) error {
		var index *C.git_index
		if err := nativeError("open index", C.git_repository_index(&index, repo)); err != nil {
			return err
		}
		defer C.git_index_free(index)
		if err := nativeError("refresh index", C.git_index_read(index, 1)); err != nil {
			return err
		}
		if C.git_index_has_conflicts(index) != 0 {
			return errors.New("index has unresolved conflicts")
		}
		var oid C.git_oid
		if err := nativeError("write index tree", C.git_index_write_tree(&oid, index)); err != nil {
			return err
		}
		result = objectID(&oid)
		return nil
	})
	return result, err
}

func (r *Repository) CleanupOperation() error {
	return r.call("finish merge state", func(repo *C.git_repository) error {
		return nativeError("finish merge state", C.git_repository_state_cleanup(repo))
	})
}

// IndexMatches compares the indexed tree to a commit without changing the index.
func (r *Repository) IndexMatches(revision ID) (bool, error) {
	id, err := r.IndexTree()
	if err != nil {
		return false, err
	}
	var matches bool
	err = r.call("compare index tree", func(repo *C.git_repository) error {
		tree, err := commitTree(repo, revision)
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		matches = id == objectID(C.git_tree_id(tree))
		return nil
	})
	return matches, err
}

// Checkout safely advances a clean implementation checkout, applying native
// attributes/filters rather than the raw metadata-only checkout policy.
func (r *Repository) Checkout(base, target ID) error {
	if err := r.validateIndex(); err != nil {
		return err
	}
	return r.call("checkout implementation", func(repo *C.git_repository) error {
		restore := encodingComparison(repo)
		defer restore()
		old, err := commitTree(repo, base)
		if err != nil {
			return err
		}
		defer C.git_tree_free(old)
		next, err := commitTree(repo, target)
		if err != nil {
			return err
		}
		defer C.git_tree_free(next)
		if err := validateCheckoutOperation(repo, old, next, true); err != nil {
			return err
		}
		return checkoutPreservingSparse(repo, old, next, func() error {
			return nativeError("checkout implementation", C.task_checkout_implementation(repo, old, next))
		})
	})
}

func (r *Repository) RequireClean() error {
	changes, err := r.Status(false)
	if err != nil {
		return err
	}
	if len(changes) != 0 {
		paths, err := r.Paths()
		if err != nil {
			return err
		}
		return fmt.Errorf("worktree is dirty: %s; commit or move changes before merging", paths.WorkDir)
	}
	return r.RequireIdle()
}

func (r *Repository) RequireIdle() error {
	paths, err := r.Paths()
	if err != nil {
		return err
	}
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_START", "index.lock", "HEAD.lock"} {
		if _, err := os.Lstat(filepath.Join(paths.GitDir, name)); err == nil {
			return fmt.Errorf("Git operation in progress at %s: %s", paths.WorkDir, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
