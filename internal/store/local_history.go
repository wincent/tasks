package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
)

const localHistorySetup = `local refs/heads/tasks is missing; task initialization never probes or fetches remotes and never creates independent history automatically.
For existing shared history, use your normal Git/OpenSSH configuration from this repository (replace REMOTE with your chosen remote):

    git fetch REMOTE refs/heads/tasks:refs/heads/tasks

` + "Then run `tasks init`. Do not create independent history when shared tasks already exist.\n" +
	`Only for an intentionally new independent task history, explicitly create an empty orphan commit without changing your checkout or index:

    tree=$(git hash-object -w -t tree --stdin </dev/null) &&
    commit=$(git commit-tree "$tree" -m "chore: initialize task store") &&
    git update-ref refs/heads/tasks "$commit" ""

` + "Then run `tasks init`. The empty expected old value prevents replacing an existing local branch."

// ErrMissingLocalHistory distinguishes a missing local branch from a store
// that could not be inspected for another reason.
var ErrMissingLocalHistory = errors.New(localHistorySetup)

// RequireLocalHistory is a read-only preflight before creating local task-state
// directories or acquiring permanent metadata locks. Ensure rechecks under lock.
func RequireLocalHistory(repository *git.Repository) error {
	ref, err := repository.Reference(Branch)
	if git.HasCode(err, git.NotFound) {
		return ErrMissingLocalHistory
	}
	if err != nil {
		return err
	}
	if ref.Symbolic != "" {
		return errors.New("tasks must be a direct branch")
	}
	return nil
}

// MayHaveSharedHistory checks local evidence after Inspect establishes that
// the expected checkout and local branch are absent. In particular, a remote
// with only main fetched does not prove the shared tasks branch is absent.
func MayHaveSharedHistory(repo *git.Repository) (bool, error) {
	remotes, err := repo.Remotes()
	if err != nil || len(remotes) != 0 {
		return len(remotes) != 0, err
	}
	refs, err := repo.References("refs/remotes/")
	if err != nil {
		return false, err
	}
	for name := range refs {
		if strings.HasSuffix(strings.ToLower(name), "/tasks") {
			return true, nil
		}
	}
	paths, err := repo.Paths()
	if err != nil {
		return false, err
	}
	// Cleanup itself creates operation locks. The metadata lock, unlike
	// those locks, indicates a task store was used or being initialized.
	for _, relative := range []string{"tasks/lock", "logs/refs/heads/tasks"} {
		if _, err := os.Lstat(filepath.Join(paths.CommonDir, relative)); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}
