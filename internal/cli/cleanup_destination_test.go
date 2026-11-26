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
	t.Parallel()
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean", true: "dirty"}[dirty], func(t *testing.T) {
			t.Parallel()
			_, root := fixture(t)
			const branch = "task/example-1"
			run(t, root, "create", "Parent", "--body", "Merge into main")
			path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Uses a legacy destination"))
			content := string(uiBytes(t, path))
			writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
			commitFixtureFiles(t, filepath.Dir(path), "record a dependent topic", filepath.Base(path))

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
	t.Parallel()
	for _, removal := range []string{"worktree", "branch"} {
		t.Run(removal, func(t *testing.T) {
			t.Parallel()
			repo, root := fixture(t)
			const branch = "task/example-1"
			const refName = "refs/heads/" + branch
			ticket := strings.TrimSpace(run(t, root, "create", "Land on a topic", "--topic", "before", "--body", "Implementation"))
			setRef(t, repo, "refs/heads/after", fixtureRevision(t, repo, mainRef), git.ID{})
			tip := commitImplementation(t, root, "feature.txt", "task work\n")
			setRef(t, repo, "refs/heads/before", tip, git.ID{})
			setRef(t, repo, refName, tip, git.ID{})

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
			target, err := cleanupDestination(repo, "EXAMPLE", "main", 1)
			if err != nil || target.destination != "before" {
				t.Fatalf("expected old topic, got %q: %v", target.destination, err)
			}

			// Direct Git commits do not honor the task lock.
			writeSafetyFile(t, ticket, strings.Replace(string(uiBytes(t, ticket)), "Topic: before\n", "Topic: after\n", 1))
			systemGit(t, filepath.Dir(ticket), "commit", "-am", "retarget task")
			if removal == "worktree" {
				err = cleanMerged(repo, entry, root, "EXAMPLE", "main", target, true)
			} else {
				err = cleanMergedBranch(repo, refName, ref, nil, "EXAMPLE", "main", target, true, nil)
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
	t.Parallel()
	repo, root := fixture(t)

	const branch = "task/example-2"
	path := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Will use a legacy destination"))
	content := string(uiBytes(t, path))
	writeSafetyFile(t, path, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+branch+"\n", 1))
	setRef(t, repo, "refs/heads/"+branch, fixtureRevision(t, repo, mainRef), git.ID{})
	if out := run(t, root, "cleanup", "--apply"); !strings.Contains(out, "recorded destination for EXAMPLE-1") {
		t.Fatal(out)
	}
}

func TestCleanupPreviewReusesDestinationsAcrossMultipleBranches(t *testing.T) {
	t.Parallel()
	_, root := fixture(t)
	const protected = "task/example-1-protected"
	const removableA = "task/example-2-first"
	const removableB = "task/example-2-second"
	const unmerged = "task/example-4-unmerged"

	run(t, root, "create", "Parent", "--body", "Protected by child topic")
	run(t, root, "create", "Removable", "--body", "Merged into main")
	childPath := strings.TrimSpace(run(t, root, "create", "Child", "--body", "Uses protected branch"))
	run(t, root, "create", "Pending", "--body", "Has unmerged commits")
	content := string(uiBytes(t, childPath))
	writeSafetyFile(t, childPath, strings.Replace(content, "Status: open\n", "Status: open\nTopic: "+protected+"\n", 1))
	metadata := filepath.Dir(childPath)
	systemGit(t, metadata, "add", "--", filepath.Base(childPath))
	systemGit(t, metadata, "commit", "-m", "record protected destination")

	systemGit(t, root, "branch", protected)
	systemGit(t, root, "branch", removableA)
	systemGit(t, root, "branch", removableB)
	systemGit(t, root, "switch", "-c", unmerged)
	commitImplementation(t, root, "unmerged.txt", "pending work\n")
	systemGit(t, root, "switch", "main")

	worktreeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(worktreeDir, "example-2")
	systemGit(t, root, "worktree", "add", wt, removableA)

	out := run(t, root, "cleanup")
	for _, want := range []string{
		"Would remove " + wt,
		"Keep branch " + protected + ": branch is the recorded destination for EXAMPLE-3; retarget that task first",
		"Would remove branch " + removableA,
		"Would remove branch " + removableB,
		"Keep branch " + unmerged + ": branch is not merged into local main",
	} {
		if !strings.Contains(out, want+"\n") {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
