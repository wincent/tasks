package git

/*
#include <git2.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// libgit2 discovers repositories but does not enforce safe.bareRepository.
// Preserve the discovery origin: an absolute pathname is not an explicit Git
// directory selection, and core.bare=false does not make bare discovery safe.
func validateBareDiscoveryPolicy(path string, flags C.uint, ceiling *C.char, configuration *environmentConfiguration) error {
	name, err := cstring(path)
	if err != nil {
		return err
	}
	defer free(name)
	var discovered C.git_buf
	defer C.git_buf_dispose(&discovered)
	var across C.int
	if flags&C.GIT_REPOSITORY_OPEN_CROSS_FS != 0 {
		across = 1
	}
	if C.git_repository_discover(&discovered, name, across, ceiling) < 0 {
		// The ordinary opening path retains its existing discovery diagnostic.
		return nil
	}
	gitdir := filepath.Clean(C.GoString(discovered.ptr))
	viaDotGit, err := discoveryUsesDotGit(path, gitdir)
	if err != nil {
		return err
	}
	if viaDotGit {
		return nil
	}
	values, err := configuration.protectedValues("safe.barerepository")
	if err != nil {
		return err
	}
	policy := "all"
	for _, value := range values {
		if value == nil || (*value != "all" && *value != "explicit") {
			return errors.New("safe.bareRepository must be 'all' or 'explicit' in protected Git configuration")
		}
		policy = *value
	}
	if policy == "explicit" && !implicitGitDirectory(gitdir) {
		return fmt.Errorf("cannot discover bare repository %s: safe.bareRepository is 'explicit'; run from an intended worktree without relaxing the required policy", gitdir)
	}
	return nil
}

// These are Git's implicit administrative-directory exceptions, not arbitrary
// suffix matching such as repository.git or a conversion of discovery to an
// explicit --git-dir operation.
func implicitGitDirectory(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return filepath.Base(path) == ".git" || strings.Contains(path, "/.git/worktrees/") || strings.Contains(path, "/.git/modules/")
}

func discoveryUsesDotGit(start, gitdir string) (bool, error) {
	found, err := os.Stat(gitdir)
	if err != nil {
		return false, err
	}
	current, err := filepath.EvalSymlinks(start)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(current)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		// Ask the public native discoverer to resolve each actual .git marker,
		// including linked/separate gitfiles; do not reproduce gitfile parsing.
		marker := filepath.Join(current, ".git")
		if _, err := os.Lstat(marker); err == nil {
			matches, err := dotGitSelects(marker, current, found)
			if err != nil {
				return false, err
			}
			if matches {
				return true, nil
			}
		} else if !os.IsNotExist(err) {
			return false, err
		}
		info, err := os.Stat(current)
		if err != nil {
			return false, err
		}
		if os.SameFile(info, found) {
			return false, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, errors.New("cannot establish the native repository discovery origin")
		}
		current = parent
	}
}

func dotGitSelects(marker, ceiling string, expected os.FileInfo) (bool, error) {
	path, err := cstring(marker)
	if err != nil {
		return false, err
	}
	defer free(path)
	limit, err := cstring(ceiling)
	if err != nil {
		return false, err
	}
	defer free(limit)
	var discovered C.git_buf
	defer C.git_buf_dispose(&discovered)
	if C.git_repository_discover(&discovered, path, 0, limit) < 0 {
		return false, nil
	}
	actual, err := os.Stat(C.GoString(discovered.ptr))
	if err != nil {
		return false, err
	}
	return os.SameFile(actual, expected), nil
}
