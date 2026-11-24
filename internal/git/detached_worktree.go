package git

/*
#include <git2.h>
#include <git2/sys/repository.h>
static int task_detached_worktree_register(git_worktree **out, git_repository *repo,
 const char *name, const char *path, git_reference *ref) {
 git_worktree_add_options options = GIT_WORKTREE_ADD_OPTIONS_INIT;
 options.ref = ref;
 options.checkout_options.checkout_strategy = GIT_CHECKOUT_NONE | GIT_CHECKOUT_DONT_UPDATE_INDEX;
 return git_worktree_add(out, repo, name, path, &options);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AddDetachedWorktree admits the explicit target tree and both commit actors in
// the future detached configuration before any registration, ref or checkout
// write. prepareParent, when non-nil, runs only after admission; it may prepare
// the destination's parent but must not call r or change repository inputs.
// Native exclusive directory creation and reference CAS protect collisions.
// After writes begin, failures retain evidence rather than remove unknown paths.
// The checked configuration is retained through checkout, not a filesystem lease.
func (r *Repository) AddDetachedWorktree(name, path string, revision ID, prepareParent func() error) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.call("add detached worktree", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		config, err := r.prospectiveWorktreeConfiguration(repo, name, absolute, "")
		if err != nil {
			return err
		}
		defer C.git_config_free(config)
		tree, err := commitTree(repo, revision)
		if err != nil {
			return err
		}
		defer C.git_tree_free(tree)
		var actors Signatures
		if err := withProspectiveWorktreeView(repo, config, tree, func(view *Repository) error {
			var err error
			actors, err = view.Signatures()
			return err
		}); err != nil {
			return err
		}
		if err := preflightWorktree(repo, config, tree, nil); err != nil {
			return err
		}
		n, err := cstring(name)
		if err != nil {
			return err
		}
		defer free(n)
		p, err := cstring(absolute)
		if err != nil {
			return err
		}
		defer free(p)
		branch := "refs/heads/task/" + name
		b, err := cstring(branch)
		if err != nil {
			return err
		}
		defer free(b)
		// Detect ordinary collisions without creating even the transient ref. Native
		// mkdir(EXCL) remains authoritative if another writer races this observation.
		if _, err := os.Lstat(absolute); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return fmt.Errorf("worktree destination already exists: %s", absolute)
		}
		if prepareParent != nil {
			if err := prepareParent(); err != nil {
				return err
			}
		}
		// A borrowed wrapper avoids recursively locking r. Prepare owns its separate
		// native handle and takes explicit actors; it does not resolve source identity.
		source := &Repository{ptr: repo}
		update := func(next, expected ID, message string) error {
			tx, err := source.Prepare(branch, next, map[string]ID{branch: expected}, actors.Committer, message)
			if err != nil {
				return err
			}
			defer tx.Close()
			return tx.Commit()
		}
		retain := func(err error) error {
			return fmt.Errorf("detached worktree setup failed; inspect retained branch %s and path %s: %w", branch, absolute, err)
		}
		if err := update(revision, ID{}, "prepare detached worktree"); err != nil {
			return retain(err)
		}
		var ref *C.git_reference
		if err := nativeError("lookup transient worktree branch", C.git_reference_lookup(&ref, repo, b)); err != nil {
			return retain(err)
		}
		defer C.git_reference_free(ref)
		if C.git_reference_type(ref) != C.GIT_REFERENCE_DIRECT || objectID(C.git_reference_target(ref)) != revision {
			return retain(errors.New("transient worktree branch changed before registration"))
		}
		var worktree *C.git_worktree
		if err := nativeError("register detached worktree", C.task_detached_worktree_register(&worktree, repo, n, p, ref)); err != nil {
			return retain(err)
		}
		C.git_worktree_free(worktree)
		var child *C.git_repository
		if err := nativeError("open detached worktree", C.git_repository_open(&child, p)); err != nil {
			return retain(err)
		}
		defer C.git_repository_free(child)
		if err := nativeError("select detached configuration", C.git_repository_set_config(child, config)); err != nil {
			return retain(err)
		}
		if err := detachRegisteredWorktree(child, branch, revision, actors.Committer); err != nil {
			return retain(err)
		}
		if err := materializeWorktree(p, config, tree); err != nil {
			return retain(err)
		}
		if err := update(ID{}, revision, "remove detached worktree registration branch"); err != nil {
			return retain(err)
		}
		return nil
	})
}

// A public reference transaction replaces symbolic HEAD without resolving or
// updating its branch. Its reflog uses the admitted committer, never the author
// or a newly resolved native default identity.
func detachRegisteredWorktree(repo *C.git_repository, branch string, revision ID, who Signature) error {
	var tx *C.git_transaction
	if err := nativeError("allocate detached HEAD transaction", C.git_transaction_new(&tx, repo)); err != nil {
		return err
	}
	defer C.git_transaction_free(tx)
	head, err := cstring("HEAD")
	if err != nil {
		return err
	}
	defer free(head)
	if err := nativeError("lock detached HEAD", C.git_transaction_lock_ref(tx, head)); err != nil {
		return err
	}
	actual, err := lookup(repo, "HEAD")
	if err != nil {
		return err
	}
	if actual.Symbolic != branch {
		return errors.New("registered worktree HEAD changed before detaching")
	}
	actor, err := signature(who)
	if err != nil {
		return err
	}
	defer C.git_signature_free(actor)
	message, err := cstring("detach integration candidate")
	if err != nil {
		return err
	}
	defer free(message)
	id := nativeID(revision)
	if err := nativeError("prepare detached HEAD", C.git_transaction_set_target(tx, head, &id, actor, message)); err != nil {
		return err
	}
	return nativeError("publish detached HEAD", C.git_transaction_commit(tx))
}
