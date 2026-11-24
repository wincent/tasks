package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testSignature = Signature{
	Name: "Task Test", Email: "task@example.invalid", When: time.Unix(1700000000, 0).UTC(),
}

func fixture(t *testing.T, bare bool) (*Repository, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	r, err := Init(root, "", bare)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r, root
}

func commit(t *testing.T, r *Repository, message string, parents ...ID) ID {
	t.Helper()
	blob, err := r.WriteBlob([]byte(message))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := r.WriteTree([]TreeEntry{{Name: "content.txt", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.WriteCommit(tree, parents, testSignature, testSignature, message+"\n")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func publish(t *testing.T, r *Repository, name string, next, old ID) {
	t.Helper()
	tx, err := r.Prepare(name, next, map[string]ID{name: old}, testSignature, "test publication")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func expectRef(t *testing.T, r *Repository, name string, want ID) {
	t.Helper()
	ref, err := r.Reference(name)
	if want == (ID{}) {
		if !HasCode(err, NotFound) {
			t.Fatalf("expected absent %s, got %+v, %v", name, ref, err)
		}
		return
	}
	if err != nil || ref.Symbolic != "" || ref.ID != want {
		t.Fatalf("%s: got %+v, %v; want %s", name, ref, err, want)
	}
}

func TestObjectIDs(t *testing.T) {
	for _, value := range []string{"", "abc", strings.Repeat("g", 40), strings.Repeat("0", 64)} {
		if _, err := ParseID(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	value := strings.Repeat("aB", 20)
	id, err := ParseID(value)
	if err != nil || id.String() != strings.ToLower(value) {
		t.Fatalf("roundtrip: %v, %v", id, err)
	}
}

func TestRepositoryDiscovery(t *testing.T) {
	for _, bare := range []bool{false, true} {
		t.Run(fmt.Sprintf("bare=%v", bare), func(t *testing.T) {
			r, root := fixture(t, bare)
			paths, err := r.Paths()
			if err != nil || paths.Bare != bare {
				t.Fatalf("paths: %+v, %v", paths, err)
			}
			if bare && paths.WorkDir != "" {
				t.Fatalf("bare repo has workdir: %+v", paths)
			}
			if !bare {
				subdir := filepath.Join(root, "a", "b")
				if err := os.MkdirAll(subdir, 0755); err != nil {
					t.Fatal(err)
				}
				opened, err := Open(subdir)
				if err != nil {
					t.Fatal(err)
				}
				defer opened.Close()
				other, err := opened.Paths()
				if err != nil || other != paths {
					t.Fatalf("discovery mismatch: %+v != %+v; %v", other, paths, err)
				}
			}
			if duplicate, err := Init(root, "", bare); err == nil {
				duplicate.Close()
				t.Fatal("reinitialized existing repository")
			}
		})
	}
}

func TestSeparateGitDirectory(t *testing.T) {
	root := t.TempDir()
	r, err := Init(filepath.Join(root, "private.git"), filepath.Join(root, "checkout"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	paths, err := r.Paths()
	if err != nil || filepath.Base(paths.GitDir) != "private.git" || filepath.Base(paths.WorkDir) != "checkout" {
		t.Fatalf("paths: %+v, %v", paths, err)
	}
	id := commit(t, r, "separate")
	publish(t, r, "refs/heads/main", id, ID{})
	opened, err := Open(paths.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	expectRef(t, opened, "refs/heads/main", id)
}

func TestLinkedWorktreeFromBareContainer(t *testing.T) {
	container := t.TempDir()
	r, err := Init(filepath.Join(container, ".git"), "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	id := commit(t, r, "linked")
	publish(t, r, "refs/heads/main", id, ID{})
	workdir := filepath.Join(container, "main")
	if err := r.AddWorktree("main", workdir, "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(workdir)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	common, _ := r.Paths()
	paths, err := linked.Paths()
	if err != nil || paths.CommonDir != common.CommonDir || paths.GitDir == paths.CommonDir || paths.Bare {
		t.Fatalf("linked paths: %+v, common %+v, %v", paths, common, err)
	}
	data, err := os.ReadFile(filepath.Join(paths.WorkDir, "content.txt"))
	if err != nil || string(data) != "linked" {
		t.Fatalf("checkout: %q, %v", data, err)
	}
	next := commit(t, linked, "second", id)
	publish(t, linked, "refs/heads/main", next, id)
	expectRef(t, r, "refs/heads/main", next)
	if err := r.AddWorktree("duplicate", filepath.Join(container, "duplicate"), "refs/heads/main"); err == nil {
		t.Fatal("allowed same branch in multiple worktrees")
	}
}

func TestObjectsAndAncestry(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	orphan := commit(t, r, "orphan")
	merge := commit(t, r, "merge", second, orphan)
	for _, pair := range []struct {
		a, b ID
		want bool
	}{{first, first, true}, {first, second, true}, {second, first, false}, {orphan, second, false}, {first, merge, true}, {orphan, merge, true}} {
		got, err := r.IsAncestor(pair.a, pair.b)
		if err != nil || got != pair.want {
			t.Fatalf("ancestry %s %s: %v %v", pair.a, pair.b, got, err)
		}
	}
	if _, err := r.IsAncestor(ID{}, ID{}); err == nil {
		t.Fatal("nonexistent equal objects treated as ancestry")
	}
	empty, err := r.WriteBlob(nil)
	if err != nil || empty.String() != "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391" {
		t.Fatalf("empty blob: %s %v", empty, err)
	}
	if _, err := r.IsAncestor(empty, empty); err == nil {
		t.Fatal("blob treated as commit")
	}
	tree, err := r.WriteTree(nil)
	if err != nil || tree.String() != "4b825dc642cb6eb9a060e54bf8d69288fbee4904" {
		t.Fatalf("empty tree: %s %v", tree, err)
	}
	for _, name := range []string{"tab\tname", "line\nbreak", "literal[abc]", "-option", "utf8-ß"} {
		if _, err := r.WriteTree([]TreeEntry{{Name: name, ID: empty, Mode: 0100644}}); err != nil {
			t.Fatalf("unusual filename %q: %v", name, err)
		}
	}
	for _, name := range []string{"", "../escape", "a/b", "nul\x00suffix", ".git"} {
		if _, err := r.WriteTree([]TreeEntry{{Name: name, ID: empty, Mode: 0100644}}); err == nil {
			t.Fatalf("unsafe tree name accepted: %q", name)
		}
	}
	duplicate := TreeEntry{Name: "duplicate", ID: empty, Mode: 0100644}
	if _, err := r.WriteTree([]TreeEntry{duplicate, duplicate}); err == nil {
		t.Fatal("duplicate entry accepted")
	}
	// Missing object must still be rejected with a libgit2 error.
	if _, err := r.WriteTree([]TreeEntry{{Name: "missing.txt", ID: ID{1}, Mode: 0100644}}); err == nil {
		t.Fatal("missing object accepted in tree")
	} else {
		var nativeErr *Error
		if !errors.As(err, &nativeErr) || nativeErr.Message == "" {
			t.Fatalf("expected native error with message, got %#v", err)
		}
	}
	// Wide tree writes preserve exact ordering and entries.
	wide := make([]TreeEntry, 0, 256)
	for i := 255; i >= 0; i-- {
		wide = append(wide, TreeEntry{Name: fmt.Sprintf("entry-%03d.txt", i), ID: empty, Mode: 0100644})
	}
	wideTree, err := r.WriteTree(wide)
	if err != nil {
		t.Fatalf("wide WriteTree: %v", err)
	}
	wideCommit, err := r.WriteCommit(wideTree, nil, testSignature, testSignature, "wide\n")
	if err != nil {
		t.Fatal(err)
	}
	gotWide, err := r.Tree(wideCommit)
	if err != nil || len(gotWide) != len(wide) || gotWide[0].Name != "entry-000.txt" || gotWide[255].Name != "entry-255.txt" {
		t.Fatalf("wide tree entries: len=%d err=%v", len(gotWide), err)
	}
}

func BenchmarkWriteTree(b *testing.B) {
	root := filepath.Join(b.TempDir(), "repository")
	r, err := Init(root, "", true)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close()
	blob, err := r.WriteBlob([]byte("task content\n"))
	if err != nil {
		b.Fatal(err)
	}
	const width = 500
	blobEntries := make([]TreeEntry, 0, width)
	linkEntries := make([]TreeEntry, 0, width)
	for i := range width {
		name := fmt.Sprintf("TASKS-%04d Benchmark task entry title %d.md", i+1, i+1)
		blobEntries = append(blobEntries, TreeEntry{Name: name, ID: blob, Mode: 0100644})
		linkEntries = append(linkEntries, TreeEntry{Name: name, ID: blob, Mode: 0160000})
	}
	b.Run("blobs", func(b *testing.B) {
		for b.Loop() {
			if _, err := r.WriteTree(blobEntries); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("gitlinks", func(b *testing.B) {
		for b.Loop() {
			if _, err := r.WriteTree(linkEntries); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func TestPreparedPublication(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	publish(t, r, "refs/heads/source", second, ID{})
	expected := map[string]ID{"refs/heads/main": first, "refs/heads/source": second, "refs/heads/missing": {}}
	tx, err := r.Prepare("refs/heads/main", second, expected, testSignature, "publish")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	expectRef(t, r, "refs/heads/main", first)
	for name, old := range expected {
		other, err := r.Prepare(name, second, map[string]ID{name: old}, testSignature, "interloper")
		if other != nil {
			other.Close()
		}
		if !HasCode(err, Locked) {
			t.Fatalf("%s was not locked: %v", name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expectRef(t, r, "refs/heads/main", second)
	expectRef(t, r, "refs/heads/source", second)
	expectRef(t, r, "refs/heads/missing", ID{})
	if err := tx.Commit(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second commit: %v", err)
	}
	// Every verification lock must be released after publication.
	publish(t, r, "refs/heads/missing", second, ID{})
	publish(t, r, "refs/heads/source", first, second)
}

func TestPublicationAbortAndFailureReleaseAllLocks(t *testing.T) {
	r, _ := fixture(t, false)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	tx, err := r.Prepare("refs/heads/main", second, map[string]ID{"refs/heads/main": first}, testSignature, "abort")
	if err != nil {
		t.Fatal(err)
	}
	tx.Close()
	tx.Close()
	expectRef(t, r, "refs/heads/main", first)
	for _, expected := range []map[string]ID{
		{"refs/heads/main": second},
		{"refs/heads/main": {}},
		{"refs/heads/a-first-lock": {}, "refs/heads/main": first, "refs/heads/z-missing": second},
	} {
		tx, err := r.Prepare("refs/heads/main", second, expected, testSignature, "stale")
		if tx != nil {
			tx.Close()
		}
		var changed *ChangedError
		if !errors.As(err, &changed) {
			t.Fatalf("expected changed error: %v", err)
		}
	}
	publish(t, r, "refs/heads/a-first-lock", first, ID{})
	publish(t, r, "refs/heads/main", second, first)
}

func TestFreshReferenceComparison(t *testing.T) {
	r, root := fixture(t, false)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	expectRef(t, r, "refs/heads/main", first)
	other, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	publish(t, other, "refs/heads/main", second, first)
	tx, err := r.Prepare("refs/heads/main", first, map[string]ID{"refs/heads/main": first}, testSignature, "stale cache")
	if tx != nil {
		tx.Close()
	}
	var changed *ChangedError
	if !errors.As(err, &changed) || changed.Actual.ID != second {
		t.Fatalf("did not compare fresh ref: %v", err)
	}
}

func TestSymbolicReferenceRejected(t *testing.T) {
	r, _ := fixture(t, false)
	id := commit(t, r, "first")
	publish(t, r, "refs/heads/main", id, ID{})
	paths, _ := r.Paths()
	alias := filepath.Join(paths.CommonDir, "refs", "heads", "alias")
	if err := os.WriteFile(alias, []byte("ref: refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []ID{{}, id} {
		tx, err := r.Prepare("refs/heads/alias", id, map[string]ID{"refs/heads/alias": expected}, testSignature, "alias")
		if tx != nil {
			tx.Close()
		}
		var changed *ChangedError
		if !errors.As(err, &changed) || changed.Actual.Symbolic != "refs/heads/main" {
			t.Fatalf("symbolic target accepted or misclassified: %v", err)
		}
	}
	expectRef(t, r, "refs/heads/main", id)
}

func TestPackedRefAndDeletion(t *testing.T) {
	r, _ := fixture(t, true)
	first := commit(t, r, "packed")
	second := commit(t, r, "second", first)
	paths, _ := r.Paths()
	// Construct a packed-ref fixture directly, independently of the update API.
	if err := os.WriteFile(filepath.Join(paths.CommonDir, "packed-refs"), []byte("# pack-refs with: peeled fully-peeled sorted\n"+first.String()+" refs/heads/main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	expectRef(t, r, "refs/heads/main", first)
	publish(t, r, "refs/heads/main", second, first)
	publish(t, r, "refs/heads/main", ID{}, second)
	expectRef(t, r, "refs/heads/main", ID{})
	opened, err := Open(paths.GitDir)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	expectRef(t, opened, "refs/heads/main", ID{})
}

func TestPreparedTransactionOutlivesRepository(t *testing.T) {
	r, root := fixture(t, true)
	id := commit(t, r, "first")
	tx, err := r.Prepare("refs/heads/main", id, map[string]ID{"refs/heads/main": {}}, testSignature, "independent handle")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	r.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Paths(); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed repository call: %v", err)
	}
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	expectRef(t, opened, "refs/heads/main", id)
}

func TestPublicationRejectsInvalidInputs(t *testing.T) {
	r, _ := fixture(t, true)
	id := commit(t, r, "first")
	blob, err := r.WriteBlob([]byte("not a commit"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main", "HEAD", "refs/heads/../escape", "refs/heads/with space", "refs/heads/nul\x00suffix"} {
		tx, err := r.Prepare(name, id, map[string]ID{name: {}}, testSignature, "invalid ref")
		if tx != nil {
			tx.Close()
		}
		if err == nil {
			t.Fatalf("accepted reference %q", name)
		}
	}
	for _, next := range []ID{blob, {}, {1}} {
		tx, err := r.Prepare("refs/heads/main", next, map[string]ID{"refs/heads/main": {}}, testSignature, "invalid target")
		if tx != nil {
			tx.Close()
		}
		if err == nil {
			t.Fatalf("accepted publication target %s", next)
		}
	}
	if tx, err := r.Prepare("refs/heads/main", id, nil, testSignature, "missing expectation"); err == nil {
		tx.Close()
		t.Fatal("accepted unconditional update")
	}
	publish(t, r, "refs/heads/main", id, ID{})
}

func TestConcurrentCompareAndPublish(t *testing.T) {
	r, _ := fixture(t, true)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	start := make(chan struct{})
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			tx, err := r.Prepare("refs/heads/main", second, map[string]ID{"refs/heads/main": first}, testSignature, "concurrent")
			if err == nil {
				err = tx.Commit()
				tx.Close()
			}
			results <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			var changed *ChangedError
			if !HasCode(err, Locked) && !errors.As(err, &changed) {
				t.Fatalf("unexpected concurrent error: %v", err)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("%d publications succeeded, expected exactly one", successes)
	}
	expectRef(t, r, "refs/heads/main", second)
}

func TestPublicationFailureReportsUncertainty(t *testing.T) {
	r, _ := fixture(t, true)
	first := commit(t, r, "first")
	second := commit(t, r, "second", first)
	publish(t, r, "refs/heads/main", first, ID{})
	tx, err := r.Prepare("refs/heads/main", second, map[string]ID{"refs/heads/main": first, "refs/heads/verified": {}}, testSignature, "fail publication")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	paths, _ := r.Paths()
	// Remove only this test's own lock file, simulating failed publication IO.
	if err := os.Remove(filepath.Join(paths.CommonDir, "refs/heads/main.lock")); err != nil {
		t.Fatal(err)
	}
	var uncertain *PublicationError
	if err := tx.Commit(); !errors.As(err, &uncertain) {
		t.Fatalf("expected explicit publication uncertainty, got %v", err)
	}
	expectRef(t, r, "refs/heads/main", first)
	publish(t, r, "refs/heads/verified", second, ID{})
	publish(t, r, "refs/heads/main", second, first)
}

func TestReferencePublicationPreservesUnrelatedBytes(t *testing.T) {
	r, root := fixture(t, false)
	id := commit(t, r, "first")
	paths, _ := r.Paths()
	files := map[string]string{
		filepath.Join(paths.GitDir, "index"):      "opaque unrelated index bytes\x00",
		filepath.Join(root, "untracked[1]\nfile"): "untracked bytes\x00",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	publish(t, r, "refs/heads/main", id, ID{})
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("modified unrelated %s: %q %v", path, got, err)
		}
	}
}

func TestExternalProcessCannotPublishLockedReference(t *testing.T) {
	if os.Getenv("TASKTOOL_LOCK_CHILD") == "1" {
		r, err := Open(os.Getenv("TASKTOOL_LOCK_REPO"))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		id, err := ParseID(os.Getenv("TASKTOOL_LOCK_ID"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"refs/heads/main", "refs/heads/verified", "refs/heads/absent"} {
			old := id
			if name == "refs/heads/absent" {
				old = ID{}
			}
			tx, err := r.Prepare(name, id, map[string]ID{name: old}, testSignature, "competing process")
			if tx != nil {
				tx.Close()
			}
			if !HasCode(err, Locked) {
				t.Fatalf("external process acquired %s: %v", name, err)
			}
		}
		return
	}
	r, root := fixture(t, true)
	id := commit(t, r, "first")
	publish(t, r, "refs/heads/main", id, ID{})
	publish(t, r, "refs/heads/verified", id, ID{})
	tx, err := r.Prepare("refs/heads/main", id, map[string]ID{"refs/heads/main": id, "refs/heads/verified": id, "refs/heads/absent": {}}, testSignature, "held locks")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestExternalProcessCannotPublishLockedReference$", "-test.timeout=5s")
	command.Env = append(os.Environ(), "TASKTOOL_LOCK_CHILD=1", "TASKTOOL_LOCK_REPO="+root, "TASKTOOL_LOCK_ID="+id.String(), "PATH="+t.TempDir())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("competing native process: %v\n%s", err, output)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestNoGitOrPythonOnPATH(t *testing.T) {
	if os.Getenv("TASKTOOL_NATIVE_CHILD") == "1" {
		r, _ := fixture(t, true)
		first := commit(t, r, "no tools")
		second := commit(t, r, "still no tools", first)
		publish(t, r, "refs/heads/main", first, ID{})
		publish(t, r, "refs/heads/main", second, first)
		if ancestor, err := r.IsAncestor(first, second); err != nil || !ancestor {
			t.Fatalf("ancestry: %v, %v", ancestor, err)
		}
		expectRef(t, r, "refs/heads/main", second)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run=^TestNoGitOrPythonOnPATH$", "-test.timeout=10s")
	command.Env = append(os.Environ(), "TASKTOOL_NATIVE_CHILD=1", "PATH="+t.TempDir())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native-only child: %v\n%s", err, output)
	}
}
