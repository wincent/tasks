package testutil

import (
	"fmt"
	"os"
)

// RunWithIsolatedHome keeps tests from loading personal Git configuration.
// Call it from TestMain before libgit2 caches its config search paths.
func RunWithIsolatedHome(run func() int) int {
	const isolated = "TASKS_TEST_HOME_ISOLATED"
	if os.Getenv(isolated) == "1" {
		// Re-executed test binaries inherit the parent's isolated home or an
		// explicit fixture home for tests of global configuration discovery.
		return run()
	}

	home, err := os.MkdirTemp("", "tasks-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(home)

	for _, setting := range [][2]string{{"HOME", home}, {"XDG_CONFIG_HOME", home}, {isolated, "1"}} {
		if err := os.Setenv(setting[0], setting[1]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return run()
}
