package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
)

func TestCleanupBranchesDefaultsToLocalMainWithoutTaskRecords(t *testing.T) {
	repo, root := fixture(t)
	commitMainConfig(t, repo, "prefix: DEV\n")
	base := systemGit(t, root, "rev-parse", "main")
	for _, name := range []string{"task/dev-7-first", "task/dev-7-second", "task/dev-70", "task/example-7", "other"} {
		systemGit(t, root, "branch", name, base)
	}
	systemGit(t, root, "switch", "-c", "task/dev-7-unmerged")
	tip := commitImplementation(t, root, "unmerged.txt", "not in local main\n")
	systemGit(t, root, "switch", "main")
	systemGit(t, root, "update-ref", "refs/remotes/origin/main", tip)
	systemGit(t, root, "symbolic-ref", "refs/heads/task/dev-7-alias", "refs/heads/main")
	systemGit(t, root, "pack-refs", "--all")

	for _, apply := range []bool{false, true} {
		args, action := []string{"cleanup", "7"}, "Would remove"
		if apply {
			args, action = append(args, "--apply"), "Removed"
		}
		out := run(t, root, args...)
		for _, want := range []string{
			action + " branch task/dev-7-first",
			action + " branch task/dev-7-second",
			"Keep branch task/dev-7-alias: symbolic branch refs are protected",
			"Keep branch task/dev-7-unmerged: branch is not merged into local main",
		} {
			if !strings.Contains(out, want+"\n") {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
		}
		for _, name := range []string{"task/dev-7-first", "task/dev-7-second"} {
			_, err := repo.Reference("refs/heads/" + name)
			if (!apply && err != nil) || (apply && !git.HasCode(err, git.NotFound)) {
				t.Fatalf("unexpected lookup for %s after %v: %v", name, args, err)
			}
		}
		for _, name := range []string{"main", "tasks", "task/dev-70", "task/dev-7-unmerged", "task/dev-7-alias", "task/example-7", "other"} {
			if _, err := repo.Reference("refs/heads/" + name); err != nil {
				t.Fatalf("lost %s after %v: %v", name, args, err)
			}
		}
	}
	if out := run(t, root, "cleanup"); !strings.Contains(out, "Would remove branch task/dev-70\n") || strings.Contains(out, "task/example-7") {
		t.Fatal(out)
	}
}

func TestCleanupKeepsBranchesInUse(t *testing.T) {
	repo, root := fixture(t)
	for _, name := range []string{"task/example-7-current", "task/example-7-rebase", "task/example-7-rebase-update", "task/example-7-bisect", "task/example-7-unused", "task/example-8-running"} {
		systemGit(t, root, "branch", name)
	}
	// A task branch checked out at the primary worktree is never removable.
	systemGit(t, root, "switch", "task/example-7-current")
	directory, err := operationState(repo)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := taskLock(directory, "EXAMPLE", 8)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	worktree := filepath.Join(t.TempDir(), "detached")
	systemGit(t, root, "worktree", "add", "--detach", worktree, "main")
	gitDir := systemGit(t, worktree, "rev-parse", "--absolute-git-dir")
	rebase := filepath.Join(gitDir, "rebase-merge")
	if err := os.Mkdir(rebase, 0700); err != nil {
		t.Fatal(err)
	}
	writeSafetyFile(t, filepath.Join(rebase, "head-name"), "refs/heads/task/example-7-rebase\n")
	writeSafetyFile(t, filepath.Join(rebase, "update-refs"), "refs/heads/task/example-7-rebase-update\n"+systemGit(t, root, "rev-parse", "main")+"\n"+(git.ID{}).String()+"\n")
	writeSafetyFile(t, filepath.Join(gitDir, "BISECT_START"), "task/example-7-bisect\n")
	for _, apply := range []bool{false, true} {
		args, action := []string{"cleanup"}, "Would remove"
		if apply {
			args, action = append(args, "--apply"), "Removed"
		}
		out := run(t, root, args...)
		for _, want := range []string{
			"Keep branch task/example-7-current: still checked out",
			"Keep branch task/example-7-rebase: rebase",
			"Keep branch task/example-7-rebase-update: rebase",
			"Keep branch task/example-7-bisect: bisect",
			"Keep branch task/example-8-running:",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
			branch, _, _ := strings.Cut(strings.TrimPrefix(want, "Keep branch "), ":")
			if _, err := repo.Reference("refs/heads/" + branch); err != nil {
				t.Fatalf("lost branch %s: %v", branch, err)
			}
		}
		if !strings.Contains(out, action+" branch task/example-7-unused\n") {
			t.Fatal(out)
		}
	}
}

func TestCleanupBranchRechecksRefAndCheckout(t *testing.T) {
	repo, root := fixture(t)
	const name = "refs/heads/task/example-7"
	systemGit(t, root, "branch", "task/example-7")
	ref, err := repo.Reference(name)
	if err != nil {
		t.Fatal(err)
	}
	commitImplementation(t, root, "main.txt", "another merged commit\n")
	systemGit(t, root, "branch", "-f", "task/example-7", "main")
	var changed *git.ChangedError
	if err := cleanMergedBranch(repo, name, ref, nil, "main", true); !errors.As(err, &changed) {
		t.Fatalf("expected changed ref error, got %v", err)
	}
	ref, err = repo.Reference(name)
	if err != nil {
		t.Fatal(err)
	}
	systemGit(t, root, "worktree", "add", filepath.Join(t.TempDir(), "new"), "task/example-7")
	if err := cleanMergedBranch(repo, name, ref, nil, "main", true); err == nil || !strings.Contains(err.Error(), "still checked out") {
		t.Fatalf("expected refusal with new checkout, got %v", err)
	}
	if actual, err := repo.Reference(name); err != nil || actual != ref {
		t.Fatalf("branch changed: %+v, %v", actual, err)
	}
}
