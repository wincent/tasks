package store

import (
	"errors"

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

// RequireLocalHistory is a read-only preflight before creating local task-state
// directories or acquiring permanent metadata locks. Ensure rechecks under lock.
func RequireLocalHistory(repository *git.Repository) error {
	ref, err := repository.Reference(Branch)
	if git.HasCode(err, git.NotFound) {
		return errors.New(localHistorySetup)
	}
	if err != nil {
		return err
	}
	if ref.Symbolic != "" {
		return errors.New("tasks must be a direct branch")
	}
	return nil
}
