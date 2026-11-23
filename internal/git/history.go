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
