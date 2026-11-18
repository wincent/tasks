package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/wincent/tasks/internal/git"
)

// The metadata lock serializes allocation and the create commit. Git history
// keeps deleted task numbers from being reused; the old file is read only for
// its high-water mark, so previously issued numbers also remain retired.
func (s *Session) NextTaskNumber(prefix Prefix) (int, error) {
	if err := s.check(); err != nil {
		return 0, err
	}
	data, err := regularContents(filepath.Join(s.Directory, "ledger.json"))
	if err != nil {
		return 0, err
	}
	var legacy struct {
		HighWater *int `json:"high_water"`
	}
	max := 0
	if data != nil {
		if err := json.Unmarshal(data, &legacy); err != nil {
			return 0, fmt.Errorf("read historical ticket numbers: %w", err)
		}
		if legacy.HighWater == nil || *legacy.HighWater < 0 {
			return 0, errors.New("invalid historical ticket number high-water mark")
		}
		max = *legacy.HighWater
	}
	numbers, err := s.observedNumbers(prefix)
	if err != nil {
		return 0, err
	}
	for _, n := range numbers {
		if n > max {
			max = n
		}
	}
	if max == math.MaxInt {
		return 0, errors.New("no more task numbers are available")
	}
	return max + 1, nil
}

func (s *Session) observedNumbers(prefix Prefix) ([]int, error) {
	names := []string{}
	path, err := Location(s.Repository)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		store, err := Open(s.Repository)
		if err != nil {
			return nil, err
		}
		defer store.Close()
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		index, err := store.Repository.IndexEntries()
		if err != nil {
			return nil, err
		}
		for _, entry := range index {
			names = append(names, entry.Path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	refs := []string{Branch}
	remotes, err := s.Repository.Remotes()
	if err != nil {
		return nil, err
	}
	for _, remote := range remotes {
		refs = append(refs, "refs/remotes/"+remote+"/tasks")
	}
	tips := []git.ID{}
	for _, name := range refs {
		ref, err := s.Repository.Reference(name)
		if git.HasCode(err, git.NotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		id, err := s.Repository.ResolveCommit(name)
		if err != nil {
			return nil, err
		}
		_ = ref
		tips = append(tips, id)
	}
	history, err := s.Repository.HistoryPaths(tips)
	if err != nil {
		return nil, err
	}
	local := len(names)
	names = append(names, history...)
	// Reserve issued numbers from history even after a prefix change, but
	// don't treat an unrelated worktree or index file as an issued ticket.
	numbers := []int{}
	for i, name := range names {
		if filepath.Base(name) != name {
			continue
		}
		match := ticketName.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		current := match[1] == string(prefix)
		if !current && i < local {
			continue
		}
		n, err := decimalNumber(match[2])
		if err != nil {
			if !current {
				continue // Not a usable historical ticket number.
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		numbers = append(numbers, n)
	}
	return numbers, nil
}
