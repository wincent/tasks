package git

/*
#include <git2.h>

static int task_index_add(git_index *index, const char *path,
                          const git_oid *id, unsigned int mode) {
    git_index_entry entry = {0};
    const git_index_entry *previous = git_index_get_bypath(index, path, 0);
    if (previous) {
        entry.flags = previous->flags;
        entry.flags_extended = previous->flags_extended;
    }
    entry.path = path;
    entry.id = *id;
    entry.mode = mode;
    return git_index_add(index, &entry);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// IndexEntry retains the safety-relevant flags in addition to mode/object/path.
// A nonzero stage is a conflict; AssumeValid and SkipWorktree hide local content
// from ordinary status and must be checked before destructive cleanup.
type IndexEntry struct {
	Path         string
	ID           ID
	Mode         uint32
	Stage        int
	AssumeValid  bool
	SkipWorktree bool
	IntentToAdd  bool
}

func validateRepositoryIndex(repo *C.git_repository) error {
	return validateIndexFormat(filepath.Clean(C.GoString(C.git_repository_path(repo))) + "/index")
}

// IndexEntries opens a new handle to the on-disk index, not a repository-cached
// copy. It never creates, refreshes or writes the real index.
func (r *Repository) IndexEntries() ([]IndexEntry, error) {
	var result []IndexEntry
	err := r.call("read index entries", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		var cached, index *C.git_index
		if err := nativeError("locate index", C.git_repository_index(&cached, repo)); err != nil {
			return err
		}
		defer C.git_index_free(cached)
		if err := nativeError("open fresh index", C.git_index_open(&index, C.git_index_path(cached))); err != nil {
			return err
		}
		defer C.git_index_free(index)
		for i := C.size_t(0); i < C.git_index_entrycount(index); i++ {
			entry := C.git_index_get_byindex(index, i)
			result = append(result, IndexEntry{
				Path: C.GoString(entry.path), ID: objectID(&entry.id), Mode: uint32(entry.mode),
				Stage:        int(C.git_index_entry_stage(entry)),
				AssumeValid:  entry.flags&C.GIT_INDEX_ENTRY_VALID != 0,
				SkipWorktree: entry.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0,
				IntentToAdd:  entry.flags_extended&C.GIT_INDEX_ENTRY_INTENT_TO_ADD != 0,
			})
		}
		return nil
	})
	return result, err
}

// TreeEdit uses a literal root-relative path. Zero mode deletes an entry;
// otherwise ID must already exist in the repository's object database.
type TreeEdit struct {
	Path string
	ID   ID
	Mode uint32
}

func editIndex(repo *C.git_repository, index *C.git_index, edits []TreeEdit) error {
	seen := make(map[string]bool)
	for _, edit := range edits {
		if err := literalPath(edit.Path); err != nil {
			return err
		}
		if seen[edit.Path] {
			return fmt.Errorf("duplicate index edit: %q", edit.Path)
		}
		seen[edit.Path] = true
		path, _ := cstring(edit.Path)
		if edit.Mode == 0 {
			code := C.git_index_remove_bypath(index, path)
			free(path)
			if code != C.GIT_ENOTFOUND {
				if err := nativeError("remove index entry", code); err != nil {
					return err
				}
			}
			continue
		}
		var kind C.git_object_t
		switch edit.Mode {
		case 0100644, 0100755, 0120000:
			kind = C.GIT_OBJECT_BLOB
		case 0160000:
			kind = C.GIT_OBJECT_COMMIT
		default:
			free(path)
			return fmt.Errorf("invalid index entry mode: %o", edit.Mode)
		}
		id := nativeID(edit.ID)
		// Gitlinks refer to another repository and need not exist in this ODB.
		if kind != C.GIT_OBJECT_COMMIT {
			var object *C.git_object
			if err := nativeError("validate index object", C.git_object_lookup(&object, repo, &id, kind)); err != nil {
				free(path)
				return err
			}
			C.git_object_free(object)
		}
		code := C.task_index_add(index, path, &id, C.uint(edit.Mode))
		free(path)
		if err := nativeError("update index entry", code); err != nil {
			return err
		}
	}
	return nil
}

// EditTree constructs a tree from a commit plus selected edits entirely in
// memory. A zero base starts an empty tree. The real index is never involved.
func (r *Repository) EditTree(base ID, edits []TreeEdit) (ID, error) {
	var result ID
	err := r.callNative("edit tree", false, func(repo *C.git_repository) error {
		var index *C.git_index
		if err := nativeError("create memory index", C.git_index_new(&index)); err != nil {
			return err
		}
		defer C.git_index_free(index)
		if base != (ID{}) {
			tree, err := commitTree(repo, base)
			if err != nil {
				return err
			}
			defer C.git_tree_free(tree)
			if err := nativeError("read base tree", C.git_index_read_tree(index, tree)); err != nil {
				return err
			}
		}
		if err := editIndex(repo, index, edits); err != nil {
			return err
		}
		var id C.git_oid
		if err := nativeError("write edited tree", C.git_index_write_tree_to(&id, index, repo)); err != nil {
			return err
		}
		result = objectID(&id)
		return nil
	})
	return result, err
}

// EditIndexFile edits an isolated index prepared by the caller. It refuses the
// real index path; callers own real-index locking and atomic installation. The
// optional base initializes a missing index from a commit. Existing conflict
// stages and unrelated entry flags survive edits to other paths.
func (r *Repository) EditIndexFile(path string, base ID, edits []TreeEdit) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	return r.call("edit isolated index", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		if err := validateIndexFormat(path); err != nil {
			return err
		}
		var real *C.git_index
		if err := nativeError("locate real index", C.git_repository_index(&real, repo)); err != nil {
			return err
		}
		defer C.git_index_free(real)
		realPath := filepath.Clean(C.GoString(C.git_index_path(real)))
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		if err != nil {
			return err
		}
		absolute = filepath.Join(parent, filepath.Base(absolute))
		realParent, err := filepath.EvalSymlinks(filepath.Dir(realPath))
		if err != nil {
			return err
		}
		if absolute == filepath.Join(realParent, filepath.Base(realPath)) {
			return errors.New("refusing to edit real index without publication protocol")
		}
		info, err := os.Lstat(absolute)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info != nil {
			if !info.Mode().IsRegular() {
				return errors.New("isolated index must be a regular file, not a symlink or directory")
			}
			realInfo, err := os.Stat(realPath)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if realInfo != nil && os.SameFile(info, realInfo) {
				return errors.New("isolated index aliases the real index")
			}
			if base != (ID{}) {
				return errors.New("refusing to replace an existing isolated index with a base tree")
			}
		}
		name, err := cstring(absolute)
		if err != nil {
			return err
		}
		defer free(name)
		var index *C.git_index
		if err := nativeError("open isolated index", C.git_index_open(&index, name)); err != nil {
			return err
		}
		defer C.git_index_free(index)
		if base != (ID{}) {
			tree, err := commitTree(repo, base)
			if err != nil {
				return err
			}
			defer C.git_tree_free(tree)
			if err := nativeError("initialize isolated index", C.git_index_read_tree(index, tree)); err != nil {
				return err
			}
		}
		if err := editIndex(repo, index, edits); err != nil {
			return err
		}
		return nativeError("write isolated index", C.git_index_write(index))
	})
}
