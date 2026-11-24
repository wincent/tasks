package cli

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

// Use systemGit for fixtures that exercise Git CLI interoperability and for
// assertions that need an independent view of the repository.
func systemGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func openFixtureRepo(t *testing.T, dir string) *git.Repository {
	t.Helper()
	repo, err := git.Open(dir)
	if err != nil {
		t.Fatalf("open fixture repository %s: %v", dir, err)
	}
	t.Cleanup(repo.Close)
	// Do not mistake a containing repository for the requested worktree.
	workdir := fixturePaths(t, repo).WorkDir
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if workdir != want {
		t.Fatalf("opened worktree %s, want %s", workdir, want)
	}
	return repo
}

func fixtureRevision(t *testing.T, repo *git.Repository, revision string) git.ID {
	t.Helper()
	id, err := repo.ResolveCommit(revision)
	if err != nil {
		t.Fatalf("resolve fixture revision %q: %v", revision, err)
	}
	return id
}

func fixtureRootTree(t *testing.T, repo *git.Repository, id git.ID) git.ID {
	t.Helper()
	tree, err := repo.RootTreeID(id)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func fixtureHead(t *testing.T, repo *git.Repository) git.Head {
	t.Helper()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head
}

func fixturePaths(t *testing.T, repo *git.Repository) git.Paths {
	t.Helper()
	paths, err := repo.Paths()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func fixtureBranchNames(t *testing.T, repo *git.Repository) []string {
	t.Helper()
	refs, err := repo.References("refs/heads/")
	if err != nil {
		t.Fatal(err)
	}
	return slices.Sorted(maps.Keys(refs))
}

func fixtureHasBranch(t *testing.T, repo *git.Repository, name string) bool {
	t.Helper()
	_, err := repo.Reference("refs/heads/" + name)
	if err != nil && !git.HasCode(err, git.NotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// Seed a non-branch ref in a private fixture. Prepare only publishes branches
// and detached HEAD, so write the loose ref directly for tags and tracking refs.
func writeFixtureLooseRef(t *testing.T, repo *git.Repository, name string, id git.ID) {
	t.Helper()
	path := filepath.Join(fixturePaths(t, repo).CommonDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, path, id.String()+"\n")
}

func setFixtureConfig(t *testing.T, repo *git.Repository, key, value string) {
	t.Helper()
	if err := repo.SetLocalConfig(key, value); err != nil {
		t.Fatal(err)
	}
}

// Stage regular fixture files without checking out or disturbing other entries.
func stageFixtureFiles(t *testing.T, repo *git.Repository, names ...string) {
	t.Helper()
	paths := fixturePaths(t, repo)
	var edits []git.TreeEdit
	for _, name := range names {
		path := filepath.Join(paths.WorkDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatalf("expected a regular fixture file: %s", path)
		}
		mode := uint32(0100644)
		if info.Mode()&0100 != 0 {
			mode = 0100755
		}
		blob, err := repo.WriteWorktreeBlob(name, uiBytes(t, path))
		if err != nil {
			t.Fatal(err)
		}
		edits = append(edits, git.TreeEdit{Path: name, ID: blob, Mode: mode})
	}
	index := filepath.Join(paths.GitDir, "index")
	pending := filepath.Join(paths.GitDir, "fixture-index")
	defer os.Remove(pending)
	if err := os.WriteFile(pending, uiBytes(t, index), 0600); err != nil {
		t.Fatal(err)
	}
	if err := repo.EditIndexFile(pending, git.ID{}, edits); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(pending, index); err != nil {
		t.Fatal(err)
	}
}

// Make an ordinary one-parent commit. Tests completing a real Git merge or
// simulating an external Git writer should still use the Git CLI.
func commitFixtureFiles(t *testing.T, dir, message string, names ...string) git.ID {
	t.Helper()
	repo := openFixtureRepo(t, dir)
	if err := repo.RequireIdle(); err != nil {
		t.Fatal(err)
	}
	head := fixtureHead(t, repo)
	if head.ID == (git.ID{}) {
		t.Fatal("fixture commit requires a parent")
	}
	stageFixtureFiles(t, repo, names...)
	tree, err := repo.IndexTree()
	if err != nil {
		t.Fatal(err)
	}
	who, err := repo.Signatures()
	if err != nil {
		t.Fatal(err)
	}
	id, err := repo.WriteCommit(tree, []git.ID{head.ID}, who.Author, who.Committer, message+"\n")
	if err != nil {
		t.Fatal(err)
	}
	ref := head.Branch
	if ref == "" {
		ref = "HEAD"
	}
	setRef(t, repo, ref, id, head.ID)
	return id
}

func commitImplementation(t *testing.T, dir, name, text string) git.ID {
	t.Helper()
	writeSafetyFile(t, filepath.Join(dir, name), text)
	return commitFixtureFiles(t, dir, "implement "+name, name)
}
