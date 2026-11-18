package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeInventoryIncludesLockedAndMissing(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	publish(t, r, "refs/heads/linked", first, ID{})
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "linked\ncheckout")
	if err := r.AddWorktree("linked", path, "refs/heads/linked"); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	primary, err := linked.PrimaryPaths()
	want, _ := r.Paths()
	if err != nil || primary != want {
		t.Fatalf("primary: %+v %v, want %+v", primary, err, want)
	}
	entries, err := linked.Worktrees()
	if err != nil || len(entries) != 1 || entries[0].Path != path || entries[0].Name != "linked" || entries[0].InvalidReason != "" || entries[0].Locked {
		t.Fatalf("inventory: %+v %v", entries, err)
	}
	paths, _ := linked.Paths()
	writeFile(t, filepath.Join(paths.GitDir, "locked"), []byte("portable drive\n"))
	entries, err = r.Worktrees()
	if err != nil || len(entries) != 1 || !entries[0].Locked || entries[0].LockReason != "portable drive\n" {
		t.Fatalf("locked: %+v %v", entries, err)
	}
	if err := os.Rename(path, path+"-moved"); err != nil {
		t.Fatal(err)
	}
	entries, err = r.Worktrees()
	if err != nil || len(entries) != 1 || entries[0].InvalidReason == "" || !entries[0].Locked || entries[0].Path != path {
		t.Fatalf("missing: %+v %v", entries, err)
	}
}
