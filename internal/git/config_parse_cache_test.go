package git

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func parseCacheLoad(t *testing.T, cache *configurationParseCache, path string) []hasconfigRecord {
	t.Helper()
	records, err := newConfigurationFileCapture(cache).loadRecords(path)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func parseCacheCount(cache *configurationParseCache) uint64 {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.parses
}

func TestConfigurationParseCacheReuseAndFreshBytes(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var cache configurationParseCache
	path := filepath.Join(t.TempDir(), "config")
	writeCommandInclude(t, path, "[tasktool]\nvalue = first\nimplicit\nempty =\nvalue = again\n")
	before := parseCacheLoad(t, &cache, path)
	if len(before) != 4 || before[1].value != nil || before[2].value == nil || *before[2].value != "" || before[0].origin != path || before[3].name != before[0].name || before[3].value == nil || *before[3].value != "again" {
		t.Fatalf("lost duplicate/nil/empty/origin records: %#v", before)
	}
	if got := parseCacheLoad(t, &cache, path); !reflect.DeepEqual(got, before) || parseCacheCount(&cache) != 1 {
		t.Fatalf("unchanged file reparsed or changed: %#v, parses=%d", got, parseCacheCount(&cache))
	}
	before[0].level, before[0].depth, before[0].conditionalURL = 999, 10, true
	*before[0].value = "corrupted"
	fresh := parseCacheLoad(t, &cache, path)
	if *fresh[0].value != "first" || fresh[0].level == 999 || fresh[0].depth != 0 || fresh[0].conditionalURL {
		t.Fatal("caller modified cached records")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = other\nimplicit\nempty =\nvalue = again\n")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if got := parseCacheLoad(t, &cache, path); *got[0].value != "other" || parseCacheCount(&cache) != 2 {
		t.Fatal("same-size, same-mtime edit was not reparsed")
	}
	// Hits need no parser input, but input-creation failures on misses must
	// invalidate previous success rather than silently reuse stale records.
	t.Run("unavailable-temporary-parser-input", func(t *testing.T) {
		t.Setenv("TMPDIR", filepath.Join(filepath.Dir(path), "nonexistent"))
		if got := parseCacheLoad(t, &cache, path); *got[0].value != "other" || parseCacheCount(&cache) != 2 {
			t.Fatal("cache hit attempted parsing")
		}
		writeCommandInclude(t, path, "[tasktool]\nvalue = third\n")
		if _, err := newConfigurationFileCapture(&cache).loadRecords(path); err == nil || parseCacheCount(&cache) != 3 {
			t.Fatal("cache miss reused old success when parser input creation failed")
		}
	})
	writeCommandInclude(t, path, "[malformed")
	if _, err := newConfigurationFileCapture(&cache).loadRecords(path); err == nil {
		t.Fatal("cache miss used old success for malformed input")
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = third\n")
	if got := parseCacheLoad(t, &cache, path); *got[0].value != "third" || parseCacheCount(&cache) != 5 {
		t.Fatal("failed parse poisoned later recovery")
	}
	command := &environmentConfiguration{}
	defer command.close()
	if _, err := command.loadRecords(path); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = fourth\n")
	if got := parseCacheLoad(t, &cache, path); *got[0].value != "fourth" {
		t.Fatal("ordinary cache inherited frozen command capture")
	}
	if got, err := command.loadRecords(path); err != nil || *got[0].value != "third" {
		t.Fatalf("ordinary refresh mutated independent command capture: %#v %v", got, err)
	}
}

func TestConfigurationParseCacheMissingIdentityAndFailures(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var cache configurationParseCache
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	capture := newConfigurationFileCapture(&cache)
	if got, err := capture.loadRecords(path); err != nil || len(got) != 0 {
		t.Fatalf("missing: %#v %v", got, err)
	}
	writeCommandInclude(t, path, "[tasktool]\nvalue = one\n")
	if got, err := capture.loadRecords(path); err != nil || len(got) != 0 {
		t.Fatalf("operation capture changed: %#v %v", got, err)
	}
	if got := parseCacheLoad(t, &cache, path); *got[0].value != "one" || parseCacheCount(&cache) != 1 {
		t.Fatal("missing file appearance not parsed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := parseCacheLoad(t, &cache, path); len(got) != 0 {
		t.Fatal("deleted file remained effective")
	}
	writeCommandInclude(t, path, "[malformed")
	for range 2 {
		if _, err := newConfigurationFileCapture(&cache).loadRecords(path); err == nil {
			t.Fatal("invalid bytes accepted")
		}
	}
	if parseCacheCount(&cache) != 3 {
		t.Fatal("parse errors were cached")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := newConfigurationFileCapture(&cache).loadRecords(path); err == nil {
		t.Fatal("read failure accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(dir, "one"), filepath.Join(dir, "two")
	for _, target := range []string{one, two} {
		writeCommandInclude(t, target, "[tasktool]\nvalue = same\n")
	}
	if err := os.Symlink(one, path); err != nil {
		t.Fatal(err)
	}
	first := parseCacheLoad(t, &cache, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(two, path); err != nil {
		t.Fatal(err)
	}
	second := parseCacheLoad(t, &cache, path)
	if !reflect.DeepEqual(first, second) || first[0].origin != path || parseCacheCount(&cache) != 5 {
		t.Fatal("symlink target identity was not revalidated")
	}
	// Replacement at the same resolved path also changes file identity.
	if err := os.Rename(one, two); err != nil {
		t.Fatal(err)
	}
	parseCacheLoad(t, &cache, path)
	if parseCacheCount(&cache) != 6 {
		t.Fatal("same-byte replacement reused old file identity")
	}
	alias := parseCacheLoad(t, &cache, two)
	if alias[0].origin != two || parseCacheCount(&cache) != 7 {
		t.Fatal("distinct lexical origins shared parser records")
	}
}

func TestConfigurationParseCacheReevaluatesGraph(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var cache configurationParseCache
	dir := t.TempDir()
	root, remote, leaf := filepath.Join(dir, "config"), filepath.Join(dir, "remote"), filepath.Join(dir, "leaf")
	writeCommandInclude(t, root, "[includeIf \"onbranch:tasks\"]\npath = remote\n[includeIf \"hasconfig:remote.*.url:https://host/**\"]\npath = leaf\n")
	writeCommandInclude(t, remote, "[remote \"origin\"]\nurl = https://host/repo\n")
	writeCommandInclude(t, leaf, "[tasktool]\nvalue = leaf\n")
	evaluate := func(branch string, want bool) {
		t.Helper()
		capture := newConfigurationFileCapture(&cache)
		records, err := capture.loadRecords(root)
		if err != nil {
			t.Fatal(err)
		}
		context := configurationContext{gitdir: dir, branch: branch}
		got, err := (&hasconfigEvaluation{matches: context.matches}).evaluate([]hasconfigSource{{records: records, load: capture.loadRecords}})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, record := range got {
			if record.name == "tasktool.value" {
				found = true
				if record.origin != leaf || record.depth != 1 || !record.conditionalURL {
					t.Fatal("lost evaluated origin/depth/closure")
				}
			}
		}
		if found != want {
			t.Fatalf("branch %q: leaf=%v, want %v", branch, found, want)
		}
	}
	evaluate("refs/heads/tasks", true)
	evaluate("", false)
	evaluate("refs/heads/tasks", true)
	if parseCacheCount(&cache) != 3 {
		t.Fatal("branch change reparsed unchanged files")
	}
	writeCommandInclude(t, remote, "[remote \"origin\"]\nurl = https://else/repo\n")
	evaluate("refs/heads/tasks", false)
	if parseCacheCount(&cache) != 4 {
		t.Fatal("URL edit did not reparse only the changed source")
	}
	// False hasconfig closures must still be validated after a successful hit.
	writeCommandInclude(t, leaf, "[remote \"forbidden\"]\nurl = https://host/repo\n")
	capture := newConfigurationFileCapture(&cache)
	records, err := capture.loadRecords(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&hasconfigEvaluation{}).evaluate([]hasconfigSource{{records: records, load: capture.loadRecords}}); err == nil {
		t.Fatal("cached graph skipped false-closure validation")
	}
	if parseCacheCount(&cache) != 5 {
		t.Fatal("false closure change was not parsed")
	}
}

func TestConfigurationParseCacheOperationFreshness(t *testing.T) {
	repo, root := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(t.TempDir(), "command")
	writeCommandInclude(t, command, "[tasktool]\ncommand = frozen\n")
	injectConfiguration(t, [2]string{"include.path", command})
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	check := func(key, want string) {
		t.Helper()
		if got, err := opened.Config(key); err != nil || got != want {
			t.Fatalf("%s = %q, want %q: %v", key, got, want, err)
		}
	}
	check("tasktool.command", "frozen")
	cache := &opened.configuration.parsedFiles
	count := parseCacheCount(cache)
	for range 3 {
		check("tasktool.command", "frozen")
	}
	if parseCacheCount(cache) != count {
		t.Fatal("unchanged repository operations reparsed ordinary sources")
	}
	writeCommandInclude(t, command, "[malformed")
	check("tasktool.command", "frozen")
	if err := opened.SetLocalConfig("tasktool.local", "fresh"); err != nil {
		t.Fatal(err)
	}
	check("tasktool.local", "fresh")
	if parseCacheCount(cache) != count+1 {
		t.Fatal("local write did not reparse exactly the changed local config")
	}
	worktree := filepath.Join(paths.GitDir, "config.worktree")
	writeCommandInclude(t, worktree, "[tasktool]\nworktree = first\n")
	if err := opened.SetLocalConfig("extensions.worktreeConfig", "true"); err != nil {
		t.Fatal(err)
	}
	check("tasktool.worktree", "first")
	count = parseCacheCount(cache)
	check("tasktool.worktree", "first")
	if parseCacheCount(cache) != count {
		t.Fatal("unchanged worktree config reparsed")
	}
	writeCommandInclude(t, worktree, "[tasktool]\nworktree = later\n")
	check("tasktool.worktree", "later")
	if parseCacheCount(cache) != count+1 {
		t.Fatal("worktree edit not reparsed")
	}
	if err := opened.WithProspectiveWorktree("child", filepath.Join(t.TempDir(), "child"), "refs/heads/tasks", func(view *Repository) error {
		if _, err := view.Config("tasktool.worktree"); !HasCode(err, NotFound) {
			t.Fatalf("prospective context inherited cached current worktree: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := opened.SetLocalConfig("extensions.worktreeConfig", "false"); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, worktree, "[malformed")
	if _, err := opened.Config("tasktool.worktree"); !HasCode(err, NotFound) {
		t.Fatalf("disabled worktree config was still read: %v", err)
	}
}

func TestConfigurationEffectiveOperationWork(t *testing.T) {
	for _, mode := range []string{"ordinary", "command", "empty-command-include", "worktree", "empty-worktree"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("GIT_CONFIG_COUNT", "0")
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			setup := time.Now()
			repo, root := fixture(t, false)
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(t.TempDir(), "leaf")
			writeCommandInclude(t, leaf, "[tasktool]\nvalue = ordinary\n")
			if err := repo.SetLocalConfig("include.path", leaf); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "command":
				injectConfiguration(t, [2]string{"tasktool.value", "command"})
			case "empty-command-include":
				empty := filepath.Join(t.TempDir(), "empty")
				writeCommandInclude(t, empty, "")
				injectConfiguration(t, [2]string{"include.path", empty})
			case "worktree", "empty-worktree":
				if err := repo.SetLocalConfig("extensions.worktreeConfig", "true"); err != nil {
					t.Fatal(err)
				}
				contents := ""
				if mode == "worktree" {
					contents = "[tasktool]\nvalue = worktree\n"
				}
				writeCommandInclude(t, filepath.Join(paths.GitDir, "config.worktree"), contents)
			}
			setupTime := time.Since(setup)
			start := time.Now()
			opened, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			openTime := time.Since(start)
			var work configurationWork
			opened.configuration.work = &work
			parses := parseCacheCount(&opened.configuration.parsedFiles)
			start = time.Now()
			for range 2 {
				if got, err := opened.Paths(); err != nil || got != paths {
					t.Fatalf("paths changed: %+v %v", got, err)
				}
			}
			want := "ordinary"
			if mode == "command" || mode == "worktree" {
				want = mode
			}
			if got, err := opened.Config("tasktool.value"); err != nil || got != want {
				t.Fatalf("value = %q, want %q: %v", got, want, err)
			}
			operationTime := time.Since(start)
			evaluations := uint64(6)
			if mode == "ordinary" {
				evaluations = 3
			}
			if work.paths != 2 || work.evaluations != evaluations || work.assemblies != 3 || work.copies != work.nativeRecords || work.reads < 6 {
				t.Fatalf("unexpected operation work: %+v", work)
			}
			if count := parseCacheCount(&opened.configuration.parsedFiles) - parses; count != 0 {
				t.Fatalf("warm operations reparsed %d inputs", count)
			}
			t.Logf("setup=%s open/discovery/admission=%s opens=1 operations=%s parse_misses=0 work=%+v (capture/graph durations overlap)", setupTime, openTime, operationTime, work)
		})
	}
}

func TestHeadWorkdirConfigurationWork(t *testing.T) {
	_, root := fixture(t, false)
	opened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	var separate, combined configurationWork
	opened.configuration.work = &separate
	paths, err := opened.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Head(); err != nil {
		t.Fatal(err)
	}
	opened.configuration.work = &combined
	got, err := opened.HeadWorkdir()
	if err != nil || got != paths.WorkDir {
		t.Fatalf("workdir: %q %v; want %q", got, err, paths.WorkDir)
	}
	if separate.paths != 1 || separate.assemblies != 2 || combined.paths != 0 || combined.assemblies != 1 || separate.evaluations != 2*combined.evaluations || combined.evaluations == 0 {
		t.Fatalf("configuration work: separate=%+v combined=%+v", separate, combined)
	}
}

func TestConfigurationEffectiveReuseFreshnessAndRecords(t *testing.T) {
	injectConfiguration(t)
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	repo, _ := fixture(t, false)
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	top, alias := filepath.Join(dir, "top"), filepath.Join(dir, "alias")
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	contents := func(value string) string { return "[tasktool]\nvalue = " + value + "\nimplicit\nempty =\n" }
	writeCommandInclude(t, first, contents("first"))
	writeCommandInclude(t, second, contents("later"))
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, top, "[include]\npath = alias\n[includeIf \"onbranch:main\"]\npath = alias\n[includeIf \"gitdir:"+paths.GitDir+"\"]\npath = alias\n[includeIf \"hasconfig:remote.*.url:https://host/**\"]\npath = alias\n[remote \"later\"]\nurl = https://host/repo\n")
	if err := repo.SetLocalConfig("include.path", top); err != nil {
		t.Fatal(err)
	}
	// Compare the fast path to a complete final evaluation, including origins,
	// ordering, levels, depths, nil values, and independent duplicate values.
	check := func(branch, value string) {
		t.Helper()
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		configuration := repo.configuration
		files := newConfigurationFileCapture(&configuration.parsedFiles)
		sources, err := configuration.sources(files, repo.ptr)
		if err != nil {
			t.Fatal(err)
		}
		context := configurationContext{gitdir: paths.GitDir, branch: branch}
		want, err := (&hasconfigEvaluation{matches: context.matches}).evaluate(sources)
		if err != nil {
			t.Fatal(err)
		}
		var work configurationWork
		configuration.work = &work
		defer func() { configuration.work = nil }()
		got, err := configuration.effectiveRecords(repo.ptr, nil)
		if err != nil || !reflect.DeepEqual(got, want) || work.evaluations != 1 {
			t.Fatalf("single evaluation differs: %v work=%+v", err, work)
		}
		found := 0
		for i := range got {
			if got[i].name == "tasktool.value" {
				if got[i].value == nil || *got[i].value != value || got[i].origin != alias {
					t.Fatalf("lost fresh value/lexical origin or aliased occurrence: %+v", got[i])
				}
				*got[i].value = "private mutation"
				found++
			}
		}
		if found < 3 {
			t.Fatalf("missing include occurrences: %d", found)
		}
		if again, err := configuration.effectiveRecords(repo.ptr, nil); err != nil || !reflect.DeepEqual(again, want) {
			t.Fatalf("output mutated a retained input: %v", err)
		}
	}
	check("refs/heads/main", "first")
	info, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}
	writeCommandInclude(t, first, contents("other"))
	if err := os.Chtimes(first, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	check("refs/heads/main", "other")
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	check("refs/heads/main", "later")
	writeCommandInclude(t, filepath.Join(paths.GitDir, "HEAD"), "ref: refs/heads/topic\n")
	check("refs/heads/topic", "later")
	if err := os.Remove(second); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Config("tasktool.value"); !HasCode(err, NotFound) {
		t.Fatalf("missing include retained previous records: %v", err)
	}
	writeCommandInclude(t, second, contents("again"))
	check("refs/heads/topic", "again")
	writeCommandInclude(t, second, "[malformed")
	if _, err := repo.Paths(); err == nil {
		t.Fatal("Paths bypassed fresh malformed include")
	}
	if err := repo.SetLocalConfig("tasktool.mustnotwrite", "bad"); err == nil {
		t.Fatal("local mutation bypassed fresh malformed include")
	}
	writeCommandInclude(t, second, contents("fixed"))
	if _, err := repo.Config("tasktool.mustnotwrite"); !HasCode(err, NotFound) {
		t.Fatalf("refused mutation changed local configuration: %v", err)
	}
	check("refs/heads/topic", "fixed")
	// The disabled worktree and false hasconfig closure remain validated with
	// exactly the old error precedence, even after a successful fast operation.
	writeCommandInclude(t, top, "[extensions]\nworktreeConfig = invalid\n[include]\npath\n")
	if _, err := repo.Paths(); err == nil || !strings.Contains(err.Error(), "active Git include.path requires a value") {
		t.Fatalf("value conversion masked graph failure: %v", err)
	}
	writeCommandInclude(t, top, "[extensions]\nworktreeConfig = invalid\n")
	if _, err := repo.Paths(); err == nil || err.Error() != "invalid extensions.worktreeConfig" {
		t.Fatalf("conversion failure lost: %v", err)
	}
	writeCommandInclude(t, top, "[extensions]\nworktreeConfig = false\n[includeIf \"hasconfig:remote.*.url:never\"]\npath = alias\n")
	writeCommandInclude(t, second, "[remote \"forbidden\"]\nurl = https://host/repo\n")
	if _, err := repo.Paths(); err == nil || !strings.Contains(err.Error(), "remote URLs cannot be configured") {
		t.Fatalf("false closure validation bypassed: %v", err)
	}
}

func TestConfigurationEffectiveReuseRequiresIdenticalSources(t *testing.T) {
	for _, scope := range []string{"command", "worktree"} {
		t.Run(scope, func(t *testing.T) {
			injectConfiguration(t)
			t.Setenv("GIT_CONFIG_PARAMETERS", "")
			repo, root := fixture(t, false)
			paths, err := repo.Paths()
			if err != nil {
				t.Fatal(err)
			}
			leaf := filepath.Join(t.TempDir(), "leaf")
			writeCommandInclude(t, leaf, "[tasktool]\nmatched = final\n")
			if err := repo.SetLocalConfig("includeIf.hasconfig:remote.*.url:https://later/**.path", leaf); err != nil {
				t.Fatal(err)
			}
			if scope == "command" {
				injectConfiguration(t, [2]string{"remote.later.url", "https://later/repo"})
			} else {
				if err := repo.SetLocalConfig("extensions.worktreeConfig", "true"); err != nil {
					t.Fatal(err)
				}
				writeCommandInclude(t, filepath.Join(paths.GitDir, "config.worktree"), "[remote \"later\"]\nurl = https://later/repo\n")
			}
			opened, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Close()
			var work configurationWork
			opened.configuration.work = &work
			if got, err := opened.Config("tasktool.matched"); err != nil || got != "final" || work.evaluations != 2 {
				t.Fatalf("ordinary graph reused despite later URL: %q %v %+v", got, err, work)
			}
		})
	}
}

func TestConfigurationParseCacheBounds(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var cache configurationParseCache
	dir := t.TempDir()
	first := filepath.Join(dir, "0")
	for i := 0; i <= configurationParseCacheEntries; i++ {
		path := filepath.Join(dir, fmt.Sprint(i))
		writeCommandInclude(t, path, "[tasktool]\nvalue = bounded\n")
		parseCacheLoad(t, &cache, path)
		if len(cache.entries) > configurationParseCacheEntries || cache.bytes > configurationParseCacheBytes {
			t.Fatal("cache exceeded bounds")
		}
	}
	count := parseCacheCount(&cache)
	parseCacheLoad(t, &cache, first)
	if parseCacheCount(&cache) != count+1 {
		t.Fatal("evicted file did not use parser")
	}
	parseCacheLoad(t, &cache, first)
	if parseCacheCount(&cache) != count+1 {
		t.Fatal("reinserted file did not reuse parse")
	}
	large := filepath.Join(dir, "large")
	writeCommandInclude(t, large, "#"+strings.Repeat("x", configurationParseCacheBytes)+"\n[tasktool]\nvalue = large\n")
	for range 2 {
		if got := parseCacheLoad(t, &cache, large); *got[0].value != "large" {
			t.Fatal("oversized uncached file changed semantics")
		}
	}
	if _, ok := cache.entries[large]; ok || cache.bytes > configurationParseCacheBytes || parseCacheCount(&cache) != count+3 {
		t.Fatal("oversized file was retained or did not reparse")
	}
	cache.clear()
	if len(cache.entries) != 0 || cache.bytes != 0 {
		t.Fatal("close did not release Go cache")
	}
}

func TestConfigurationParseCacheConcurrentCaptures(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	var cache configurationParseCache
	path := filepath.Join(t.TempDir(), "config")
	writeCommandInclude(t, path, "[tasktool]\nvalue = original\n")
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			records, err := newConfigurationFileCapture(&cache).loadRecords(path)
			if err != nil {
				t.Error(err)
				return
			}
			if len(records) != 1 || *records[0].value != "original" {
				t.Error("concurrent capture changed records")
				return
			}
			*records[0].value = "private"
		}()
	}
	workers.Wait()
	if parseCacheCount(&cache) != 1 {
		t.Fatal("concurrent captures did not reuse a single parse")
	}
}
