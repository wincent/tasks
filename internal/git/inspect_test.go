package git

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDescribeChanges(t *testing.T) {
	t.Parallel()
	changes := []Change{
		{Path: "both", Status: IndexModified | WorktreeModified},
		{Path: "conflict", Status: Conflicted},
		{Path: "new", Status: WorktreeNew},
		{Path: "cache", Status: Ignored},
		{Path: "unreadable", Status: WorktreeUnreadable},
	}
	got := DescribeChanges(changes)
	for _, want := range []string{"1 staged (\"both\")", "1 unstaged (\"both\")", "1 conflicted (\"conflict\")", "1 untracked (\"new\")", "1 ignored (\"cache\")", "1 unreadable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, " or ") || DescribeChanges(nil) != "" {
		t.Fatal(got)
	}
	many := []Change{}
	for i := 0; i < 8; i++ {
		many = append(many, Change{Path: fmt.Sprint(i), Status: WorktreeNew})
	}
	if got := DescribeChanges(many); !strings.Contains(got, "8 untracked") || !strings.Contains(got, "and 5 more") {
		t.Fatal(got)
	}
}

func TestHeadAndRevisionResolution(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	head, err := r.Head()
	if err != nil || head != (Head{Branch: "refs/heads/main"}) {
		t.Fatalf("unborn: %+v %v", head, err)
	}
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	head, err = r.Head()
	if err != nil || head != (Head{Branch: "refs/heads/main", ID: first}) {
		t.Fatalf("attached: %+v %v", head, err)
	}
	publish(t, r, "refs/heads/main", second, first)
	head, err = r.Head()
	if err != nil || head.ID != second {
		t.Fatalf("stale head: %+v %v", head, err)
	}
	for expression, want := range map[string]ID{"HEAD": second, "HEAD^": first, "main~1": first, second.String(): second} {
		got, err := r.ResolveCommit(expression)
		if err != nil || got != want {
			t.Fatalf("%q: %s %v", expression, got, err)
		}
	}
	for _, expression := range []string{"--help", "HEAD:content.txt", "missing", "nul\x00suffix"} {
		if _, err := r.ResolveCommit(expression); err == nil {
			t.Fatalf("accepted %q", expression)
		}
	}
	paths, _ := r.Paths()
	writeFile(t, filepath.Join(paths.GitDir, "HEAD"), []byte(first.String()+"\n"))
	head, err = r.Head()
	if err != nil || head != (Head{ID: first}) {
		t.Fatalf("detached: %+v %v", head, err)
	}
	writeFile(t, filepath.Join(paths.GitDir, "HEAD"), []byte("ref: refs/heads/alias\n"))
	writeFile(t, filepath.Join(paths.CommonDir, "refs", "heads", "alias"), []byte("ref: refs/heads/main\n"))
	if _, err := r.Head(); err == nil {
		t.Fatal("accepted symbolic branch alias")
	}
}

func TestHeadValidation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"attached", "bare", "detached", "unborn", "nonbranch", "alias", "missing-object", "blob", "malformed", "missing", "configuration"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r, _ := fixture(t, mode == "bare")
			tip := commit(t, r, "first")
			publish(t, r, "refs/heads/main", tip, ID{})
			paths, err := r.Paths()
			if err != nil {
				t.Fatal(err)
			}
			head := filepath.Join(paths.GitDir, "HEAD")
			want := Head{Branch: "refs/heads/main", ID: tip}
			switch mode {
			case "detached":
				writeFile(t, head, []byte(tip.String()+"\n"))
				want = Head{ID: tip}
			case "unborn":
				writeFile(t, head, []byte("ref: refs/heads/unborn\n"))
				want = Head{Branch: "refs/heads/unborn"}
			case "nonbranch":
				writeFile(t, head, []byte("ref: refs/tags/not-a-branch\n"))
			case "alias":
				writeFile(t, head, []byte("ref: refs/heads/alias\n"))
				writeFile(t, filepath.Join(paths.CommonDir, "refs", "heads", "alias"), []byte("ref: refs/heads/main\n"))
			case "missing-object":
				writeFile(t, head, []byte(strings.Repeat("1", 40)+"\n"))
			case "blob":
				blob, err := r.WriteBlob([]byte("not a commit"))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, head, []byte(blob.String()+"\n"))
			case "malformed":
				writeFile(t, head, []byte("invalid HEAD\n"))
			case "missing":
				if err := os.Remove(head); err != nil {
					t.Fatal(err)
				}
			case "configuration":
				writeFile(t, filepath.Join(paths.CommonDir, "config"), []byte("[invalid\n"))
			}
			got, err := r.Head()
			accepted := mode == "attached" || mode == "bare" || mode == "detached" || mode == "unborn"
			if (err == nil) != accepted || accepted && got != want {
				t.Fatalf("head: %+v %v; accepted=%v want=%+v", got, err, accepted, want)
			}
		})
	}
}

func TestFreshReferencesAndConfiguration(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	paths, _ := r.Paths()
	writeFile(t, filepath.Join(paths.CommonDir, "refs", "heads", "alias"), []byte("ref: refs/heads/main\n"))
	refs, err := r.References("refs/heads/")
	if err != nil || len(refs) != 2 || refs["refs/heads/main"].ID != first || refs["refs/heads/alias"].Symbolic != "refs/heads/main" {
		t.Fatalf("references: %+v %v", refs, err)
	}
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", second, first)
	refs, err = r.References("refs/heads/main")
	if err != nil || len(refs) != 1 || refs["refs/heads/main"].ID != second {
		t.Fatalf("stale refs: %+v %v", refs, err)
	}
	if _, err := r.Config("tasktool.missing"); !HasCode(err, NotFound) {
		t.Fatalf("missing key: %v", err)
	}
	for _, value := range []string{"first", "", "line\nbreak", "second"} {
		if err := r.SetLocalConfig("tasktool.value", value); err != nil {
			t.Fatal(err)
		}
		got, err := r.Config("tasktool.value")
		if err != nil || got != value {
			t.Fatalf("config %q: %q %v", value, got, err)
		}
	}
	other, err := Open(paths.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.SetLocalConfig("tasktool.value", "external change"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Config("tasktool.value")
	if err != nil || got != "external change" {
		t.Fatalf("stale config: %q %v", got, err)
	}
	if err := r.SetLocalConfig("tasktool.value", "nul\x00suffix"); err == nil {
		t.Fatal("accepted NUL")
	}
}

func TestMergeBasesAndHistoricalNames(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	blob, err := r.WriteBlob([]byte("task"))
	if err != nil {
		t.Fatal(err)
	}
	makeCommit := func(names []string, parents ...ID) ID {
		t.Helper()
		var entries []TreeEntry
		for _, name := range names {
			entries = append(entries, TreeEntry{Name: name, ID: blob, Mode: 0100644})
		}
		tree, err := r.WriteTree(entries)
		if err != nil {
			t.Fatal(err)
		}
		id, err := r.WriteCommit(tree, parents, testSignature, testSignature, "history\n")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := makeCommit([]string{"EXAMPLE-1 old.md"})
	left := makeCommit([]string{"EXAMPLE-2 left.md"}, first)
	right := makeCommit([]string{"EXAMPLE-3 right.md"}, first)
	merge := makeCommit(nil, left, right)
	orphan := makeCommit([]string{"EXAMPLE-99 orphan.md"})
	paths, err := r.HistoryPaths([]ID{merge, orphan, first}, "", 0)
	want := []string{"EXAMPLE-1 old.md", "EXAMPLE-2 left.md", "EXAMPLE-3 right.md", "EXAMPLE-99 orphan.md"}
	if err != nil || !reflect.DeepEqual(paths, want) {
		t.Fatalf("history: %v %v", paths, err)
	}
	for _, pair := range []struct {
		a, b ID
		want []ID
	}{
		{left, right, []ID{first}}, {merge, first, []ID{first}}, {orphan, first, nil}, {first, first, []ID{first}},
	} {
		got, err := r.MergeBases(pair.a, pair.b)
		if err != nil || !reflect.DeepEqual(got, pair.want) {
			t.Fatalf("bases: %v %v, want %v", got, err, pair.want)
		}
	}
	if _, err := r.MergeBases(ID{}, first); err == nil {
		t.Fatal("missing object treated as independent history")
	}
	if _, err := r.HistoryPaths([]ID{{}}, "", 0); err == nil {
		t.Fatal("accepted missing history tip")
	}
	paths, err = r.HistoryPaths(nil, "", 0)
	if err != nil || len(paths) != 0 {
		t.Fatalf("empty history: %v %v", paths, err)
	}
}

func TestHistoryPathsSubtreesAndDeduplication(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	blob, err := r.WriteBlob([]byte("task"))
	if err != nil {
		t.Fatal(err)
	}
	emptyTree, err := r.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two archive buckets with identical contents share a subtree OID under
	// distinct prefixes; both prefixes must appear in the result.
	sharedBucket, err := r.WriteTree([]TreeEntry{
		{Name: "EXAMPLE-1 shared.md", ID: blob, Mode: 0100644},
		{Name: "nested", ID: emptyTree, Mode: 0040000},
	})
	if err != nil {
		t.Fatal(err)
	}
	deepLeaf, err := r.WriteTree([]TreeEntry{{Name: "too-deep.md", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	deepBucket, err := r.WriteTree([]TreeEntry{{Name: "sub", ID: deepLeaf, Mode: 0040000}})
	if err != nil {
		t.Fatal(err)
	}
	archiveTree, err := r.WriteTree([]TreeEntry{
		{Name: "0000", ID: sharedBucket, Mode: 0040000},
		{Name: "0001", ID: sharedBucket, Mode: 0040000},
		{Name: "0002", ID: deepBucket, Mode: 0040000},
		{Name: "README.md", ID: blob, Mode: 0100644},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherSubtree, err := r.WriteTree([]TreeEntry{{Name: "ignored.md", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	// Commit 1: "archive" is a regular file, not a directory.
	tree1, err := r.WriteTree([]TreeEntry{
		{Name: "archive", ID: blob, Mode: 0100644},
		{Name: "EXAMPLE-10 a.md", ID: blob, Mode: 0100644},
	})
	if err != nil {
		t.Fatal(err)
	}
	c1, err := r.WriteCommit(tree1, nil, testSignature, testSignature, "file archive\n")
	if err != nil {
		t.Fatal(err)
	}
	// Commit 2: "archive" becomes a directory, plus an unrelated subtree.
	tree2, err := r.WriteTree([]TreeEntry{
		{Name: "archive", ID: archiveTree, Mode: 0040000},
		{Name: "other", ID: otherSubtree, Mode: 0040000},
		{Name: "EXAMPLE-10 a.md", ID: blob, Mode: 0100644},
	})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := r.WriteCommit(tree2, []ID{c1}, testSignature, testSignature, "dir archive\n")
	if err != nil {
		t.Fatal(err)
	}
	// Commit 3: identical root tree OID to commit 2.
	c3, err := r.WriteCommit(tree2, []ID{c2}, testSignature, testSignature, "same root tree\n")
	if err != nil {
		t.Fatal(err)
	}
	// Commit 4: deleted file and different root tree reusing archiveTree.
	tree4, err := r.WriteTree([]TreeEntry{
		{Name: "archive", ID: archiveTree, Mode: 0040000},
		{Name: "EXAMPLE-11 b.md", ID: blob, Mode: 0100644},
	})
	if err != nil {
		t.Fatal(err)
	}
	c4, err := r.WriteCommit(tree4, []ID{c3}, testSignature, testSignature, "next\n")
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.HistoryPaths([]ID{c4}, "archive", 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"EXAMPLE-10 a.md",
		"EXAMPLE-11 b.md",
		"archive",
		"archive/0000",
		"archive/0000/EXAMPLE-1 shared.md",
		"archive/0000/nested",
		"archive/0001",
		"archive/0001/EXAMPLE-1 shared.md",
		"archive/0001/nested",
		"archive/0002",
		"archive/0002/sub",
		"archive/README.md",
		"other",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("depth 2:\ngot:  %v\nwant: %v", got, want)
	}

	gotDepth1, err := r.HistoryPaths([]ID{c4}, "archive", 1)
	if err != nil {
		t.Fatal(err)
	}
	wantDepth1 := []string{
		"EXAMPLE-10 a.md",
		"EXAMPLE-11 b.md",
		"archive",
		"archive/0000",
		"archive/0001",
		"archive/0002",
		"archive/README.md",
		"other",
	}
	if !reflect.DeepEqual(gotDepth1, wantDepth1) {
		t.Fatalf("depth 1:\ngot:  %v\nwant: %v", gotDepth1, wantDepth1)
	}

	gotDepth0, err := r.HistoryPaths([]ID{c4}, "archive", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantDepth0 := []string{
		"EXAMPLE-10 a.md",
		"EXAMPLE-11 b.md",
		"archive",
		"other",
	}
	if !reflect.DeepEqual(gotDepth0, wantDepth0) {
		t.Fatalf("depth 0:\ngot:  %v\nwant: %v", gotDepth0, wantDepth0)
	}

	// Non-matching directory with NUL byte should behave like a non-match.
	gotNul, err := r.HistoryPaths([]ID{c4}, "archive\x00", 2)
	if err != nil || !reflect.DeepEqual(gotNul, wantDepth0) {
		t.Fatalf("nul directory: %v %v", gotNul, err)
	}

	// Verify libgit2 error code and message are preserved on invalid tip.
	_, err = r.HistoryPaths([]ID{{1}}, "archive", 2)
	var nativeErr *Error
	if !errors.As(err, &nativeErr) || nativeErr.Code != NotFound || nativeErr.Message == "" {
		t.Fatalf("invalid tip error: %#v", err)
	}
}

func BenchmarkHistoryPaths(b *testing.B) {
	root := filepath.Join(b.TempDir(), "repository")
	r, err := Init(root, "", true)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	blob, err := r.WriteBlob([]byte("task body\n"))
	if err != nil {
		b.Fatal(err)
	}
	const width = 200
	const commits = 150
	entries := make([]TreeEntry, 0, width)
	for i := range width {
		entries = append(entries, TreeEntry{Name: fmt.Sprintf("TASKS-%d Task title %d.md", i+1, i+1), ID: blob, Mode: 0100644})
	}
	tree, err := r.WriteTree(entries)
	if err != nil {
		b.Fatal(err)
	}
	tip, err := r.WriteCommit(tree, nil, testSignature, testSignature, "seed\n")
	if err != nil {
		b.Fatal(err)
	}
	for i := 1; i < commits; i++ {
		if i%3 == 0 {
			// Repeated root tree OID (e.g., empty/revert commits).
			tip, err = r.WriteCommit(tree, []ID{tip}, testSignature, testSignature, "repeat\n")
			if err != nil {
				b.Fatal(err)
			}
			continue
		}
		updated, err := r.WriteBlob([]byte(fmt.Sprintf("task body %d\n", i)))
		if err != nil {
			b.Fatal(err)
		}
		tree, err = r.EditTree(tip, []TreeEdit{{Path: fmt.Sprintf("TASKS-%d Task title %d.md", (i%width)+1, (i%width)+1), ID: updated, Mode: 0100644}})
		if err != nil {
			b.Fatal(err)
		}
		tip, err = r.WriteCommit(tree, []ID{tip}, testSignature, testSignature, fmt.Sprintf("commit %d\n", i))
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		paths, err := r.HistoryPaths([]ID{tip}, "archive", 2)
		if err != nil || len(paths) != width {
			b.Fatalf("HistoryPaths: %d %v", len(paths), err)
		}
	}
}

func TestLiteralTreeAndBlobReading(t *testing.T) {
	t.Parallel()
	r, _ := fixture(t, false)
	data := []byte{0, '\r', '\n', 0xff, 'x'}
	blob, err := r.WriteBlob(data)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"literal[abc]", "line\nbreak", "-option", "tab\tname", ":(glob)*"}
	var entries []TreeEntry
	for _, name := range names {
		entries = append(entries, TreeEntry{Name: name, ID: blob, Mode: 0100644})
	}
	subtree, err := r.WriteTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.WriteTree([]TreeEntry{{Name: "nested", ID: subtree, Mode: 0040000}, {Name: "link", ID: blob, Mode: 0120000}})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := r.WriteCommit(tree, nil, testSignature, testSignature, "paths\n")
	if err != nil {
		t.Fatal(err)
	}
	root, err := r.Tree(revision)
	if err != nil || len(root) != 2 || root[0].Name != "link" || root[1].Mode != 0040000 {
		t.Fatalf("root: %+v %v", root, err)
	}
	for _, name := range append(names, "../link") {
		path := "nested/" + name
		entry, err := r.Entry(revision, path)
		if name == "../link" {
			if err == nil {
				t.Fatal("accepted traversal")
			}
			continue
		}
		if err != nil || entry.ID != blob || entry.Name != path {
			t.Fatalf("entry %q: %+v %v", path, entry, err)
		}
		got, err := r.Blob(entry.ID)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("blob: %v %v", got, err)
		}
	}
	for _, path := range []string{"", "/link", "nested//x", "./link", ".git/config", "nul\x00suffix"} {
		if _, err := r.Entry(revision, path); err == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	if _, err := r.Entry(revision, "missing"); !HasCode(err, NotFound) {
		t.Fatalf("missing path: %v", err)
	}
	if _, err := r.Blob(tree); err == nil {
		t.Fatal("read tree as blob")
	}
}

func TestStatusIncludesUnreadableTrackedContent(t *testing.T) {
	t.Parallel()
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	checkoutInitial(t, r, first)
	path := filepath.Join(root, "content.txt")
	// Keep the size unchanged so status must inspect content instead of
	// classifying a size change without trying to read the file.
	writeFile(t, path, []byte("other"))
	readable, err := r.Status(false)
	if err != nil || len(readable) != 1 || readable[0].Status != WorktreeModified {
		t.Fatalf("readable fixture status: %+v %v", readable, err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("host can read mode-000 files; unreadable-file behavior cannot be exercised")
	}
	changes, err := r.Status(false)
	if err != nil {
		// Some native diff paths return a file-access error instead of a
		// status entry. Propagating that error also prevents a false clean.
		if !HasCode(err, Locked) {
			t.Fatalf("unexpected unreadable-file error: %v", err)
		}
		return
	}
	if len(changes) != 1 || changes[0].Path != "content.txt" || changes[0].Status&WorktreeUnreadable == 0 {
		t.Fatalf("unreadable file omitted: %+v", changes)
	}
}

func TestStatusRejectsUnreadableDirectory(t *testing.T) {
	t.Parallel()
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	checkoutInitial(t, r, first)
	path := filepath.Join(root, "hidden")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(path, "precious"), []byte("local data"))
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(path, 0700) })
	if _, err := os.ReadDir(path); err == nil {
		t.Skip("host can read mode-000 directories; unreadable-directory behavior cannot be exercised")
	}
	if changes, err := r.Status(false); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unreadable directory omitted: %+v %v", changes, err)
	}
	paths, _ := r.Paths()
	if err := os.MkdirAll(filepath.Join(paths.CommonDir, "info"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(paths.CommonDir, "info", "exclude"), []byte("hidden/\n"))
	if changes, err := r.Status(false); err != nil || len(changes) != 0 {
		t.Fatalf("untracked ignored directory not excluded: %+v %v", changes, err)
	}
	if changes, err := r.Status(true); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("ignored directory omitted from full status: %+v %v", changes, err)
	}
}

func TestStatusAndIndexAreFreshAndReadOnly(t *testing.T) {
	t.Parallel()
	r, root := fixture(t, false)
	first := commit(t, r, "original")
	publish(t, r, "refs/heads/main", first, ID{})
	checkoutInitial(t, r, first)
	paths, _ := r.Paths()
	indexPath := filepath.Join(paths.GitDir, "index")
	before := readFile(t, indexPath)
	changes, err := r.Status(false)
	if err != nil || len(changes) != 0 {
		t.Fatalf("initial status: %+v %v", changes, err)
	}
	writeFile(t, filepath.Join(root, "content.txt"), []byte("changed"))
	writeFile(t, filepath.Join(root, ".gitignore"), []byte("ignored/\n"))
	if err := os.Mkdir(filepath.Join(root, "ignored"), 0700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "ignored", "precious"), []byte("keep"))
	writeFile(t, filepath.Join(root, "new\nfile"), []byte("new"))
	changes, err = r.Status(true)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]Status)
	for _, change := range changes {
		got[change.Path] = change.Status
	}
	want := map[string]Status{".gitignore": WorktreeNew, "content.txt": WorktreeModified, "ignored/precious": Ignored, "new\nfile": WorktreeNew}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status: %+v want %+v", got, want)
	}
	entries, err := r.IndexEntries()
	if err != nil || len(entries) != 1 || entries[0].Path != "content.txt" || entries[0].Stage != 0 {
		t.Fatalf("index: %+v %v", entries, err)
	}
	if !bytes.Equal(before, readFile(t, indexPath)) {
		t.Fatal("read-only status/index changed index bytes")
	}
	changes, err = r.Status(false)
	if err != nil || len(changes) != 3 {
		t.Fatalf("ignored exclusion: %+v %v", changes, err)
	}
	// Atomically replace the on-disk index while the repository handle stays open.
	blob, err := r.WriteBlob([]byte("staged"))
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(paths.GitDir, "replacement")
	writeFile(t, replacement, before)
	if err := r.EditIndexFile(replacement, ID{}, []TreeEdit{{Path: "content.txt", ID: blob, Mode: 0100644}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, indexPath); err != nil {
		t.Fatal(err)
	}
	entries, err = r.IndexEntries()
	if err != nil || entries[0].ID != blob {
		t.Fatalf("stale index: %+v %v", entries, err)
	}
	changes, err = r.Status(false)
	if err != nil {
		t.Fatal(err)
	}
	got = make(map[string]Status)
	for _, change := range changes {
		got[change.Path] = change.Status
	}
	if got["content.txt"] != IndexModified|WorktreeModified {
		t.Fatalf("stale status: %+v", got)
	}
}
