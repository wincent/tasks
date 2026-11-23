package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/terminal"
)

func init() {
	single := withTaskStore(storeRequirements{mutation: true, taskLock: true}, moveTask)
	bulk := withTasks(archiveByQuery)
	commands.register(commandSpec{
		name: "archive", max: 1, numbered: true,
		options: map[string]optionKind{"--query": valueOption},
		validate: func(a arguments) error {
			hasQuery := len(a.values["--query"]) != 0
			if hasQuery && len(a.positional) != 0 {
				return errors.New("NUMBER and --query are mutually exclusive")
			}
			if !hasQuery && len(a.positional) == 0 {
				return errors.New("`tasks archive` expects 1 positional argument (NUMBER) or --query EXPR")
			}
			return nil
		},
		usage:   "NUMBER | --query EXPR",
		summary: "Hide a task from the default working set.",
		details: `Move one ticket to the archive in a local tasks-branch commit. The task's
status, artifacts, checkout and session are unchanged. A running task cannot
be archived. Use 'tasks unarchive NUMBER' to restore the ticket.

Options:
  -q, --query EXPR  Preview matching unarchived tasks in list format.

Archive only the previewed tickets in a single commit after you type yes in a
terminal. Without a terminal, print the preview without changing any tickets.

` + queryHelp,
		run: func(ctx context.Context, cwd string, a arguments, streams IO) error {
			if a.query != nil {
				return bulk(ctx, cwd, a, streams)
			}
			return single(ctx, cwd, a, streams)
		},
	})
	commands.register(commandSpec{
		name: "unarchive", min: 1, max: 1, numbered: true,
		usage:   "NUMBER",
		summary: "Restore an archived task to the working set.",
		details: `Move one ticket back to the tasks-branch root in a local commit. The task's
status, artifacts, checkout and session are unchanged.`,
		run: withTaskStore(storeRequirements{mutation: true}, moveTask),
	})
}

func archiveByQuery(ctx context.Context, _ string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
	head, err := tasks.Head()
	if err != nil {
		return err
	}
	candidates, err := tasks.ScanArchiveCandidates(head.ID)
	if err != nil {
		return fmt.Errorf("incomplete archive selection: %w", err)
	}
	matcher := queryMatcher{expr: a.query}
	if a.query.NeedsUpdated() {
		matcher.history = tasks.TaskHistory(head.ID)
	}
	var selected []store.Task
	updates := make(map[int]store.TaskUpdate)
	for _, task := range candidates {
		match, update, err := matcher.match(ctx, task)
		if err != nil {
			return err
		}
		if match {
			selected = append(selected, task)
			if matcher.history != nil {
				updates[task.Number] = update
			}
		}
	}
	plan, err := tasks.PlanArchive(head.ID, selected, updates)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		_, err := fmt.Fprintln(streams.Out, "No unarchived tasks match the query.")
		return err
	}
	// The query and plan use recorded status; only the preview shows the
	// effective status, just as list does.
	for i, task := range selected {
		info, _, err := describeActivity(invocation.directory, tasks.Prefix, task)
		if err != nil {
			return err
		}
		selected[i].Status = info.Status
	}
	p := terminalPresentation(streams.Out)
	heading := fmt.Sprintf("Tasks to archive (%d):", len(selected))
	if err := printTaskListing(p, tasks, selected, heading); err != nil {
		return err
	}
	if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Out) {
		_, err := fmt.Fprintln(streams.Out, "No changes made; confirmation requires a terminal.")
		return err
	}
	if _, err := fmt.Fprint(streams.Out, "Archive these tasks? Type yes to confirm: "); err != nil {
		return err
	}
	reply, err := bufio.NewReader(terminal.Input(ctx, streams.In)).ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return err
	}
	if err != nil || strings.TrimSpace(string(reply)) != "yes" {
		_, err := fmt.Fprintln(streams.Out, "Archive cancelled; no changes made.")
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if invocation.root == "" {
		return errors.New("run task commands from an implementation worktree")
	}
	repo := invocation.repository
	if _, err := store.MetadataSignatures(repo); err != nil {
		return err
	}
	directory, err := operationState(repo)
	if err != nil {
		return err
	}
	// Candidates are in ticket order. Take every lifetime lock before the
	// metadata lock, and keep them all until the batch has finished.
	var locks []*state.Lock
	defer func() {
		for _, lock := range locks {
			lock.Close()
		}
	}()
	for _, task := range selected {
		lock, err := taskLock(directory, a.prefix, task.Number)
		if err != nil {
			return err
		}
		locks = append(locks, lock)
	}
	session, err := store.Begin(ctx, repo)
	if err != nil {
		return err
	}
	defer session.Close()
	moved, err := session.ArchiveTasks(plan)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(streams.Out, "Archived %d task(s).\n", len(moved))
	return err
}

func moveTask(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
	task, err := invocation.session.MoveTask(a.prefix, a.number(), a.action == "archive")
	if err != nil {
		return err
	}
	action := "Unarchived"
	if task.Archived {
		action = "Archived"
	}
	return taskResult(streams.Out, action, task.ID(), task.Title, task.Path)
}
