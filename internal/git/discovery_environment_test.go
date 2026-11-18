package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDiscoveryHonorsCeilingsWithoutRedirectingRepositories(t *testing.T) {
	repo, root := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	root = paths.WorkDir
	boundary := filepath.Join(root, "inside")
	nested := filepath.Join(boundary, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	opened, err := Open(nested)
	if err != nil {
		t.Fatal(err)
	}
	found, err := opened.Paths()
	opened.Close()
	if err != nil || found != paths {
		t.Fatalf("wrong discovery %+v %v", found, err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", boundary)
	opened, err = Open(nested)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Fatal("discovery crossed ceiling")
	}
	// Explicit worktree and metadata handles below/at their starting path
	// remain addressable; ambient ceilings only limit upward discovery.
	opened, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
	for _, value := range []string{"0", "false", "no", "off", "1", "true", "yes", "on"} {
		t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", value)
		opened, err = Open(root)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		opened.Close()
	}
	t.Setenv("GIT_DISCOVERY_ACROSS_FILESYSTEM", "invalid")
	opened, err = Open(root)
	if opened != nil {
		opened.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "boolean") {
		t.Fatal(err)
	}
}
