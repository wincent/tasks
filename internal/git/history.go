package git

/*
#include <git2.h>
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
