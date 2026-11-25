package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/terminal"
)

func splitEditor(value string) ([]string, error) { return process.Split(value) }

func editorCommand(streams IO) ([]string, error) {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(streams.getenv(key)); value != "" {
			return splitEditor(value)
		}
	}
	return []string{"vi"}, nil
}

func runEditor(ctx context.Context, cwd string, command []string, draft string, streams IO) error {
	executable := command[0]
	if streams.Env != nil {
		var err error
		executable, err = streams.lookPath(executable)
		if err != nil {
			return fmt.Errorf("editor failed (%v); task edit cancelled", err)
		}
	}
	args := append(append([]string{}, command[1:]...), draft)
	process := exec.CommandContext(ctx, executable, args...)
	process.Dir = cwd
	process.Env = streams.Env
	process.Stdin = streams.In
	process.Stdout = streams.Err
	process.Stderr = streams.Err
	if err := process.Run(); err != nil {
		return fmt.Errorf("editor failed (%v); task edit cancelled", err)
	}
	return nil
}

func openForEditor(ctx context.Context, repo *git.Repository, prefix store.Prefix) (*store.Session, *store.Store, error) {
	if _, err := store.MetadataSignatures(repo); err != nil {
		return nil, nil, err
	}
	session, err := store.Begin(ctx, repo)
	if err != nil {
		return nil, nil, err
	}
	tasks, err := session.Preview(prefix)
	if err != nil {
		session.Close()
		return nil, nil, err
	}
	return session, tasks, nil
}

func editorBody(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) (string, error) {
	if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Err) {
		return "", errors.New("--body or --body-file is required outside an interactive terminal (use `--body-file -` for stdin)")
	}
	session, tasks, err := openForEditor(ctx, repo, a.prefix)
	if err != nil {
		return "", err
	}
	p := streams.presentation(streams.Err)
	label := p.paint("Creating task:", "1") + " " + p.paint(strings.TrimSpace(a.positional[0]), "3")
	kind := "description"
	operation := "task creation"
	if a.action != "create" {
		task, err := tasks.ReadWorking(a.number())
		if err != nil {
			tasks.Close()
			session.Close()
			return "", err
		}
		label = p.task(task.ID(), task.Title, ": ")
		kind = "comment"
		operation = "task " + a.action
	}
	tasks.Close()
	session.Close()
	command, err := editorCommand(streams)
	if err != nil {
		return "", err
	}
	directory, err := os.MkdirTemp("", "task-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	draft := filepath.Join(directory, "body.md")
	if err := os.WriteFile(draft, nil, 0600); err != nil {
		return "", err
	}
	// An interactive create already gets a one-line confirmation on stdout.
	// Keep guidance if that stream is redirected or if adding a note.
	if a.action != "create" || !terminal.IsTerminal(streams.Out) {
		fmt.Fprintf(streams.Err, "%s\nWrite the %s, then save and quit. Leave it empty to cancel.\n", label, kind)
	}
	if err := runEditor(ctx, cwd, command, draft, streams); err != nil {
		return "", fmt.Errorf("%s cancelled: %w", operation, err)
	}
	data, err := os.ReadFile(draft)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", errors.New("editor body must be UTF-8")
	}
	body := strings.TrimSpace(string(data))
	if body == "" {
		return "", fmt.Errorf("empty %s; %s cancelled", kind, operation)
	}
	return body, nil
}

func editTask(ctx context.Context, cwd string, repo *git.Repository, prefix store.Prefix, defaultTarget destination, n int, streams IO) (err error) {
	session, tasks, err := openForEditor(ctx, repo, prefix)
	if err != nil {
		return err
	}
	task, err := tasks.ReadWorking(n)
	if err == nil && (!terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Err)) {
		err = errors.New("`tasks edit` requires an interactive terminal")
	}
	if err == nil {
		err = tasks.RequireClean(filepath.Base(task.Path))
	}
	var original os.FileInfo
	if err == nil && !tasks.Prospective() {
		original, err = os.Lstat(task.Path)
	}
	tasks.Close()
	session.Close()
	if err != nil {
		return err
	}
	command, err := editorCommand(streams)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp(session.Directory, "edit-")
	if err != nil {
		return err
	}
	draft := filepath.Join(directory, filepath.Base(task.Path))
	saved := false
	defer func() {
		if saved {
			os.RemoveAll(directory)
		} else {
			fmt.Fprintf(streams.Err, "Task edit was not completed; draft retained at %s\n", draft)
		}
	}()
	if err := os.WriteFile(draft, []byte(task.Content), 0600); err != nil {
		return err
	}
	p := streams.presentation(streams.Err)
	fmt.Fprintf(streams.Err, "%s %s. Save and quit to commit; leave unchanged to cancel.\n",
		p.paint("Editing", "1"), p.task(task.ID(), task.Title, " "))
	if err := runEditor(ctx, cwd, command, draft, streams); err != nil {
		return err
	}
	data, err := os.ReadFile(draft)
	if err != nil {
		return err
	}
	if bytes.Equal(data, []byte(task.Content)) {
		saved = true
		return taskResult(streams, "Unchanged", task.ID(), task.Title, task.Path)
	}
	if !utf8.Valid(data) {
		return errors.New("edited task must be UTF-8")
	}
	if _, err := store.Status(string(data)); err != nil {
		return err
	}
	target, err := taskDestination(string(data), defaultTarget)
	if err != nil {
		return err
	}
	previous, err := store.Topic(task.Content)
	if previous == "" && err == nil {
		previous = string(defaultTarget)
	}
	if err != nil || destination(previous) != target {
		// Do not hold the lock while the editor runs. Take it before the
		// metadata lock and recheck the original record below.
		lock, err := taskLock(session.Directory, prefix, n)
		if err != nil {
			return fmt.Errorf("change topic: %w", err)
		}
		defer lock.Close()
	}
	session, err = store.Begin(ctx, repo)
	if err != nil {
		return err
	}
	defer session.Close()
	tasks, err = session.Preview(prefix)
	if err != nil {
		return err
	}
	defer tasks.Close()
	current, err := tasks.ReadWorking(n)
	if err != nil {
		return err
	}
	var actual os.FileInfo
	if !tasks.Prospective() {
		actual, err = os.Lstat(task.Path)
		if err != nil {
			return err
		}
	}
	head, err := tasks.Head()
	if err != nil {
		return err
	}
	if current.Path != task.Path || current.Content != task.Content || (original != nil && !state.SameVersion(original, actual)) {
		return errors.New("task changed while the editor was open; reconcile the draft and retry")
	}
	changes := map[string][]byte{filepath.Base(task.Path): data}
	if err := tasks.RequireClean(filepath.Base(task.Path)); err != nil {
		return err
	}
	if err := tasks.ValidateContents(changes); err != nil {
		return err
	}
	opened, err := session.Ensure(git.Signature{})
	if err != nil {
		return err
	}
	opened.Close()
	if _, err := session.CommitFiles(changes, store.CommitOptions{FromEnvironment: true, Message: "chore: edit " + task.ID(), ExpectedRefs: map[string]git.ID{store.Branch: head.ID}}); err != nil {
		return err
	}
	saved = true
	return taskResult(streams, "Edited", task.ID(), task.Title, task.Path)
}
