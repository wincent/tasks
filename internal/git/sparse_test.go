package git

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Raw full-index fixture, independent of Git CLI and libgit2 flag setters.
func sparseIndexFixture(t *testing.T, r *Repository, entries []IndexEntry) string {
	t.Helper()
	entries = append([]IndexEntry(nil), entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var data bytes.Buffer
	data.WriteString("DIRC")
	binary.Write(&data, binary.BigEndian, uint32(3))
	binary.Write(&data, binary.BigEndian, uint32(len(entries)))
	for _, entry := range entries {
		start := data.Len()
		for i := 0; i < 10; i++ {
			var value uint32
			if i == 6 {
				value = entry.Mode
			}
			binary.Write(&data, binary.BigEndian, value)
		}
		data.Write(entry.ID[:])
		flags := uint16(len(entry.Path)) | uint16(entry.Stage<<12)
		var extended uint16
		if entry.AssumeValid {
			flags |= 0x8000
		}
		if entry.SkipWorktree {
			extended |= 0x4000
		}
		if entry.IntentToAdd {
			extended |= 0x2000
		}
		if extended != 0 {
			flags |= 0x4000
		}
		binary.Write(&data, binary.BigEndian, flags)
		if extended != 0 {
			binary.Write(&data, binary.BigEndian, extended)
		}
		data.WriteString(entry.Path)
		data.WriteByte(0)
		for (data.Len()-start)%8 != 0 {
			data.WriteByte(0)
		}
	}
	checksum := sha1.Sum(data.Bytes())
	data.Write(checksum[:])
	paths, err := r.Paths()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(paths.GitDir, "index")
	writeFile(t, file, data.Bytes())
	return file
}

func sparseCommit(t *testing.T, r *Repository, base ID, contents map[string]string, removed ...string) ID {
	t.Helper()
	var edits []TreeEdit
	for path, content := range contents {
		id, err := r.WriteBlob([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		edits = append(edits, TreeEdit{Path: path, ID: id, Mode: 0100644})
	}
	for _, path := range removed {
		edits = append(edits, TreeEdit{Path: path})
	}
	tree, err := r.EditTree(base, edits)
	if err != nil {
		t.Fatal(err)
	}
	var parents []ID
	if base != (ID{}) {
		parents = []ID{base}
	}
	id, err := r.WriteCommit(tree, parents, testSignature, testSignature, "sparse fixture\n")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func setupSparse(t *testing.T, patterns string) (*Repository, string, ID, string) {
	t.Helper()
	r, root := fixture(t, false)
	base := sparseCommit(t, r, ID{}, map[string]string{"keep/file": "base", "omit/file": "base", "assumed": "base", "unrelated": "base"})
	publish(t, r, "refs/heads/main", base, ID{})
	if err := r.CheckoutMetadata(ID{}, base); err != nil {
		t.Fatal(err)
	}
	entries, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		entries[i].SkipWorktree = strings.HasPrefix(entries[i].Path, "omit/")
		entries[i].AssumeValid = entries[i].Path == "assumed"
	}
	index := sparseIndexFixture(t, r, entries)
	if err := os.Remove(filepath.Join(root, "omit", "file")); err != nil {
		t.Fatal(err)
	}
	if patterns != "" {
		if err := r.SetLocalConfig("core.sparseCheckout", "true"); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(filepath.Dir(index), "info"), 0700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(filepath.Dir(index), "info", "sparse-checkout"), []byte(patterns))
	}
	return r, root, base, index
}

func TestSparsePatterns(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, patterns    string
		included, omitted []string
	}{
		{"cone", "/*\n!/*/\n/keep/\n", []string{"root", "keep/a", "keep/nested/a"}, []string{"omit/a", "other/new"}},
		{"noncone", "/keep/*.go\n!/keep/private*\n/docs/**/readme?\n", []string{"keep/a.go", "docs/readme1", "docs/a/b/readme2"}, []string{"keep/private.go", "keep/a.txt", "keep/sub/a.go", "else/readme1"}},
		{"ancestors", "/*\n!/omit/\n/omit/keep\n", []string{"root", "other/file", "omit/keep"}, []string{"omit/file"}},
		{"escaped", "# comment\n\\#literal\n\\!literal\nspace\\ \n", []string{"#literal", "!literal", "space "}, []string{"comment", "space"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rules, err := parseSparsePatterns(tc.patterns, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range tc.included {
				if !rules.includes(path) {
					t.Errorf("omitted %q", path)
				}
			}
			for _, path := range tc.omitted {
				if rules.includes(path) {
					t.Errorf("included %q", path)
				}
			}
		})
	}
	for _, tc := range []struct{ pattern, yes, no string }{
		{"[[:alpha:]][0-9]", "a9", "99"}, {"[!a-c]", "d", "b"}, {"[]-]", "]", "a"},
		{"[[:upper:]]", "A", "9"}, {"[[:space:]]", "\t", "x"}, {"[a-c]", "B", "z"},
	} {
		rules, err := parseSparsePatterns(tc.pattern, true)
		if err != nil || !rules.includes(tc.yes) || rules.includes(tc.no) {
			t.Fatalf("bracket %+v: %v", tc, err)
		}
	}
	for _, pattern := range []string{"[", "[[:unknown:]]", "[abc"} {
		rules, err := parseSparsePatterns(pattern, false)
		if err != nil || rules.includes("a") {
			t.Fatalf("malformed bracket %q: %v", pattern, err)
		}
	}
	// Non-ASCII and raw high bytes must still match byte-by-byte with optional
	// ASCII case folding and directory/ancestor slicing.
	for _, tc := range []struct {
		name       string
		patterns   string
		ignoreCase bool
		included   []string
		omitted    []string
	}{
		{
			name:       "utf8-and-high-bytes",
			patterns:   "/café/\n!/café/SKIP*\n/\xff/ok\n",
			ignoreCase: true,
			included:   []string{"CAFé/file", "café/nested/file", "\xff/OK"},
			omitted:    []string{"café/skip.txt", "CAFé/SKIP/child", "\xfe/ok"},
		},
		{
			name:       "empty-and-slashes",
			patterns:   "/*\n!/*/\n/a//b\n",
			ignoreCase: false,
			included:   []string{"", "root", "a//b"},
			omitted:    []string{"a/", "a/b"},
		},
		{
			name:       "empty-rules",
			patterns:   "",
			ignoreCase: false,
			included:   nil,
			omitted:    []string{"", "a", "a/b"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rules, err := parseSparsePatterns(tc.patterns, tc.ignoreCase)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range tc.included {
				if !rules.includes(path) {
					t.Errorf("omitted %q", path)
				}
			}
			for _, path := range tc.omitted {
				if rules.includes(path) {
					t.Errorf("included %q", path)
				}
			}
		})
	}
}

func BenchmarkSparsePatternsIncludes(b *testing.B) {
	patterns := "/*\n!/*/\n/cmd/\n/internal/\n!/internal/vendor/\n/docs/**/README*\n*.go\n!secret*\n"
	paths := []string{
		"internal/git/sparse_patterns.go",
		"internal/vendor/pkg/nested/file.go",
		"docs/a/b/c/README.md",
		"other/deep/nested/path/file.txt",
		"café/nested/UPPER/file.go",
	}
	for _, ignoreCase := range []bool{false, true} {
		name := "case-sensitive"
		if ignoreCase {
			name = "ignore-case"
		}
		rules, err := parseSparsePatterns(patterns, ignoreCase)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(name, func(b *testing.B) {
			b.ResetTimer()
			for b.Loop() {
				for _, path := range paths {
					_ = rules.includes(path)
				}
			}
		})
	}
}

func TestSparseStatusMissingAndStaged(t *testing.T) {
	t.Parallel()
	r, _, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
	before := readFile(t, index)
	if changes, err := r.Status(false); err != nil || len(changes) != 0 {
		t.Fatalf("clean sparse status: %+v %v", changes, err)
	}
	if !bytes.Equal(before, readFile(t, index)) {
		t.Fatal("status rewrote index")
	}
	entries, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.WriteBlob([]byte("staged"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range entries {
		if entries[i].Path == "omit/file" {
			entries[i].ID = id
		}
	}
	sparseIndexFixture(t, r, entries)
	changes, err := r.Status(false)
	if err != nil || len(changes) != 1 || changes[0].Path != "omit/file" || changes[0].Status != IndexModified {
		t.Fatalf("staged sparse status: %+v %v (base %s)", changes, err, base)
	}
}

func TestSparseCheckoutPreservesOmissionsFlagsAndUnrelatedState(t *testing.T) {
	t.Parallel()
	for _, metadata := range []bool{false, true} {
		t.Run(map[bool]string{false: "implementation", true: "metadata"}[metadata], func(t *testing.T) {
			t.Parallel()
			r, root, base, _ := setupSparse(t, "/*\n!/*/\n/keep/\n")
			entries, err := r.IndexEntries()
			if err != nil {
				t.Fatal(err)
			}
			staged, err := r.WriteBlob([]byte("staged"))
			if err != nil {
				t.Fatal(err)
			}
			for i := range entries {
				if entries[i].Path == "unrelated" {
					entries[i].ID = staged
				}
			}
			sparseIndexFixture(t, r, entries)
			writeFile(t, filepath.Join(root, "unrelated"), []byte("unstaged"))
			writeFile(t, filepath.Join(root, "untracked"), []byte("precious"))
			writeFile(t, filepath.Join(root, ".gitignore"), []byte("ignored\n"))
			writeFile(t, filepath.Join(root, "ignored"), []byte("ignored precious"))
			next := sparseCommit(t, r, base, map[string]string{"keep/file": "next", "keep/new": "new", "omit/file": "next", "omit/new": "new", "newdir/file": "new"})
			if metadata {
				err = r.CheckoutMetadata(base, next)
			} else {
				err = r.Checkout(base, next)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"omit/file", "omit/new", "newdir/file"} {
				if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
					t.Fatalf("materialized %s: %v", path, err)
				}
			}
			for path, want := range map[string]string{"keep/file": "next", "keep/new": "new", "unrelated": "unstaged", "untracked": "precious", "ignored": "ignored precious"} {
				if got := string(readFile(t, filepath.Join(root, path))); got != want {
					t.Fatalf("%s: %q", path, got)
				}
			}
			after, err := r.IndexEntries()
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range after {
				if entry.Path == "assumed" && !entry.AssumeValid {
					t.Fatal("lost assume-valid")
				}
				if (strings.HasPrefix(entry.Path, "omit/") || strings.HasPrefix(entry.Path, "newdir/")) && !entry.SkipWorktree {
					t.Fatalf("lost omission: %+v", entry)
				}
				if entry.Path == "unrelated" && entry.ID != staged {
					t.Fatal("lost staging")
				}
			}
		})
	}
}

func TestSparseCheckoutRenamesAcrossSelectionAndDeletes(t *testing.T) {
	t.Parallel()
	r, root, base, _ := setupSparse(t, "/keep/\n/assumed\n/unrelated\n")
	next := sparseCommit(t, r, base, map[string]string{"keep/from-omit": "base", "omit/from-keep": "base"}, "keep/file", "omit/file")
	if err := r.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"keep/file", "omit/file", "omit/from-keep"} {
		if _, err := os.Lstat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", path, err)
		}
	}
	if got := string(readFile(t, filepath.Join(root, "keep/from-omit"))); got != "base" {
		t.Fatal(got)
	}
	entries, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Path == "keep/file" || entry.Path == "omit/file" {
			t.Fatalf("retained deleted entry: %+v", entry)
		}
	}
}

func TestSparseCheckoutProtectsDirtyIgnoredAndStagedPaths(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"dirty", "ignored", "staged", "present-skipped"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
			path := "keep/file"
			if kind == "ignored" {
				path = "keep/new"
				writeFile(t, filepath.Join(root, ".gitignore"), []byte("keep/new\n"))
			}
			if kind == "present-skipped" {
				path = "omit/file"
			}
			writeFile(t, filepath.Join(root, path), []byte("precious"))
			if kind == "staged" {
				entries, err := r.IndexEntries()
				if err != nil {
					t.Fatal(err)
				}
				id, err := r.WriteBlob([]byte("staged"))
				if err != nil {
					t.Fatal(err)
				}
				for i := range entries {
					if entries[i].Path == path {
						entries[i].ID = id
					}
				}
				sparseIndexFixture(t, r, entries)
			}
			before := readFile(t, index)
			next := sparseCommit(t, r, base, map[string]string{path: "replacement"})
			if err := r.Checkout(base, next); err == nil {
				t.Fatal("overwrote protected path")
			}
			if !bytes.Equal(before, readFile(t, index)) || string(readFile(t, filepath.Join(root, path))) != "precious" {
				t.Fatal("failed checkout changed state")
			}
		})
	}
}

func TestSparsePerFileFlagsWithoutSparseConfiguration(t *testing.T) {
	t.Parallel()
	r, root, base, _ := setupSparse(t, "")
	next := sparseCommit(t, r, base, map[string]string{"omit/file": "next", "newdir/file": "new"})
	if err := r.Checkout(base, next); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "omit/file")); !os.IsNotExist(err) {
		t.Fatalf("materialized skipped path: %v", err)
	}
	if string(readFile(t, filepath.Join(root, "newdir/file"))) != "new" {
		t.Fatal("per-file flags incorrectly excluded new path")
	}
}

func TestSparseMergePreservesOmissions(t *testing.T) {
	t.Parallel()
	r, root, base, index := setupSparse(t, "/*\n!/*/\n/keep/\n")
	tip := sparseCommit(t, r, base, map[string]string{"omit/file": "theirs"})
	merged, err := r.MergeIntoIndex(tip)
	if err != nil || merged == (ID{}) {
		t.Fatalf("sparse merge: %s %v", merged, err)
	}
	after, err := r.IndexEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range after {
		if entry.Path == "omit/file" && !entry.SkipWorktree {
			t.Fatal("lost skip-worktree")
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(index), "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root, "omit/file")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("merge guard changed %s: %v", path, err)
		}
	}
}
