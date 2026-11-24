package store

import (
	"os"
	"testing"

	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/testutil"
)

func TestMain(m *testing.M) {
	state.DisableSyncForTesting()
	os.Exit(testutil.RunWithIsolatedHome(m.Run))
}
