package main

import (
	"testing"
	"time"

	"github.com/wincent/tasks/internal/git"
)

func seedMainConfig(t *testing.T, repo *git.Repository) {
	t.Helper()
	if err := repo.SetHead("refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	blob, err := repo.WriteBlob([]byte("prefix: EXAMPLE\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Fixture", Email: "fixture@example.invalid", When: time.Unix(1, 0)}
	empty, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, err := repo.WriteCommit(empty, nil, who, who, "fixture: empty main\n")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.WriteCommit(tree, []git.ID{base}, who, who, "fixture: task configuration\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Checkout(base, commit); err != nil {
		t.Fatal(err)
	}
	tx, err := repo.Prepare("refs/heads/main", commit, map[string]git.ID{"refs/heads/main": {}}, who, "fixture task configuration")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedLocalTaskHistory(t *testing.T, repo *git.Repository) {
	t.Helper()
	tree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Fixture", Email: "fixture@example.invalid", When: time.Unix(1, 0)}
	tip, err := repo.WriteCommit(tree, nil, who, who, "fixture: explicit independent task history\n")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := repo.Prepare("refs/heads/tasks", tip, map[string]git.ID{"refs/heads/tasks": {}}, who, "fixture task history")
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
