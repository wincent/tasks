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
	"github.com/wincent/tasks/internal/terminal"
)

func init() {
	commands.register(commandSpec{
		name: "cleanup", min: 0, max: 1, numbered: true,
		autoCorrect: func(a arguments) bool { return !a.flags["--apply"] },
		options:     map[string]optionKind{"--apply": flagOption},
		usage:       "[NUMBER] [--apply]",
		summary:     "Preview removal of merged task checkouts and branches.",
		details: `Omit NUMBER to consider all tasks. Run from outside any checkout you want
to remove. The recorded topic (the configured default_target_branch, or main
by default) must contain the task tip or remain unchanged after a conflict-free
merge of the task tip. This applies even to archived tasks. Missing topics,
branches in use, and saved sessions are kept.

Options:
  --apply  Remove eligible branches and checkouts (including ignored files).`,
		run: cleanupTask,
	})
}

func cleanupTask(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
	paths, err := repo.Paths()
	if err != nil {
		return err
	}
	apply := a.flags["--apply"]
	var preview *cleanupPreview
	if !apply {
		preview = &cleanupPreview{repo: repo, prefix: a.prefix, defaultTarget: a.defaultTarget}
		defer preview.close()
	}
	selectDestination := func(n int) (cleanupTarget, error) {
		if preview != nil {
			return preview.destination(n)
		}
		return cleanupDestination(repo, a.prefix, a.defaultTarget, n)
	}
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
	previewRemovals := 0
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
			previewRemovals++
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
			target, err := selectDestination(n)
			if err != nil {
				return err
			}
			return cleanMergedWithPreview(repo, entry, cwd, a.prefix, a.defaultTarget, target, apply, preview)
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
			target, err := selectDestination(n)
			if err != nil {
				return err
			}
			return cleanMergedBranch(repo, name, refs[name], retained, a.prefix, a.defaultTarget, target, apply, preview)
		})
	}
	if previewRemovals > 0 && terminal.IsTerminal(streams.Err) {
		removal := "removal"
		if previewRemovals > 1 {
			removal = "removals"
		}
		fmt.Fprintf(streams.Err, "Run `tasks cleanup --apply` to perform %s.\n", removal)
	}
	return nil
}

type cachedCleanupTarget struct {
	target cleanupTarget
	err    error
}

// cleanupPreview reuses one open store handle, per-task destinations, and one
// RecordedTopics scan across a read-only preview pass.
type cleanupPreview struct {
	repo          *git.Repository
	prefix        store.Prefix
	defaultTarget destination
	storeLoaded   bool
	tasks         *store.Store
	storeErr      error
	topicsLoaded  bool
	topics        map[string]string
	topicsErr     error
	targets       map[int]cachedCleanupTarget
}

func (p *cleanupPreview) close() {
	if p != nil && p.tasks != nil {
		p.tasks.Close()
		p.tasks = nil
	}
}

func (p *cleanupPreview) store() (*store.Store, error) {
	if !p.storeLoaded {
		p.tasks, p.storeErr = store.Inspect(p.repo, p.prefix)
		p.storeLoaded = true
	}
	return p.tasks, p.storeErr
}

func (p *cleanupPreview) destination(n int) (cleanupTarget, error) {
	if cached, ok := p.targets[n]; ok {
		return cached.target, cached.err
	}
	tasks, err := p.store()
	if err != nil {
		return cleanupTarget{}, err
	}
	target, err := cleanupDestinationWithStore(tasks, p.defaultTarget, n)
	if p.targets == nil {
		p.targets = make(map[int]cachedCleanupTarget)
	}
	p.targets[n] = cachedCleanupTarget{target: target, err: err}
	return target, err
}

func (p *cleanupPreview) requireUnrecordedDestination(ref string) error {
	if !p.topicsLoaded {
		tasks, err := p.store()
		if err != nil {
			p.topicsErr = fmt.Errorf("cannot verify task destinations: %w", err)
		} else {
			p.topics, p.topicsErr = tasks.RecordedTopics()
			if p.topicsErr != nil {
				p.topicsErr = fmt.Errorf("cannot verify task destinations: %w", p.topicsErr)
			}
		}
		p.topicsLoaded = true
	}
	if p.topicsErr != nil {
		return p.topicsErr
	}
	return checkUnrecordedDestination(p.topics, ref)
}

func checkUnrecordedDestination(topics map[string]string, ref string) error {
	for topic, id := range topics {
		if strings.EqualFold(destination(topic).ref(), ref) {
			return fmt.Errorf("branch is the recorded destination for %s; retarget that task first", id)
		}
	}
	return nil
}

// Check records across all tickets, even for numbered or automatic cleanup.
func requireUnrecordedDestination(repo *git.Repository, prefix store.Prefix, ref string) error {
	tasks, err := store.Inspect(repo, prefix)
	if err != nil {
		return fmt.Errorf("cannot verify task destinations: %w", err)
	}
	defer tasks.Close()
	topics, err := tasks.RecordedTopics()
	if err != nil {
		return fmt.Errorf("cannot verify task destinations: %w", err)
	}
	return checkUnrecordedDestination(topics, ref)
}

type cleanupTarget struct {
	destination destination
	metadata    git.ID
}

func cleanupDestination(repo *git.Repository, prefix store.Prefix, defaultTarget destination, n int) (cleanupTarget, error) {
	tasks, err := store.Inspect(repo, prefix)
	if err != nil {
		return cleanupTarget{}, err
	}
	defer tasks.Close()
	return cleanupDestinationWithStore(tasks, defaultTarget, n)
}

func cleanupDestinationWithStore(tasks *store.Store, defaultTarget destination, n int) (cleanupTarget, error) {
	target := cleanupTarget{destination: defaultTarget}
	head, err := tasks.Head()
	if err != nil {
		return cleanupTarget{}, err
	}
	if head.Branch != store.Branch || head.ID == (git.ID{}) {
		return cleanupTarget{}, errors.New("task checkout is not on the committed tasks branch")
	}
	target.metadata = head.ID

	task, err := tasks.Read(n)
	if !errors.Is(err, store.ErrTaskNotFound) {
		if err != nil {
			return cleanupTarget{}, err
		}
		if err := tasks.RequireClean(tasks.RelativePath(task)); err != nil {
			return cleanupTarget{}, err
		}
		target.destination, err = taskDestination(task.Content, defaultTarget)
		if err != nil {
			return cleanupTarget{}, err
		}
	}
	// Pin the revision even when no record exists: a commit can add one.
	current, err := tasks.Head()
	if err != nil {
		return cleanupTarget{}, err
	}
	if current != head {
		return cleanupTarget{}, errors.New("task metadata changed during destination selection; retry")
	}
	return target, nil
}

// Eligibility uses actual Git history, not the status written in Markdown.
// The caller holds the task lock whenever it requests removal.
func cleanMerged(repo *git.Repository, entry checkout, cwd string, prefix store.Prefix, defaultTarget destination, selected cleanupTarget, apply bool) error {
	return cleanMergedWithPreview(repo, entry, cwd, prefix, defaultTarget, selected, apply, nil)
}

func cleanMergedWithPreview(repo *git.Repository, entry checkout, cwd string, prefix store.Prefix, defaultTarget destination, selected cleanupTarget, apply bool, preview *cleanupPreview) error {
	dest := selected.destination
	n := taskBranchNumber(prefix, entry.Head.Branch)
	if entry.Name == "" || entry.Locked || entry.Invalid != "" || n == 0 {
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
	base, err := dest.tip(repo, defaultTarget)
	if err != nil {
		return err
	}
	ok, err := cleanupIntegrated(repo, entry.Head.ID, base, dest)
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
	leasePath := checkoutUseLockPath(stateDirectory(entry.Paths), prefix, n)
	if !apply {
		if preview != nil {
			if err := preview.requireUnrecordedDestination(entry.Head.Branch); err != nil {
				return err
			}
		} else if err := requireUnrecordedDestination(repo, prefix, entry.Head.Branch); err != nil {
			return err
		}
		probe, err := state.ProbeExclusiveLock(leasePath)
		if err != nil {
			return err
		}
		defer probe.Close()
		if probe.Held {
			return errors.New("checkout is in use by tasks sh or cleanup")
		}
		return nil
	}

	// Serialize removal with tasks merges and keep out new shell commands
	// until RemoveWorktree has rechecked and pruned the registration.
	directory, err := operationState(repo)
	if err != nil {
		return err
	}
	lease, err := state.TryLock(checkoutUseLockPath(directory, prefix, n))
	if errors.Is(err, state.ErrBusy) {
		return errors.New("checkout is in use by tasks sh or cleanup")
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	lock, err := state.TryLock(filepath.Join(directory, "merge.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	refs, err := repo.LockRefs(map[string]git.ID{
		entry.Head.Branch: entry.Head.ID, dest.ref(): base, store.Branch: selected.metadata,
	})
	if err != nil {
		return err
	}
	defer refs.Close()
	if err := requireUnrecordedDestination(repo, prefix, entry.Head.Branch); err != nil {
		return err
	}
	return repo.RemoveWorktree(entry.Name, entry.Paths, entry.Head, true)
}

func cleanupIntegrated(repo *git.Repository, source, tip git.ID, dest destination) (bool, error) {
	if reachable, err := repo.IsAncestor(source, tip); err != nil || reachable {
		return reachable, err
	}
	entry, err := destinationCheckout(repo, dest)
	if err != nil {
		return false, err
	}
	if entry != nil {
		if entry.Head.ID != tip {
			return false, fmt.Errorf("local %s changed during cleanup", dest)
		}
		repo, err = openCheckout(*entry)
		if err != nil {
			return false, err
		}
		defer repo.Close()
	}
	return repo.MergeWouldKeepTree(source, tip, dest.ref())
}
