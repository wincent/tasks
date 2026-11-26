package git

/*
#include <git2.h>
#include <git2/sys/odb_backend.h>
#include <git2/sys/repository.h>
#include <git2/sys/errors.h>
#include <stdlib.h>
#include <string.h>
// A disk alternate can still freshen existing objects. Forward only reads to
// the original ODB, including its packs/alternates, with no write/freshen API.
typedef struct { git_odb_backend base; git_odb *source; } task_sparse_readonly_odb;
static int task_sparse_odb_read(void **data, size_t *len, git_object_t *type,
 git_odb_backend *backend, const git_oid *id) {
 git_odb_object *object = NULL;
 int error = git_odb_read(&object, ((task_sparse_readonly_odb *)backend)->source, id);
 if (error < 0) return error;
 *len = git_odb_object_size(object);
 *type = git_odb_object_type(object);
 *data = git_odb_backend_data_alloc(backend, *len ? *len : 1);
 if (!*data) { git_odb_object_free(object); return GIT_ERROR; }
 memcpy(*data, git_odb_object_data(object), *len);
 git_odb_object_free(object);
 return 0;
}
static int task_sparse_odb_header(size_t *len, git_object_t *type,
 git_odb_backend *backend, const git_oid *id) {
 return git_odb_read_header(len, type, ((task_sparse_readonly_odb *)backend)->source, id);
}
static int task_sparse_odb_exists(git_odb_backend *backend, const git_oid *id) {
 return git_odb_exists(((task_sparse_readonly_odb *)backend)->source, id);
}
static int task_sparse_odb_refresh(git_odb_backend *backend) {
 return git_odb_refresh(((task_sparse_readonly_odb *)backend)->source);
}
static void task_sparse_odb_free(git_odb_backend *backend) {
 git_odb_free(((task_sparse_readonly_odb *)backend)->source);
 free(backend);
}
static int task_sparse_add_readonly_odb(git_odb *target, git_repository *source) {
 task_sparse_readonly_odb *backend = calloc(1, sizeof(*backend));
 if (!backend) { git_error_set_oom(); return GIT_ERROR; }
 int error = git_odb_init_backend(&backend->base, GIT_ODB_BACKEND_VERSION);
 if (!error) error = git_repository_odb(&backend->source, source);
 if (error < 0) { free(backend); return error; }
 backend->base.read = task_sparse_odb_read;
 backend->base.read_header = task_sparse_odb_header;
 backend->base.exists = task_sparse_odb_exists;
 backend->base.refresh = task_sparse_odb_refresh;
 backend->base.free = task_sparse_odb_free;
 error = git_odb_add_alternate(target, &backend->base, 0);
 if (error < 0) task_sparse_odb_free(&backend->base);
 return error;
}


*/
import "C"

import (
	"errors"
	"fmt"
	"os"
)

// A separate handle and read-only forwarding backend keep failed planning from
// writing OR freshening the source ODB, including objects in packs/alternates.
func withSparseMergePlan(source *C.git_repository, tip ID, driver *C.char, apply func(*C.git_index, sparsePlan) error) (err error) {
	directory, err := os.MkdirTemp("", "tasktool-sparse-merge-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	path, _ := cstring(directory)
	defer free(path)
	var odb *C.git_odb
	if err := nativeError("open sparse quarantine", C.git_odb_open(&odb, path)); err != nil {
		return err
	}
	defer C.git_odb_free(odb)
	if err := nativeError("attach read-only sparse objects", C.task_sparse_add_readonly_odb(odb, source)); err != nil {
		return err
	}
	var repo *C.git_repository
	if err := nativeError("open sparse planning handle", C.git_repository_open(&repo, C.git_repository_path(source))); err != nil {
		return err
	}
	defer C.git_repository_free(repo)
	restore := encodingComparison(repo)
	defer restore()
	if err := nativeError("select sparse quarantine", C.git_repository_set_odb(repo, odb)); err != nil {
		return err
	}
	var config *C.git_config
	if err := nativeError("read sparse merge config", C.git_repository_config_snapshot(&config, source)); err != nil {
		return err
	}
	defer C.git_config_free(config)
	if err := nativeError("select sparse merge config", C.git_repository_set_config(repo, config)); err != nil {
		return err
	}
	var head *C.git_reference
	if err := nativeError("read sparse merge HEAD", C.git_repository_head(&head, repo)); err != nil {
		return err
	}
	defer C.git_reference_free(head)
	var ours, theirs *C.git_commit
	if err := nativeError("read sparse ours", C.git_commit_lookup(&ours, repo, C.git_reference_target(head))); err != nil {
		return err
	}
	defer C.git_commit_free(ours)
	id := nativeID(tip)
	if err := nativeError("read sparse theirs", C.git_commit_lookup(&theirs, repo, &id)); err != nil {
		return err
	}
	defer C.git_commit_free(theirs)
	var options C.git_merge_options
	if err := nativeError("initialize sparse merge", C.git_merge_options_init(&options, C.GIT_MERGE_OPTIONS_VERSION)); err != nil {
		return err
	}
	options.default_driver = driver
	var after, before, current *C.git_index
	if err := nativeError("plan sparse merge", C.git_merge_commits(&after, repo, ours, theirs, &options)); err != nil {
		return err
	}
	defer C.git_index_free(after)
	var tree *C.git_tree
	if err := nativeError("read sparse merge tree", C.git_commit_tree(&tree, ours)); err != nil {
		return err
	}
	defer C.git_tree_free(tree)
	if err := nativeError("create sparse merge baseline", C.git_index_new(&before)); err != nil {
		return err
	}
	defer C.git_index_free(before)
	if err := nativeError("read sparse merge baseline", C.git_index_read_tree(before, tree)); err != nil {
		return err
	}
	if err := nativeError("read sparse merge index", C.git_repository_index(&current, repo)); err != nil {
		return err
	}
	defer C.git_index_free(current)
	if err := nativeError("refresh sparse merge index", C.git_index_read(current, 1)); err != nil {
		return err
	}
	// Native merge refuses staging anywhere unless it already equals the result.
	// Check this before it can write MERGE_HEAD or produce source ODB objects.
	for _, name := range sparseIndexPaths(before, current) {
		p, _ := cstring(name)
		old, entry, next := C.git_index_get_bypath(before, p, 0), C.git_index_get_bypath(current, p, 0), C.git_index_get_bypath(after, p, 0)
		free(p)
		if !sparseEqual(old, entry) && !sparseEqual(entry, next) {
			return fmt.Errorf("merge would overwrite staged changes at %q", name)
		}
	}
	if C.git_index_has_conflicts(current) != 0 {
		return errors.New("checkout requires an index without conflicts")
	}
	if C.git_index_has_conflicts(after) != 0 {
		return ErrMergeConflict
	}
	plan, err := planSparseIndex(repo, before, after, current)
	if err != nil {
		return err
	}
	// Leave the comparison context bound to the source during application.
	restore()
	return apply(after, plan)
}
