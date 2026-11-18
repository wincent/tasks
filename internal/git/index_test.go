package git

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEditTreeLeavesIndexAndWorktreeUntouched(t *testing.T) {
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
	paths, _ := r.Paths()
	indexPath := filepath.Join(paths.GitDir, "index")
	before := readFile(t, indexPath)
	blob, err := r.WriteBlob([]byte("metadata"))
	if err != nil {
		t.Fatal(err)
	}
	edits := []TreeEdit{{Path: "nested/literal[abc]", ID: blob, Mode: 0100644}, {Path: "content.txt", Mode: 0}, {Path: "link", ID: blob, Mode: 0120000}}
	tree, err := r.EditTree(first, edits)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := r.WriteCommit(tree, []ID{first}, testSignature, testSignature, "edit\n")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := r.Entry(revision, "nested/literal[abc]")
	if err != nil || entry.ID != blob || entry.Mode != 0100644 {
		t.Fatalf("entry: %+v %v", entry, err)
	}
	if _, err := r.Entry(revision, "content.txt"); !HasCode(err, NotFound) {
		t.Fatalf("removed entry: %v", err)
	}
	entry, err = r.Entry(revision, "link")
	if err != nil || entry.Mode != 0120000 {
		t.Fatalf("symlink: %+v %v", entry, err)
	}
	if !bytes.Equal(before, readFile(t, indexPath)) || string(readFile(t, filepath.Join(root, "content.txt"))) != "first" {
		t.Fatal("tree edit changed real checkout")
	}
	if _, err := os.Lstat(filepath.Join(root, "nested")); !os.IsNotExist(err) {
		t.Fatalf("created worktree path: %v", err)
	}
	for _, invalid := range [][]TreeEdit{
		{{Path: "../escape", ID: blob, Mode: 0100644}},
		{{Path: "bad", ID: blob, Mode: 0040000}},
		{{Path: "missing", ID: ID{}, Mode: 0100644}},
		{{Path: "duplicate", ID: blob, Mode: 0100644}, {Path: "duplicate", Mode: 0}},
	} {
		if _, err := r.EditTree(first, invalid); err == nil {
			t.Fatalf("accepted invalid edits: %+v", invalid)
		}
	}
	empty, err := r.EditTree(ID{}, nil)
	if err != nil || empty.String() != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("empty tree: %s %v", empty, err)
	}
}

func TestIsolatedIndexPreservesUnrelatedStaging(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
	paths, _ := r.Paths()
	real := filepath.Join(paths.GitDir, "index")
	before := readFile(t, real)
	isolated := filepath.Join(paths.GitDir, "private-index")
	writeFile(t, isolated, before)
	blob, err := r.WriteBlob([]byte("staged"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.EditIndexFile(isolated, ID{}, []TreeEdit{{Path: "unrelated", ID: blob, Mode: 0100755}}); err != nil {
		t.Fatal(err)
	}
	if err := r.EditIndexFile(isolated, ID{}, []TreeEdit{{Path: "EXAMPLE-1 task.md", ID: blob, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readFile(t, real)) {
		t.Fatal("isolated edit changed real index")
	}
	if err := os.Rename(isolated, real); err != nil {
		t.Fatal(err)
	}
	entries, err := r.IndexEntries()
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries: %+v %v", entries, err)
	}
	if entries[2].Path != "unrelated" || entries[2].Mode != 0100755 || entries[2].ID != blob {
		t.Fatalf("lost unrelated staged entry: %+v", entries)
	}
	if err := r.EditIndexFile(real, ID{}, nil); err == nil {
		t.Fatal("accepted real index")
	}
	if err := r.EditIndexFile(isolated, first, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.EditIndexFile(isolated, first, nil); err == nil {
		t.Fatal("replaced existing nonempty isolated index")
	}
}

// Version-3 fixture encoding is independent of libgit2's index writer. It
// supplies conflict stages and flags that ordinary tree construction cannot.
func writeFlaggedIndex(t *testing.T, path string, id ID) {
	t.Helper()
	var data bytes.Buffer
	data.WriteString("DIRC")
	binary.Write(&data, binary.BigEndian, uint32(3))
	binary.Write(&data, binary.BigEndian, uint32(6))
	for _, entry := range []struct {
		name            string
		flags, extended uint16
	}{
		{"assumed", 0x8000, 0}, {"conflict", 0x1000, 0}, {"conflict", 0x2000, 0}, {"conflict", 0x3000, 0}, {"skipped", 0x4000, 0x4000}, {"zz-intent", 0x4000, 0x2000},
	} {
		start := data.Len()
		for i := 0; i < 10; i++ {
			var value uint32
			if i == 6 {
				value = 0100644
			}
			binary.Write(&data, binary.BigEndian, value)
		}
		data.Write(id[:])
		binary.Write(&data, binary.BigEndian, entry.flags|uint16(len(entry.name)))
		if entry.flags&0x4000 != 0 {
			binary.Write(&data, binary.BigEndian, entry.extended)
		}
		data.WriteString(entry.name)
		data.WriteByte(0)
		for (data.Len()-start)%8 != 0 {
			data.WriteByte(0)
		}
	}
	checksum := sha1.Sum(data.Bytes())
	data.Write(checksum[:])
	writeFile(t, path, data.Bytes())
}

func TestIsolatedIndexPreservesConflictStagesAndFlags(t *testing.T) {
	r, _ := fixture(t, false)
	blob, err := r.WriteBlob([]byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := r.Paths()
	real := filepath.Join(paths.GitDir, "index")
	writeFlaggedIndex(t, real, blob)
	before, err := r.IndexEntries()
	if err != nil || len(before) != 6 || !before[5].IntentToAdd || !before[0].AssumeValid || before[1].Stage != 1 || before[2].Stage != 2 || before[3].Stage != 3 || !before[4].SkipWorktree {
		t.Fatalf("fixture: %+v %v", before, err)
	}
	isolated := filepath.Join(paths.GitDir, "isolated")
	writeFile(t, isolated, readFile(t, real))
	if err := r.EditIndexFile(isolated, ID{}, []TreeEdit{{Path: "z-task", ID: blob, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(isolated, real); err != nil {
		t.Fatal(err)
	}
	after, err := r.IndexEntries()
	if err != nil || len(after) != 7 || !reflect.DeepEqual(before[:5], after[:5]) || !reflect.DeepEqual(before[5], after[6]) {
		t.Fatalf("lost index state: %+v %v", after, err)
	}
}

func TestIsolatedIndexRejectsFilesystemAliases(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
	paths, _ := r.Paths()
	real := filepath.Join(paths.GitDir, "index")
	before := readFile(t, real)
	parentAlias := filepath.Join(t.TempDir(), "gitdir")
	if err := os.Symlink(paths.GitDir, parentAlias); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(paths.GitDir, "index-symlink")
	if err := os.Symlink(real, symlink); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(paths.GitDir, "index-hardlink")
	if err := os.Link(real, hardlink); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(parentAlias, "index"), symlink, hardlink} {
		if err := r.EditIndexFile(path, ID{}, []TreeEdit{{Path: "content.txt", Mode: 0}}); err == nil {
			t.Fatalf("accepted real index alias: %s", path)
		}
		if !bytes.Equal(before, readFile(t, real)) {
			t.Fatal("modified real index via alias")
		}
	}
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if err := r.EditIndexFile(filepath.Join(parentAlias, "index"), first, nil); err == nil {
		t.Fatal("created absent real index through aliased parent")
	}
	if _, err := os.Stat(real); !os.IsNotExist(err) {
		t.Fatalf("created real index: %v", err)
	}
	empty := filepath.Join(paths.GitDir, "empty-index")
	if err := r.EditIndexFile(empty, ID{}, nil); err != nil {
		t.Fatal(err)
	}
	emptyBefore := readFile(t, empty)
	if err := r.EditIndexFile(empty, first, nil); err == nil {
		t.Fatal("replaced existing empty index with a base tree")
	}
	if !bytes.Equal(emptyBefore, readFile(t, empty)) {
		t.Fatal("modified existing empty index on refusal")
	}
}

func TestSafeCheckoutPreservesDirtyAndIgnoredFiles(t *testing.T) {
	for _, obstruction := range []string{"dirty", "untracked", "ignored"} {
		t.Run(obstruction, func(t *testing.T) {
			r, root := fixture(t, false)
			first := commit(t, r, "first")
			publish(t, r, "refs/heads/main", first, ID{})
			if err := r.CheckoutMetadata(ID{}, first); err != nil {
				t.Fatal(err)
			}
			path := "content.txt"
			if obstruction != "dirty" {
				path = "obstruction"
			}
			if obstruction == "ignored" {
				writeFile(t, filepath.Join(root, ".gitignore"), []byte("obstruction\n"))
			}
			writeFile(t, filepath.Join(root, path), []byte("precious"))
			blob, err := r.WriteBlob([]byte("new"))
			if err != nil {
				t.Fatal(err)
			}
			tree, err := r.EditTree(first, []TreeEdit{{Path: path, ID: blob, Mode: 0100644}})
			if err != nil {
				t.Fatal(err)
			}
			second, err := r.WriteCommit(tree, []ID{first}, testSignature, testSignature, "second\n")
			if err != nil {
				t.Fatal(err)
			}
			paths, _ := r.Paths()
			index := filepath.Join(paths.GitDir, "index")
			before := readFile(t, index)
			if err := r.CheckoutMetadata(first, second); err == nil {
				t.Fatal("overwrote obstruction")
			}
			if string(readFile(t, filepath.Join(root, path))) != "precious" {
				t.Fatal("lost local content")
			}
			if !bytes.Equal(before, readFile(t, index)) {
				t.Fatal("failed checkout changed index")
			}
			expectRef(t, r, "refs/heads/main", first)
		})
	}
}
