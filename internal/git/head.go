package git

/*
#include <git2.h>
*/
import "C"

// ValidBranchName checks a literal, unqualified branch name without resolving it.
func ValidBranchName(name string) bool {
	if initialize() != nil {
		return false
	}
	value, err := cstring(name)
	if err != nil {
		return false
	}
	defer free(value)
	var valid C.int
	return C.git_branch_name_is_valid(&valid, value) == 0 && valid != 0
}

// SetHead changes only HEAD, not the index or worktree. Workflow callers must
// establish their checkout/cleanliness preconditions separately.
func (r *Repository) SetHead(branch string) error {
	return r.call("set HEAD", func(repo *C.git_repository) error {
		name, err := cstring(branch)
		if err != nil {
			return err
		}
		defer free(name)
		return nativeError("set HEAD", C.git_repository_set_head(repo, name))
	})
}

func (r *Repository) DetachHead(id ID) error {
	return r.call("detach HEAD", func(repo *C.git_repository) error {
		oid := nativeID(id)
		return nativeError("detach HEAD", C.git_repository_set_head_detached(repo, &oid))
	})
}
