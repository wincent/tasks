package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestScanIgnoresDotfiles(t *testing.T) {
	repo, err := git.Init(filepath.Join(t.TempDir(), "repository"), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()

	files := map[string]string{
		".gitignore":           "ignored\n",
		".mailmap":             "<public@example.invalid> <old@example.invalid>\n",
		"EXAMPLE-1 Example.md": "# EXAMPLE-1: Example\n\nStatus: open\n",
	}
	var entries []git.TreeEntry
	for name, content := range files {
		id, err := repo.WriteBlob([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, git.TreeEntry{Name: name, ID: id, Mode: 0100644})
	}
	tree, err := repo.WriteTree(entries)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := repo.WriteCommit(tree, nil, signature, signature, "tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir()
	for name, content := range files {
		mustWrite(t, filepath.Join(path, name), []byte(content))
	}
	for _, tc := range []struct {
		name        string
		prospective bool
	}{{"checkout", false}, {"tree", true}} {
		t.Run(tc.name, func(t *testing.T) {
			store := Store{Prefix: "EXAMPLE", Path: path, Repository: repo, prospective: tc.prospective, revision: revision}
			tasks, err := store.Scan()
			if err != nil || len(tasks) != 1 || tasks[0].Number != 1 {
				t.Fatalf("Scan() found %d tasks, err = %v; want only EXAMPLE-1", len(tasks), err)
			}
		})
	}

	mustWrite(t, filepath.Join(path, "unexpected"), []byte("not a ticket\n"))
	store := Store{Prefix: "EXAMPLE", Path: path}
	if _, err := store.Scan(); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("Scan() error = %v; want a diagnostic for unexpected", err)
	}
}
