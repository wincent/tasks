package git

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeMergePreservesBothParents(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	base := commit(t, repo, "base")
	publish(t, repo, "refs/heads/main", base, ID{})
	checkoutInitial(t, repo, base)
	blob, err := repo.WriteBlob([]byte("feature\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.EditTree(base, []TreeEdit{{Path: "feature", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	tip, err := repo.WriteCommit(tree, []ID{base}, testSignature, testSignature, "feature\n")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(paths.GitDir, "HEAD"), []byte(base.String()+"\n"))
	mergedTree, err := repo.MergeCleanIntoIndex(tip)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := repo.WriteCommit(mergedTree, []ID{base, tip}, testSignature, testSignature, "merge\n")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := repo.Prepare("HEAD", merged, map[string]ID{"HEAD": base}, testSignature, "merge")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := repo.CleanupOperation(); err != nil {
		t.Fatal(err)
	}
	if err := repo.RequireClean(); err != nil {
		t.Fatal(err)
	}
	for _, parent := range []ID{base, tip} {
		ancestor, err := repo.IsAncestor(parent, merged)
		if err != nil || !ancestor {
			t.Fatalf("lost parent %s: %v", parent, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "feature")); err != nil || string(data) != "feature\n" {
		t.Fatalf("%q %v", data, err)
	}
}

func TestNativeMergeRefusesConflictedCandidate(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	base := commit(t, repo, "base")
	makeTip := func(text string) ID {
		blob, err := repo.WriteBlob([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		tree, err := repo.EditTree(base, []TreeEdit{{Path: "conflict", ID: blob, Mode: 0100644}})
		if err != nil {
			t.Fatal(err)
		}
		tip, err := repo.WriteCommit(tree, []ID{base}, testSignature, testSignature, text)
		if err != nil {
			t.Fatal(err)
		}
		return tip
	}
	left, right := makeTip("left\n"), makeTip("right\n")
	publish(t, repo, "refs/heads/main", left, ID{})
	checkoutInitial(t, repo, left)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	beforeIndex := readFile(t, filepath.Join(paths.GitDir, "index"))
	if _, err := repo.MergeCleanIntoIndex(right); !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}
	if !bytes.Equal(beforeIndex, readFile(t, filepath.Join(paths.GitDir, "index"))) {
		t.Fatal("conflicted merge mutated index")
	}
	if got := string(readFile(t, filepath.Join(root, "conflict"))); got != "left\n" {
		t.Fatalf("conflicted merge mutated worktree: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(paths.GitDir, "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("conflicted merge created MERGE_HEAD: %v", err)
	}
	head, err := repo.Head()
	if err != nil || head.ID != left {
		t.Fatal(head, err)
	}
	if err := repo.RequireClean(); err != nil {
		t.Fatalf("conflicted merge left dirty state: %v", err)
	}
}

func TestRemoveWorktreeProtectsIgnoredAndChangedResources(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t, false)
	base := commit(t, repo, "base")
	publish(t, repo, "refs/heads/main", base, ID{})
	publish(t, repo, "refs/heads/topic", base, ID{})
	path := filepath.Join(filepath.Dir(root), "linked")
	if err := repo.AddWorktree("linked", path, "refs/heads/topic"); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	paths, err := linked.Paths()
	if err != nil {
		t.Fatal(err)
	}
	head, err := linked.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.CommonDir, "info", "exclude"), []byte("precious\n"), 0600); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(path, "precious")
	if err := os.WriteFile(precious, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree("linked", paths, head, false); err == nil {
		t.Fatal("removed ignored file")
	}
	if data, err := os.ReadFile(precious); err != nil || string(data) != "keep" {
		t.Fatalf("lost content: %s %v", data, err)
	}
	if err := os.Remove(precious); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(paths.GitDir, "index")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	flagged := append([]byte{}, index...)
	flagged[12+60] |= 0x80
	checksum := sha1.Sum(flagged[:len(flagged)-20])
	copy(flagged[len(flagged)-20:], checksum[:])
	if err := os.WriteFile(indexPath, flagged, 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree("linked", paths, head, false); err == nil || !strings.Contains(err.Error(), "index entries") {
		t.Fatalf("did not protect hidden index flags: %v", err)
	}
	if err := os.WriteFile(indexPath, index, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.GitDir, "locked"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree("linked", paths, head, false); err == nil {
		t.Fatal("removed locked worktree")
	}
	if err := os.Remove(filepath.Join(paths.GitDir, "locked")); err != nil {
		t.Fatal(err)
	}
	if err := repo.RemoveWorktree("linked", paths, head, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
	expectRef(t, repo, "refs/heads/topic", base)
}
