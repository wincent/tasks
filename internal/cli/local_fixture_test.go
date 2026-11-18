package cli

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func seedLocalTasks(t *testing.T, repo *git.Repository) git.ID {
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

// copyFixtureTasks transfers immutable fixture objects and a tracking ref in
// process. It does not invoke any remote transport or external Git command.
func copyFixtureTasks(t *testing.T, source, target *git.Repository) git.ID {
	t.Helper()
	from, err := source.Paths()
	if err != nil {
		t.Fatal(err)
	}
	to, err := target.Paths()
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(from.CommonDir, "objects")
	err = filepath.WalkDir(objects, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(objects, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(to.CommonDir, "objects", relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if existing, err := os.ReadFile(destination); err == nil {
			if !bytes.Equal(existing, data) {
				return fmt.Errorf("fixture object differs: %s", relative)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		return os.WriteFile(destination, data, 0444)
	})
	if err != nil {
		t.Fatal(err)
	}
	ref, err := source.Reference(store.Branch)
	if err != nil {
		t.Fatal(err)
	}
	const tracking = "refs/remotes/origin/tasks"
	if ref.Symbolic != "" || ref.ID == (git.ID{}) {
		t.Fatalf("fixture task reference must be a direct commit: %+v", ref)
	}
	// Seed an already-fetched loose ref, not a task publication. The runtime
	// transaction API deliberately restricts publication to local branches.
	trackingPath := filepath.Join(to.CommonDir, filepath.FromSlash(tracking))
	if err := os.MkdirAll(filepath.Dir(trackingPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackingPath, []byte(ref.ID.String()+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if actual, err := target.Reference(tracking); err != nil || actual.ID != ref.ID {
		t.Fatalf("fixture tracking reference: %+v %v", actual, err)
	}
	return ref.ID
}
