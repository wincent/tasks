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

func TestHeadWorkdirValidation(t *testing.T) {
	for _, mode := range []string{"attached", "bare", "detached", "unborn", "nonbranch", "alias", "missing-object", "blob", "malformed", "missing", "configuration"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := fixture(t, mode == "bare")
			tip := commit(t, r, "first")
			publish(t, r, "refs/heads/main", tip, ID{})
			paths, err := r.Paths()
			if err != nil {
				t.Fatal(err)
			}
			if got, err := r.HeadWorkdir(); err != nil || got != paths.WorkDir {
				t.Fatalf("initial workdir: %q %v; want %q", got, err, paths.WorkDir)
			}
			head := filepath.Join(paths.GitDir, "HEAD")
			switch mode {
			case "detached":
				writeFile(t, head, []byte(tip.String()+"\n"))
			case "unborn":
				writeFile(t, head, []byte("ref: refs/heads/unborn\n"))
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
			_, wantErr := r.Head()
			got, err := r.HeadWorkdir()
			if fmt.Sprint(err) != fmt.Sprint(wantErr) {
				t.Fatalf("workdir error: %v; Head error: %v", err, wantErr)
			}
			accepted := mode == "attached" || mode == "bare" || mode == "detached" || mode == "unborn"
			if (err == nil) != accepted || accepted && got != paths.WorkDir || !accepted && got != "" {
				t.Fatalf("workdir: %q %v; accepted=%v", got, err, accepted)
			}
		})
	}
}

func TestFreshReferencesAndConfiguration(t *testing.T) {
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
	paths, err := r.HistoryPaths([]ID{merge, orphan, first})
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
	if _, err := r.HistoryPaths([]ID{{}}); err == nil {
		t.Fatal("accepted missing history tip")
	}
	paths, err = r.HistoryPaths(nil)
	if err != nil || len(paths) != 0 {
		t.Fatalf("empty history: %v %v", paths, err)
	}
}

func TestLiteralTreeAndBlobReading(t *testing.T) {
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
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
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
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
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
	r, root := fixture(t, false)
	first := commit(t, r, "original")
	publish(t, r, "refs/heads/main", first, ID{})
	if err := r.CheckoutMetadata(ID{}, first); err != nil {
		t.Fatal(err)
	}
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
