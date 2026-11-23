package git

/*
#include <git2.h>
#include <git2/sys/errors.h>
#include <git2/sys/repository.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
 git_repository *repo;
 int flags;
 int error;
} task_merge_policy;

static int task_check_merge_attr(const char *root, const git_tree_entry *entry, void *data) {
 if (git_tree_entry_type(entry) == GIT_OBJECT_TREE) return 0;
 task_merge_policy *policy = data;
 const char *name = git_tree_entry_name(entry);
 size_t len = strlen(root) + strlen(name) + 1;
 char *path = malloc(len);
 if (!path) { git_error_set_oom(); policy->error = GIT_ERROR; return -1; }
 snprintf(path, len, "%s%s", root, name);
 const char *value = NULL;
 policy->error = git_attr_get(&value, policy->repo, policy->flags, path, "merge");
 if (!policy->error && GIT_ATTR_HAS_VALUE(value)) {
  git_error_set(GIT_ERROR_MERGE, "unsupported cleanup merge attribute at '%s'", path);
  policy->error = GIT_ERROR;
 }
 free(path);
 return policy->error ? -1 : 0;
}

static int task_check_merge_attrs(git_repository *repo, git_tree *tree, int flags) {
 task_merge_policy policy = { repo, flags, 0 };
 int error = git_tree_walk(tree, GIT_TREEWALK_PRE, task_check_merge_attr, &policy);
 return policy.error ? policy.error : error;
}
*/
import "C"

import (
	"fmt"
	"strings"
)

// MergeWouldKeepTree tests the default three-way merge, not the history of
// destination snapshots. Callers should check literal ancestry first.
func (r *Repository) MergeWouldKeepTree(source, destination ID, destinationRef string) (bool, error) {
	mergeDriverRegistry.Lock()
	defer mergeDriverRegistry.Unlock()
	var same bool
	err := r.call("simulate cleanup merge", func(repo *C.git_repository) error {
		if C.git_repository_is_shallow(repo) != 0 {
			return fmt.Errorf("cannot simulate cleanup merge with shallow history")
		}
		if err := checkCleanupMergeConfig(repo, destinationRef); err != nil {
			return err
		}
		return withMergeQuarantine(repo, func(quarantine *C.git_repository) error {
			var ours, theirs *C.git_commit
			dst, src := nativeID(destination), nativeID(source)
			if err := nativeError("read cleanup destination", C.git_commit_lookup(&ours, quarantine, &dst)); err != nil {
				return err
			}
			defer C.git_commit_free(ours)
			if err := nativeError("read cleanup source", C.git_commit_lookup(&theirs, quarantine, &src)); err != nil {
				return err
			}
			defer C.git_commit_free(theirs)
			var base C.git_oid
			if err := nativeError("find cleanup merge base", C.git_merge_base(&base, quarantine, &dst, &src)); err != nil {
				return err
			}
			var options C.git_merge_options
			if err := nativeError("initialize cleanup merge", C.git_merge_options_init(&options, C.GIT_MERGE_OPTIONS_VERSION)); err != nil {
				return err
			}
			var index *C.git_index
			if err := nativeError("simulate cleanup merge", C.git_merge_commits(&index, quarantine, ours, theirs, &options)); err != nil {
				return err
			}
			defer C.git_index_free(index)
			if C.git_index_has_conflicts(index) != 0 {
				return nil
			}
			var tree C.git_oid
			if err := nativeError("write simulated cleanup tree", C.git_index_write_tree_to(&tree, index, quarantine)); err != nil {
				return err
			}
			if C.git_oid_equal(&tree, C.git_commit_tree_id(ours)) == 0 {
				return nil
			}
			// No external drivers are registered and the snapshot rejected
			// driver configuration. Check attributes before accepting even
			// an apparently unchanged result (e.g., a custom or union driver).
			var oursTree, theirsTree *C.git_tree
			if err := nativeError("read destination attribute tree", C.git_commit_tree(&oursTree, ours)); err != nil {
				return err
			}
			defer C.git_tree_free(oursTree)
			if err := nativeError("read source attribute tree", C.git_commit_tree(&theirsTree, theirs)); err != nil {
				return err
			}
			defer C.git_tree_free(theirsTree)
			if err := checkCleanupMergeAttrs(quarantine, oursTree, theirsTree); err != nil {
				return err
			}
			same = true
			return nil
		})
	})
	return same && err == nil, err
}

func checkCleanupMergeAttrs(repo *C.git_repository, ours, theirs *C.git_tree) error {
	check := func(flags C.int) error {
		for _, tree := range []*C.git_tree{ours, theirs} {
			if err := nativeError("verify cleanup merge attributes", C.task_check_merge_attrs(repo, tree, flags)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(0); err != nil {
		return err
	}
	// The destination need not be checked out. Check its attributes in
	// isolation from the working checkout, using a disposable index.
	var index *C.git_index
	if err := nativeError("create cleanup attribute index", C.git_index_new(&index)); err != nil {
		return err
	}
	defer C.git_index_free(index)
	if err := nativeError("load cleanup attribute index", C.git_index_read_tree(index, ours)); err != nil {
		return err
	}
	if err := nativeError("select cleanup attribute index", C.git_repository_set_index(repo, index)); err != nil {
		return err
	}
	if err := nativeError("refresh cleanup attributes", C.git_attr_cache_flush(repo)); err != nil {
		return err
	}
	return check(C.GIT_ATTR_CHECK_INDEX_ONLY)
}

func checkCleanupMergeConfig(repo *C.git_repository, destinationRef string) error {
	var config *C.git_config
	if err := nativeError("read cleanup merge configuration", C.git_repository_config_snapshot(&config, repo)); err != nil {
		return err
	}
	defer C.git_config_free(config)
	var iterator *C.git_config_iterator
	if err := nativeError("iterate cleanup merge configuration", C.git_config_iterator_new(&iterator, config)); err != nil {
		return err
	}
	defer C.git_config_iterator_free(iterator)
	branchOptions := "branch." + strings.TrimPrefix(destinationRef, "refs/heads/") + ".mergeoptions"
	for {
		var entry *C.git_config_entry
		code := C.git_config_next(&entry, iterator)
		if code == C.GIT_ITEROVER {
			return nil
		}
		if err := nativeError("read cleanup merge configuration entry", code); err != nil {
			return err
		}
		key := C.GoString(entry.name)
		switch key {
		case "merge.ff", "merge.autostash", "merge.conflictstyle", "merge.gitsign", "merge.log", "merge.stat", "merge.verifysignatures":
			continue // These do not select a content merge policy.
		}
		if key == branchOptions || strings.HasPrefix(key, "merge.") {
			return fmt.Errorf("unsupported cleanup merge policy: %s", key)
		}
	}
}
