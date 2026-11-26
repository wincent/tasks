package git

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func cloneConfigurationRecords(records []hasconfigRecord) []hasconfigRecord {
	result := append([]hasconfigRecord(nil), records...)
	for i := range result {
		if result[i].value != nil {
			value := *result[i].value
			result[i].value = &value
		}
	}
	return result
}

func TestConfigurationNativeParserPreservesCapturedRecords(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, leaf := filepath.Join(dir, "config"), filepath.Join(dir, "leaf")
	writeCommandInclude(t, leaf, "[malformed")
	input := "[tasktool]\nimplicit\nempty =\nvalue = first\nvalue = second\n[include]\npath = leaf\n[includeIf \"onbranch:tasks\"]\npath = leaf\n[tasktool-frozen-include]\npath = literal\n"
	writeCommandInclude(t, path, input)
	records, err := configurationParseFileRecords(path, []byte(input))
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"tasktool.implicit", "tasktool.empty", "tasktool.value", "tasktool.value", "include.path", "includeif.onbranch:tasks.path", "tasktool-frozen-include.path"}
	wantValues := []string{"", "", "first", "second", "leaf", "leaf", "literal"}
	if len(records) != len(wantNames) {
		t.Fatalf("records: %#v", records)
	}
	for i, record := range records {
		if record.name != wantNames[i] || record.origin != path || record.backend != "file" || record.depth != 0 || record.level == 0 || record.level != records[0].level {
			t.Fatalf("record %d metadata: %#v", i, record)
		}
		if i == 0 {
			if record.value != nil {
				t.Fatal("implicit value became non-nil")
			}
		} else if record.value == nil || *record.value != wantValues[i] {
			t.Fatalf("record %d value: %#v", i, record)
		}
	}
	for _, input := range [][]byte{nil, {}} {
		if got, err := configurationParseFileRecords(path, input); err != nil || len(got) != 0 {
			t.Fatalf("empty input: %#v %v", got, err)
		}
	}
	if _, err := configurationParseFileRecords("secret-origin", []byte("[secret-section\nsecret-value")); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("parser failure missing or unredacted: %v", err)
	}
	var cache configurationParseCache
	if got := parseCacheLoad(t, &cache, path); !reflect.DeepEqual(got, records) {
		t.Fatal("ordinary file parse differs from captured input parse")
	}
	// Cached parsing must not hide genuine source read failures.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := newConfigurationFileCapture(&cache).borrowRecords(path); err == nil {
		t.Fatal("ordinary read failure reused previous success")
	}
	command := &environmentConfiguration{}
	defer command.close()
	if _, err := command.loadRecords(path); err == nil {
		t.Fatal("command read failure accepted")
	}
}

func TestConfigurationBorrowedTraversalOwnership(t *testing.T) {
	t.Parallel()
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	root, leaf := filepath.Join(dir, "config"), filepath.Join(dir, "leaf")
	writeCommandInclude(t, root, "[include]\npath = leaf\n[includeIf \"hasconfig:remote.*.url:https://host/**\"]\npath = leaf\n[remote \"origin\"]\nurl = https://host/repo\n")
	writeCommandInclude(t, leaf, "[tasktool]\nvalue = original\nimplicit\nempty =\n")
	var cache configurationParseCache
	capture := newConfigurationFileCapture(&cache)
	roots, err := capture.borrowRecords(root)
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := capture.borrowRecords(leaf)
	if err != nil {
		t.Fatal(err)
	}
	beforeRoots, beforeLeaves := cloneConfigurationRecords(roots), cloneConfigurationRecords(leaves)
	sources := []hasconfigSource{
		{records: roots, load: capture.borrowRecords, level: 2},
		{records: roots, load: capture.borrowRecords, level: 5},
	}
	evaluate := func() []hasconfigRecord {
		t.Helper()
		got, err := (&hasconfigEvaluation{}).evaluate(sources)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := evaluate()
	if len(got) != 18 {
		t.Fatalf("lost flattened records: %#v", got)
	}
	for i, record := range got {
		level := 2
		if i >= 9 {
			level = 5
		}
		position := i % 9
		included := position >= 1 && position <= 3 || position >= 5 && position <= 7
		depth := uint(0)
		if included {
			depth = 1
		}
		if record.level != level || record.depth != depth {
			t.Fatalf("record %d inheritance: %#v", i, record)
		}
	}
	want := cloneConfigurationRecords(got)
	*got[1].value = "private"
	got[1].level, got[1].depth = 999, 99
	if *got[5].value != "original" || *got[10].value != "original" {
		t.Fatal("duplicate include results share mutable values")
	}
	if !reflect.DeepEqual(evaluate(), want) || !reflect.DeepEqual(roots, beforeRoots) || !reflect.DeepEqual(leaves, beforeLeaves) {
		t.Fatal("evaluation or returned records mutated borrowed inputs")
	}
	// Captures keep immutable references valid across cache eviction.
	cache.clear()
	if !reflect.DeepEqual(evaluate(), want) {
		t.Fatal("eviction changed an existing capture")
	}
	// Separate operation captures share the cache but not returned values.
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			capture := newConfigurationFileCapture(&cache)
			records, err := capture.borrowRecords(root)
			if err != nil {
				t.Error(err)
				return
			}
			got, err := (&hasconfigEvaluation{}).evaluate([]hasconfigSource{{records: records, load: capture.borrowRecords, level: 2}})
			if err != nil || !reflect.DeepEqual(got, want[:9]) {
				t.Errorf("concurrent borrowed evaluation: %#v %v", got, err)
				return
			}
			*got[1].value = "private"
			got[1].level, got[1].depth = 999, 99
		}()
	}
	workers.Wait()
	if !reflect.DeepEqual(parseCacheLoad(t, &cache, leaf), beforeLeaves) {
		t.Fatal("concurrent results corrupted cached input")
	}
}
