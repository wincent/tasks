package git

/*
#include <git2.h>
#include <git2/sys/merge.h>
#include <git2/sys/errors.h>
#include <git2/sys/odb_backend.h>
#include <git2/sys/repository.h>
#include <stdlib.h>
#include <string.h>

// A disk alternate can still freshen existing objects. Forward only reads to
// the original ODB, including its packs/alternates, with no write/freshen API.
typedef struct { git_odb_backend base; git_odb *source; } task_readonly_odb;
static int task_odb_read(void **data, size_t *len, git_object_t *type,
 git_odb_backend *backend, const git_oid *id) {
 git_odb_object *object = NULL;
 int error = git_odb_read(&object, ((task_readonly_odb *)backend)->source, id);
 if (error < 0) return error;
 *len = git_odb_object_size(object);
 *type = git_odb_object_type(object);
 *data = git_odb_backend_data_alloc(backend, *len ? *len : 1);
 if (!*data) { git_odb_object_free(object); return GIT_ERROR; }
 memcpy(*data, git_odb_object_data(object), *len);
 git_odb_object_free(object);
 return 0;
}
static int task_odb_header(size_t *len, git_object_t *type,
 git_odb_backend *backend, const git_oid *id) {
 return git_odb_read_header(len, type, ((task_readonly_odb *)backend)->source, id);
}
static int task_odb_exists(git_odb_backend *backend, const git_oid *id) {
 return git_odb_exists(((task_readonly_odb *)backend)->source, id);
}
static int task_odb_refresh(git_odb_backend *backend) {
 return git_odb_refresh(((task_readonly_odb *)backend)->source);
}
static void task_odb_free(git_odb_backend *backend) {
 git_odb_free(((task_readonly_odb *)backend)->source);
 free(backend);
}
static int task_add_readonly_odb(git_odb *target, git_repository *source) {
 task_readonly_odb *backend = calloc(1, sizeof(*backend));
 if (!backend) { git_error_set_oom(); return GIT_ERROR; }
 int error = git_odb_init_backend(&backend->base, GIT_ODB_BACKEND_VERSION);
 if (!error) error = git_repository_odb(&backend->source, source);
 if (error < 0) { free(backend); return error; }
 backend->base.read = task_odb_read;
 backend->base.read_header = task_odb_header;
 backend->base.exists = task_odb_exists;
 backend->base.refresh = task_odb_refresh;
 backend->base.free = task_odb_free;
 error = git_odb_add_alternate(target, &backend->base, 0);
 if (error < 0) task_odb_free(&backend->base);
 return error;
}

static int task_reject_external_merge(git_merge_driver *self,
 const char **path_out, uint32_t *mode_out, git_buf *merged_out,
 const char *name, const git_merge_driver_source *source) {
 const git_index_entry *entry = git_merge_driver_source_ours(source);
 if (!entry) entry = git_merge_driver_source_theirs(source);
 git_error_set(GIT_ERROR_MERGE,
  "path '%s' requires external merge driver '%s'; tasktool does not execute external drivers; resolve using your normal Git workflow and retry (no text fallback)",
  entry ? entry->path : "(unknown)", name);
 return GIT_ERROR;
}
// Builtin names cannot be unregistered. Wrap their public apply callbacks
// under the same registry lock, restoring every callback before releasing it.
// Git permits a configured external driver named text/binary/union when named
// explicitly, while boolean merge attributes still select native builtins.
typedef struct {
 git_merge_driver *driver;
 git_merge_driver_apply_fn apply;
 const char *name;
 int is_default;
} task_builtin_guard;
static task_builtin_guard task_builtin_guards[3];
static int task_guarded_builtin(git_merge_driver *self,
 const char **path_out, uint32_t *mode_out, git_buf *merged_out,
 const char *name, const git_merge_driver_source *source) {
 task_builtin_guard *guard = NULL;
 for (int i = 0; i < 3; i++)
  if (task_builtin_guards[i].driver == self) guard = &task_builtin_guards[i];
 if (!guard) return GIT_ERROR;
 const git_index_entry *a = git_merge_driver_source_ancestor(source);
 const git_index_entry *o = git_merge_driver_source_ours(source);
 const git_index_entry *t = git_merge_driver_source_theirs(source);
 // Let the public file merger select the attribute path, including renames.
 // Empty text inputs avoid doing a second content merge or binary short-cut.
 git_merge_file_input ai = GIT_MERGE_FILE_INPUT_INIT;
 git_merge_file_input oi = GIT_MERGE_FILE_INPUT_INIT;
 git_merge_file_input ti = GIT_MERGE_FILE_INPUT_INIT;
 ai.ptr = oi.ptr = ti.ptr = "";
 if (a) { ai.path = a->path; ai.mode = a->mode; }
 if (o) { oi.path = o->path; oi.mode = o->mode; }
 if (t) { ti.path = t->path; ti.mode = t->mode; }
 git_merge_file_result result = {0};
 int error = git_merge_file(&result, a ? &ai : NULL, &oi, &ti, NULL);
 if (error < 0) return error;
 if (!result.path) {
  git_merge_file_result_free(&result);
  git_error_set(GIT_ERROR_MERGE, "cannot determine native merge attribute path");
  return GIT_ERROR;
 }
 const char *value = NULL;
 error = git_attr_get(&value, git_merge_driver_source_repo(source), 0, result.path, "merge");
 int external = !error &&
  ((git_attr_value(value) == GIT_ATTR_VALUE_STRING && !strcmp(value, guard->name)) ||
   (git_attr_value(value) == GIT_ATTR_VALUE_UNSPECIFIED && guard->is_default));
 if (external) {
  git_error_set(GIT_ERROR_MERGE,
   "path '%s' requires external merge driver '%s'; tasktool does not execute external drivers; resolve using your normal Git workflow and retry (no text fallback)",
   result.path, guard->name);
  error = GIT_ERROR;
 }
 git_merge_file_result_free(&result);
 if (error < 0) return error;
 return guard->apply(self, path_out, mode_out, merged_out, name, source);
}
static int task_guard_builtin(const char *name, int is_default) {
 const char *names[] = { "text", "binary", "union" };
 for (int i = 0; i < 3; i++) {
  if (strcmp(name, names[i])) continue;
  git_merge_driver *driver = git_merge_driver_lookup(name);
  if (!driver) return GIT_ERROR;
  task_builtin_guards[i] = (task_builtin_guard){ driver, driver->apply, names[i], is_default };
  driver->apply = task_guarded_builtin;
  return 0;
 }
 return GIT_ENOTFOUND;
}
static void task_restore_builtins(void) {
 for (int i = 0; i < 3; i++) {
  task_builtin_guard *guard = &task_builtin_guards[i];
  if (guard->driver) guard->driver->apply = guard->apply;
  memset(guard, 0, sizeof(*guard));
 }
}
static git_merge_driver task_external_driver = {
 GIT_MERGE_DRIVER_VERSION, NULL, NULL, task_reject_external_merge
};
static int task_register_merge_guard(const char *name) {
 return git_merge_driver_register(name, &task_external_driver);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
)

// libgit2's driver registry is process-global and not thread-safe. All native
// merges hold this lock through registration, preflight, merge and removal.
var mergeDriverRegistry sync.Mutex

func guardExternalMergeDrivers(repo *C.git_repository, defaultDriver string) (func() error, error) {
	var names []*C.char
	cleanup := func() error {
		C.task_restore_builtins()
		var err error
		for _, name := range names {
			err = errors.Join(err, nativeError("remove external merge guard", C.git_merge_driver_unregister(name)))
			free(name)
		}
		return err
	}
	var config *C.git_config
	if err := nativeError("read merge drivers", C.git_repository_config_snapshot(&config, repo)); err != nil {
		return cleanup, err
	}
	defer C.git_config_free(config)
	var iterator *C.git_config_iterator
	if err := nativeError("iterate merge drivers", C.git_config_iterator_new(&iterator, config)); err != nil {
		return cleanup, err
	}
	defer C.git_config_iterator_free(iterator)
	seen := map[string]bool{}
	for {
		var entry *C.git_config_entry
		code := C.git_config_next(&entry, iterator)
		if code == C.GIT_ITEROVER {
			break
		}
		if err := nativeError("read merge driver", code); err != nil {
			return cleanup, err
		}
		key := C.GoString(entry.name)
		if !strings.HasPrefix(key, "merge.") || !strings.HasSuffix(key, ".driver") {
			continue
		}
		command, err := configuredCommand(repo, key)
		if err != nil {
			return cleanup, err
		}
		if command == "" {
			continue
		}
		driver := strings.TrimSuffix(strings.TrimPrefix(key, "merge."), ".driver")
		if seen[driver] {
			continue
		}
		seen[driver] = true
		name, err := cstring(driver)
		if err != nil {
			return cleanup, err
		}
		if driver == "text" || driver == "binary" || driver == "union" {
			var isDefault C.int
			if driver == defaultDriver {
				isDefault = 1
			}
			err := nativeError("guard named builtin merge driver", C.task_guard_builtin(name, isDefault))
			free(name)
			if err != nil {
				return cleanup, err
			}
			continue
		}
		if err := nativeError("install external merge guard", C.task_register_merge_guard(name)); err != nil {
			free(name)
			return cleanup, err
		}
		names = append(names, name)
	}
	return cleanup, nil
}

// attachReadOnlyObjects exposes no write or freshen callback to the source ODB.
func attachReadOnlyObjects(target *C.git_odb, source *C.git_repository) error {
	return nativeError("attach read-only objects", C.task_add_readonly_odb(target, source))
}

// Use a separate handle at the same repository location, with the caller's
// configuration snapshot. Its object cache and all merge writes are disposable;
// swapping an ODB on the live handle would leave quarantined objects cached.
// The caller holds both the repository and merge-driver registry locks.
func withMergeQuarantine(source *C.git_repository, run func(*C.git_repository) error) (err error) {
	directory, err := os.MkdirTemp("", "tasktool-merge-preflight-")
	if err != nil {
		return fmt.Errorf("create merge quarantine: %w", err)
	}
	defer func() {
		if cleanup := os.RemoveAll(directory); cleanup != nil {
			err = errors.Join(err, fmt.Errorf("remove merge quarantine %q: %w", directory, cleanup))
		}
	}()
	path, err := cstring(directory)
	if err != nil {
		return err
	}
	defer free(path)
	var odb *C.git_odb
	if err := nativeError("open merge quarantine", C.git_odb_open(&odb, path)); err != nil {
		return err
	}
	defer C.git_odb_free(odb)
	if err := nativeError("attach read-only merge objects", C.task_add_readonly_odb(odb, source)); err != nil {
		return err
	}
	var repo *C.git_repository
	if err := nativeError("open merge preflight", C.git_repository_open(&repo, C.git_repository_path(source))); err != nil {
		return err
	}
	defer C.git_repository_free(repo)
	if err := nativeError("select merge quarantine", C.git_repository_set_odb(repo, odb)); err != nil {
		return err
	}
	var config *C.git_config
	if err := nativeError("read merge configuration", C.git_repository_config_snapshot(&config, source)); err != nil {
		return err
	}
	defer C.git_config_free(config)
	if err := nativeError("select preflight configuration", C.git_repository_set_config(repo, config)); err != nil {
		return err
	}
	return run(repo)
}
