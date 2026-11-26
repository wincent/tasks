package git

/*
#include <git2.h>
#include <stdlib.h>

static int task_sparse_checkout(git_repository *repo, git_tree *base,
                                git_index *target, char **paths, size_t count) {
    git_checkout_options opts = GIT_CHECKOUT_OPTIONS_INIT;
    opts.checkout_strategy = GIT_CHECKOUT_SAFE |
        GIT_CHECKOUT_DONT_OVERWRITE_IGNORED | GIT_CHECKOUT_DONT_UPDATE_INDEX |
        GIT_CHECKOUT_DISABLE_PATHSPEC_MATCH | GIT_CHECKOUT_RECREATE_MISSING;
    opts.baseline = base;
    opts.paths.strings = paths;
    opts.paths.count = count;
    return git_checkout_index(repo, target, &opts);
}

static int task_sparse_valid(git_index *index, const char *path, int valid) {
    const git_index_entry *old = git_index_get_bypath(index, path, 0);
    if (!old) return 0;
    git_index_entry entry = *old;
    if (valid) entry.flags |= GIT_INDEX_ENTRY_VALID;
    else entry.flags &= ~GIT_INDEX_ENTRY_VALID;
    return git_index_add(index, &entry);
}

static int task_sparse_update(git_index *index, const git_index_entry *target,
                              int skip) {
    git_index_entry entry = *target;
    const git_index_entry *old = git_index_get_bypath(index, target->path, 0);
    if (old) {
        entry.flags = old->flags;
        entry.flags_extended = old->flags_extended;
    }
    entry.flags_extended &= ~GIT_INDEX_ENTRY_SKIP_WORKTREE;
    if (skip) {
        entry.flags |= GIT_INDEX_ENTRY_EXTENDED;
        entry.flags_extended |= GIT_INDEX_ENTRY_SKIP_WORKTREE;
    }
    return git_index_add(index, &entry);
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

type sparsePolicy struct {
	enabled  bool
	patterns sparsePatterns
}

func readSparsePolicy(repo *C.git_repository) (sparsePolicy, error) {
	var policy sparsePolicy
	var config *C.git_config
	if err := nativeError("read sparse configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
		return policy, err
	}
	defer C.git_config_free(config)
	readBool := func(key string) (bool, error) {
		name, _ := cstring(key)
		defer free(name)
		var value C.int
		code := C.git_config_get_bool(&value, config, name)
		if code == C.GIT_ENOTFOUND {
			return false, nil
		}
		return value != 0, nativeError("read "+key, code)
	}
	var err error
	policy.enabled, err = readBool("core.sparseCheckout")
	if err != nil || !policy.enabled {
		return policy, err
	}
	ignoreCase, err := readBool("core.ignoreCase")
	if err != nil {
		return policy, err
	}
	// sparse-checkout is per-worktree, even when the ordinary info directory
	// comes from the common repository. Never fall back to another worktree's file.
	path := filepath.Join(C.GoString(C.git_repository_path(repo)), "info", "sparse-checkout")
	info, err := os.Lstat(path)
	if err != nil {
		return policy, fmt.Errorf("read sparse-checkout patterns: %w", err)
	}
	if !info.Mode().IsRegular() {
		return policy, errors.New("sparse-checkout patterns must be a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return policy, err
	}
	policy.patterns, err = parseSparsePatterns(string(data), ignoreCase)
	return policy, err
}

func sparseOmitted(policy sparsePolicy, index *C.git_index, path *C.char) bool {
	if policy.enabled {
		return !policy.patterns.includes(C.GoString(path))
	}
	entry := C.git_index_get_bypath(index, path, 0)
	if entry != nil && entry.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0 {
		return true
	}
	return false
}

func sparseOperationPaths(repo *C.git_repository, paths []string) ([]string, error) {
	policy, err := readSparsePolicy(repo)
	if err != nil {
		return nil, err
	}
	var index *C.git_index
	if err := nativeError("read sparse operation index", C.git_repository_index(&index, repo)); err != nil {
		return nil, err
	}
	defer C.git_index_free(index)
	if err := nativeError("refresh sparse operation index", C.git_index_read(index, 1)); err != nil {
		return nil, err
	}
	var selected []string
	for _, path := range paths {
		p, _ := cstring(path)
		omitted := sparseOmitted(policy, index, p)
		free(p)
		_, presentErr := os.Lstat(filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(path)))
		if !omitted || presentErr == nil {
			selected = append(selected, path)
		} else if !errors.Is(presentErr, os.ErrNotExist) {
			return nil, presentErr
		}
	}
	return selected, nil
}

// Include pattern transitions even when the tree content did not change.
func sparseCheckoutPaths(repo *C.git_repository, base, target *C.git_tree) ([]string, error) {
	paths, err := changedOperationPaths(repo, base, target)
	if err != nil {
		return nil, err
	}
	policy, err := readSparsePolicy(repo)
	if err != nil {
		return nil, err
	}
	var index *C.git_index
	if err := nativeError("read sparse transitions", C.git_repository_index(&index, repo)); err != nil {
		return nil, err
	}
	defer C.git_index_free(index)
	if err := nativeError("refresh sparse transitions", C.git_index_read(index, 1)); err != nil {
		return nil, err
	}
	for i := C.size_t(0); i < C.git_index_entrycount(index); i++ {
		e := C.git_index_get_byindex(index, i)
		if sparseOmitted(policy, index, e.path) != (e.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0) {
			paths = append(paths, C.GoString(e.path))
		}
	}
	return paths, nil
}

func sparseEqual(a, b *C.git_index_entry) bool {
	return a == nil && b == nil || a != nil && b != nil && a.mode == b.mode && C.git_oid_equal(&a.id, &b.id) != 0
}

func sparseIndexPaths(indexes ...*C.git_index) []string {
	names := map[string]bool{}
	for _, index := range indexes {
		for i := C.size_t(0); i < C.git_index_entrycount(index); i++ {
			names[C.GoString(C.git_index_get_byindex(index, i).path)] = true
		}
	}
	paths := make([]string, 0, len(names))
	for p := range names {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// Explicitly clean/hash flagged files without writing an object or trusting
// index stat caches. The complete plan is checked before checkout can mutate.
func sparseCheckClean(repo *C.git_repository, entry *C.git_index_entry, path string, current *C.git_index) error {
	full := filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if entry == nil {
		if info.IsDir() {
			return filepath.WalkDir(full, func(child string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					return nil
				}
				name, err := filepath.Rel(C.GoString(C.git_repository_workdir(repo)), child)
				if err != nil {
					return err
				}
				name = filepath.ToSlash(name)
				p, _ := cstring(name)
				tracked := C.git_index_get_bypath(current, p, 0)
				free(p)
				if tracked == nil {
					return fmt.Errorf("checkout would overwrite untracked or ignored path %q", name)
				}
				return sparseCheckClean(repo, tracked, name, current)
			})
		}
		return fmt.Errorf("checkout would overwrite untracked or ignored path %q", path)
	}
	var data []byte
	if entry.mode == 0120000 && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		if err != nil {
			return err
		}
		data = []byte(target)
	} else if (entry.mode == 0100644 || entry.mode == 0100755) && info.Mode().IsRegular() {
		if err := validateWorktreeContent(repo, []string{path}); err != nil {
			return err
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		p, _ := cstring(path)
		defer free(p)
		var filters *C.git_filter_list
		if err := nativeError("load sparse clean filters", C.git_filter_list_load(&filters, repo, nil, p, C.GIT_FILTER_TO_ODB, 0)); err != nil {
			return err
		}
		defer C.git_filter_list_free(filters)
		var input *C.char
		if len(raw) > 0 {
			input = (*C.char)(unsafe.Pointer(&raw[0]))
		}
		var output C.git_buf
		if err := nativeError("clean sparse content", C.git_filter_list_apply_to_buffer(&output, filters, input, C.size_t(len(raw)))); err != nil {
			return err
		}
		data = C.GoBytes(unsafe.Pointer(output.ptr), C.int(output.size))
		C.git_buf_dispose(&output)
		filemode, err := configuredCommand(repo, "core.filemode")
		if err != nil {
			return err
		}
		if filemode != "false" && ((info.Mode()&0111 != 0) != (entry.mode == 0100755)) {
			return fmt.Errorf("checkout would overwrite dirty mode at %q", path)
		}
	} else {
		return fmt.Errorf("checkout would overwrite changed file type at %q", path)
	}
	var id C.git_oid
	var input unsafe.Pointer
	if len(data) > 0 {
		input = unsafe.Pointer(&data[0])
	}
	if err := nativeError("hash sparse content", C.git_odb_hash(&id, input, C.size_t(len(data)), C.GIT_OBJECT_BLOB)); err != nil {
		return err
	}
	if C.git_oid_equal(&id, &entry.id) == 0 {
		return fmt.Errorf("checkout would overwrite dirty path %q", path)
	}
	return nil
}

type sparseUpdate struct {
	path    string
	omitted bool
}
type sparsePlan struct {
	updates  []sparseUpdate
	selected []string
}

func planSparseIndex(repo *C.git_repository, before, after, current *C.git_index) (sparsePlan, error) {
	var plan sparsePlan
	policy, err := readSparsePolicy(repo)
	if err != nil {
		return plan, err
	}
	if C.git_index_has_conflicts(current) != 0 {
		return plan, errors.New("checkout requires an index without conflicts")
	}
	for _, name := range sparseIndexPaths(before, after, current) {
		p, _ := cstring(name)
		old, next, entry := C.git_index_get_bypath(before, p, 0), C.git_index_get_bypath(after, p, 0), C.git_index_get_bypath(current, p, 0)
		omitted := sparseOmitted(policy, current, p)
		wasOmitted := entry != nil && entry.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0
		free(p)
		transition := next != nil && omitted != wasOmitted
		if sparseEqual(old, next) && !transition {
			continue
		}
		if !sparseEqual(entry, old) && !sparseEqual(entry, next) {
			return plan, fmt.Errorf("checkout would overwrite staged changes at %q", name)
		}
		if err := sparseCheckClean(repo, entry, name, current); err != nil {
			return plan, err
		}
		plan.updates = append(plan.updates, sparseUpdate{name, omitted})
		_, presentErr := os.Lstat(filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(name)))
		if !omitted || presentErr == nil {
			plan.selected = append(plan.selected, name)
		} else if !errors.Is(presentErr, os.ErrNotExist) {
			return plan, presentErr
		}
	}
	if err := validateWorktreeContent(repo, plan.selected); err != nil {
		return plan, err
	}
	var smudge []string
	for _, u := range plan.updates {
		if !u.omitted {
			smudge = append(smudge, u.path)
		}
	}
	err = withAttributeIndex(repo, current, after, func() error {
		if err := validateOperationPaths(repo, smudge, "smudge", true); err != nil {
			return err
		}
		return validateIndexContent(repo, after, smudge)
	})
	return plan, err
}

// libgit2's one-sided workdir diff honors assume-valid, but not skip-worktree.
// Temporarily mark only verified absent skipped entries for native merge's
// safety diff. The native dry run must never write this borrowed index.
func withSparseMergeFlags(repo *C.git_repository, index *C.git_index, f func() error) (err error) {
	var marked []string
	defer func() {
		for _, path := range marked {
			p, _ := cstring(path)
			err = errors.Join(err, nativeError("restore sparse merge flags", C.task_sparse_valid(index, p, 0)))
			free(p)
		}
	}()
	for _, path := range sparseIndexPaths(index) {
		p, _ := cstring(path)
		e := C.git_index_get_bypath(index, p, 0)
		if e != nil && e.flags_extended&C.GIT_INDEX_ENTRY_SKIP_WORKTREE != 0 && e.flags&C.GIT_INDEX_ENTRY_VALID == 0 {
			_, statErr := os.Lstat(filepath.Join(C.GoString(C.git_repository_workdir(repo)), filepath.FromSlash(path)))
			if errors.Is(statErr, os.ErrNotExist) {
				if err := nativeError("mask absent sparse merge path", C.task_sparse_valid(index, p, 1)); err != nil {
					free(p)
					return err
				}
				marked = append(marked, path)
			} else if statErr != nil {
				free(p)
				return statErr
			}
		}
		free(p)
	}
	return f()
}

func copySparseIndex(source *C.git_index) (*C.git_index, error) {
	var result *C.git_index
	if err := nativeError("create index copy", C.git_index_new(&result)); err != nil {
		return nil, err
	}
	for i := C.size_t(0); i < C.git_index_entrycount(source); i++ {
		if err := nativeError("copy index entry", C.git_index_add(result, C.git_index_get_byindex(source, i))); err != nil {
			C.git_index_free(result)
			return nil, err
		}
	}
	return result, nil
}

func applySparseIndex(repo *C.git_repository, base *C.git_tree, after, current *C.git_index, plan sparsePlan) error {
	visible, err := copySparseIndex(after)
	if err != nil {
		return err
	}
	defer C.git_index_free(visible)
	for _, u := range plan.updates {
		if u.omitted {
			p, _ := cstring(u.path)
			code := C.git_index_remove_bypath(visible, p)
			free(p)
			if code != C.GIT_ENOTFOUND {
				if err := nativeError("omit sparse target", code); err != nil {
					return err
				}
			}
		}
	}
	if len(plan.selected) > 0 {
		array := C.malloc(C.size_t(len(plan.selected)) * C.size_t(unsafe.Sizeof(uintptr(0))))
		if array == nil {
			return errors.New("allocate sparse paths")
		}
		defer C.free(array)
		pointers := unsafe.Slice((**C.char)(array), len(plan.selected))
		for i, p := range plan.selected {
			pointers[i], _ = cstring(p)
			defer free(pointers[i])
		}
		if err := nativeError("checkout sparse selection", C.task_sparse_checkout(repo, base, visible, (**C.char)(array), C.size_t(len(plan.selected)))); err != nil {
			return err
		}
	}
	for _, u := range plan.updates {
		p, _ := cstring(u.path)
		next := C.git_index_get_bypath(after, p, 0)
		var code C.int
		if next != nil {
			var skip C.int
			if u.omitted {
				skip = 1
			}
			code = C.task_sparse_update(current, next, skip)
		} else {
			code = C.git_index_remove_bypath(current, p)
			if code == C.GIT_ENOTFOUND {
				code = 0
			}
		}
		free(p)
		if err := nativeError("update sparse index", code); err != nil {
			return err
		}
	}
	return nativeError("write sparse index", C.git_index_write(current))
}

func checkoutPreservingSparse(repo *C.git_repository, base, target *C.git_tree, ordinary func() error) error {
	policy, err := readSparsePolicy(repo)
	if err != nil {
		return err
	}
	var current, before, after *C.git_index
	if err := nativeError("read sparse index", C.git_repository_index(&current, repo)); err != nil {
		return err
	}
	defer C.git_index_free(current)
	if err := nativeError("refresh sparse index", C.git_index_read(current, 1)); err != nil {
		return err
	}
	adapt := policy.enabled
	for i := C.size_t(0); i < C.git_index_entrycount(current); i++ {
		e := C.git_index_get_byindex(current, i)
		adapt = adapt || e.flags&C.GIT_INDEX_ENTRY_VALID != 0 || e.flags_extended != 0
	}
	if !adapt {
		return ordinary()
	}
	if err := nativeError("create sparse baseline", C.git_index_new(&before)); err != nil {
		return err
	}
	defer C.git_index_free(before)
	if base != nil {
		if err := nativeError("read sparse baseline", C.git_index_read_tree(before, base)); err != nil {
			return err
		}
	}
	if err := nativeError("create sparse target", C.git_index_new(&after)); err != nil {
		return err
	}
	defer C.git_index_free(after)
	if err := nativeError("read sparse target", C.git_index_read_tree(after, target)); err != nil {
		return err
	}
	plan, err := planSparseIndex(repo, before, after, current)
	if err != nil {
		return err
	}
	return applySparseIndex(repo, base, after, current, plan)
}
