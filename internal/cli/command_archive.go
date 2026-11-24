package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/wincent/tasks/internal/git"
	"github.com/wincent/tasks/internal/store"
	"github.com/wincent/tasks/internal/terminal"
)

func init() {
	explicit := withTaskStore(storeRequirements{mutation: true, taskLock: true}, moveTasks)
	bulk := withTasks(archiveByQuery)
	commands.register(commandSpec{
		name: "archive", autoCorrect: allowCorrection, max: math.MaxInt, numberedAll: true,
		options: map[string]optionKind{"--query": valueOption},
		validate: func(a arguments) error {
			hasQuery := len(a.values["--query"]) != 0
			if hasQuery && len(a.positional) != 0 {
				return errors.New("NUMBER and --query are mutually exclusive")
			}
			if !hasQuery && len(a.positional) == 0 {
				return errors.New("`tasks archive` expects at least 1 positional argument (NUMBER) or --query EXPR")
			}
			return nil
		},
		usage:   "NUMBER [NUMBER ...] | --query EXPR",
		summary: "Hide tasks from the default working set.",
		details: `Move one or more tickets to the archive in a single local tasks-branch
commit. If any listed ticket cannot be moved, none are moved. A running task
cannot be archived. Task status, artifacts, checkouts and sessions are
unchanged. Use 'tasks unarchive NUMBER [NUMBER ...]' to restore tickets.

Options:
  -q, --query EXPR  Preview matching unarchived tasks in list format.

Archive only the previewed tickets in a single commit after you type yes in a
terminal. Without a terminal, print the preview without changing any tickets.

` + queryHelp("tasks archive -q 'updated: < 3 days ago'"),
		run: func(ctx context.Context, cwd string, repo *git.Repository, a arguments, streams IO) error {
			if a.query != nil {
				return bulk(ctx, cwd, repo, a, streams)
			}
			// Explicit numbers normally skip confirmation. Do not let a
			// corrected spelling take that shortcut.
			if a.correction.from != "" {
				if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Out) {
					return errors.New("confirmation requires a terminal; use `tasks archive` explicitly")
				}
				ids := make([]string, len(a.tasks))
				for i, task := range a.tasks {
					ids[i] = a.prefix.ID(task.number)
				}
				confirmed, err := confirmArchive(ctx, streams, strings.Join(ids, ", "))
				if err != nil || !confirmed {
					return err
				}
			}
			return explicit(ctx, cwd, repo, a, streams)
		},
	})
	commands.register(commandSpec{
		name: "unarchive", min: 1, max: math.MaxInt, numberedAll: true,
		usage:   "NUMBER [NUMBER ...]",
		summary: "Restore archived tasks to the working set.",
		details: `Move one or more tickets back to the tasks-branch root in a single local
commit. If any listed ticket cannot be moved, none are moved. Task status,
artifacts, checkouts and sessions are unchanged.`,
		run: withTaskStore(storeRequirements{mutation: true}, moveTasks),
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
	matcher := queryMatcher{expr: a.query, tasks: tasks, head: head.ID}
	var selected []store.Task
	updates := make(map[int]store.TaskUpdate)
	for _, task := range candidates {
		match, update, err := matcher.match(ctx, task)
		if err != nil {
			return err
		}
		if match {
			selected = append(selected, task)
			if update != (store.TaskUpdate{}) {
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
	p := streams.presentation(streams.Out)
	heading := fmt.Sprintf("Tasks to archive (%d):", len(selected))
	if err := printTaskListing(p, tasks, selected, heading); err != nil {
		return err
	}
	if !terminal.IsTerminal(streams.In) || !terminal.IsTerminal(streams.Out) {
		_, err := fmt.Fprintln(streams.Out, "No changes made; confirmation requires a terminal.")
		return err
	}
	confirmed, err := confirmArchive(ctx, streams, "these tasks")
	if err != nil || !confirmed {
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
	numbers := make([]int, len(selected))
	for i, task := range selected {
		numbers[i] = task.Number
	}
	unlock, err := lockTasks(directory, a.prefix, numbers)
	if err != nil {
		return err
	}
	defer unlock()
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

func confirmArchive(ctx context.Context, streams IO, selection string) (bool, error) {
	if _, err := fmt.Fprintf(streams.Out, "Archive %s? Type yes to confirm: ", selection); err != nil {
		return false, err
	}
	reply, err := bufio.NewReader(terminal.Input(ctx, streams.In)).ReadSlice('\n')
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false, err
	}
	if err != nil || strings.TrimSpace(string(reply)) != "yes" {
		_, err := fmt.Fprintln(streams.Out, "Archive cancelled; no changes made.")
		return false, err
	}
	return true, ctx.Err()
}

func moveTasks(_ context.Context, _ string, a arguments, streams IO, invocation storeInvocation) error {
	archive := a.action == "archive"
	tasks, err := invocation.session.MoveTasks(a.prefix, a.numbers(), archive)
	if err != nil {
		return err
	}
	action := "Unarchived"
	if archive {
		action = "Archived"
	}
	for _, task := range tasks {
		if err := taskResult(streams, action, task.ID(), task.Title, task.Path); err != nil {
			return err
		}
	}
	return nil
}
