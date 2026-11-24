package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestConfigurationSharedParseHandlesAndContexts(t *testing.T) {
	repo, root := fixture(t, false)
	include := filepath.Join(t.TempDir(), "ordinary")
	writeCommandInclude(t, include, "[tasktool]\nvalue = ordinary\n[includeIf \"onbranch:tasks\"]\npath = future\n")
	writeCommandInclude(t, filepath.Join(filepath.Dir(include), "future"), "[tasktool]\nfuture = prospective\n")
	if err := repo.SetLocalConfig("include.path", include); err != nil {
		t.Fatal(err)
	}
	// Warm both current and prospective file parses before opening new handles.
	if _, err := repo.Config("tasktool.value"); err != nil {
		t.Fatal(err)
	}
	future := func(r *Repository) {
		t.Helper()
		if err := r.WithProspectiveWorktree("child", filepath.Join(t.TempDir(), "child"), "refs/heads/tasks", func(view *Repository) error {
			if got, err := view.Config("tasktool.future"); err != nil || got != "prospective" {
				t.Fatalf("prospective: %q %v", got, err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	future(repo)
	command := filepath.Join(t.TempDir(), "command")
	writeCommandInclude(t, command, "[tasktool]\ncommand = first\n")
	parseCacheLoad(t, &configurationParseCache{shared: &sharedConfigurationParses}, command)
	injectConfiguration(t, [2]string{"include.path", command}, [2]string{"tasktool.value", "first-command"})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	writeCommandInclude(t, command, "[tasktool]\ncommand = second\n")
	injectConfiguration(t, [2]string{"include.path", command}, [2]string{"tasktool.value", "second-command"})
	second, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for i, r := range []*Repository{first, second} {
		want := []string{"first", "second"}[i]
		for key, expected := range map[string]string{"tasktool.value": want + "-command", "tasktool.command": want} {
			if got, err := r.Config(key); err != nil || got != expected {
				t.Fatalf("handle %d %s: %q %v", i, key, got, err)
			}
		}
		if _, err := r.Config("tasktool.future"); !HasCode(err, NotFound) {
			t.Fatal("current context inherited prospective condition", err)
		}
		future(r)
		if n := parseCacheCount(&r.configuration.parsedFiles); n != 0 {
			t.Fatalf("new handle reparsed warmed ordinary files: %d", n)
		}
	}
	first.Close()
	if got, err := second.Config("tasktool.command"); err != nil || got != "second" {
		t.Fatal("closing first handle invalidated second", got, err)
	}
}

func TestConfigurationSharedParseFreshIdentityBytesAndErrors(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var pool configurationParseCache
	fresh := func() *configurationParseCache { return &configurationParseCache{shared: &pool} }
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	missing := newConfigurationFileCapture(fresh())
	if got, err := missing.loadRecords(path); err != nil || len(got) != 0 {
		t.Fatal("missing file capture", got, err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = first\n")
	if got, err := missing.loadRecords(path); err != nil || len(got) != 0 {
		t.Fatal("missing operation capture changed", got, err)
	}
	original := parseCacheLoad(t, fresh(), path)
	t.Run("hit-without-parser-input", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(dir, "absent"))
		cache := fresh()
		if got := parseCacheLoad(t, cache, path); !reflect.DeepEqual(got, original) || parseCacheCount(cache) != 0 {
			t.Fatal("fresh handle did not reuse the parse")
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = other\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	cache := fresh()
	if got := parseCacheLoad(t, cache, path); *got[0].value != "other" || parseCacheCount(cache) != 1 {
		t.Fatal("same-size same-mtime edit reused old bytes")
	}
	replacement := filepath.Join(dir, "replacement")
	writeCommandInclude(t, replacement, "[tasktool]\nvalue = other\n")
	if err := os.Chtimes(replacement, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	cache = fresh()
	parseCacheLoad(t, cache, path)
	if parseCacheCount(cache) != 1 {
		t.Fatal("same-byte replacement identity was ignored")
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	cache = fresh()
	if got := parseCacheLoad(t, cache, alias); got[0].origin != alias || parseCacheCount(cache) != 1 {
		t.Fatal("lexical alias reused another origin")
	}
	writeCommandInclude(t, replacement, "[tasktool]\nvalue = other\n")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, alias); err != nil {
		t.Fatal(err)
	}
	cache = fresh()
	parseCacheLoad(t, cache, alias)
	if parseCacheCount(cache) != 1 {
		t.Fatal("retargeted symlink reused previous identity")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := parseCacheLoad(t, fresh(), path); len(got) != 0 {
		t.Fatal("removed file reused pooled records")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := newConfigurationFileCapture(fresh()).loadRecords(path); err == nil || err.Error() != "cannot read ordinary Git configuration" {
		t.Fatal("read error reused pooled records", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = restored\n")
	parseCacheLoad(t, fresh(), path)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if _, err := newConfigurationFileCapture(fresh()).loadRecords(path); err == nil {
			t.Fatal("unreadable file reused pooled records")
		}
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if got := parseCacheLoad(t, fresh(), path); *got[0].value != "restored" {
		t.Fatal("readable-again file not freshly captured")
	}
	writeCommandInclude(t, path, "[secret-malformed")
	for range 2 {
		cache := fresh()
		if _, err := newConfigurationFileCapture(cache).loadRecords(path); err == nil || strings.Contains(err.Error(), "secret") || parseCacheCount(cache) != 1 {
			t.Fatal("parse error cached, bypassed or unredacted", err)
		}
	}
	t.Run("failed-parser-input-not-cached", func(t *testing.T) {
		writeCommandInclude(t, path, "[tasktool]\nvalue = recovered\n")
		t.Setenv("TMPDIR", filepath.Join(dir, "absent"))
		for range 2 {
			cache := fresh()
			if _, err := newConfigurationFileCapture(cache).loadRecords(path); err == nil || parseCacheCount(cache) != 1 {
				t.Fatal("parser input failure was cached or bypassed", err)
			}
		}
	})
	if got := parseCacheLoad(t, fresh(), path); *got[0].value != "recovered" {
		t.Fatal("failed parse poisoned recovery")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := newConfigurationFileCapture(fresh()).loadRecords(alias); err == nil || err.Error() != "cannot resolve ordinary Git configuration" {
		t.Fatal("identity resolution error bypassed", err)
	}
}

func TestConfigurationSharedParseMutationBoundsAndLifetime(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var pool configurationParseCache
	first := &configurationParseCache{shared: &pool}
	dir := t.TempDir()
	path := filepath.Join(dir, "first")
	writeCommandInclude(t, path, "[tasktool]\nvalue = original\nimplicit\nempty =\nvalue = duplicate\n")
	owned := parseCacheLoad(t, first, path)
	borrowed, err := newConfigurationFileCapture(first).borrowRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	*owned[0].value, owned[0].origin, owned[0].level = "mutated", "wrong", 999
	owned[0].conditionalURL, owned[0].depth = true, 100
	second := &configurationParseCache{shared: &pool}
	got := parseCacheLoad(t, second, path)
	if *got[0].value != "original" || got[0].origin != path || got[0].level == 999 || got[0].conditionalURL || got[0].depth != 0 || got[1].value != nil || *got[2].value != "" || *got[3].value != "duplicate" {
		t.Fatal("owned output contaminated shared parse")
	}
	checkBounds := func() {
		t.Helper()
		pool.mu.Lock()
		defer pool.mu.Unlock()
		size := 0
		for _, file := range pool.entries {
			size += file.size
		}
		if len(pool.entries) > configurationParseCacheEntries || size != pool.bytes || size > configurationParseCacheBytes {
			t.Fatal("pool exceeded entry/byte accounting bounds")
		}
	}
	for i := 0; i < configurationParseCacheEntries; i++ {
		name := filepath.Join(dir, fmt.Sprint(i))
		writeCommandInclude(t, name, "[tasktool]\nvalue = bounded\n")
		parseCacheLoad(t, &configurationParseCache{shared: &pool}, name)
		checkBounds()
	}
	if _, ok := pool.entries[path]; ok {
		t.Fatal("entry limit did not evict initial file")
	}
	count := parseCacheCount(second)
	parseCacheLoad(t, second, path)
	if parseCacheCount(second) != count {
		t.Fatal("pool eviction invalidated handle-local retention")
	}
	first.clear()
	second.clear()
	if *borrowed[0].value != "original" || borrowed[0].origin != path {
		t.Fatal("eviction/close invalidated borrowed records")
	}
	for i := 0; i < 4; i++ {
		name := filepath.Join(dir, fmt.Sprintf("bytes-%d", i))
		writeCommandInclude(t, name, "#"+strings.Repeat("x", configurationParseCacheBytes/3)+"\n[tasktool]\nvalue = bytes\n")
		parseCacheLoad(t, &configurationParseCache{shared: &pool}, name)
		checkBounds()
	}
	if _, ok := pool.entries[filepath.Join(dir, "bytes-0")]; ok {
		t.Fatal("byte limit did not evict earlier files")
	}
	large := filepath.Join(dir, "large")
	writeCommandInclude(t, large, "#"+strings.Repeat("x", configurationParseCacheBytes)+"\n[tasktool]\nvalue = large\n")
	for range 2 {
		cache := &configurationParseCache{shared: &pool}
		if got := parseCacheLoad(t, cache, large); *got[0].value != "large" || parseCacheCount(cache) != 1 {
			t.Fatal("oversized input was reused or changed")
		}
		if len(cache.entries) != 0 {
			t.Fatal("oversized input retained locally")
		}
		checkBounds()
	}
	if _, ok := pool.entries[large]; ok {
		t.Fatal("oversized input retained globally")
	}
	// A caller-owned capture buffer must not become a mutable exact-byte witness.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := configurationIncludeIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	pool.clear()
	if _, err := pool.records(path, identity, configurationFileContents{data: data, info: info}); err != nil {
		t.Fatal(err)
	}
	copy(data, bytes.Repeat([]byte{'x'}, len(data)))
	cache := &configurationParseCache{shared: &pool}
	if got := parseCacheLoad(t, cache, path); *got[0].value != "original" || parseCacheCount(cache) != 0 {
		t.Fatal("caller modified retained byte witness")
	}
}

func TestConfigurationSharedParseConcurrentHandles(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var pool configurationParseCache
	path := filepath.Join(t.TempDir(), "config")
	writeCommandInclude(t, path, "[tasktool]\nvalue = original\n")
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			cache := &configurationParseCache{shared: &pool}
			defer cache.clear()
			for range 4 {
				records, err := newConfigurationFileCapture(cache).loadRecords(path)
				if err != nil || len(records) != 1 || records[0].value == nil || *records[0].value != "original" {
					t.Error("concurrent handle changed records", err)
					return
				}
				*records[0].value = "private"
			}
		}()
	}
	workers.Wait()
	if parseCacheCount(&pool) != 1 {
		t.Fatal("concurrent handles did not coalesce one parse")
	}
}

func TestConfigurationSharedParseOwnershipRechecked(t *testing.T) {
	const child = "TASKTOOL_SHARED_PARSE_POLICY_CHILD"
	if os.Getenv(child) != "1" {
		home := t.TempDir()
		writeCommandInclude(t, filepath.Join(home, ".gitconfig"), "[tasktool]\nvalue = global\n")
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
		t.Setenv(child, "1")
		injectConfiguration(t)
		t.Setenv("GIT_CONFIG_PARAMETERS", "")
		command := exec.Command(os.Args[0], "-test.run=^TestConfigurationSharedParseOwnershipRechecked$")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("shared policy subprocess: %v\n%s", err, output)
		}
		return
	}
	_, root := fixture(t, false)
	global := filepath.Join(os.Getenv("HOME"), ".gitconfig")
	leaf := filepath.Join(os.Getenv("HOME"), "policy")
	writeCommandInclude(t, leaf, "[safe]\ndirectory =\n")
	for _, condition := range []string{"hasconfig:remote.*.url:never", "onbranch:main"} {
		text := "[includeIf \"" + condition + "\"]\npath = policy\n"
		if condition == "onbranch:main" {
			writeCommandInclude(t, filepath.Join(os.Getenv("HOME"), "nested"), "[includeIf \"hasconfig:remote.*.url:never\"]\npath = policy\n")
			text = "[includeIf \"onbranch:main\"]\npath = nested\n"
		}
		writeCommandInclude(t, global, text)
		// Warm pure parses without performing admission or condition evaluation.
		for _, path := range []string{global, leaf} {
			parseCacheLoad(t, &configurationParseCache{shared: &sharedConfigurationParses}, path)
		}
		for range 2 {
			opened, err := Open(root)
			if opened != nil {
				opened.Close()
			}
			if err == nil || err.Error() != conditionalOwnershipPolicyError().Error() {
				t.Fatal("warm parses bypassed conditional global policy rejection", err)
			}
		}
	}
	writeCommandInclude(t, global, "[tasktool]\nvalue = global\n")
	injectConfiguration(t, [2]string{"include.path", leaf})
	if opened, err := Open(root); err == nil {
		opened.Close()
		t.Fatal("ordinary parse reused as command ownership admission")
	} else if err.Error() != commandOwnershipPolicyError().Error() {
		t.Fatal("wrong command policy error", err)
	}
}

func TestConfigurationSharedParseFalseClosureRechecked(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var pool configurationParseCache
	dir := t.TempDir()
	root, leaf := filepath.Join(dir, "root"), filepath.Join(dir, "leaf")
	writeCommandInclude(t, root, "[includeIf \"hasconfig:remote.*.url:never\"]\npath = leaf\n")
	writeCommandInclude(t, leaf, "[remote \"forbidden\"]\nurl = https://secret/repo\n")
	for _, path := range []string{root, leaf} {
		parseCacheLoad(t, &configurationParseCache{shared: &pool}, path)
	}
	for range 2 {
		cache := &configurationParseCache{shared: &pool}
		capture := newConfigurationFileCapture(cache)
		records, err := capture.borrowRecords(root)
		if err != nil {
			t.Fatal(err)
		}
		_, err = (&hasconfigEvaluation{}).evaluate([]hasconfigSource{{records: records, load: capture.borrowRecords}})
		if err == nil || err.Error() != "remote URLs cannot be configured directly or indirectly inside hasconfig includes" || parseCacheCount(cache) != 0 {
			t.Fatal("warm parse bypassed false-closure validation or reparsed", err)
		}
	}
}

func TestConfigurationExpandPathPlainBytesAndTilde(t *testing.T) {
	t.Parallel()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", ".", "../relative/./path", "/absolute//path/../file", "line\nbreak\r\t", "middle~user/path", "./~/path", "\\plain", "\xffbytes"} {
		if got, err := configurationExpandPath(path); err != nil || got != path {
			t.Fatalf("plain path %q changed: %q %v", path, got, err)
		}
	}
	for _, path := range []string{"\x00", "plain\x00path", "~/\x00path", "~user\x00"} {
		if _, err := configurationExpandPath(path); err == nil || err.Error() != "Git argument contains NUL" {
			t.Fatalf("NUL path %q: %v", path, err)
		}
	}
	home, err := configurationExpandPath("~")
	if err != nil || !filepath.IsAbs(home) {
		t.Fatal("native home expansion failed", home, err)
	}
	if got, err := configurationExpandPath("~/file"); err != nil || got != filepath.Join(home, "file") {
		t.Fatal("native home-relative expansion changed", got, err)
	}
	for _, path := range []string{"~user", "~user/file", "~~", "~\\file"} {
		if _, err := configurationExpandPath(path); err == nil || !strings.Contains(err.Error(), "retrieving a homedir by name is not supported") {
			t.Fatalf("unsupported tilde path %q: %v", path, err)
		}
	}
}
