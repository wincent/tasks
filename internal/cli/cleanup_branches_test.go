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

func TestCleanupLegacyRepositoryWithoutTaskStore(t *testing.T) {
	for _, mode := range []string{"absent", "configured remote", "tracking ref", "previous store", "obstructed checkout"} {
		t.Run(mode, func(t *testing.T) {
			repo, root := fixture(t)
			const branch = "task/example-7"
			storePath := filepath.Join(root, "tasks")
			switch mode {
			case "configured remote":
				// A remote restricted to main may still have task history.
				systemGit(t, root, "remote", "add", "-t", "main", "origin", "https://example.invalid/repo.git")
			case "tracking ref":
				systemGit(t, root, "update-ref", "refs/remotes/old/tasks", "tasks")
			case "previous store":
				run(t, root, "init")
				systemGit(t, root, "worktree", "remove", storePath)
			case "obstructed checkout":
				writeSafetyFile(t, storePath, "not a tasks checkout\n")
			}
			systemGit(t, root, "branch", "-D", "tasks")
			systemGit(t, root, "branch", branch)
			for _, apply := range []bool{false, true} {
				args := []string{"cleanup", "7"}
				want := "Keep branch " + branch
				if !apply && mode == "absent" {
					want = "Would remove branch " + branch
				}
				if apply {
					args = append(args, "--apply")
					if mode == "absent" {
						want = "Removed branch " + branch
					}
				}
				if out := run(t, root, args...); !strings.Contains(out, want) {
					t.Fatalf("%v: want %q, got %s", args, want, out)
				}
				_, err := repo.Reference("refs/heads/" + branch)
				if apply && mode == "absent" {
					if !git.HasCode(err, git.NotFound) {
						t.Fatalf("expected branch removal: %v", err)
					}
				} else if err != nil {
					t.Fatalf("lost branch: %v", err)
				}
			}
		})
	}
}

func TestCleanupKeepsLegacyTaskDestination(t *testing.T) {
	_, root := fixture(t)
	const parent = "task/example-2"
	const child = "task/example-1"
	ticket := run(t, root, "create", "Child", "--body", "Land on the legacy topic")
	run(t, root, "create", "Parent", "--body", "Land on main")
	path := strings.TrimSpace(ticket)
	metadata := filepath.Dir(path)
	content := strings.Replace(string(uiBytes(t, path)), "Status: open\n", "Status: open\nTopic: "+parent+"\n", 1)
	writeSafetyFile(t, path, content)
	systemGit(t, metadata, "add", "--", filepath.Base(path))
	systemGit(t, metadata, "commit", "-m", "record legacy topic")
	run(t, root, "archive", "1")

	systemGit(t, root, "switch", "-c", child)
	childTip := commitImplementation(t, root, "child.txt", "child changes\n")
	systemGit(t, root, "switch", "-c", parent, "main")
	systemGit(t, root, "merge", "--squash", child)
	systemGit(t, root, "commit", "-m", "squash child")
	parentTip := commitImplementation(t, root, "parent.txt", "parent changes\n")
	systemGit(t, root, "switch", "main")
	systemGit(t, root, "merge", "--squash", parent)
	systemGit(t, root, "commit", "-m", "squash parent")

	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(directory, "parent")
	systemGit(t, root, "worktree", "add", worktree, parent)
	for _, args := range [][]string{{"cleanup", "2"}, {"cleanup", "--apply"}} {
		out := run(t, root, args...)
		for _, label := range []string{worktree, "branch " + parent} {
			if !strings.Contains(out, "Keep "+label+": branch is the recorded destination for EXAMPLE-1") {
				t.Fatalf("%v did not protect %s:\n%s", args, label, out)
			}
		}
	}
	if got := systemGit(t, worktree, "rev-parse", "HEAD"); got != parentTip {
		t.Fatalf("parent checkout moved: %s", got)
	}
	if got := systemGit(t, root, "rev-parse", child); got != childTip {
		t.Fatalf("child branch moved: %s", got)
	}
}

func TestCleanupMatchesRewrittenCommitTrees(t *testing.T) {
	repo, root := fixture(t)
	const rewritten = "task/example-7-rewritten"
	const mode = "task/example-7-mode"
	const revert = "task/example-7-revert"
	base := systemGit(t, root, "rev-parse", "main")

	systemGit(t, root, "switch", "-c", rewritten)
	commitImplementation(t, root, "one.txt", "first task change\n")
	commitImplementation(t, root, "two.txt", "second task change\n")
	systemGit(t, root, "switch", "-c", mode)
	if err := os.Chmod(filepath.Join(root, "one.txt"), 0755); err != nil {
		t.Fatal(err)
	}
	systemGit(t, root, "update-index", "--chmod=+x", "one.txt")
	systemGit(t, root, "commit", "-m", "make one.txt executable")
	systemGit(t, root, "switch", "main")

	systemGit(t, root, "merge", "--squash", rewritten)
	systemGit(t, root, "commit", "-m", "squash the task changes")
	if got, want := systemGit(t, root, "rev-parse", "main^{tree}"), systemGit(t, root, "rev-parse", rewritten+"^{tree}"); got != want {
		t.Fatalf("squash tree %s does not match task tree %s", got, want)
	}
	main := commitImplementation(t, root, "later.txt", "destination moved on\n")
	systemGit(t, root, "switch", "-c", revert)
	systemGit(t, root, "restore", "--source="+base, "--staged", "--worktree", "--", ".")
	systemGit(t, root, "commit", "-m", "restore the old shared snapshot")
	systemGit(t, root, "switch", "main")

	for _, apply := range []bool{false, true} {
		args, action := []string{"cleanup", "7"}, "Would remove"
		if apply {
			args, action = append(args, "--apply"), "Removed"
		}
		out := run(t, root, args...)
		for _, want := range []string{
			action + " branch " + rewritten,
			"Keep branch " + mode + ": branch is not merged into local main",
			"Keep branch " + revert + ": branch is not merged into local main",
		} {
			if !strings.Contains(out, want+"\n") {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
		}
		_, err := repo.Reference("refs/heads/" + rewritten)
		if (!apply && err != nil) || (apply && !git.HasCode(err, git.NotFound)) {
			t.Fatalf("unexpected lookup for %s after %v: %v", rewritten, args, err)
		}
		for _, name := range []string{mode, revert} {
			if _, err := repo.Reference("refs/heads/" + name); err != nil {
				t.Fatalf("lost %s after %v: %v", name, args, err)
			}
		}
		if got := systemGit(t, root, "rev-parse", "main"); got != main {
			t.Fatalf("cleanup changed main from %s to %s", main, got)
		}
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

func TestCleanupUsesCurrentMergeResult(t *testing.T) {
	for _, scenario := range []string{"ancestral-revert", "historical-tree", "conflict", "ours-strategy", "custom-attribute"} {
		t.Run(scenario, func(t *testing.T) {
			_, root := fixture(t)
			const branch = "task/example-9"
			commitImplementation(t, root, "feature.txt", "base\n")
			systemGit(t, root, "branch", branch)
			switch scenario {
			case "historical-tree":
				commitImplementation(t, root, "feature.txt", "incoming\n")
				systemGit(t, root, "revert", "--no-edit", "HEAD")
			case "conflict":
				commitImplementation(t, root, "feature.txt", "destination\n")
			}
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			checkout := filepath.Join(dir, "task")
			systemGit(t, root, "worktree", "add", checkout, branch)
			systemGit(t, checkout, "commit", "--allow-empty", "-m", "distinct task history")
			tip := commitImplementation(t, checkout, "feature.txt", "incoming\n")
			switch scenario {
			case "ancestral-revert":
				systemGit(t, root, "merge", "--ff-only", branch)
				systemGit(t, root, "revert", "--no-edit", "HEAD")
			case "ours-strategy", "custom-attribute":
				systemGit(t, root, "cherry-pick", tip)
				commitImplementation(t, root, "unrelated.txt", "different tree\n")
				if scenario == "ours-strategy" {
					systemGit(t, root, "config", "branch.main.mergeOptions", "-s ours")
				} else {
					commitImplementation(t, root, ".gitattributes", "feature.txt merge=ours\n")
				}
			}

			for _, apply := range []bool{false, true} {
				args, action := []string{"cleanup", "9"}, "Would remove"
				if apply {
					args, action = append(args, "--apply"), "Removed"
				}
				if scenario != "ancestral-revert" {
					action = "Keep"
				}
				out := run(t, root, args...)
				for _, label := range []string{checkout, "branch " + branch} {
					if !strings.Contains(out, action+" "+label) {
						t.Fatalf("missing %s %s in %s", action, label, out)
					}
				}
			}
			if scenario != "ancestral-revert" {
				if got := systemGit(t, checkout, "rev-parse", "HEAD"); got != tip {
					t.Fatalf("checkout changed to %s", got)
				}
				if got := systemGit(t, root, "rev-parse", branch); got != tip {
					t.Fatalf("branch changed to %s", got)
				}
			} else if _, err := os.Stat(checkout); !os.IsNotExist(err) {
				t.Fatalf("checkout not removed: %v", err)
			}
		})
	}
}
