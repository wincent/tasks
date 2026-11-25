package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
be clean. The destination defaults to the configured default_target_branch
(local main by default). After merging, the command removes the checkout if
it is safe.

If there is a conflict, merge the destination into the task branch, resolve
and commit there, then retry this command.

Options:
` + worktreeHelp,
		run: mergeTask,
	})
}

func mergeTask(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
	if _, supplied := a.values["--worktree"]; supplied && a.value("--worktree") == "" {
		return errors.New("--worktree requires a nonempty path")
	}
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
	return mergeWork(ctx, repo, directory, cwd, prefix, a.defaultTarget, n, a.value("--worktree"), "", streams)
}

// The caller holds the task lock. Only the local merge (not the agent or
// development checks) needs serialization against other branch updates.
func mergeWork(ctx context.Context, repo *git.Repository, directory, cwd string, prefix store.Prefix, defaultTarget destination, n int, selected string, launchedTarget destination, streams IO) error {
	task, err := readTask(ctx, repo, prefix, n)
	if err != nil {
		return err
	}
	if _, err := store.Status(task.Content); err != nil {
		return err
	}
	target, err := taskDestination(task.Content, defaultTarget)
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
	if _, err := confirmMergeDestination(ctx, repo, prefix, defaultTarget, n, target); err != nil {
		return err
	}

	lock, err := state.Acquire(ctx, filepath.Join(directory, "merge.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	base, err := target.tip(repo, defaultTarget)
	if err != nil {
		return err
	}
	already, err := repo.IsAncestor(source.Head.ID, base)
	if err != nil {
		return err
	}
	if !already {
		p := streams.presentation(streams.Err)
		p.printTaskHeadline(task, "🔄", p.paint("Merging", "1;36")+" ", fmt.Sprintf(" into local %s...", target))
		if err := publishMerge(ctx, repo, source, task, prefix, defaultTarget, target, base, streams); err != nil {
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
				resume = errors.Join(resume, setResultForTopic(ctx, repo, prefix, defaultTarget, n, "blocked", "", target))
			}
			return resume
		}
	}
	lock.Close()

	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := setResultForTopic(finish, repo, prefix, defaultTarget, n, "merged", "", target); err != nil {
		return fmt.Errorf("%s is in local %s, but recording the result failed; rerun `tasks merge %d`: %w", task.ID(), target, n, err)
	}
	if saved != nil && (saved.Branch == "" || saved.Branch == source.Head.Branch) {
		saved.Reason, saved.Revision = "", source.Head.ID.String()
		if err := writeTaskSession(directory, n, *saved); err != nil {
			fmt.Fprintf(streams.Err, "Could not record the merged tip locally: %v\n", err)
		}
	}
	p := streams.presentation(streams.Out)
	p.printTaskHeadline(task, "✅", "", " "+p.paint(fmt.Sprintf("merged into local %s.", target), "1;32"))
	if saved != nil && saved.SessionFile != "" {
		fmt.Fprintf(streams.Out, "Session: %s\n", saved.SessionFile)
	}
	if source.Paths.WorkDir != "" {
		cleanup, err := cleanupDestination(repo, prefix, defaultTarget, n)
		if err == nil {
			if cleanup.destination != target {
				err = fmt.Errorf("topic changed from %q to %q; review and retry", target, cleanup.destination)
			} else {
				err = cleanMerged(repo, source, cwd, prefix, defaultTarget, cleanup, true)
			}
		}
		if err != nil {
			fmt.Fprintf(streams.Err, "Kept checkout %s: %v\n", source.Paths.WorkDir, err)
		}
	}
	return nil
}

func confirmMergeDestination(ctx context.Context, repo *git.Repository, prefix store.Prefix, defaultTarget destination, n int, target destination) (git.ID, error) {
	var confirmed git.ID
	err := withMetadata(ctx, repo, func(session *store.Session) error {
		tasks, err := session.Preview(prefix)
		if err != nil {
			return err
		}
		defer tasks.Close()
		head, err := tasks.Head()
		if err != nil {
			return err
		}
		task, err := tasks.ReadWorking(n)
		if err != nil {
			return err
		}
		if err := requireTaskDestination(task, defaultTarget, target); err != nil {
			return err
		}
		if err := tasks.RequireClean(tasks.RelativePath(task)); err != nil {
			return err
		}
		current, err := tasks.Head()
		if err != nil {
			return err
		}
		if head.Branch != store.Branch || head != current {
			return errors.New("task metadata changed during destination confirmation; retry")
		}
		confirmed = head.ID
		return nil
	})
	return confirmed, err
}

func prepareMergePublication(repo *git.Repository, target destination, merged, base, metadata git.ID, source git.Head, actor git.Signature) (*git.Transaction, error) {
	return repo.Prepare(target.ref(), merged, map[string]git.ID{
		target.ref(): base, source.Branch: source.ID, store.Branch: metadata,
	}, actor, "task merge")
}

func publishMerge(ctx context.Context, repo *git.Repository, source checkout, task store.Task, prefix store.Prefix, defaultTarget, target destination, base git.ID, streams IO) error {
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
	var entries []git.ConfigurationEntry
	if destinationRepo != nil {
		config = destinationRepo
		entries, err = config.ConfigurationEntries()
	} else {
		entries, err = repo.BranchConfigurationEntries(target.ref())
	}
	if err != nil {
		return fmt.Errorf("read merge configuration for %s: %w", target, err)
	}
	for _, entry := range slices.Backward(entries) {
		if entry.Name != "merge.ff" {
			continue
		}
		switch entry.Value {
		case "only":
			if !ff {
				return fmt.Errorf("merge.ff=only: merge local %s into the task branch, then retry", target)
			}
		case "":
			// Preserve the default for a missing or empty setting.
		default:
			allowed, err := git.ConfigurationBool(entry)
			if err != nil {
				return fmt.Errorf("merge.ff: %w", err)
			}
			ff = ff && allowed
		}
		break
	}
	policy, err := signing.ParsePolicy(entries)
	if err != nil {
		return fmt.Errorf("read signature policy for %s: %w", target, err)
	}
	paths, err := config.Paths()
	if err != nil {
		return err
	}
	cryptoDir := paths.WorkDir
	if destinationRepo == nil {
		// Without a destination worktree, do not resolve relative crypto paths
		// within whichever checkout the caller happened to use.
		cryptoDir = paths.CommonDir
	}
	crypto := signing.Adapter{Runner: streams.Runner, Directory: cryptoDir, Environment: streams.environ(), Diagnostic: streams.Err}
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
	// Hooks and validators can take time or use Git directly. Check the
	// record again before publishing, even though tasks edit takes our lock.
	metadata, err := confirmMergeDestination(ctx, repo, prefix, defaultTarget, task.Number, target)
	if err != nil {
		return err
	}
	// A raw commit to the metadata branch bypasses the task and metadata
	// locks. Verify and hold the confirmed ref through publication.
	tx, err := prepareMergePublication(repo, target, merged, base, metadata, source.Head, actor)
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
