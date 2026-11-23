package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wincent/tasks/internal/adapters/signing"
	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "merge", min: 0, max: 1, numbered: true,
		options: map[string]optionKind{"--worktree": valueOption},
		usage:   "[NUMBER] [--worktree PATH]",
		summary: "Merge into the recorded topic without pushing.",
		details: `Omit NUMBER when running from the task branch. The task checkout must
be clean. The destination defaults to local main. After merging, the command
removes the checkout if it is safe.

If there is a conflict, merge the destination into the task branch, resolve
and commit there, then retry this command.

Options:
` + worktreeHelp,
		run: mergeTask,
	})
}

func mergeTask(ctx context.Context, cwd string, a arguments, streams IO) error {
	if _, supplied := a.values["--worktree"]; supplied && a.value("--worktree") == "" {
		return errors.New("--worktree requires a nonempty path")
	}
	repo, err := git.Open(cwd)
	if err != nil {
		return err
	}
	defer repo.Close()
	prefix, n := a.prefix, a.number()
	if n == 0 {
		head, err := repo.Head()
		if err != nil {
			return err
		}
		n = taskBranchNumber(prefix, head.Branch)
		if n == 0 {
			return errors.New("cannot infer task from current branch; use `tasks merge NUMBER`")
		}
	}
	if err := store.RequireLocalHistory(repo); err != nil {
		return err
	}
	if _, err := store.MetadataSignatures(repo); err != nil {
		return err
	}
	directory, err := operationState(repo)
	if err != nil {
		return err
	}
	lock, err := taskLock(directory, prefix, n)
	if err != nil {
		return err
	}
	defer lock.Close()
	return mergeWork(ctx, repo, directory, cwd, prefix, n, a.value("--worktree"), "", streams)
}

// The caller holds the task lock. Only the local merge (not the agent or
// development checks) needs serialization against other branch updates.
func mergeWork(ctx context.Context, repo *git.Repository, directory, cwd string, prefix store.Prefix, n int, selected string, launchedTarget destination, streams IO) error {
	task, err := readTask(ctx, repo, prefix, n)
	if err != nil {
		return err
	}
	if _, err := store.Status(task.Content); err != nil {
		return err
	}
	target, err := taskDestination(task.Content)
	if err != nil {
		return fmt.Errorf("%s: %w", task.ID(), err)
	}
	if launchedTarget != "" && launchedTarget != target {
		return fmt.Errorf("%s: topic changed during the run from %q to %q; review and retry with `tasks merge %d`", task.ID(), launchedTarget, target, n)
	}
	saved, err := readTaskSession(directory, n)
	if err != nil {
		return err
	}
	source, err := findCheckout(repo, cwd, prefix, n, selected, saved)
	if err != nil {
		return err
	}
	if source.Head.ID == (git.ID{}) {
		source.Head, err = retainedBranch(repo, prefix, n, saved)
		if err != nil {
			return err
		}
		if source.Head.ID == (git.ID{}) {
			return fmt.Errorf("no implementation branch for %s; use `tasks run %d`", task.ID(), n)
		}
	}
	if err := withMetadata(ctx, repo, func(session *store.Session) error {
		tasks, err := session.Preview(prefix)
		if err != nil {
			return err
		}
		defer tasks.Close()
		return tasks.RequireClean(filepath.Base(task.Path))
	}); err != nil {
		return err
	}

	lock, err := state.Acquire(ctx, filepath.Join(directory, "merge.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	base, err := target.tip(repo)
	if err != nil {
		return err
	}
	already, err := repo.IsAncestor(source.Head.ID, base)
	if err != nil {
		return err
	}
	if !already {
		p := terminalPresentation(streams.Err)
		p.printTaskHeadline(task, "🔄", p.paint("Merging", "1;36")+" ", fmt.Sprintf(" into local %s...", target))
		if err := publishMerge(ctx, repo, source, task, target, base, streams); err != nil {
			if saved == nil || (source.Paths.WorkDir != "" && source.Paths.WorkDir != saved.Worktree) {
				saved = &taskSession{Worktree: source.Paths.WorkDir, Branch: source.Head.Branch}
			}
			resume := fmt.Errorf("%s needs attention: %w\nRetry with `tasks merge %d`", task.ID(), err, n)
			if saved.Worktree != "" {
				resume = fmt.Errorf("%w\nCheckout: %s; session: %s", resume, saved.Worktree, saved.SessionFile)
				saved.Reason = shortResult(err.Error())
				resume = errors.Join(resume, writeTaskSession(directory, n, *saved))
			}
			if ctx.Err() == nil {
				resume = errors.Join(resume, setResult(ctx, repo, prefix, n, "blocked", ""))
			}
			return resume
		}
	}
	lock.Close()

	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := setResult(finish, repo, prefix, n, "merged", ""); err != nil {
		return fmt.Errorf("%s is in local %s, but recording the result failed; rerun `tasks merge %d`: %w", task.ID(), target, n, err)
	}
	if saved != nil && (saved.Branch == "" || saved.Branch == source.Head.Branch) {
		saved.Reason, saved.Revision = "", source.Head.ID.String()
		if err := writeTaskSession(directory, n, *saved); err != nil {
			fmt.Fprintf(streams.Err, "Could not record the merged tip locally: %v\n", err)
		}
	}
	p := terminalPresentation(streams.Out)
	p.printTaskHeadline(task, "✅", "", " "+p.paint(fmt.Sprintf("merged into local %s.", target), "1;32"))
	if saved != nil && saved.SessionFile != "" {
		fmt.Fprintf(streams.Out, "Session: %s\n", saved.SessionFile)
	}
	if source.Paths.WorkDir != "" {
		if err := cleanMerged(repo, source, cwd, prefix, target, true); err != nil {
			fmt.Fprintf(streams.Err, "Kept checkout %s: %v\n", source.Paths.WorkDir, err)
		}
	}
	return nil
}

func publishMerge(ctx context.Context, repo *git.Repository, source checkout, task store.Task, target destination, base git.ID, streams IO) error {
	if err := unchangedSource(source); err != nil {
		return err
	}
	destinationTree, err := destinationCheckout(repo, target)
	if err != nil {
		return err
	}
	var destinationRepo *git.Repository
	if destinationTree != nil {
		destinationRepo, err = openCheckout(*destinationTree)
		if err != nil {
			return err
		}
		defer destinationRepo.Close()
		if _, err := destinationChanges(destinationRepo, target); err != nil {
			return err
		}
	}
	ff, err := repo.IsAncestor(base, source.Head.ID)
	if err != nil {
		return err
	}
	config := repo
	if destinationRepo != nil {
		config = destinationRepo
	}
	ffSetting, err := config.Config("merge.ff")
	if err != nil && !git.HasCode(err, git.NotFound) {
		return err
	}
	if ffSetting == "only" && !ff {
		return fmt.Errorf("merge.ff=only: merge local %s into the task branch, then retry", target)
	}
	if ffSetting != "" && ffSetting != "only" {
		allowed, err := config.ConfigBool("merge.ff")
		if err != nil {
			return err
		}
		ff = ff && allowed
	}
	policy, err := signing.Load(config)
	if err != nil {
		return err
	}
	crypto := signing.Adapter{Runner: streams.Runner, Directory: configPath(config), Environment: os.Environ(), Diagnostic: streams.Err}
	if policy.Verify {
		sig, payload, err := repo.ExtractCommitSignature(source.Head.ID)
		if err == nil {
			err = crypto.VerifyMerge(ctx, policy, payload, sig)
		}
		if err != nil {
			return fmt.Errorf("verify incoming commit signature: %w", err)
		}
	}
	merged := source.Head.ID
	if !ff {
		merged, err = buildMerge(ctx, repo, source, task, target, base, policy, crypto, streams)
		if err != nil {
			return err
		}
	}
	actor, err := config.CommitterSignature()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := repo.Prepare(target.ref(), merged, map[string]git.ID{target.ref(): base, source.Head.Branch: source.Head.ID}, actor, "task merge")
	if err != nil {
		return err
	}
	defer tx.Close()
	if err := unchangedSource(source); err != nil {
		return err
	}
	current, err := destinationCheckout(repo, target)
	if err != nil {
		return err
	}
	if (destinationTree == nil) != (current == nil) || (destinationTree != nil && (*destinationTree != *current || current.Head.ID != base)) {
		return fmt.Errorf("%s checkout changed during merge; retry", target)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if destinationRepo != nil {
		staged, err := destinationChanges(destinationRepo, target)
		if err != nil {
			return err
		}
		if staged {
			// A previous process may have updated the index and worktree but
			// stopped before publishing the ref. Finish only if both already
			// match the exact result; never reset unexplained changes.
			matches, err := destinationRepo.IndexMatches(merged)
			if err != nil {
				return err
			}
			if !matches {
				return fmt.Errorf("worktree is dirty: %s has unrelated staged changes; commit or move them before merging", target)
			}
		} else if err := destinationRepo.Checkout(base, merged); err != nil {
			return fmt.Errorf("could not update %s checkout: %w", target, err)
		}
	}
	// Do not add a cancellation point between checkout and ref update.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("could not publish %s; inspect its checkout and ref before retrying: %w", target, err)
	}
	if destinationRepo != nil {
		if err := hook(ctx, destinationRepo, "post-merge", []string{"0"}, streams); err != nil {
			fmt.Fprintf(streams.Err, "Local %s was updated, but post-merge failed: %v\n", target, err)
		}
	}
	return nil
}

func configPath(repo *git.Repository) string {
	paths, _ := repo.Paths()
	return paths.WorkDir
}

func buildMerge(ctx context.Context, repo *git.Repository, source checkout, task store.Task, target destination, base git.ID, policy signing.Policy, crypto signing.Adapter, streams IO) (id git.ID, err error) {
	scratch, err := createDetachedCheckout(repo, "task-merge-", base)
	if err != nil {
		return id, err
	}
	defer func() {
		if e := repo.RemoveWorktree(scratch.Name, scratch.Paths, scratch.Head, false); e != nil {
			fmt.Fprintf(streams.Err, "Kept temporary merge checkout %s: %v\n", scratch.Paths.WorkDir, e)
		}
	}()
	merge, err := openCheckout(scratch)
	if err != nil {
		return id, err
	}
	defer merge.Close()
	if _, err := merge.MergeCleanIntoIndex(source.Head.ID); err != nil {
		if errors.Is(err, git.ErrMergeConflict) {
			where := "the task branch"
			if source.Paths.WorkDir != "" {
				where = source.Paths.WorkDir
			}
			return id, fmt.Errorf("merge conflict; %s is unchanged. In %s, merge %s, resolve and commit, then retry", target, where, target)
		}
		return id, err
	}
	if err := hook(ctx, merge, "pre-merge-commit", nil, streams); err != nil {
		return id, err
	}
	messagePath := filepath.Join(scratch.Paths.GitDir, "MERGE_MSG")
	if err := state.AtomicWrite(messagePath, []byte(fmt.Sprintf("Merge %s: %s\n", task.ID(), task.Title))); err != nil {
		return id, err
	}
	if err := hook(ctx, merge, "prepare-commit-msg", []string{messagePath, "merge"}, streams); err != nil {
		return id, err
	}
	if err := hook(ctx, merge, "commit-msg", []string{messagePath}, streams); err != nil {
		return id, err
	}
	message, err := os.ReadFile(messagePath)
	if err != nil {
		return id, err
	}
	tree, err := merge.IndexTree()
	if err != nil {
		return id, err
	}
	actors, err := merge.Signatures()
	if err != nil {
		return id, err
	}
	parents := []git.ID{base, source.Head.ID}
	if policy.Sign {
		payload, err := merge.CommitBuffer(tree, parents, actors.Author, actors.Committer, string(message))
		if err != nil {
			return id, err
		}
		signature, err := crypto.Sign(ctx, policy, payload, actors.Committer)
		if err != nil {
			return id, err
		}
		id, err = merge.WriteSignedCommit(payload, signature)
		if err != nil {
			return id, err
		}
	} else {
		id, err = merge.WriteCommit(tree, parents, actors.Author, actors.Committer, string(message))
		if err != nil {
			return id, err
		}
	}
	if err := ctx.Err(); err != nil {
		return id, err
	}
	tx, err := merge.Prepare("HEAD", id, map[string]git.ID{"HEAD": base}, actors.Committer, "prepare task merge")
	if err != nil {
		return id, err
	}
	defer tx.Close()
	if err := tx.Commit(); err != nil {
		return id, err
	}
	scratch.Head.ID = id
	if err := merge.CleanupOperation(); err != nil {
		return id, err
	}
	if err := requireClean(merge); err != nil {
		return id, err
	}
	return id, nil
}

func openCheckout(entry checkout) (*git.Repository, error) {
	repo, err := git.Open(entry.Paths.WorkDir)
	if err != nil {
		return nil, err
	}
	paths, err := repo.Paths()
	if err == nil && paths != entry.Paths {
		err = errors.New("checkout identity changed; retry")
	}
	if err != nil {
		repo.Close()
		return nil, err
	}
	return repo, nil
}

func unchangedSource(source checkout) error {
	if source.Paths.WorkDir == "" {
		return nil
	}
	repo, err := openCheckout(source)
	if err != nil {
		return err
	}
	defer repo.Close()
	head, err := repo.Head()
	if err != nil {
		return err
	}
	if head != source.Head {
		return errors.New("task branch changed during merge; retry")
	}
	return requireClean(repo)
}

func destinationChanges(repo *git.Repository, target destination) (staged bool, err error) {
	if err := requireInspectable(repo); err != nil {
		return false, err
	}
	changes, err := repo.Status(false)
	if err != nil {
		return false, err
	}
	const stagedOnly = git.IndexNew | git.IndexModified | git.IndexDeleted | git.IndexRenamed | git.IndexTypeChange
	for _, change := range changes {
		if change.Status&^stagedOnly != 0 {
			return false, fmt.Errorf("worktree is dirty: %s; commit or move changes before merging (%s)", target, git.DescribeChanges(changes))
		}
	}
	return len(changes) != 0, nil
}

func requireClean(repo *git.Repository) error {
	if err := repo.RequireClean(); err != nil {
		return err
	}
	return requireInspectable(repo)
}

func requireInspectable(repo *git.Repository) error {
	if err := repo.RequireIdle(); err != nil {
		return err
	}
	index, err := repo.IndexEntries()
	if err != nil {
		return err
	}
	for _, entry := range index {
		if entry.AssumeValid {
			return fmt.Errorf("cannot verify worktree with assume-unchanged index entry %q", entry.Path)
		}
	}
	return nil
}
