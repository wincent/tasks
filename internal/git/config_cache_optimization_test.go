package git

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestConfigurationEffectiveCacheInvalidation(t *testing.T) {
	injectConfiguration(t)
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	repo, _ := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	top := filepath.Join(dir, "top")
	alias := filepath.Join(dir, "alias")
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	optional := filepath.Join(dir, "optional")
	branchLeaf := filepath.Join(dir, "branch-leaf")

	writeCommandInclude(t, first, "[tasktool]\nvalue = first\nimplicit\nempty =\n")
	writeCommandInclude(t, second, "[tasktool]\nvalue = second\n")
	writeCommandInclude(t, branchLeaf, "[tasktool]\nbranch = main-only\n")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, top, "[include]\npath = alias\n[include]\npath = optional\n[includeIf \"onbranch:main\"]\npath = branch-leaf\n")
	if err := repo.SetLocalConfig("include.path", top); err != nil {
		t.Fatal(err)
	}

	var work configurationWork
	repo.configuration.work = &work
	defer func() { repo.configuration.work = nil }()

	// First call after SetLocalConfig rebuilds once; repeated calls reuse cache.
	if got, err := repo.Config("tasktool.value"); err != nil || got != "first" {
		t.Fatalf("initial value: %q %v", got, err)
	}
	if work.assemblies != 1 || work.evaluations != 1 {
		t.Fatalf("expected 1 initial assembly/evaluation, got %+v", work)
	}
	for range 3 {
		if got, err := repo.Config("tasktool.value"); err != nil || got != "first" {
			t.Fatalf("cached value: %q %v", got, err)
		}
		if got, err := configBool(repo, "tasktool.implicit"); err != nil || !got {
			t.Fatalf("cached implicit bool: %v %v", got, err)
		}
		if got, err := repo.Config("tasktool.empty"); err != nil || got != "" {
			t.Fatalf("cached empty value: %q %v", got, err)
		}
		if got, err := repo.Config("tasktool.branch"); err != nil || got != "main-only" {
			t.Fatalf("cached branch value: %q %v", got, err)
		}
	}
	if work.assemblies != 1 || work.evaluations != 1 {
		t.Fatalf("repeated unchanged calls rebuilt config: %+v", work)
	}

	// Same-size, same-mtime edit invalidates cache.
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, first, "[tasktool]\nvalue = other\nimplicit\nempty =\n")
	if err := os.Chtimes(first, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Config("tasktool.value"); err != nil || got != "other" {
		t.Fatalf("same-mtime edit missed: %q %v", got, err)
	}
	if work.assemblies != 2 {
		t.Fatalf("same-mtime edit did not rebuild: %+v", work)
	}

	// Same-bytes inode replacement invalidates cache.
	replacement := filepath.Join(dir, "replacement")
	writeCommandInclude(t, replacement, "[tasktool]\nvalue = other\nimplicit\nempty =\n")
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, first); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Config("tasktool.value"); err != nil || got != "other" {
		t.Fatalf("inode replacement value: %q %v", got, err)
	}
	if work.assemblies != 3 {
		t.Fatalf("inode replacement did not invalidate cache: %+v", work)
	}

	// Symlink retargeting invalidates cache.
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Config("tasktool.value"); err != nil || got != "second" {
		t.Fatalf("retargeted symlink value: %q %v", got, err)
	}
	if work.assemblies != 4 {
		t.Fatalf("retargeted symlink did not invalidate cache: %+v", work)
	}

	// Previously missing include file appearing invalidates cache.
	writeCommandInclude(t, optional, "[tasktool]\noptional = appeared\n")
	if got, err := repo.Config("tasktool.optional"); err != nil || got != "appeared" {
		t.Fatalf("appeared optional include: %q %v", got, err)
	}
	if work.assemblies != 5 {
		t.Fatalf("appeared optional include did not invalidate cache: %+v", work)
	}

	// Deleting an existing include file invalidates cache.
	if err := os.Remove(optional); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Config("tasktool.optional"); !HasCode(err, NotFound) {
		t.Fatalf("deleted optional include remained visible: %v", err)
	}
	if work.assemblies != 6 {
		t.Fatalf("deleted optional include did not invalidate cache: %+v", work)
	}

	// Branch change invalidates cache, and an inactive branch's malformed file
	// must not fail validation once HEAD switched away.
	writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/heads/topic\n")
	writeCommandInclude(t, branchLeaf, "[malformed")
	if _, err := repo.Config("tasktool.branch"); !HasCode(err, NotFound) {
		t.Fatalf("branch switch retained old branch include or failed on inactive file: %v", err)
	}
	if work.assemblies != 7 {
		t.Fatalf("branch switch did not invalidate cache: %+v", work)
	}

	// Switching back onto main sees the malformed file and invalidates cache.
	writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/heads/main\n")
	if _, err := repo.Config("tasktool.branch"); err == nil {
		t.Fatal("malformed active include was bypassed by cache")
	}
	if repo.configuration.cached != nil {
		t.Fatal("failed apply retained stale cached config")
	}
	writeCommandInclude(t, branchLeaf, "[tasktool]\nbranch = recovered\n")
	if got, err := repo.Config("tasktool.branch"); err != nil || got != "recovered" {
		t.Fatalf("recovery after malformed include: %q %v", got, err)
	}

	// Unreadable active file invalidates cache and fails cleanly.
	if os.Geteuid() != 0 {
		if err := os.Chmod(branchLeaf, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Config("tasktool.branch"); err == nil {
			t.Fatal("unreadable active include was bypassed by cache")
		}
		if err := os.Chmod(branchLeaf, 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := repo.Config("tasktool.branch"); err != nil || got != "recovered" {
			t.Fatalf("recovery after chmod: %q %v", got, err)
		}
	}
}

func TestConfigurationBatchedNativeOverlaySnapshotsAndEntries(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Empty record set succeeds cleanly.
	empty, err := configurationNativeRecords(nil)
	if err != nil || empty == nil {
		t.Fatalf("empty native records: %v %v", empty, err)
	}
	// Verify empty overlay can be freed safely.
	repo, _ := fixture(t, false)
	defer repo.Close()
	if err := repo.SetLocalConfig("tasktool.dup", "first"); err != nil {
		t.Fatal(err)
	}
	// Exercise duplicate keys, valueless entries, empty values, and snapshots
	// across repeated ConfigurationEntries / Config calls.
	dir := t.TempDir()
	inc := filepath.Join(dir, "inc")
	writeCommandInclude(t, inc, "[tasktool]\ndup = second\nimplicit\nempty =\n")
	if err := repo.SetLocalConfig("include.path", inc); err != nil {
		t.Fatal(err)
	}
	entries, err := repo.ConfigurationEntries()
	if err != nil {
		t.Fatal(err)
	}
	var seen []ConfigurationEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, "tasktool.") {
			seen = append(seen, entry)
		}
	}
	want := []ConfigurationEntry{
		{Name: "tasktool.dup", Value: "first", HasValue: true},
		{Name: "tasktool.dup", Value: "second", HasValue: true},
		{Name: "tasktool.implicit", Value: "", HasValue: false},
		{Name: "tasktool.empty", Value: "", HasValue: true},
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("entries = %#v, want %#v", seen, want)
	}
	if got, err := repo.Config("tasktool.dup"); err != nil || got != "second" {
		t.Fatalf("last duplicate key did not win: %q %v", got, err)
	}
	if got, err := configBool(repo, "tasktool.implicit"); err != nil || !got {
		t.Fatalf("implicit bool: %v %v", got, err)
	}
	if got, err := configBool(repo, "tasktool.empty"); err != nil || got {
		t.Fatalf("empty bool: %v %v", got, err)
	}
	// Ensure empty native config can be freed via helper without leaking.
	tmp := &environmentConfiguration{cached: &cachedEffectiveConfig{config: empty}}
	tmp.close()
}

func TestPureODBOperationsSkipConfigurationRefresh(t *testing.T) {
	t.Parallel()
	repo, _ := fixture(t, false)
	var work configurationWork
	repo.configuration.work = &work
	defer func() { repo.configuration.work = nil }()

	blobA, err := repo.WriteBlob([]byte("alpha\n"))
	if err != nil {
		t.Fatal(err)
	}
	blobB, err := repo.WriteBlob([]byte("beta\n"))
	if err != nil {
		t.Fatal(err)
	}
	subTree, err := repo.WriteTree([]TreeEntry{{Name: "a.txt", ID: blobA, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	rootTree, err := repo.WriteTree([]TreeEntry{
		{Name: "dir", ID: subTree, Mode: 0040000},
		{Name: "b.txt", ID: blobB, Mode: 0100644},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.WriteCommit(rootTree, nil, testSignature, testSignature, "first\n")
	if err != nil {
		t.Fatal(err)
	}
	editedTree, err := repo.EditTree(first, []TreeEdit{{Path: "dir/c.txt", ID: blobB, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.WriteCommit(editedTree, []ID{first}, testSignature, testSignature, "second\n")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := repo.Tree(second); err != nil || len(got) != 2 {
		t.Fatalf("Tree: %+v %v", got, err)
	}
	if got, err := repo.TreeAt(second, "dir"); err != nil || len(got) != 2 {
		t.Fatalf("TreeAt: %+v %v", got, err)
	}
	if _, err := repo.TreeAt(second, "b.txt"); err == nil || !strings.Contains(err.Error(), "expected a directory") {
		t.Fatalf("TreeAt non-directory: %v", err)
	}
	if _, err := repo.TreeAt(second, "../escape"); err == nil {
		t.Fatal("TreeAt accepted unsafe path")
	}
	if got, err := repo.Entry(second, "dir/c.txt"); err != nil || got.ID != blobB {
		t.Fatalf("Entry: %+v %v", got, err)
	}
	if got, err := repo.Blob(blobA); err != nil || !bytes.Equal(got, []byte("alpha\n")) {
		t.Fatalf("Blob: %q %v", got, err)
	}
	if got, err := repo.IsAncestor(first, second); err != nil || !got {
		t.Fatalf("IsAncestor: %v %v", got, err)
	}
	if got, err := repo.MergeBases(first, second); err != nil || !reflect.DeepEqual(got, []ID{first}) {
		t.Fatalf("MergeBases: %+v %v", got, err)
	}
	if got, err := repo.HistoryPaths([]ID{second}, "dir", 1); err != nil || len(got) == 0 {
		t.Fatalf("HistoryPaths: %+v %v", got, err)
	}
	payload, err := repo.CommitBuffer(editedTree, []ID{first}, testSignature, testSignature, "signed\n")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := repo.WriteSignedCommit(payload, []byte("-----BEGIN TEST-----\nabc\n-----END TEST-----\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.ExtractCommitSignature(signed); err != nil {
		t.Fatal(err)
	}
	if work.captures != 0 || work.reads != 0 || work.evaluations != 0 || work.assemblies != 0 || work.paths != 0 {
		t.Fatalf("pure ODB operations triggered configuration work: %+v", work)
	}
}

func TestValidateIndexSingleApply(t *testing.T) {
	t.Parallel()
	repo, _ := fixture(t, false)
	first := commit(t, repo, "first")
	publish(t, repo, "refs/heads/main", first, ID{})

	var baseline configurationWork
	repo.configuration.work = &baseline
	if _, err := repo.Paths(); err != nil {
		t.Fatal(err)
	}
	oneApplyCaptures := baseline.captures
	if oneApplyCaptures == 0 {
		t.Fatal("expected nonzero baseline captures")
	}

	checkSingle := func(name string, op func() error) {
		t.Helper()
		var work configurationWork
		repo.configuration.work = &work
		if err := op(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if work.paths != 0 || work.captures != oneApplyCaptures {
			t.Fatalf("%s: expected 1 apply (%d captures, 0 paths), got %+v", name, oneApplyCaptures, work)
		}
	}

	checkSingle("Status", func() error {
		_, err := repo.Status(false)
		return err
	})
	checkSingle("IndexEntries", func() error {
		_, err := repo.IndexEntries()
		return err
	})
	checkSingle("ValidateWorktreePaths", func() error {
		return repo.ValidateWorktreePaths([]string{"content.txt"})
	})
	checkSingle("ValidateWorktreeContents", func() error {
		return repo.ValidateWorktreeContents(map[string][]byte{"content.txt": []byte("hello\n")})
	})
	checkSingle("IndexTree", func() error {
		_, err := repo.IndexTree()
		return err
	})
	isolated := filepath.Join(t.TempDir(), "isolated.index")
	checkSingle("EditIndexFile", func() error {
		return repo.EditIndexFile(isolated, first, nil)
	})
	repo.configuration.work = nil
}

func TestConfigurationWildmatchCacheAndEdgeCases(t *testing.T) {
	cases := []struct {
		pattern string
		subject string
		fold    bool
		want    bool
	}{
		{"**/repo/**", "/a/b/repo/c/d", false, true},
		{"**/REPO/**", "/a/b/repo/c/d", true, true},
		{"**/REPO/**", "/a/b/repo/c/d", false, false},
		{"a?c", "abc", false, true},
		{"a?c", "a/c", false, false},
		{"prefix\\*", "prefix*", false, true},
		{"trailing\\", "trailing\\", false, false},
		{"[unclosed", "a", false, false},
		{"[a-z]", "m", false, true},
		{"raw/\xff/*", "raw/\xff/file", false, true},
		{"raw/\xff/*", "raw/\xfe/file", false, false},
	}
	for range 2 {
		for _, tc := range cases {
			got, err := configurationWildmatch(tc.pattern, tc.subject, tc.fold)
			if err != nil || got != tc.want {
				t.Fatalf("wildmatch(%q, %q, %v) = %v, %v; want %v", tc.pattern, tc.subject, tc.fold, got, err, tc.want)
			}
		}
	}
	if matched, err := hasconfigMatches("https://host/**", nil); err != nil || matched {
		t.Fatalf("hasconfigMatches empty urls: %v %v", matched, err)
	}
	if matched, err := hasconfigMatches("https://host/**", []string{"https://other/repo", "https://host/repo"}); err != nil || !matched {
		t.Fatalf("hasconfigMatches multi urls: %v %v", matched, err)
	}
	// Oversized patterns are not cached but still match accurately.
	huge := strings.Repeat("a", configurationWildmatchCacheMaxPattern+10)
	if got, err := configurationWildmatch(huge, huge, false); err != nil || !got {
		t.Fatalf("oversized pattern: %v %v", got, err)
	}
	// Concurrent matching and cache bound eviction remain race-free.
	var workers sync.WaitGroup
	for g := range 8 {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for i := range configurationWildmatchCacheEntries/4 + 16 {
				pattern := fmt.Sprintf("**/group-%d/item-%d/**", id, i)
				subject := fmt.Sprintf("/root/group-%d/item-%d/file", id, i)
				if got, err := configurationWildmatch(pattern, subject, i%2 == 0); err != nil || !got {
					t.Errorf("concurrent wildmatch %q: %v %v", pattern, got, err)
					return
				}
			}
		}(g)
	}
	workers.Wait()
	configurationWildmatchCache.mu.RLock()
	size := len(configurationWildmatchCache.entries)
	configurationWildmatchCache.mu.RUnlock()
	if size > configurationWildmatchCacheEntries {
		t.Fatalf("wildmatch cache exceeded bound: %d > %d", size, configurationWildmatchCacheEntries)
	}
}
