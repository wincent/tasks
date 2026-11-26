package store

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func selectedFilterFiles(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				result[path] = string(data)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func TestSelectedFilterCommitPreflightsEntireSelection(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"clean", "process"} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			repo, tasks, _ := fixture(t)
			session, err := Begin(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			mustWrite(t, filepath.Join(tasks.Path, ".gitattributes"), []byte("EXAMPLE-2* filter=custom\n"))
			if err := repo.SetLocalConfig("filter.custom."+command, "never-execute-this"); err != nil {
				t.Fatal(err)
			}
			before := selectedFilterFiles(t, tasks.Paths.CommonDir, tasks.Path)
			_, err = session.CommitFiles(map[string][]byte{
				"EXAMPLE-1 Allowed.md":  []byte("must not write even this first blob\n"),
				"EXAMPLE-2 Filtered.md": []byte("must not bypass filter\n"),
			}, options())
			if err == nil || !strings.Contains(err.Error(), "external filter") || !strings.Contains(err.Error(), "EXAMPLE-2 Filtered.md") {
				t.Fatalf("expected selected-filter refusal: %v", err)
			}
			if !reflect.DeepEqual(before, selectedFilterFiles(t, tasks.Paths.CommonDir, tasks.Path)) {
				t.Fatal("refusal changed objects, index, refs, metadata or worktree content")
			}
		})
	}
}

func TestSelectedContentPreflightDoesNotWriteEarlierBlobs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, attribute, content, diagnostic string }{
		{"encoding", "working-tree-encoding=UTF-16", "missing BOM", "BOM"},
		{"line-endings", "text eol=lf", "first\r\nsecond\n", "LF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, tasks, _ := fixture(t)
			session, err := Begin(context.Background(), repo)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			mustWrite(t, filepath.Join(tasks.Path, ".gitattributes"), []byte("EXAMPLE-2* "+tc.attribute+"\n"))
			for key, value := range map[string]string{"core.autocrlf": "false", "core.safecrlf": "true"} {
				if err := repo.SetLocalConfig(key, value); err != nil {
					t.Fatal(err)
				}
			}
			before := selectedFilterFiles(t, tasks.Paths.CommonDir, tasks.Path)
			_, err = session.CommitFiles(map[string][]byte{
				"EXAMPLE-1 Allowed.md": []byte("must not persist this earlier valid blob\n"),
				"EXAMPLE-2 Invalid.md": []byte(tc.content),
			}, options())
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) || !strings.Contains(err.Error(), "EXAMPLE-2 Invalid.md") {
				t.Fatalf("expected complete-selection conversion refusal: %v", err)
			}
			if !reflect.DeepEqual(before, selectedFilterFiles(t, tasks.Paths.CommonDir, tasks.Path)) {
				t.Fatal("invalid later content changed objects, index, refs, metadata or worktree")
			}
		})
	}
}

func TestSelectedFilterCommitCleansCRLFAndPreservesUnrelatedStaging(t *testing.T) {
	t.Parallel()
	repo, tasks, _ := fixture(t)
	ctx := context.Background()
	name := "EXAMPLE-1 Native.md"
	if _, err := commitTestFiles(ctx, repo, map[string][]byte{name: []byte("before\n"), "large.bin": []byte("pointer\n")}, options()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tasks.Path, ".gitattributes"), []byte("*.md text eol=crlf\nlarge.bin filter=lfs\n"))
	for _, command := range []string{"clean", "smudge", "process"} {
		if err := repo.SetLocalConfig("filter.lfs."+command, "never-execute-this"); err != nil {
			t.Fatal(err)
		}
	}
	staged, err := repo.WriteBlob([]byte("staged pointer\n"))
	if err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(tasks.Paths.GitDir, "index")
	isolated := filepath.Join(tasks.Paths.GitDir, "selected-filter-index")
	mustWrite(t, isolated, mustRead(t, index))
	if err := tasks.Repository.EditIndexFile(isolated, git.ID{}, []git.TreeEdit{{Path: "large.bin", ID: staged, Mode: 0100755}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(isolated, index); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(tasks.Path, "large.bin"), []byte("unrelated manual content\n"))
	content := []byte("native\r\nline endings\r\n")
	revision, err := commitTestFiles(ctx, repo, map[string][]byte{name: content}, options())
	if err != nil {
		t.Fatal(err)
	}
	entries, err := tasks.Repository.IndexEntries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("index: %+v %v", entries, err)
	}
	if entries[1].Path != "large.bin" || entries[1].ID != staged || entries[1].Mode != 0100755 {
		t.Fatalf("unrelated staging changed: %+v", entries)
	}
	data, err := repo.Blob(entries[0].ID)
	if err != nil || string(data) != "native\nline endings\n" {
		t.Fatalf("selected index blob was not cleaned: %q %v", data, err)
	}
	tree, err := repo.Tree(revision)
	if err != nil || len(tree) != 2 || tree[0].ID != entries[0].ID {
		t.Fatalf("committed tree differs from cleaned index: %+v %v", tree, err)
	}
	if got := string(mustRead(t, filepath.Join(tasks.Path, name))); got != string(content) {
		t.Fatalf("worktree CRLF bytes changed: %q", got)
	}
	if got := string(mustRead(t, filepath.Join(tasks.Path, "large.bin"))); got != "unrelated manual content\n" {
		t.Fatalf("unrelated content changed: %q", got)
	}
	if err := tasks.RequireClean(name); err != nil {
		t.Fatalf("native CRLF commit left selected file dirty: %v", err)
	}
}
