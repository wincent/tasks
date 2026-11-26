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
