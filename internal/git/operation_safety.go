package git

/*
#include <git2.h>
#include <git2/sys/repository.h>

static int task_attribute(const char **out, git_repository *repo,
                          const char *path, const char *name, int index_only) {
    return git_attr_get(out, repo, index_only ? GIT_ATTR_CHECK_INDEX_ONLY :
                        GIT_ATTR_CHECK_FILE_THEN_INDEX, path, name);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unsafe"
)

// Operation checks inspect only paths participating in a transformation. Merely
// configuring a driver, or tracking an unrelated filtered file, is harmless.
func configuredCommand(repo *C.git_repository, key string) (string, error) {
	var config *C.git_config
	if err := nativeError("read operation configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
		return "", err
	}
	defer C.git_config_free(config)
	name, err := cstring(key)
	if err != nil {
		return "", err
	}
	defer free(name)
	var value *C.char
	code := C.git_config_get_string(&value, config, name)
	if code == C.GIT_ENOTFOUND {
		return "", nil
	}
	if err := nativeError("read "+key, code); err != nil {
		return "", err
	}
	return C.GoString(value), nil
}

func operationAttribute(repo *C.git_repository, path, attribute string, indexOnly bool) (string, error) {
	p, err := cstring(path)
	if err != nil {
		return "", err
	}
	defer free(p)
	a, _ := cstring(attribute)
	defer free(a)
	var value *C.char
	var flags C.int
	if indexOnly {
		flags = 1
	}
	if err := nativeError("read "+attribute+" attribute", C.task_attribute(&value, repo, p, a, flags)); err != nil {
		return "", err
	}
	if C.git_attr_value(value) != C.GIT_ATTR_VALUE_STRING {
		return "", nil
	}
	return C.GoString(value), nil
}

func validateOperationPaths(repo *C.git_repository, paths []string, direction string, indexOnly bool) error {
	if err := nativeError("refresh attributes", C.git_attr_cache_flush(repo)); err != nil {
		return err
	}
	keys := []string{"clean", "smudge", "process"}
	if direction == "clean" {
		keys = []string{"clean", "process"}
	}
	if direction == "smudge" {
		keys = []string{"smudge", "process"}
	}
	for _, path := range paths {
		if err := literalPath(path); err != nil {
			return err
		}
		driver, err := operationAttribute(repo, path, "filter", indexOnly)
		if err != nil {
			return err
		}
		if driver == "" {
			continue
		}
		for _, key := range keys {
			command, err := configuredCommand(repo, "filter."+driver+"."+key)
			if err != nil {
				return err
			}
			if command != "" {
				return fmt.Errorf("path %q requires external filter driver %q (%s); tasktool does not execute external drivers; perform this operation with your normal Git workflow and retry (no filter bypass)", path, driver, key)
			}
		}
	}
	return nil
}

// withAttributeTree installs only an in-memory index, never a disk index. This
// checks target attributes even when they differ from the current checkout.
func withAttributeTree(repo *C.git_repository, tree *C.git_tree, f func() error) (err error) {
	var original, temporary *C.git_index
	if err = nativeError("read attribute index", C.git_repository_index(&original, repo)); err != nil {
		return err
	}
	defer C.git_index_free(original)
	if err = nativeError("create attribute index", C.git_index_new(&temporary)); err != nil {
		return err
	}
	defer C.git_index_free(temporary)
	if tree != nil {
		if err = nativeError("read attribute tree", C.git_index_read_tree(temporary, tree)); err != nil {
			return err
		}
	}
	return withAttributeIndex(repo, original, temporary, f)
}

func withAttributeIndex(repo *C.git_repository, original, temporary *C.git_index, f func() error) (err error) {
	if err = nativeError("select attribute index", C.git_repository_set_index(repo, temporary)); err != nil {
		return err
	}
	defer func() {
		restore := nativeError("restore attribute index", C.git_repository_set_index(repo, original))
		flush := nativeError("restore attributes", C.git_attr_cache_flush(repo))
		err = errors.Join(err, restore, flush)
	}()
	return f()
}

// Keep driver/filter selection and the actual operation on one immutable
// configuration view, including injected and worktree-specific configuration.
// This does not lock files against other processes; callers still need exclusive
// ownership of the worktree while preparing and performing a merge.
func withOperationConfig(repo *C.git_repository, f func() error) (err error) {
	var original, snapshot *C.git_config
	if err = nativeError("read operation config", C.git_repository_config(&original, repo)); err != nil {
		return err
	}
	defer C.git_config_free(original)
	if err = nativeError("snapshot operation config", C.git_config_snapshot(&snapshot, original)); err != nil {
		return err
	}
	defer C.git_config_free(snapshot)
	if err = nativeError("select operation config", C.git_repository_set_config(repo, snapshot)); err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, nativeError("restore operation config", C.git_repository_set_config(repo, original)))
	}()
	return f()
}

func changedOperationPaths(repo *C.git_repository, base, target *C.git_tree) ([]string, error) {
	var diff *C.git_diff
	if err := nativeError("inspect affected paths", C.git_diff_tree_to_tree(&diff, repo, base, target, nil)); err != nil {
		return nil, err
	}
	defer C.git_diff_free(diff)
	paths := []string{}
	for i := C.size_t(0); i < C.git_diff_num_deltas(diff); i++ {
		delta := C.git_diff_get_delta(diff, i)
		// Deletions require no smudge transformation, but checkout safety may need
		// to clean the existing file before deciding whether it can remove it.
		for _, file := range []*C.git_diff_file{&delta.old_file, &delta.new_file} {
			if file.path != nil && (file.mode == 0100644 || file.mode == 0100755) {
				paths = append(paths, C.GoString(file.path))
			}
		}
	}
	return paths, nil
}

func validateCheckoutOperation(repo *C.git_repository, base, target *C.git_tree, existing bool) error {
	paths, err := changedOperationPaths(repo, base, target)
	if existing {
		paths, err = sparseCheckoutPaths(repo, base, target)
		if err == nil {
			paths, err = sparseOperationPaths(repo, paths)
		}
	}
	if err != nil {
		return err
	}
	policy, err := readSparsePolicy(repo)
	if err != nil {
		return err
	}
	var current *C.git_index
	if err := nativeError("read checkout selection", C.git_repository_index(&current, repo)); err != nil {
		return err
	}
	defer C.git_index_free(current)
	if existing {
		if err := validateWorktreeContent(repo, paths); err != nil {
			return err
		}
	}
	var materialized []string
	for _, path := range paths {
		p, _ := cstring(path)
		if existing && sparseOmitted(policy, current, p) {
			free(p)
			continue
		}
		var entry *C.git_tree_entry
		code := C.git_tree_entry_bypath(&entry, target, p)
		free(p)
		if code == C.GIT_ENOTFOUND {
			continue
		}
		if err := nativeError("inspect checkout target", code); err != nil {
			return err
		}
		mode := C.git_tree_entry_filemode(entry)
		C.git_tree_entry_free(entry)
		if mode == 0100644 || mode == 0100755 {
			materialized = append(materialized, path)
		}
	}
	return withAttributeTree(repo, target, func() error {
		if err := validateOperationPaths(repo, materialized, "smudge", true); err != nil {
			return err
		}
		var index *C.git_index
		if err := nativeError("read encoding target", C.git_repository_index(&index, repo)); err != nil {
			return err
		}
		defer C.git_index_free(index)
		return validateIndexContent(repo, index, materialized)
	})
}

// Validate the entire regular-file selection before any object or file writes.
// Lstat deliberately excludes symlinks; their target bytes are never encoded.
func validateWorktreeContent(repo *C.git_repository, paths []string) error {
	return validateWorktreeContentWithValidation(repo, paths, true)
}

func validateWorktreeContentWithValidation(repo *C.git_repository, paths []string, validatePaths bool) error {
	var regular []string
	contents := make(map[string][]byte)
	for _, path := range paths {
		if validatePaths {
			if err := literalPath(path); err != nil {
				return err
			}
		}
		full := filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(path))
		info, err := os.Lstat(full)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		regular = append(regular, path)
		contents[path] = data
	}
	if validatePaths {
		if err := validateOperationPaths(repo, regular, "clean", false); err != nil {
			return err
		}
	}
	for _, path := range regular {
		if err := validateEncodingContent(repo, path, contents[path], true, false); err != nil {
			return err
		}
	}
	return nil
}

func validateIndexContent(repo *C.git_repository, index *C.git_index, paths []string) error {
	selected := make(map[string]bool)
	for _, path := range paths {
		selected[path] = true
	}
	for i := C.size_t(0); i < C.git_index_entrycount(index); i++ {
		entry := C.git_index_get_byindex(index, i)
		path := C.GoString(entry.path)
		if !selected[path] || (entry.mode != 0100644 && entry.mode != 0100755) {
			continue
		}
		var blob *C.git_blob
		if err := nativeError("read encoding content", C.git_blob_lookup(&blob, repo, &entry.id)); err != nil {
			return err
		}
		data := C.GoBytes(C.git_blob_rawcontent(blob), C.int(C.git_blob_rawsize(blob)))
		err := validateEncodingContent(repo, path, data, false, true)
		C.git_blob_free(blob)
		if err != nil {
			return err
		}
	}
	return nil
}

// ValidateWorktreePaths must be called for the complete selection before a
// caller writes selected files, objects, indexes or refs.
func (r *Repository) ValidateWorktreePaths(paths []string) error {
	return r.call("validate selected paths", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		// Proposed metadata files can be absent. They still require clean
		// driver validation before reservation or object creation.
		if err := validateOperationPaths(repo, paths, "clean", false); err != nil {
			return err
		}
		return validateWorktreeContentWithValidation(repo, paths, false)
	})
}

// ValidateWorktreeContents checks every proposed regular-file value without
// writing objects. Callers retain these exact bytes through publication.
func (r *Repository) ValidateWorktreeContents(contents map[string][]byte) error {
	paths := make([]string, 0, len(contents))
	for path := range contents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return r.call("validate proposed content", func(repo *C.git_repository) error {
		if err := validateRepositoryIndex(repo); err != nil {
			return err
		}
		if err := validateOperationPaths(repo, paths, "clean", false); err != nil {
			return err
		}
		for _, path := range paths {
			output, err := cleanValidatedWorktreeBuffer(repo, path, contents[path])
			C.git_buf_dispose(&output)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func cleanWorktreeBuffer(repo *C.git_repository, path string, data []byte) (output C.git_buf, err error) {
	if err := validateOperationPaths(repo, []string{path}, "clean", false); err != nil {
		return output, err
	}
	return cleanValidatedWorktreeBuffer(repo, path, data)
}

func cleanValidatedWorktreeBuffer(repo *C.git_repository, path string, data []byte) (output C.git_buf, err error) {
	if err := validateEncodingContent(repo, path, data, true, false); err != nil {
		return output, err
	}
	p, err := cstring(path)
	if err != nil {
		return output, err
	}
	defer free(p)
	var filters *C.git_filter_list
	if err := nativeError("load clean filters", C.git_filter_list_load(&filters, repo, nil, p, C.GIT_FILTER_TO_ODB, 0)); err != nil {
		return output, err
	}
	defer C.git_filter_list_free(filters)
	var input *C.char
	if len(data) != 0 {
		input = (*C.char)(unsafe.Pointer(&data[0]))
	}
	err = nativeError("clean selected content", C.git_filter_list_apply_to_buffer(&output, filters, input, C.size_t(len(data))))
	return output, err
}

// WriteWorktreeBlob applies libgit2's native clean transformations, including
// line endings. Raw object construction remains available through WriteBlob.
func (r *Repository) WriteWorktreeBlob(path string, data []byte) (ID, error) {
	return r.worktreeBlobID(path, data, true)
}

// WorktreeBlobID identifies the clean bytes without adding an object.
func (r *Repository) WorktreeBlobID(path string, data []byte) (ID, error) {
	return r.worktreeBlobID(path, data, false)
}

func (r *Repository) worktreeBlobID(path string, data []byte, write bool) (ID, error) {
	var result ID
	err := r.call("write worktree blob", func(repo *C.git_repository) error {
		output, err := cleanWorktreeBuffer(repo, path, data)
		defer C.git_buf_dispose(&output)
		if err != nil {
			return err
		}
		var id C.git_oid
		if write {
			if err := nativeError("write cleaned blob", C.git_blob_create_from_buffer(&id, repo, unsafe.Pointer(output.ptr), output.size)); err != nil {
				return err
			}
		} else if err := nativeError("hash cleaned blob", C.git_odb_hash(&id, unsafe.Pointer(output.ptr), output.size, C.GIT_OBJECT_BLOB)); err != nil {
			return err
		}
		result = objectID(&id)
		return nil
	})
	return result, err
}
