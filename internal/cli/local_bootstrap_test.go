package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func TestInitRequiresExplicitLocalTaskHistory(t *testing.T) {
	repo, root := fixture(t)
	ref, err := repo.Reference(store.Branch)
	if err != nil {
		t.Fatal(err)
	}
	setRef(t, repo, store.Branch, git.ID{}, ref.ID)
	for key, value := range map[string]string{"remote.first.url": "git@ssh-alias:project.git", "remote.second.url": "/inaccessible/project.git"} {
		if err := repo.SetLocalConfig(key, value); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	config := uiBytes(t, filepath.Join(paths.CommonDir, "config"))
	t.Setenv("PATH", "/nonexistent")
	for _, args := range [][]string{{"init"}, {"create", "Missing history", "--body", "requirements"}} {
		output, _, err := invoke(root, "", args...)
		if output != "" || err == nil {
			t.Fatalf("%v: %q %v", args, output, err)
		}
		for _, example := range []string{
			"\n\n    git fetch REMOTE refs/heads/tasks:refs/heads/tasks\n\n",
			"\n    commit=$(git commit-tree ",
			"Then run `tasks init`.",
		} {
			if !strings.Contains(err.Error(), example) {
				t.Errorf("%v: missing delimited example %q in %v", args, example, err)
			}
		}
		if _, err := repo.Reference(store.Branch); !git.HasCode(err, git.NotFound) {
			t.Fatalf("created tasks branch: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "tasks")); !os.IsNotExist(err) {
			t.Fatalf("created checkout: %v", err)
		}
		if string(config) != string(uiBytes(t, filepath.Join(paths.CommonDir, "config"))) {
			t.Fatal("changed configuration")
		}
	}
	seedLocalTasks(t, repo)
	if got := strings.TrimSpace(run(t, root, "init")); got != filepath.Join(root, "tasks") {
		t.Fatal(got)
	}
	help := run(t, root, "init", "--help")
	for _, fragment := range []string{"existing local tasks branch", "README.md in the github.com/wincent/tasks", "does not fetch, push, or reset"} {
		if !strings.Contains(help, fragment) {
			t.Fatalf("help missing %q: %s", fragment, help)
		}
	}
}
