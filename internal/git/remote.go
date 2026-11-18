package git

/*
#include <git2.h>
*/
import "C"

import "unsafe"

// Remotes reads locally configured names, never contacting their URLs. The
// reservation ledger uses these names to inspect already-fetched local refs.
func (r *Repository) Remotes() ([]string, error) {
	names := []string{}
	err := r.call("list configured remotes", func(repo *C.git_repository) error {
		var list C.git_strarray
		if err := nativeError("list configured remotes", C.git_remote_list(&list, repo)); err != nil {
			return err
		}
		defer C.git_strarray_dispose(&list)
		for _, name := range unsafe.Slice(list.strings, int(list.count)) {
			names = append(names, C.GoString(name))
		}
		return nil
	})
	return names, err
}
