package git

/*
#include <git2.h>
#include <git2/sys/repository.h>
static int task_prospective_index_add(git_index *index, const char *path, const git_oid *id) {
 git_index_entry entry = {0};
 entry.path = path;
 entry.mode = GIT_FILEMODE_BLOB;
 git_oid_cpy(&entry.id, id);
 return git_index_add(index, &entry);
}
static int task_checkout_new_worktree(git_repository *repo, git_tree *tree) {
 git_checkout_options options = GIT_CHECKOUT_OPTIONS_INIT;
 options.checkout_strategy = GIT_CHECKOUT_SAFE | GIT_CHECKOUT_RECREATE_MISSING;
 return git_checkout_tree(repo, (git_object *)tree, &options);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unsafe"
)

// No workdir is exposed to the planning handle: FILE_THEN_INDEX filters can
// therefore see the target index, never the caller's .gitattributes. Every
// checkout preflight reads existing objects or transforms buffers. Proposed
// content writes are isolated below; no on-disk index or registration is changed.
func withProspectiveWorktreeView(source *C.git_repository, config *C.git_config, tree *C.git_tree, inspect func(*Repository) error) error {
	var repo *C.git_repository
	if err := nativeError("open worktree planning view", C.git_repository_open_bare(&repo, C.git_repository_path(source))); err != nil {
		return err
	}
	defer C.git_repository_free(repo)
	if err := nativeError("select worktree planning config", C.git_repository_set_config(repo, config)); err != nil {
		return err
	}
	return withAttributeTree(repo, tree, func() error { return inspect(&Repository{ptr: repo}) })
}

func preflightWorktree(source *C.git_repository, config *C.git_config, tree *C.git_tree, contents map[string][]byte) error {
	return withProspectiveWorktreeView(source, config, tree, func(view *Repository) error {
		repo := view.ptr
		if tree != nil {
			paths, err := changedOperationPaths(repo, nil, tree)
			if err != nil {
				return err
			}
			if err := validateOperationPaths(repo, paths, "smudge", true); err != nil {
				return err
			}
			var index *C.git_index
			if err := nativeError("read planned worktree index", C.git_repository_index(&index, repo)); err != nil {
				return err
			}
			defer C.git_index_free(index)
			if err := validateIndexContent(repo, index, paths); err != nil {
				return err
			}
		}
		paths := make([]string, 0, len(contents))
		for path := range contents {
			if err := literalPath(path); err != nil {
				return err
			}
			if strings.Contains(path, "/") {
				return errors.New("prospective content must be a root regular file")
			}
			paths = append(paths, path)
		}
		sort.Strings(paths)
		if len(paths) == 0 {
			return nil
		}
		return withProspectiveContentObjects(repo, source, func() error {
			var index *C.git_index
			if err := nativeError("read proposed content index", C.git_repository_index(&index, repo)); err != nil {
				return err
			}
			defer C.git_index_free(index)
			// Model the future workdir's attributes before cleaning ANY selection.
			// Keep other baseline entries until cleaning is complete: safeCRLF
			// consults the old index, not an uncleaned copy of the proposed file.
			if attrs, ok := contents[".gitattributes"]; ok {
				if err := addProspectiveContent(repo, index, ".gitattributes", attrs); err != nil {
					return err
				}
				if err := nativeError("refresh proposed attributes", C.git_attr_cache_flush(repo)); err != nil {
					return err
				}
			}
			if err := validateOperationPaths(repo, paths, "clean", true); err != nil {
				return err
			}
			cleaned := make(map[string][]byte, len(paths))
			for _, path := range paths {
				output, err := cleanWorktreeBuffer(repo, path, contents[path])
				if err == nil {
					cleaned[path] = append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(output.ptr)), int(output.size))...)
				}
				C.git_buf_dispose(&output)
				if err != nil {
					return err
				}
			}
			// Complete the proposed target index only with validated clean bytes.
			// Neither this index nor its quarantined blobs are ever published.
			for _, path := range paths {
				if err := addProspectiveContent(repo, index, path, cleaned[path]); err != nil {
					return err
				}
			}
			return nil
		})
	})
}

// All writes use a disposable ODB. A disk alternate is insufficient because it
// can freshen source objects; the forwarding backend has no write/freshen API.
func withProspectiveContentObjects(repo, source *C.git_repository, inspect func() error) (err error) {
	dir, err := os.MkdirTemp("", "tasktool-content-preflight-")
	if err != nil {
		return err
	}
	defer func() {
		if cleanup := os.RemoveAll(dir); cleanup != nil {
			err = errors.Join(err, fmt.Errorf("remove content quarantine: %w", cleanup))
		}
	}()
	path, err := cstring(dir)
	if err != nil {
		return err
	}
	defer free(path)
	var odb *C.git_odb
	if err := nativeError("open content quarantine", C.git_odb_open(&odb, path)); err != nil {
		return err
	}
	defer C.git_odb_free(odb)
	if err := attachReadOnlyObjects(odb, source); err != nil {
		return err
	}
	if err := nativeError("select content quarantine", C.git_repository_set_odb(repo, odb)); err != nil {
		return err
	}
	return inspect()
}

func addProspectiveContent(repo *C.git_repository, index *C.git_index, path string, data []byte) error {
	var buffer unsafe.Pointer
	if len(data) != 0 {
		buffer = unsafe.Pointer(&data[0])
	}
	var id C.git_oid
	if err := nativeError("write quarantined content", C.git_blob_create_from_buffer(&id, repo, buffer, C.size_t(len(data)))); err != nil {
		return err
	}
	name, err := cstring(path)
	if err != nil {
		return err
	}
	defer free(name)
	return nativeError("index quarantined content", C.task_prospective_index_add(index, name, &id))
}

func materializeWorktree(path *C.char, config *C.git_config, tree *C.git_tree) error {
	var child *C.git_repository
	if err := nativeError("open registered worktree", C.git_repository_open(&child, path)); err != nil {
		return err
	}
	defer C.git_repository_free(child)
	if err := nativeError("select checked worktree configuration", C.git_repository_set_config(child, config)); err != nil {
		return err
	}
	// Checkout requires an object owned by this exact handle, even though
	// parent and child share the same object database.
	var childTree *C.git_tree
	if err := nativeError("read registered worktree tree", C.git_tree_lookup(&childTree, child, C.git_tree_id(tree))); err != nil {
		return err
	}
	defer C.git_tree_free(childTree)
	// Seed target attributes before any native filter sees the new workdir.
	var index *C.git_index
	if err := nativeError("read new worktree index", C.git_repository_index(&index, child)); err != nil {
		return err
	}
	defer C.git_index_free(index)
	if err := nativeError("seed new worktree attributes", C.git_index_read_tree(index, childTree)); err != nil {
		return err
	}
	return nativeError("materialize checked worktree", C.task_checkout_new_worktree(child, childTree))
}

// WithProspectiveWorktree supplies a borrowed target-configured repository for
// read-only inspection (including signature resolution) before registration.
// The callback must not retain/close the handle or invoke mutation methods.
// It sees the target tree's index attributes and no source workdir attributes.
func (r *Repository) WithProspectiveWorktree(name, path, branch string, inspect func(*Repository) error) error {
	return r.withProspectiveWorktree(name, path, branch, func(repo *C.git_repository, config *C.git_config, tree *C.git_tree) error {
		return withProspectiveWorktreeView(repo, config, tree, inspect)
	})
}

// ValidateProspectiveWorktreeContents checks initial checkout and proposed root
// regular-file contents before Session.Ensure or any reservation/object write.
// branch may be unborn; it must be the exact fully-qualified future branch.
// This is read-only admission, not a lease: AddWorktree repeats checkout
// admission, and callers must retain/revalidate their selected contents.
func (r *Repository) ValidateProspectiveWorktreeContents(name, path, branch string, contents map[string][]byte) error {
	return r.withProspectiveWorktree(name, path, branch, func(repo *C.git_repository, config *C.git_config, tree *C.git_tree) error {
		return preflightWorktree(repo, config, tree, contents)
	})
}

func (r *Repository) withProspectiveWorktree(name, path, branch string, inspect func(*C.git_repository, *C.git_config, *C.git_tree) error) error {
	if !strings.HasPrefix(branch, "refs/heads/") {
		return errors.New("worktree requires a fully qualified branch reference")
	}
	if err := r.validateIndex(); err != nil {
		return err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.call("validate prospective worktree", func(repo *C.git_repository) error {
		config, err := r.prospectiveWorktreeConfiguration(repo, name, absolute, branch)
		if err != nil {
			return err
		}
		defer C.git_config_free(config)
		var tree *C.git_tree
		ref, err := lookup(repo, branch)
		if err != nil && !HasCode(err, NotFound) {
			return err
		}
		if err == nil {
			if ref.Symbolic != "" {
				return errors.New("worktree branch must not be symbolic")
			}
			tree, err = commitTree(repo, ref.ID)
			if err != nil {
				return err
			}
			defer C.git_tree_free(tree)
		}
		return inspect(repo, config, tree)
	})
}
