package git

import (
	"os"
	"testing"

	"github.com/wincent/tasks/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithIsolatedHome(m.Run))
}
