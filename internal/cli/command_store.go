package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wincent/tasks/internal/git"
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

// withTaskStore preserves the shared precondition and cleanup order. Commands
// explicitly choose facilities; read-only commands never acquire an actor.
func withTaskStore(require storeRequirements, run func(context.Context, string, arguments, IO, storeInvocation) error) commandHandler {
	return func(ctx context.Context, cwd string, a arguments, streams IO) error {
		if require.prepare != nil {
			if err := require.prepare(a); err != nil {
				return err
			}
		}
		var body string
		var err error
		if require.body {
			switch {
			case len(a.values["--body"]) > 0:
				body = a.value("--body")
			case len(a.values["--body-file"]) > 0:
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
			default:
				body, err = editorBody(ctx, cwd, a, streams)
				if err != nil {
					return err
				}
			}
			body = strings.TrimSpace(body)
			if body == "" {
				return errors.New("task text must not be empty")
			}
		}
		repository, err := git.Open(cwd)
		if err != nil {
			return err
		}
		defer repository.Close()
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
			lock, err := taskLock(directory, a.prefix, a.number())
			if err != nil {
				return err
			}
			defer lock.Close()
		}
		session, err := store.Begin(ctx, repository)
		if err != nil {
			return err
		}
		defer session.Close()
		return run(ctx, cwd, a, streams, storeInvocation{repository: repository, session: session, root: root, directory: session.Directory, body: body})
	}
}

func withTasks(run func(context.Context, string, arguments, IO, storeInvocation, *store.Store) error) commandHandler {
	return func(ctx context.Context, cwd string, a arguments, streams IO) error {
		repo, err := git.Open(cwd)
		if err != nil {
			return err
		}
		defer repo.Close()
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
