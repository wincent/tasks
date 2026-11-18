package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
)

// Session serializes task number allocation and metadata edits across worktrees.
// Acquire any waiting workflow locks before opening a session.
type Session struct {
	Repository *git.Repository
	Directory  string
	lock       *state.Lock
	ctx        context.Context
}

func Begin(ctx context.Context, repository *git.Repository) (*Session, error) {
	paths, err := repository.Paths()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(paths.CommonDir, "tasks")
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("task state must be a real directory")
	}
	lock, err := state.Acquire(ctx, filepath.Join(directory, "lock"))
	if err != nil {
		return nil, err
	}
	return &Session{Repository: repository, Directory: directory, lock: lock, ctx: ctx}, nil
}

func (s *Session) Close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Close()
	s.lock = nil
	return err
}

func (s *Session) check() error {
	if s.lock == nil {
		return errors.New("task metadata session is closed")
	}
	return s.ctx.Err()
}

func (s *Session) CommitFiles(changes map[string][]byte, options CommitOptions) (git.ID, error) {
	if err := s.check(); err != nil {
		return git.ID{}, err
	}
	return commitFiles(s.ctx, s.Repository, changes, options)
}
