package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func resolvePath(path string) (string, error) {
	p, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

func inside(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

type checkout struct {
	Name    string
	Paths   git.Paths
	Head    git.Head
	Locked  bool
	Invalid string
}

func checkouts(repo *git.Repository) ([]checkout, error) {
	result := []checkout{}
	primary, err := repo.PrimaryPaths()
	if err != nil {
		return nil, err
	}
	if !primary.Bare {
		opened, err := git.Open(primary.WorkDir)
		if err != nil {
			return nil, err
		}
		head, err := opened.Head()
		opened.Close()
		if err != nil {
			return nil, err
		}
		result = append(result, checkout{Paths: primary, Head: head})
	}
	trees, err := repo.Worktrees()
	if err != nil {
		return nil, err
	}
	for _, tree := range trees {
		item := checkout{Name: tree.Name, Locked: tree.Locked, Invalid: tree.InvalidReason, Paths: git.Paths{WorkDir: tree.Path}}
		if tree.InvalidReason == "" {
			opened, err := git.Open(tree.Path)
			if err != nil {
				return nil, err
			}
			item.Paths, err = opened.Paths()
			if err == nil {
				item.Head, err = opened.Head()
			}
			opened.Close()
			if err != nil {
				return nil, err
			}
		}
		result = append(result, item)
	}
	return result, nil
}

func branchStem(prefix store.Prefix, n int) string { return strings.ToLower(prefix.ID(n)) }

func taskBranchNumber(prefix store.Prefix, branch string) int {
	rest, ok := strings.CutPrefix(branch, "refs/heads/task/"+strings.ToLower(string(prefix))+"-")
	if !ok {
		return 0
	}
	number, _, _ := strings.Cut(rest, "-")
	key, n, err := store.ParseNumber(number)
	if err != nil || key != "" {
		return 0
	}
	return n
}

func findCheckout(repo *git.Repository, cwd string, prefix store.Prefix, n int, selected string, saved *taskSession) (checkout, error) {
	entries, err := checkouts(repo)
	if err != nil {
		return checkout{}, err
	}
	if selected != "" {
		if !filepath.IsAbs(selected) {
			selected = filepath.Join(cwd, selected)
		}
		selected, err = resolvePath(selected)
		if err != nil {
			return checkout{}, err
		}
	}
	caller, err := repo.Paths()
	if err != nil {
		return checkout{}, err
	}
	var matches []checkout
	var mismatched string
	for _, entry := range entries {
		if entry.Invalid != "" {
			if selected == "" && saved != nil && filepath.Clean(entry.Paths.WorkDir) == filepath.Clean(saved.Worktree) {
				mismatched = fmt.Sprintf("saved checkout %s is invalid: %s", entry.Paths.WorkDir, entry.Invalid)
			}
			continue
		}
		path, err := resolvePath(entry.Paths.WorkDir)
		if err != nil {
			return checkout{}, err
		}
		if taskBranchNumber(prefix, entry.Head.Branch) != n {
			if saved != nil && path == saved.Worktree && entry.Head.ID != (git.ID{}) && (saved.Branch != "" || entry.Head.Branch == "") && selected == "" {
				mismatched = fmt.Sprintf("saved checkout %s is no longer on a %s branch", path, prefix.ID(n))
			}
			continue
		}
		entry.Paths.WorkDir = path
		if selected == path || (selected == "" && caller.WorkDir == entry.Paths.WorkDir) {
			return entry, nil
		}
		if selected == "" {
			matches = append(matches, entry)
		}
	}
	if selected != "" {
		return checkout{}, fmt.Errorf("%s is not a registered %s checkout", selected, prefix.ID(n))
	}
	if mismatched != "" {
		return checkout{}, fmt.Errorf("%s; switch back or use `--worktree PATH`", mismatched)
	}
	for _, entry := range matches {
		if saved != nil && entry.Paths.WorkDir == saved.Worktree && (saved.Branch == "" || saved.Branch == entry.Head.Branch) {
			return entry, nil
		}
	}
	switch len(matches) {
	case 0:
		return checkout{}, nil
	case 1:
		return matches[0], nil
	default:
		return checkout{}, fmt.Errorf("multiple checkouts for %s; use `--worktree PATH` or run from the intended checkout", prefix.ID(n))
	}
}

func retainedBranch(repo *git.Repository, prefix store.Prefix, n int, saved *taskSession) (git.Head, error) {
	refs, err := repo.References("refs/heads/task/" + branchStem(prefix, n))
	if err != nil {
		return git.Head{}, err
	}
	var matches []git.Head
	for branch, ref := range refs {
		if taskBranchNumber(prefix, branch) != n || ref.ID == (git.ID{}) || ref.Symbolic != "" {
			continue
		}
		head := git.Head{Branch: branch, ID: ref.ID}
		if saved != nil && saved.Branch == branch {
			return head, nil
		}
		matches = append(matches, head)
	}
	if len(matches) > 1 {
		return git.Head{}, fmt.Errorf("multiple %s branches without a checkout; check out the intended branch and pass `--worktree PATH`", prefix.ID(n))
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return git.Head{}, nil
}

func printTaskWorktree(repo *git.Repository, cwd, directory string, prefix store.Prefix, n int, out io.Writer) error {
	saved, err := readTaskSession(directory, n)
	if err != nil {
		return err
	}
	source, err := findCheckout(repo, cwd, prefix, n, "", saved)
	if err != nil || source.Paths.WorkDir == "" {
		return err
	}
	caller, err := resolvePath(cwd)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(caller, source.Paths.WorkDir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, relative)
	return err
}

func destinationCheckout(repo *git.Repository, target destination) (*checkout, error) {
	entries, err := checkouts(repo)
	if err != nil {
		return nil, err
	}
	var result *checkout
	for _, entry := range entries {
		if entry.Invalid != "" {
			return nil, fmt.Errorf("cannot locate %s with invalid checkout %s; inspect `git worktree list`", target, entry.Paths.WorkDir)
		}
		if entry.Head.Branch == target.ref() {
			if result != nil {
				return nil, fmt.Errorf("%s is checked out in multiple worktrees; resolve this before merging", target)
			}
			copy := entry
			result = &copy
		}
	}
	return result, nil
}
func operationState(repo *git.Repository) (string, error) {
	paths, err := repo.Paths()
	if err != nil {
		return "", err
	}
	directory := stateDirectory(paths)
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("task state must be a real directory")
	}
	return directory, nil
}

func stateDirectory(paths git.Paths) string {
	return filepath.Join(paths.CommonDir, "tasks")
}

func taskLockPath(directory string, prefix store.Prefix, n int) string {
	name := branchStem(prefix, n) + ".lock"
	path := filepath.Join(directory, name)
	// Keep existing operation lock names, but don't use the same file for
	// the operation and the running Pi process when the prefix is RUN.
	if path == runLockPath(directory, n) {
		return filepath.Join(directory, "task-"+name)
	}
	return path
}

func checkoutUseLockPath(directory string, prefix store.Prefix, n int) string {
	return filepath.Join(directory, "checkout-use-"+branchStem(prefix, n)+".lock")
}

func taskLock(directory string, prefix store.Prefix, n int) (*state.Lock, error) {
	lock, err := state.TryLock(taskLockPath(directory, prefix, n))
	if errors.Is(err, state.ErrBusy) {
		detail := ""
		if saved, _ := readTaskSession(directory, n); saved != nil {
			detail = ": " + saved.Worktree
		}
		return nil, fmt.Errorf("%s already has a running agent or task operation%s", prefix.ID(n), detail)
	}
	return lock, err
}

func token() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}
func plannedWorktreeParent(repo *git.Repository) (string, error) {
	paths, err := repo.Paths()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(paths.CommonDir), ".agent-worktrees"), nil
}

func worktreeParent(repo *git.Repository) (string, error) {
	parent, err := plannedWorktreeParent(repo)
	if err != nil {
		return "", err
	}
	return ensureWorktreeParent(parent)
}

func ensureWorktreeParent(parent string) (string, error) {
	if err := os.Mkdir(parent, 0755); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(parent)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New(".agent-worktrees must not be a symlink")
	}
	if !info.IsDir() {
		return "", errors.New(".agent-worktrees must be a directory")
	}
	return parent, nil
}
func createCheckout(repo *git.Repository, prefix string, revision git.ID, existingBranch string) (checkout, error) {
	parent, err := worktreeParent(repo)
	if err != nil {
		return checkout{}, err
	}
	directory, err := os.MkdirTemp(parent, prefix)
	if err != nil {
		return checkout{}, err
	}
	// libgit2 requires a nonexistent checkout destination. The owned empty
	// reservation can be removed without discarding anyone else's content.
	if err := os.Remove(directory); err != nil {
		return checkout{}, err
	}
	name := filepath.Base(directory)
	branch := existingBranch
	if branch == "" {
		branch = "refs/heads/task/" + name

		who, err := identity(repo)
		if err != nil {
			return checkout{}, err
		}
		tx, err := repo.Prepare(branch, revision, map[string]git.ID{branch: {}}, who, "prepare task worktree")
		if err != nil {
			return checkout{}, err
		}
		err = tx.Commit()
		tx.Close()
		if err != nil {
			return checkout{}, err
		}
	}
	if err := repo.AddWorktree(name, directory, branch); err != nil {
		return checkout{}, fmt.Errorf("worktree setup failed; inspect retained branch %s and path %s: %w", branch, directory, err)
	}
	opened, err := git.Open(directory)
	if err != nil {
		return checkout{}, err
	}
	defer opened.Close()
	paths, err := opened.Paths()
	if err != nil {
		return checkout{}, err
	}
	head, err := opened.Head()
	if err != nil {
		return checkout{}, err
	}
	return checkout{Name: name, Paths: paths, Head: head}, nil
}

func createDetachedCheckout(repo *git.Repository, prefix string, revision git.ID) (checkout, error) {
	parent, err := plannedWorktreeParent(repo)
	if err != nil {
		return checkout{}, err
	}
	suffix, err := token()
	if err != nil {
		return checkout{}, err
	}
	// Select the future context without reserving a directory or writing refs.
	// Native registration creates both directories exclusively after admission.
	name := prefix + suffix
	directory := filepath.Join(parent, name)
	if err := repo.AddDetachedWorktree(name, directory, revision, func() error {
		_, err := ensureWorktreeParent(parent)
		return err
	}); err != nil {
		return checkout{}, err
	}
	opened, err := git.Open(directory)
	if err != nil {
		return checkout{}, fmt.Errorf("inspect retained detached candidate %s: %w", directory, err)
	}
	defer opened.Close()
	paths, err := opened.Paths()
	if err != nil {
		return checkout{}, err
	}
	head, err := opened.Head()
	if err != nil {
		return checkout{}, err
	}
	return checkout{Name: name, Paths: paths, Head: head}, nil
}

func withMetadata(ctx context.Context, repo *git.Repository, body func(*store.Session) error) error {
	session, err := store.Begin(ctx, repo)
	if err != nil {
		return err
	}
	defer session.Close()
	return body(session)
}
