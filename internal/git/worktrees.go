package git

/*
#include <git2.h>
*/
import "C"

import (
	"path/filepath"
	"sort"
	"unsafe"
)

// Worktree describes a linked registration, including invalid and locked ones.
// The primary checkout is represented separately by PrimaryPaths. Invalid
// registrations remain visible and are never interpreted as safe to delete.
type Worktree struct {
	Name          string
	Path          string
	Locked        bool
	LockReason    string
	InvalidReason string
}

func (r *Repository) Worktrees() ([]Worktree, error) {
	var result []Worktree
	err := r.call("list linked worktrees", func(repo *C.git_repository) error {
		var names C.git_strarray
		if err := nativeError("list worktree names", C.git_worktree_list(&names, repo)); err != nil {
			return err
		}
		defer C.git_strarray_dispose(&names)
		for _, name := range unsafe.Slice(names.strings, int(names.count)) {
			var worktree *C.git_worktree
			if err := nativeError("lookup worktree", C.git_worktree_lookup(&worktree, repo, name)); err != nil {
				return err
			}
			entry := Worktree{Name: C.GoString(C.git_worktree_name(worktree)), Path: filepath.Clean(C.GoString(C.git_worktree_path(worktree)))}
			var reason C.git_buf
			code := C.git_worktree_is_locked(&reason, worktree)
			if err := nativeError("read worktree lock", code); err != nil {
				C.git_buf_dispose(&reason)
				C.git_worktree_free(worktree)
				return err
			}
			entry.Locked = code > 0
			if reason.ptr != nil {
				entry.LockReason = C.GoString(reason.ptr)
			}
			C.git_buf_dispose(&reason)
			if err := nativeError("validate worktree", C.git_worktree_validate(worktree)); err != nil {
				entry.InvalidReason = err.Error()
			}
			C.git_worktree_free(worktree)
			result = append(result, entry)
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, err
}

// PrimaryPaths discovers the primary repository, not the invoking linked
// checkout. Bare-in-.git layouts have no primary workdir, by design.
func (r *Repository) PrimaryPaths() (Paths, error) {
	paths, err := r.Paths()
	if err != nil {
		return Paths{}, err
	}
	primary, err := Open(paths.CommonDir)
	if err != nil {
		return Paths{}, err
	}
	defer primary.Close()
	return primary.Paths()
}
