package git

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProspectiveContentsUseProposedAttributesWithoutMutation(t *testing.T) {
	for _, mode := range []string{"encoding", "clean", "process", "safecrlf", "nested"} {
		for _, unborn := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/existing", true: "/unborn"}[unborn], func(t *testing.T) {
				repo, root := fixture(t, false)
				if !unborn {
					tip := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": "*.txt -text\n", "old.txt": "old\n"})
					publish(t, repo, "refs/heads/tasks", tip, ID{})
				}
				attrs, invalid, want := "*.txt working-tree-encoding=UTF-16LE\n", []byte("x"), "working-tree-encoding"
				switch mode {
				case "clean", "process":
					attrs, want = "*.txt filter=proposed\n", "external filter"
					if err := repo.SetLocalConfig("filter.proposed."+mode, "must-not-run"); err != nil {
						t.Fatal(err)
					}
				case "safecrlf":
					attrs, invalid, want = "*.txt text eol=lf\n", []byte("line\r\n"), "CRLF"
					if err := repo.SetLocalConfig("core.safecrlf", "true"); err != nil {
						t.Fatal(err)
					}
				case "nested":
					attrs, want = "*.txt -text\n", "root regular file"
				}
				contents := map[string][]byte{".gitattributes": []byte(attrs), "a.md": []byte("valid\n"), "z.txt": invalid}
				if mode == "nested" {
					contents["nested/file.txt"] = []byte("nested\n")
				}
				// Preexisting identical objects must not be freshened even on
				// rejection after the attribute blob has been staged privately.
				for _, data := range contents {
					if _, err := repo.WriteBlob(data); err != nil {
						t.Fatal(err)
					}
				}
				destinationRoot := t.TempDir()
				destination := filepath.Join(destinationRoot, "child")
				before := mergePreflightSnapshot(t, root, destinationRoot)
				err := repo.ValidateProspectiveWorktreeContents("child", destination, "refs/heads/tasks", contents)
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(want)) {
					t.Fatalf("expected %s: %v", want, err)
				}
				if after := mergePreflightSnapshot(t, root, destinationRoot); !reflect.DeepEqual(before, after) {
					t.Fatal("rejection changed source/destination contents, modes, directories or mtimes")
				}
			})
		}
	}
}

func TestProspectiveContentsDoNotSmudgeOldTreeUnderFutureAttributes(t *testing.T) {
	repo, root := fixture(t, false)
	tip := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": "*.txt -text\n", "old.txt": "old\n"})
	publish(t, repo, "refs/heads/tasks", tip, ID{})
	if err := repo.SetLocalConfig("filter.future.smudge", "must-not-run"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	before := mergePreflightSnapshot(t, root, dir)
	contents := map[string][]byte{".gitattributes": []byte("old.txt filter=future\nnew.txt working-tree-encoding=UTF-16LE\n"), "new.txt": {'A', 0, '\n', 0}}
	if err := repo.ValidateProspectiveWorktreeContents("child", filepath.Join(dir, "child"), "refs/heads/tasks", contents); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root, dir)) {
		t.Fatal("successful admission mutated source")
	}
	if _, err := os.Stat(filepath.Join(dir, "child")); !os.IsNotExist(err) {
		t.Fatalf("created worktree: %v", err)
	}
}

func TestProspectiveContentsStillPreflightOriginalAttributes(t *testing.T) {
	repo, root := fixture(t, false)
	tip := safetyTip(t, repo, ID{}, map[string]string{".gitattributes": "old.txt filter=original\n", "old.txt": "old\n"})
	publish(t, repo, "refs/heads/tasks", tip, ID{})
	if err := repo.SetLocalConfig("filter.original.smudge", "must-not-run"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	before := mergePreflightSnapshot(t, root, dir)
	err := repo.ValidateProspectiveWorktreeContents("child", filepath.Join(dir, "child"), "refs/heads/tasks", map[string][]byte{".gitattributes": []byte("* -filter\n"), "new.txt": []byte("new\n")})
	if err == nil || !strings.Contains(err.Error(), "external filter") {
		t.Fatalf("bypassed original checkout: %v", err)
	}
	if !reflect.DeepEqual(before, mergePreflightSnapshot(t, root, dir)) {
		t.Fatal("rejection mutated source")
	}
}
