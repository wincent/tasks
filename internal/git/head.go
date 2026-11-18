package git

/*
#include <git2.h>
*/
import "C"

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
