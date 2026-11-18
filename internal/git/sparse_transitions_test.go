package git

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSparseCheckoutCleanFlaggedAndPatternTransitions(t *testing.T) {
	r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
	// A present clean skipped file may safely be removed; assume-valid does not
	// prevent a clean included file from being updated.
	writeFile(t, filepath.Join(root, "omit/file"), []byte("base"))
	next := sparseCommit(t, r, base, map[string]string{"assumed": "next", "omit/file": "next"})
	if err := r.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(root, "assumed"))) != "next" {
		t.Fatal("assume-valid not updated")
	}
	if _, err := os.Lstat(filepath.Join(root, "omit/file")); !os.IsNotExist(err) {
		t.Fatal("present skipped file not omitted", err)
	}
	writeFile(t, filepath.Join(filepath.Dir(index), "info/sparse-checkout"), []byte("/*\n!/*/\n/omit/\n"))
	if err := r.Checkout(next, next); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(root, "omit/file"))) != "next" {
		t.Fatal("expanded file missing")
	}
	if _, err := os.Lstat(filepath.Join(root, "keep/file")); !os.IsNotExist(err) {
		t.Fatal("contracted file remains", err)
	}
	entries, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Path == "omit/file" && e.SkipWorktree || e.Path == "keep/file" && !e.SkipWorktree {
			t.Fatalf("transition flags: %+v", e)
		}
	}
}

func TestSparsePatternTransitionProtectsDirtyAndStaged(t *testing.T) {
	for _, staged := range []bool{false, true} {
		t.Run(map[bool]string{false: "dirty", true: "staged"}[staged], func(t *testing.T) {
			r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
			writeFile(t, filepath.Join(root, "keep/file"), []byte("precious"))
			if staged {
				entries, err := r.IndexEntries()
				if err != nil {
					t.Fatal(err)
				}
				id, err := r.WriteBlob([]byte("precious"))
				if err != nil {
					t.Fatal(err)
				}
				for i := range entries {
					if entries[i].Path == "keep/file" {
						entries[i].ID = id
					}
				}
				sparseIndexFixture(t, r, entries)
			}
			before := readFile(t, index)
			writeFile(t, filepath.Join(filepath.Dir(index), "info/sparse-checkout"), []byte("/assumed\n/unrelated\n"))
			if err := r.Checkout(base, base); err == nil {
				t.Fatal("discarded protected transition")
			}
			if !bytes.Equal(before, readFile(t, index)) || string(readFile(t, filepath.Join(root, "keep/file"))) != "precious" {
				t.Fatal("failed transition mutated state")
			}
		})
	}
}

func TestSparseMergeConflictsOutsideSelection(t *testing.T) {
	r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
	ours := sparseCommit(t, r, base, map[string]string{"omit/file": "ours\n"})
	theirs := sparseCommit(t, r, base, map[string]string{"omit/file": "theirs\n"})
	if err := r.Checkout(base, ours); err != nil {
		t.Fatal(err)
	}
	publish(t, r, "refs/heads/main", ours, base)
	if _, err := r.MergeIntoIndex(theirs); err == nil || !strings.Contains(err.Error(), "merge conflicts") {
		t.Fatalf("conflict: %v", err)
	}
	if !strings.Contains(string(readFile(t, filepath.Join(root, "omit/file"))), "<<<<<<<") {
		t.Fatal("omitted conflict not materialized")
	}
	entries, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	stages := 0
	for _, e := range entries {
		if e.Path == "omit/file" && e.Stage != 0 {
			stages++
		}
	}
	if stages != 3 {
		t.Fatalf("conflict stages: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(index), "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
}

func TestSparseMergeDirtyPreflightDoesNotMutate(t *testing.T) {
	r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
	tip := sparseCommit(t, r, base, map[string]string{"omit/file": "theirs"})
	writeFile(t, filepath.Join(root, "omit/file"), []byte("precious"))
	before := readFile(t, index)
	if _, err := r.MergeIntoIndex(tip); err == nil {
		t.Fatal("overwrote hidden dirty file")
	}
	if !bytes.Equal(before, readFile(t, index)) {
		t.Fatal("changed index")
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(index), "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Fatal("created merge state", err)
	}
}

func TestSparseLinkedWorktreePatternLocationAndInheritedConfig(t *testing.T) {
	r, _ := fixture(t, false)
	base := sparseCommit(t, r, ID{}, map[string]string{"keep/file": "base", "omit/file": "base"})
	publish(t, r, "refs/heads/main", base, ID{})
	publish(t, r, "refs/heads/linked", base, ID{})
	root := filepath.Join(t.TempDir(), "linked")
	if err := r.AddWorktree("linked", root, "refs/heads/linked"); err != nil {
		t.Fatal(err)
	}
	paths, err := r.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.GitDir, "info"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(paths.GitDir, "info/sparse-checkout"), []byte("/omit/\n"))
	if err := r.SetLocalConfig("core.sparseCheckout", "true"); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	lp, err := linked.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lp.GitDir, "info"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(lp.GitDir, "info/sparse-checkout"), []byte("/keep/\n"))
	next := sparseCommit(t, r, base, map[string]string{"keep/new": "included", "omit/new": "excluded"})
	if err := linked.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(root, "keep/new"))) != "included" {
		t.Fatal("ignored linked patterns")
	}
	if _, err := os.Lstat(filepath.Join(root, "omit/new")); !os.IsNotExist(err) {
		t.Fatal("used common patterns", err)
	}
}

func TestSparseOmittedEncodingDoesNotBlockCheckout(t *testing.T) {
	r, root, base, _ := setupSparse(t, "/*\n!/*/\n/keep/\n")
	next := sparseCommit(t, r, base, map[string]string{".gitattributes": "omit/* working-tree-encoding=UNKNOWN-TEST-ENCODING\n", "omit/file": "unconvertible", "keep/file": "next"})
	if err := r.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if string(readFile(t, filepath.Join(root, "keep/file"))) != "next" {
		t.Fatal("included content unchanged")
	}
}
