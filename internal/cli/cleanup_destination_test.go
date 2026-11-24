package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
)

func TestMergeKeepsCheckoutUsedAsRecordedDestination(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			_, root := fixture(t)
			const branch = "task/example-1"
			run(t, root, "create", "Parent", "--body", "Merge into main")
			path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Uses a legacy destination"))
			content := string(uiBytes(t, path))
			writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
			metadata := filepath.Dir(path)
			systemGit(t, metadata, "add", "--", filepath.Base(path))
			systemGit(t, metadata, "commit", "-m", "record a dependent topic")
			if dirty {
				writeSafetyFile(t, path, content)
			}

			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			checkout := filepath.Join(dir, "task")
			systemGit(t, root, "worktree", "add", "-b", branch, checkout, "main")
			commitImplementation(t, checkout, ".gitignore", "ignored.txt\n")
			ignored := filepath.Join(checkout, "ignored.txt")
			writeSafetyFile(t, ignored, "preserve me\n")
			out, diagnostic, err := invoke(root, "", "merge", "1")
			if err != nil || !strings.Contains(out, "merged into local main") ||
				!strings.Contains(diagnostic, "recorded destination for EXAMPLE-2") {
				t.Fatalf("merge: %v\n%s\n%s", err, out, diagnostic)
			}
			if got := string(uiBytes(t, ignored)); got != "preserve me\n" {
				t.Fatal(got)
			}
		})
	}
}

func TestCleanupRejectsRetargetAfterDestinationSelection(t *testing.T) {
	for _, removal := range []string{"worktree", "branch"} {
		t.Run(removal, func(t *testing.T) {
			repo, root := fixture(t)
			const branch = "task/example-1"
			const refName = "refs/heads/" + branch
			ticket := strings.TrimSpace(run(t, root, "create", "Land on a topic", "--topic", "before", "--body", "Implementation"))
			systemGit(t, root, "branch", "after")
			commitImplementation(t, root, "feature.txt", "task work\n")
			systemGit(t, root, "branch", "before")
			systemGit(t, root, "branch", branch)
			ref, err := repo.Reference(refName)
			if err != nil {
				t.Fatal(err)
			}

			var entry checkout
			if removal == "worktree" {
				path := filepath.Join(t.TempDir(), "task")
				systemGit(t, root, "worktree", "add", path, branch)
				entry, err = findCheckout(repo, root, "EXAMPLE", 1, path, nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			directory, err := operationState(repo)
			if err != nil {
				t.Fatal(err)
			}
			lock, err := taskLock(directory, "EXAMPLE", 1)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			target, err := cleanupDestination(repo, "EXAMPLE", 1)
			if err != nil || target.destination != "before" {
				t.Fatalf("expected old topic, got %q: %v", target.destination, err)
			}

			// Direct Git commits do not honor the task lock.
			writeSafetyFile(t, ticket, strings.Replace(string(uiBytes(t, ticket)), "Topic: before\n", "Topic: after\n", 1))
			systemGit(t, filepath.Dir(ticket), "commit", "-am", "retarget task")
			if removal == "worktree" {
				err = cleanMerged(repo, entry, root, "EXAMPLE", target, true)
			} else {
				err = cleanMergedBranch(repo, refName, ref, nil, "EXAMPLE", target, true)
			}
			var changed *git.ChangedError
			if !errors.As(err, &changed) || changed.Name != store.Branch || changed.Expected != target.metadata {
				t.Errorf("expected rejection of stale metadata, got %v", err)
			}
			if actual, err := repo.Reference(refName); err != nil || actual != ref {
				t.Errorf("branch changed: %+v, %v", actual, err)
			}
			if removal == "worktree" {
				if got := string(uiBytes(t, filepath.Join(entry.Paths.WorkDir, "feature.txt"))); got != "task work\n" {
					t.Errorf("worktree content changed: %q", got)
				}
			}
		})
	}
}

func TestCleanupProtectsWorkingOnlyDestination(t *testing.T) {
	_, root := fixture(t)
	const branch = "task/example-2"
	path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Will use a legacy destination"))
	content := string(uiBytes(t, path))
	writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
	systemGit(t, root, "branch", branch)
	if out := run(t, root, "cleanup", "--apply"); !strings.Contains(out, "recorded destination for EXAMPLE-1") {
		t.Fatal(out)
	}
}
