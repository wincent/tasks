package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
)

// The caller holds the task lock in apply mode. A branch with no checkout
// needs only Git history and an unused ref, not a task record or session.
func cleanMergedBranch(repo *git.Repository, name string, ref git.Reference, retained []checkout, apply bool) error {
	if ref.Symbolic != "" {
		return errors.New("symbolic branch refs are protected")
	}
	main, err := repo.Reference(mainRef)
	if err != nil {
		return err
	}
	if main.Symbolic != "" {
		return errors.New("local main is a symbolic ref")
	}
	merged, err := repo.IsAncestor(ref.ID, main.ID)
	if err != nil {
		return err
	}
	if !merged {
		return errors.New("branch is not merged into local main")
	}
	if !apply {
		return checkBranchReservations(retained, name)
	}

	// Verify both tips under ref locks: neither the branch gaining commits nor
	// main moving away from them may turn the ancestry check into a deletion.
	tx, err := repo.Prepare(name, git.ID{}, map[string]git.ID{name: ref.ID, mainRef: main.ID}, git.Signature{}, "")
	if err != nil {
		return err
	}
	defer tx.Close()
	fresh, err := checkouts(repo)
	if err != nil {
		return err
	}
	if err := checkBranchReservations(fresh, name); err != nil {
		return err
	}
	return tx.Commit()
}

func checkBranchReservations(entries []checkout, name string) error {
	for _, entry := range entries {
		if entry.Invalid != "" {
			return fmt.Errorf("cannot check branch use in invalid worktree %s; inspect git worktree list", entry.Paths.WorkDir)
		}
		if entry.Head.Branch == name {
			return fmt.Errorf("still checked out at %s", entry.Paths.WorkDir)
		}
		// Rebase and bisect detach HEAD but can still use the original branch.
		for _, rebase := range []string{"rebase-merge", "rebase-apply"} {
			dir := filepath.Join(entry.Paths.GitDir, rebase)
			info, err := os.Lstat(dir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("cannot inspect rebase state at %s", dir)
			}
			data, err := readBranchReservation(filepath.Join(dir, "head-name"))
			if err != nil {
				return err
			}
			head := strings.TrimSpace(string(data))
			if head == name {
				return fmt.Errorf("rebase at %s still uses the branch", entry.Paths.WorkDir)
			}
			if !strings.HasPrefix(head, "refs/heads/") && head != "detached HEAD" {
				return fmt.Errorf("cannot determine the rebase branch at %s", entry.Paths.WorkDir)
			}
			if err := checkRebaseUpdates(dir, name); err != nil {
				return err
			}
		}
		path := filepath.Join(entry.Paths.GitDir, "BISECT_START")
		data, err := readBranchReservation(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		start := strings.TrimSpace(string(data))
		if start == name || start == strings.TrimPrefix(name, "refs/heads/") {
			return fmt.Errorf("bisect at %s still uses the branch", entry.Paths.WorkDir)
		}
		if start == "" || strings.ContainsAny(start, " \t\r\n") {
			return fmt.Errorf("cannot determine the bisect branch at %s", entry.Paths.WorkDir)
		}
	}
	return nil
}

func readBranchReservation(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("cannot inspect branch reservation at %s", path)
	}
	return os.ReadFile(path)
}

func checkRebaseUpdates(dir, name string) error {
	path := filepath.Join(dir, "update-refs")
	data, err := readBranchReservation(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Git writes triples of ref name, original OID and new OID, one per line.
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(data) == 0 {
		return nil
	}
	if len(lines)%3 != 0 {
		return fmt.Errorf("cannot inspect rebase ref updates at %s", path)
	}
	for i := 0; i < len(lines); i += 3 {
		if lines[i] == name {
			return fmt.Errorf("rebase at %s will update the branch", dir)
		}
	}
	return nil
}
