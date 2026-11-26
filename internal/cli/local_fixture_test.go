package cli

import (
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func seedLocalTasks(t *testing.T, repo *git.Repository) git.ID {
	t.Helper()
	blob, err := repo.WriteBlob([]byte("prefix: EXAMPLE\n"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := repo.WriteTree([]git.TreeEntry{{Name: ".tasks.yml", ID: blob, Mode: 0100644}})
	if err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Task Test", Email: "task@example.invalid"}
	id, err := repo.WriteCommit(tree, nil, who, who, "orphan tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, store.Branch, id, git.ID{})
	return id
}

func seedEmptyLocalTasks(t *testing.T, repo *git.Repository) git.ID {
	t.Helper()
	tree, err := repo.WriteTree(nil)
	if err != nil {
		t.Fatal(err)
	}
	who := git.Signature{Name: "Task Test", Email: "task@example.invalid"}
	id, err := repo.WriteCommit(tree, nil, who, who, "orphan tasks\n")
	if err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, store.Branch, id, git.ID{})
	return id
}
