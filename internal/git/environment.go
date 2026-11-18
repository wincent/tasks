package git

import (
	"fmt"
	"os"
)

// Explicit repository handles must not silently redirect private task worktrees.
// GIT_CONFIG is intentionally absent: it selects a file for `git config`, not
// the ordinary repository configuration used by other Git operations.
func validateEnvironment() error {
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
		"GIT_NAMESPACE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
	} {
		if _, exists := os.LookupEnv(key); exists {
			return fmt.Errorf("%s is set: native task repository discovery does not support this Git environment override; unset it and run from the intended checkout (no Git subprocess fallback)", key)
		}
	}
	// libgit2 opens default config and validates ownership before returning a
	// handle. Its public API cannot install replacement sources beforehand;
	// an after-open overlay would already have read explicitly excluded files.
	for _, key := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM"} {
		if _, exists := os.LookupEnv(key); exists {
			return fmt.Errorf("%s is set: libgit2 1.9 cannot install per-handle configuration sources before repository opening and owner validation; refusing to read potentially excluded default configuration", key)
		}
	}
	return nil
}
