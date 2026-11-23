package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/wincent/tasks/internal/store"
)

func init() {
	list := withTasks(listArtifacts)
	save := withTaskStore(storeRequirements{mutation: true}, saveArtifacts)
	commands.register(commandSpec{
		name: "store", min: 1, max: math.MaxInt, numbered: true,
		autoCorrect: func(a arguments) bool { return len(a.positional) == 1 },
		usage:       "NUMBER [PATH ...]",
		summary:     "Save or list retained task artifacts.",
		details: `Save small, sanitized files in one local tasks-branch commit. Files keep
their original names. If names collide, nothing is saved. Use -- before
paths starting with a dash.

With no paths, list committed artifacts for the task. Printed paths are
relative to the directory you ran the command from.`,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			if len(a.positional) == 1 {
				return list(ctx, cwd, a, streams)
			}
			return save(ctx, cwd, a, streams)
		},
	})
}

func listArtifacts(_ context.Context, cwd string, a arguments, streams IO, _ storeInvocation, tasks *store.Store) error {
	task, err := tasks.Read(a.number())
	if err != nil {
		return err
	}
	names, err := tasks.Artifacts(task)
	if err != nil {
		return err
	}
	return printArtifacts(streams.Out, cwd, tasks, task, names)
}

func printArtifacts(out io.Writer, cwd string, tasks *store.Store, task store.Task, names []string) error {
	cwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return err
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return err
	}
	for _, name := range names {
		path, err := filepath.Rel(cwd, tasks.ArtifactPath(task, name))
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(out, path); err != nil {
			return err
		}
	}
	return nil
}

func saveArtifacts(_ context.Context, cwd string, a arguments, streams IO, invocation storeInvocation) error {
	tasks, err := invocation.session.Preview(a.prefix)
	if err != nil {
		return err
	}
	defer tasks.Close()
	task, err := tasks.ReadWorking(a.number())
	if err != nil {
		return err
	}
	existing, err := tasks.Artifacts(task)
	if err != nil {
		return err
	}
	inputs := a.positional[1:]
	names := make([]string, len(inputs))
	count := make(map[string]int)
	for i, path := range inputs {
		names[i] = filepath.Base(path)
		count[names[i]]++
	}
	conflict := false
	for i, name := range names {
		present, err := tasks.ArtifactPresent(task, name, existing)
		if err != nil {
			return err
		}
		if present || count[name] > 1 {
			conflict = true
			fmt.Fprintf(streams.Err, "Colliding input: %q\n", inputs[i])
		}
	}
	if conflict {
		fmt.Fprintf(streams.Err, "Stored artifacts for %s:\n", task.ID())
		err := printArtifacts(streams.Err, cwd, tasks, task, existing)
		return errors.Join(errors.New("artifact filenames collide; rename conflicting files and retry the whole batch"), err)
	}

	files := make(map[string][]byte, len(inputs))
	for i, path := range inputs {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact must be a regular file: %q", inputs[i])
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[names[i]] = data
	}
	if err := invocation.session.CommitArtifacts(tasks, task, files); err != nil {
		return err
	}
	return printArtifacts(streams.Out, cwd, tasks, task, names)
}
