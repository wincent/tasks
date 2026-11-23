package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "cleanup", min: 0, max: 1, numbered: true,
		autoCorrect: func(a arguments) bool { return !a.flags["--apply"] },
		options:     map[string]optionKind{"--apply": flagOption},
		usage:       "[NUMBER] [--apply]",
		summary:     "Preview removal of merged task checkouts and branches.",
		details: `Omit NUMBER to consider all tasks. Run from outside any checkout you want
to remove. The recorded topic (local main by default) must contain the task
tip or an identical tracked snapshot in history not shared by the task branch.
This applies even to archived tasks. Missing topics, branches in use, and saved
sessions are kept.

Options:
  --apply  Remove eligible branches and checkouts (including ignored files).`,
		run: cleanupTask,
	})
}

func cleanupTask(ctx context.Context, cwd string, a arguments, streams IO) error {
	repo, err := git.Open(cwd)
	if err != nil {
		return err
	}
	defer repo.Close()
	paths, err := repo.Paths()
	if err != nil {
		return err
	}
	apply := a.flags["--apply"]
	directory := stateDirectory(paths)
	if apply {
		directory, err = operationState(repo)
		if err != nil {
			return err
		}
	}
	entries, err := checkouts(repo)
	if err != nil {
		return err
	}
	refs, err := repo.References("refs/heads/task/" + strings.ToLower(string(a.prefix)) + "-")
	if err != nil {
		return err
	}
	matches := func(n int) bool { return n != 0 && (a.number() == 0 || a.number() == n) }
	// Check destinations across all tickets, even for numbered cleanup.
	var protected map[string]string
	var protectErr error
	if len(refs) != 0 {
		protected, protectErr = cleanupProtectedTopics(repo, a.prefix)
	}
	checkTopicUse := func(ref string) error {
		if protectErr != nil {
			return fmt.Errorf("cannot verify task destinations: %w", protectErr)
		}
		if id := protected[strings.ToLower(ref)]; id != "" {
			return fmt.Errorf("branch is the recorded destination for %s; retarget that task first", id)
		}
		return nil
	}
	attempt := func(n int, label string, clean func() error) bool {
		err := func() error {
			if apply {
				lock, err := taskLock(directory, a.prefix, n)
				if err != nil {
					return err
				}
				defer lock.Close()
			} else {
				held, err := lockHeld(taskLockPath(directory, a.prefix, n))
				if err != nil {
					return err
				}
				if held {
					return errors.New("task is running")
				}
			}
			return clean()
		}()
		switch {
		case err != nil:
			fmt.Fprintf(streams.Out, "Keep %s: %v\n", label, err)
		case apply:
			fmt.Fprintln(streams.Out, "Removed", label)
		default:
			fmt.Fprintln(streams.Out, "Would remove", label)
		}
		return err == nil
	}
	// In preview, simulate removal only for checkouts that passed all checks.
	// Apply must instead inspect actual registrations before deleting a ref.
	retained := make([]checkout, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := taskBranchNumber(a.prefix, entry.Head.Branch)
		if !matches(n) || entry.Name == "" || !attempt(n, entry.Paths.WorkDir, func() error {
			if err := checkTopicUse(entry.Head.Branch); err != nil {
				return err
			}
			target, err := cleanupDestination(repo, a.prefix, n)
			if err != nil {
				return err
			}
			return cleanMerged(repo, entry, cwd, a.prefix, target, apply)
		}) {
			retained = append(retained, entry)
		}
	}
	branches := make([]string, 0, len(refs))
	for name := range refs {
		if matches(taskBranchNumber(a.prefix, name)) {
			branches = append(branches, name)
		}
	}
	slices.Sort(branches)
	for _, name := range branches {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := taskBranchNumber(a.prefix, name)
		attempt(n, "branch "+strings.TrimPrefix(name, "refs/heads/"), func() error {
			if err := checkTopicUse(name); err != nil {
				return err
			}
			target, err := cleanupDestination(repo, a.prefix, n)
			if err != nil {
				return err
			}
			return cleanMergedBranch(repo, name, refs[name], retained, target, apply)
		})
	}
	return nil
}

func cleanupProtectedTopics(repo *git.Repository, prefix store.Prefix) (map[string]string, error) {
	tasks, err := store.Inspect(repo, prefix)
	if err != nil {
		return nil, err
	}
	defer tasks.Close()
	topics, err := tasks.RecordedTopics()
	if err != nil {
		return nil, err
	}
	protected := make(map[string]string)
	for topic, id := range topics {
		protected[strings.ToLower(destination(topic).ref())] = id
	}
	return protected, nil
}

func cleanupDestination(repo *git.Repository, prefix store.Prefix, n int) (destination, error) {
	tasks, err := store.Inspect(repo, prefix)
	if err != nil {
		return "", err
	}
	defer tasks.Close()
	task, err := tasks.Read(n)
	if errors.Is(err, store.ErrTaskNotFound) {
		// Keep supporting legacy branches with no task record.
		return "main", nil
	}
	if err != nil {
		return "", err
	}
	if err := tasks.RequireClean(tasks.RelativePath(task)); err != nil {
		return "", err
	}
	return taskDestination(task.Content)
}

// Eligibility uses actual Git history, not the status written in Markdown.
// The caller holds the task lock whenever it requests removal.
func cleanMerged(repo *git.Repository, entry checkout, cwd string, prefix store.Prefix, dest destination, apply bool) error {
	if entry.Name == "" || entry.Locked || entry.Invalid != "" || taskBranchNumber(prefix, entry.Head.Branch) == 0 {
		return errors.New("not an unlocked linked task checkout")
	}
	target, err := resolvePath(entry.Paths.WorkDir)
	if err != nil {
		return err
	}
	caller, err := resolvePath(cwd)
	if err != nil {
		return err
	}
	if inside(caller, target) {
		return errors.New("current directory is inside this checkout; run `tasks cleanup` elsewhere")
	}
	base, err := dest.tip(repo)
	if err != nil {
		return err
	}
	ok, err := repo.ContainsCommitOrTree(entry.Head.ID, base)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("branch is not merged into local %s", dest)
	}
	present, err := openCheckout(entry)
	if err != nil {
		return err
	}
	defer present.Close()
	head, err := present.Head()
	if err != nil {
		return err
	}
	if head != entry.Head {
		return errors.New("task checkout has changed")
	}
	if err := requireClean(present); err != nil {
		return err
	}
	index, err := present.IndexEntries()
	if err != nil {
		return err
	}
	for _, file := range index {
		if file.SkipWorktree {
			return errors.New("index has skip-worktree entries")
		}
		if file.Mode == 0160000 {
			return errors.New("checkout contains a submodule")
		}
	}
	trees, err := repo.Worktrees()
	if err != nil {
		return err
	}
	for _, tree := range trees {
		if tree.Name != entry.Name && (inside(tree.Path, target) || inside(tree.Path, entry.Paths.WorkDir)) {
			return errors.New("checkout contains another registered worktree")
		}
	}
	if err := filepath.WalkDir(target, func(path string, item fs.DirEntry, readErr error) error {
		if readErr != nil {
			return readErr
		}
		if item.Name() != ".git" {
			return nil
		}
		if filepath.Dir(path) != target {
			return fmt.Errorf("nested Git repository at %s", filepath.Dir(path))
		}
		if item.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}); err != nil {
		return err
	}
	if !apply {
		return nil
	}

	// Serialize removal with tasks merges. RemoveWorktree checks the exact
	// registration, HEAD and cleanliness again before pruning.
	directory, err := operationState(repo)
	if err != nil {
		return err
	}
	lock, err := state.TryLock(filepath.Join(directory, "merge.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	base, err = dest.tip(repo)
	if err != nil {
		return err
	}
	ok, err = repo.ContainsCommitOrTree(entry.Head.ID, base)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("local %s changed; branch is no longer merged", dest)
	}
	return repo.RemoveWorktree(entry.Name, entry.Paths, entry.Head, true)
}
