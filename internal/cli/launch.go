package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/terminal"
)

type exitError struct {
	Code int
	Err  error
}

func (err *exitError) Error() string {
	if err.Err != nil {
		return err.Err.Error()
	}
	return fmt.Sprintf("Pi exited with status %d", err.Code)
}
func (err *exitError) Unwrap() error { return err.Err }
func ExitCode(err error) int {
	var usage *usageError
	if errors.As(err, &usage) {
		return 2
	}
	var exit *exitError
	if errors.As(err, &exit) && exit.Code != 0 {
		if exit.Code < 0 {
			return 128 - exit.Code
		}
		return exit.Code
	}
	return 1
}

func readTask(ctx context.Context, repo *git.Repository, prefix store.Prefix, n int) (task store.Task, err error) {
	err = withMetadata(ctx, repo, func(session *store.Session) error {
		tasks, err := session.Preview(prefix)
		if err != nil {
			return err
		}
		defer tasks.Close()
		task, err = tasks.ReadWorking(n)
		return err
	})
	return task, err
}

func setResult(ctx context.Context, repo *git.Repository, prefix store.Prefix, n int, status, note string) error {
	return setResultForTopic(ctx, repo, prefix, n, status, note, "")
}

func setResultForTopic(ctx context.Context, repo *git.Repository, prefix store.Prefix, n int, status, note string, topic destination) error {
	return withMetadata(ctx, repo, func(session *store.Session) error {
		_, err := updateTask(session, prefix, n, status, "Run result", note, "record "+status+" for", topic)
		return err
	})
}

func launchTask(ctx context.Context, cwd string, a arguments, streams IO) error {
	prefix, n := a.prefix, a.number()
	interactive := a.action == "pi"
	if interactive && (!terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Out)) {
		return errors.New("`tasks pi` requires a terminal; use `tasks run` for headless work")
	}
	if _, supplied := a.values["--worktree"]; supplied && a.value("--worktree") == "" {
		return errors.New("--worktree requires a nonempty path")
	}
	repo, err := git.Open(cwd)
	if err != nil {
		return err
	}
	defer repo.Close()
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
	task, err := readTask(ctx, repo, prefix, n)
	if err != nil {
		return err
	}
	task.Status, err = store.Status(task.Content)
	if err != nil {
		return err
	}
	target, err := taskDestination(task.Content)
	if err != nil {
		return fmt.Errorf("%s: %w", task.ID(), err)
	}
	executable, err := exec.LookPath("pi")
	if err != nil {
		return errors.New("pi executable not found on PATH")
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	saved, err := readTaskSession(directory, n)
	if err != nil {
		return err
	}
	source, err := findCheckout(repo, cwd, prefix, n, a.value("--worktree"), saved)
	if err != nil {
		return err
	}
	var previous git.Head
	if source.Paths.WorkDir == "" {
		previous, err = retainedBranch(repo, prefix, n, saved)
		if err != nil {
			return err
		}
	}
	fresh := source.Paths.WorkDir == "" && previous.Branch == "" && saved == nil
	base, err := launchDestination(ctx, repo, directory, target, fresh)
	if err != nil {
		return err
	}
	if source.Paths.WorkDir == "" {
		if previous.Branch != "" && target == "main" {
			// Preserve the default workflow: after merging into main,
			// a new checkout starts from the latest main.
			merged, err := repo.IsAncestor(previous.ID, base)
			if err != nil {
				return err
			}
			if merged {
				previous = git.Head{}
			}
		}
		if previous.Branch != "" {
			base = previous.ID
		}
		source, err = createCheckout(repo, branchStem(prefix, n)+"-", base, previous.Branch)
		if err != nil {
			return err
		}
		if err := checkoutHook(ctx, source, base, streams); err != nil {
			return launchBlocked(repo, directory, prefix, n, taskSession{Worktree: source.Paths.WorkDir, Branch: source.Head.Branch}, err)
		}
	}
	root := source.Paths.WorkDir
	if source.Name == "" {
		return fmt.Errorf("%s is the primary checkout; put the task branch in a linked worktree to run Pi", root)
	}
	// The Pi wrapper scopes the sandbox to its launch directory. Start it in the
	// repository container so it can reach Git data and legacy transcripts;
	// the session header still sets Pi's working directory to the task checkout.
	scope, err := resolvePath(filepath.Dir(filepath.Dir(directory)))
	if err != nil {
		return fmt.Errorf("resolve repository container: %w", err)
	}
	if !inside(root, scope) {
		return fmt.Errorf("task checkout %s is outside repository container %s; move the checkout into the container before launching Pi", root, scope)
	}
	record, err := prepareSession(directory, scope, root, source.Head.Branch, n, saved)
	if err != nil {
		return err
	}
	runLock, err := state.TryLock(runLockPath(directory, n))
	if err != nil {
		return err
	}
	defer runLock.Close()
	loan, err := lock.Lend()
	if err != nil {
		return err
	}
	defer loan.Close()
	live, err := runLock.Lend()
	if err != nil {
		return err
	}
	defer live.Close()
	if err := setResult(ctx, repo, prefix, n, "active", ""); err != nil {
		return err
	}
	if err := writeTaskSession(directory, n, record); err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}

	diagnostics := terminalPresentation(streams.Err)
	fmt.Fprintf(streams.Err, "%s\nTopic: %s\nCheckout: %s\nSession: %s\n", diagnostics.task(task.ID(), task.Title, ": "), target, root, record.SessionFile)
	prompt := fmt.Sprintf("Read %s at %q. Work only in this checkout. Implement the task, run proportionate checks, commit your changes, and summarize what changed and which checks ran. If no changes are needed, explain why. If you cannot finish, start your final response with '%s' and explain what remains. Do not merge; the launcher handles task status and merging.", task.ID(), task.Path, attentionLabel)
	if hint := a.value("--prompt"); hint != "" {
		prompt += "\n\nAdditional instructions for this run:\n" + hint
	}
	argv := []string{executable, "--session", record.SessionFile}
	progress := &piProgress{out: streams.Out}
	child := process.Command{Directory: scope, Environment: os.Environ(), Out: streams.Out, Err: streams.Err, ExtraFiles: []*os.File{loan, live}}
	if interactive {
		child.In = streams.In
	} else {
		argv = append(argv, "--mode", "json")
		child.Out, child.Group, child.RequireGroupExit = progress, true, true
	}
	child.Args = append(argv, "--", prompt)
	runner := streams.Runner
	if runner == nil {
		runner = process.Native{}
	}
	result := runner.Run(ctx, child)
	live.Close()
	runLock.Close()
	switch {
	case ctx.Err() != nil:
		return launchBlocked(repo, directory, prefix, n, record, fmt.Errorf("Pi interrupted: %w", ctx.Err()))
	case result.Code != 0 || result.Err != nil:
		return launchBlocked(repo, directory, prefix, n, record, &exitError{Code: result.Code, Err: result.Err})
	case progress.failure != "":
		return launchBlocked(repo, directory, prefix, n, record, errors.New(progress.failure))
	case needsAttention(progress.summary):
		return launchBlocked(repo, directory, prefix, n, record, errors.New(progress.summary))
	}

	opened, err := openCheckout(source)
	if err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	defer opened.Close()
	head, err := opened.Head()
	if err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	if head.Branch != source.Head.Branch {
		return launchBlocked(repo, directory, prefix, n, record, errors.New("Pi changed the task branch; switch back to the task branch and inspect the checkout"))
	}
	record.Revision, record.Summary = head.ID.String(), shortResult(progress.summary)
	if err := requireClean(opened); err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	if err := writeTaskSession(directory, n, record); err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	base, err = target.tip(repo)
	if err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	included, err := repo.IsAncestor(head.ID, base)
	if err != nil {
		return launchBlocked(repo, directory, prefix, n, record, err)
	}
	p := terminalPresentation(streams.Out)
	if included && head.ID == source.Head.ID {
		if interactive && (task.Status == "open" || task.Status == "blocked" || task.Status == "active") {
			return launchBlocked(repo, directory, prefix, n, record, errors.New("Pi exited without implementation commits; run again to continue, or close if no changes are needed"))
		}
		note := record.Summary
		if note == "" {
			note = "Pi exited successfully without new implementation changes."
		}
		if err := setResult(ctx, repo, prefix, n, "closed", note); err != nil {
			return err
		}
		p.printTaskHeadline(task, "✅", "", " "+p.paint("closed without changes", "1;32")+".")
		fmt.Fprintf(streams.Out, "Checkout: %s; session: %s\n", root, record.SessionFile)
		return nil
	}
	if interactive || a.flags["--no-merge"] {
		if err := setResult(ctx, repo, prefix, n, "done", ""); err != nil {
			return err
		}
		p.printTaskHeadline(task, "✅", "", " "+p.paint("ready for review", "1;32")+".")
		fmt.Fprintf(streams.Out, "Checkout: %s; session: %s\n", root, record.SessionFile)
		return nil
	}
	return mergeWork(ctx, repo, directory, cwd, prefix, n, root, target, streams)
}

func launchBlocked(repo *git.Repository, directory string, prefix store.Prefix, n int, record taskSession, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	record.Reason = shortResult(cause.Error())
	return errors.Join(fmt.Errorf("%s needs attention: %w\nCheckout: %s; session: %s\nContinue with `tasks run %d`", prefix.ID(n), cause, record.Worktree, record.SessionFile, n),
		writeTaskSession(directory, n, record), setResult(ctx, repo, prefix, n, "blocked", ""))
}

func checkoutHook(ctx context.Context, source checkout, base git.ID, streams IO) error {
	repo, err := openCheckout(source)
	if err != nil {
		return err
	}
	defer repo.Close()
	return hook(ctx, repo, "post-checkout", []string{(git.ID{}).String(), base.String(), "1"}, streams)
}
