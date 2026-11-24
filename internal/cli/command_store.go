package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

type storeRequirements struct {
	mutation bool
	taskLock bool
	body     bool
	prepare  func(arguments) error
}

type storeInvocation struct {
	repository *git.Repository
	session    *store.Session
	root       string
	directory  string
	body       string
}

func identity(repository *git.Repository) (git.Signature, error) {
	return repository.CommitterSignature()
}

func usesBodyEditor(a arguments) bool {
	return len(a.values["--body"]) == 0 && len(a.values["--body-file"]) == 0
}

// withTaskStore preserves the shared precondition and cleanup order. Commands
// explicitly choose facilities; read-only commands never acquire an actor.
func withTaskStore(require storeRequirements, run func(context.Context, string, arguments, IO, storeInvocation) error) commandHandler {
	return func(ctx context.Context, cwd string, repository *git.Repository, a arguments, streams IO) error {
		if require.prepare != nil {
			if err := require.prepare(a); err != nil {
				return err
			}
		}
		var body string
		var err error
		if require.body {
			switch {
			case usesBodyEditor(a):
				body, err = editorBody(ctx, cwd, repository, a, streams)
				if err != nil {
					return err
				}
			case len(a.values["--body"]) > 0:
				body = a.value("--body")
			default:
				var data []byte
				if a.value("--body-file") == "-" {
					data, err = io.ReadAll(streams.In)
				} else {
					path := a.value("--body-file")
					if !filepath.IsAbs(path) {
						path = filepath.Join(cwd, path)
					}
					data, err = os.ReadFile(path)
				}
				if err != nil {
					return err
				}
				body = string(data)
			}
			body = strings.TrimSpace(body)
			if body == "" {
				return errors.New("task text must not be empty")
			}
		}
		paths, err := repository.Paths()
		if err != nil {
			return err
		}
		root := paths.WorkDir
		if root == "" {
			return errors.New("run task commands from an implementation worktree")
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return err
		}
		if err := store.RequireLocalHistory(repository); err != nil {
			return err
		}
		// Reject invalid actors before even creating the metadata lock directory.
		if require.mutation {
			if _, err := store.MetadataSignatures(repository); err != nil {
				return err
			}
		}
		if require.taskLock {
			directory, err := operationState(repository)
			if err != nil {
				return err
			}
			unlock, err := lockTasks(directory, a.prefix, a.numbers())
			if err != nil {
				return err
			}
			defer unlock()
		}
		session, err := store.Begin(ctx, repository)
		if err != nil {
			return err
		}
		defer session.Close()
		return run(ctx, cwd, a, streams, storeInvocation{repository: repository, session: session, root: root, directory: session.Directory, body: body})
	}
}

// lockTasks takes lifetime locks in ticket order before the metadata session.
func lockTasks(directory string, prefix store.Prefix, numbers []int) (func(), error) {
	slices.Sort(numbers)
	locks := make([]*state.Lock, 0, len(numbers))
	unlock := func() {
		for _, lock := range locks {
			lock.Close()
		}
	}
	for _, number := range numbers {
		lock, err := taskLock(directory, prefix, number)
		if err != nil {
			unlock()
			return nil, err
		}
		locks = append(locks, lock)
	}
	return unlock, nil
}

func withTasks(run func(context.Context, string, arguments, IO, storeInvocation, *store.Store) error) commandHandler {
	return func(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
		if err := store.RequireLocalHistory(repo); err != nil {
			return err
		}
		paths, err := repo.Paths()
		if err != nil {
			return err
		}
		tasks, err := store.Inspect(repo, a.prefix)
		if err != nil {
			return err
		}
		defer tasks.Close()
		return run(ctx, cwd, a, streams, storeInvocation{repository: repo, root: paths.WorkDir, directory: stateDirectory(paths)}, tasks)
	}
}
