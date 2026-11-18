package git

/*
#include <git2.h>
*/
import "C"

import "unsafe"

// WorktreeBlob reads a regular blob through the native smudge pipeline. In a
// prospective callback, the borrowed handle supplies target config and index
// attributes; this method never writes or freshens source objects.
func (r *Repository) WorktreeBlob(path string, id ID) ([]byte, error) {
	if err := literalPath(path); err != nil {
		return nil, err
	}
	var result []byte
	err := r.call("read worktree blob", func(repo *C.git_repository) error {
		if err := validateOperationPaths(repo, []string{path}, "smudge", true); err != nil {
			return err
		}
		oid := nativeID(id)
		var blob *C.git_blob
		if err := nativeError("read prospective blob", C.git_blob_lookup(&blob, repo, &oid)); err != nil {
			return err
		}
		defer C.git_blob_free(blob)
		name, err := cstring(path)
		if err != nil {
			return err
		}
		defer free(name)
		var filters *C.git_filter_list
		if err := nativeError("load prospective smudge filters", C.git_filter_list_load(&filters, repo, blob, name, C.GIT_FILTER_TO_WORKTREE, 0)); err != nil {
			return err
		}
		defer C.git_filter_list_free(filters)
		var output C.git_buf
		defer C.git_buf_dispose(&output)
		if err := nativeError("smudge prospective blob", C.git_filter_list_apply_to_blob(&output, filters, blob)); err != nil {
			return err
		}
		result = append([]byte{}, unsafe.Slice((*byte)(unsafe.Pointer(output.ptr)), int(output.size))...)
		return nil
	})
	return result, err
}

// ValidateIndexPaths admits selected clean drivers without assuming a workdir.
// Use only on a prospective view whose index contains the target attributes.
func (r *Repository) ValidateIndexPaths(paths []string) error {
	return r.call("validate prospective selected paths", func(repo *C.git_repository) error {
		return validateOperationPaths(repo, paths, "clean", true)
	})
}
