package git

/*
#include <git2.h>

typedef struct {
	const git_tree_entry *before, *after;
} history_tree_change;

// Merge two sorted trees without descending into subtrees. Do this in C to
// avoid crossing the cgo boundary for every unchanged entry.
static size_t history_tree_changes(history_tree_change *out, size_t limit,
		const git_tree *before, const git_tree *after) {
	size_t i = 0, j = 0, n = 0;
	size_t a = before ? git_tree_entrycount(before) : 0;
	size_t b = after ? git_tree_entrycount(after) : 0;
	while (i < a || j < b) {
		const git_tree_entry *old = i < a ? git_tree_entry_byindex(before, i) : NULL;
		const git_tree_entry *new = j < b ? git_tree_entry_byindex(after, j) : NULL;
		int order = !old ? 1 : !new ? -1 : git_tree_entry_cmp(old, new);
		if (order <= 0) i++;
		if (order >= 0) j++;
		if (order == 0 &&
				git_oid_equal(git_tree_entry_id(old), git_tree_entry_id(new)) &&
				git_tree_entry_filemode_raw(old) == git_tree_entry_filemode_raw(new))
			continue;
		if (n == limit)
			return limit + 1;
		out[n].before = order <= 0 ? old : NULL;
		out[n].after = order >= 0 ? new : NULL;
		n++;
	}
	return n;
}
*/
import "C"

import (
	"fmt"
	"strings"
	"time"
)

// ContainsCommitOrTree reports whether revision is reachable from tip or
// whether a commit in revision..tip has exactly the same root tree. Excluding
// shared history prevents an old snapshot from counting as an integrated revert.
func (r *Repository) ContainsCommitOrTree(revision, tip ID) (bool, error) {
	if merged, err := r.IsAncestor(revision, tip); err != nil || merged {
		return merged, err
	}

	var matched bool
	err := r.call("find equivalent commit tree", func(repo *C.git_repository) error {
		// With truncated ancestry we cannot reliably exclude shared history.
		if C.git_repository_is_shallow(repo) != 0 {
			return nil
		}
		source, target := nativeID(revision), nativeID(tip)
		tree, err := commitTreeID(repo, &source)
		if err != nil {
			return err
		}
		var walk *C.git_revwalk
		if err := nativeError("create history walker", C.git_revwalk_new(&walk, repo)); err != nil {
			return err
		}
		defer C.git_revwalk_free(walk)
		if err := nativeError("add destination tip", C.git_revwalk_push(walk, &target)); err != nil {
			return err
		}
		if err := nativeError("exclude task history", C.git_revwalk_hide(walk, &source)); err != nil {
			return err
		}
		for {
			var id C.git_oid
			code := C.git_revwalk_next(&id, walk)
			if code == C.GIT_ITEROVER {
				return nil
			}
			if err := nativeError("walk destination history", code); err != nil {
				return err
			}
			candidate, err := commitTreeID(repo, &id)
			if err != nil {
				return err
			}
			if candidate == tree {
				matched = true
				return nil
			}
		}
	})
	return matched, err
}

func commitTreeID(repo *C.git_repository, id *C.git_oid) (ID, error) {
	var commit *C.git_commit
	if err := nativeError("lookup commit for tree comparison", C.git_commit_lookup(&commit, repo, id)); err != nil {
		return ID{}, err
	}
	defer C.git_commit_free(commit)
	return objectID(C.git_commit_tree_id(commit)), nil
}

// RootTreeID reads a commit's tree ID without walking its parents. A shallow
// commit can still be inspected as a candidate parent.
func (r *Repository) RootTreeID(revision ID) (ID, error) {
	var tree ID
	err := r.call("read root tree ID", func(repo *C.git_repository) error {
		id := nativeID(revision)
		var err error
		tree, err = commitTreeID(repo, &id)
		return err
	})
	return tree, err
}

type TreeChange struct {
	Before, After TreeEntry
}

// TreeChanges compares two tree objects by entry, without descending into
// subtrees. Zero IDs denote absent trees; zero entries denote absent paths.
// If more than limit entries changed, complete is false and no changes are
// returned. Equal IDs need no comparison or object read.
func (r *Repository) TreeChanges(before, after ID, limit int) (changes []TreeChange, complete bool, err error) {
	if before == after {
		return nil, true, nil
	}
	err = r.call("compare history trees", func(repo *C.git_repository) error {
		lookup := func(id ID) (*C.git_tree, error) {
			if id == (ID{}) {
				return nil, nil
			}
			var tree *C.git_tree
			oid := nativeID(id)
			if err := nativeError("lookup history tree", C.git_tree_lookup(&tree, repo, &oid)); err != nil {
				return nil, err
			}
			return tree, nil
		}
		left, err := lookup(before)
		if err != nil {
			return err
		}
		defer C.git_tree_free(left)
		right, err := lookup(after)
		if err != nil {
			return err
		}
		defer C.git_tree_free(right)

		buf := make([]C.history_tree_change, limit)
		var out *C.history_tree_change
		if limit > 0 {
			out = &buf[0]
		}
		n := int(C.history_tree_changes(out, C.size_t(limit), left, right))
		if n > limit {
			return nil
		}
		entry := func(e *C.git_tree_entry) TreeEntry {
			if e == nil {
				return TreeEntry{}
			}
			return TreeEntry{
				Name: C.GoString(C.git_tree_entry_name(e)),
				ID:   objectID(C.git_tree_entry_id(e)),
				Mode: uint32(C.git_tree_entry_filemode_raw(e)),
			}
		}
		for _, change := range buf[:n] {
			changes = append(changes, TreeChange{Before: entry(change.before), After: entry(change.after)})
		}
		complete = true
		return nil
	})
	return
}

type CommitInfo struct {
	Parents []ID
	Time    time.Time
}

// CommitInfo reads parent IDs and the committer timestamp without using the
// checkout or invoking Git. A shallow boundary is not a root commit.
func (r *Repository) CommitInfo(revision ID) (CommitInfo, error) {
	var result CommitInfo
	err := r.call("read commit history", func(repo *C.git_repository) error {
		id := nativeID(revision)
		var commit *C.git_commit
		if err := nativeError("lookup history commit", C.git_commit_lookup(&commit, repo, &id)); err != nil {
			return err
		}
		defer C.git_commit_free(commit)
		if C.git_commit_parentcount(commit) == 0 && strings.Contains(C.GoString(C.git_commit_raw_header(commit)), "\nparent ") {
			return fmt.Errorf("shallow history at %s", revision)
		}
		result.Time = time.Unix(int64(C.git_commit_time(commit)), 0).UTC()
		for i := C.uint(0); i < C.git_commit_parentcount(commit); i++ {
			result.Parents = append(result.Parents, objectID(C.git_commit_parent_id(commit, i)))
		}
		return nil
	})
	return result, err
}
