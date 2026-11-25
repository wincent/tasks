package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestLocalInitDoesNotRequireCommitIdentity(t *testing.T) {
	t.Parallel()
	repo, root := fixture(t)
	for _, key := range []string{"user.name", "user.email"} {
		if err := repo.SetLocalConfig(key, ""); err != nil {
			t.Fatal(err)
		}
	}
	if out, diagnostic, err := invoke(root, "", "init"); err != nil || out == "" {
		t.Fatalf("out=%q diagnostic=%q error=%v", out, diagnostic, err)
	}
	if _, _, err := invoke(root, "", "create", "Identity required", "--body", "fixture"); err == nil {
		t.Fatal("metadata commit accepted missing identity")
	}
}

func TestMissingLocalHistoryGuidanceDoesNotCreateTaskState(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "repository")
	repo, err := git.Init(root, "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	seedMain(t, repo)
	for _, key := range []string{"user.name", "user.email"} {
		if err := repo.SetLocalConfig(key, ""); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = invoke(root, "", "init")
	if err == nil || !strings.Contains(err.Error(), "local refs/heads/tasks is missing") || !strings.Contains(err.Error(), "git fetch REMOTE") {
		t.Fatalf("missing setup guidance: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(paths.CommonDir, "tasks")); !os.IsNotExist(err) {
		t.Fatalf("created task state before missing-history refusal: %v", err)
	}
	if _, err := repo.Reference("refs/heads/tasks"); !git.HasCode(err, git.NotFound) {
		t.Fatalf("created task branch: %v", err)
	}
}
