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

func editorCommand() ([]string, error) {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return splitEditor(value)
		}
	}
	return []string{"vi"}, nil
}

func runEditor(ctx context.Context, cwd string, command []string, draft string, streams IO) error {
	args := append(append([]string{}, command[1:]...), draft)
	process := exec.CommandContext(ctx, command[0], args...)
	process.Dir = cwd
	process.Stdin = streams.In
	process.Stdout = streams.Err
	process.Stderr = streams.Err
	if err := process.Run(); err != nil {
		return fmt.Errorf("editor failed (%v); task edit cancelled", err)
	}
	return nil
}

func openForEditor(ctx context.Context, cwd string, prefix store.Prefix) (*git.Repository, *store.Session, *store.Store, error) {
	repo, err := git.Open(cwd)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := store.MetadataSignatures(repo); err != nil {
		repo.Close()
		return nil, nil, nil, err
	}
	session, err := store.Begin(ctx, repo)
	if err != nil {
		repo.Close()
		return nil, nil, nil, err
	}
	tasks, err := session.Preview(prefix)
	if err != nil {
		session.Close()
		repo.Close()
		return nil, nil, nil, err
	}
	return repo, session, tasks, nil
}

func editorBody(ctx context.Context, cwd string, a arguments, streams IO) (string, error) {
	if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Err) {
		return "", errors.New("--body or --body-file is required outside an interactive terminal (use --body-file - for stdin)")
	}
	repo, session, tasks, err := openForEditor(ctx, cwd, a.prefix)
	if err != nil {
		return "", err
	}
	label := "Creating task: " + strings.TrimSpace(a.positional[0])
	kind := "description"
	operation := "task creation"
	if a.action != "create" {
		task, err := tasks.Read(a.number())
		if err != nil {
			tasks.Close()
			session.Close()
			repo.Close()
			return "", err
		}
		label = fmt.Sprintf("%s: %s", task.ID(), filepath.Base(task.Path))
		kind = "comment"
		operation = "task " + a.action
	}
	tasks.Close()
	session.Close()
	repo.Close()
	command, err := editorCommand()
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
	fmt.Fprintf(streams.Err, "%s\nWrite the %s, then save and quit. Leave it empty to cancel.\n", label, kind)
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

func editTask(ctx context.Context, cwd string, prefix store.Prefix, n int, streams IO) (err error) {
	if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Err) {
		return errors.New("tasks edit requires an interactive terminal")
	}
	command, err := editorCommand()
	if err != nil {
		return err
	}
	repo, session, tasks, err := openForEditor(ctx, cwd, prefix)
	if err != nil {
		return err
	}
	defer repo.Close()
	task, err := tasks.Read(n)
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
	fmt.Fprintf(streams.Err, "Editing %s. Save and quit to commit; leave unchanged to cancel.\n", task.ID())
	if err := runEditor(ctx, cwd, command, draft, streams); err != nil {
		return err
	}
	data, err := os.ReadFile(draft)
	if err != nil {
		return err
	}
	if bytes.Equal(data, []byte(task.Content)) {
		saved = true
		_, err = fmt.Fprintln(streams.Out, task.Path)
		return err
	}
	if !utf8.Valid(data) {
		return errors.New("edited task must be UTF-8")
	}
	if _, err := store.Status(string(data)); err != nil {
		return err
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
	current, err := tasks.Read(n)
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
	_, err = fmt.Fprintln(streams.Out, task.Path)
	return err
}
