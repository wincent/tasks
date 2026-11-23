package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"syscall"

	"github.com/wincent/tasks/internal/process"
	"github.com/wincent/tasks/internal/state"
	"github.com/wincent/tasks/internal/store"
)

func init() {
	commands.register(commandSpec{
		name: "sh", min: 2, max: math.MaxInt, numbered: true, passArgsAfter: 1,
		options:         map[string]optionKind{"--worktree": valueOption},
		positionalError: "`tasks sh` expects a task number and a command",
		usage:           "[--worktree PATH] NUMBER [--] COMMAND [ARG ...]",
		summary:         "Run a command in a task's existing checkout.",
		details: `Example: tasks sh 29 -- git status --short

Put tasks options before NUMBER. After NUMBER and an optional --, the command
and its arguments are passed as-is. For shell syntax, use sh -c '...'.

Options:
` + worktreeHelp,
		validate: func(a arguments) error {
			if a.positional[1] == "" {
				return errors.New("command must not be empty")
			}
			if _, supplied := a.values["--worktree"]; supplied && a.value("--worktree") == "" {
				return errors.New("--worktree requires a nonempty path")
			}
			return nil
		},
		run: withTasks(shellTask),
	})
}

func shellTask(ctx context.Context, cwd string, a arguments, streams IO, invocation storeInvocation, tasks *store.Store) error {
	n := a.number()
	task, err := tasks.Read(n)
	if err != nil {
		return err
	}
	directory, err := operationState(invocation.repository)
	if err != nil {
		return err
	}
	// Reserve before selecting a checkout so it cannot disappear between
	// lookup and command start. The task lock remains available to Pi.
	lease, err := state.TrySharedLock(checkoutUseLockPath(directory, a.prefix, n))
	if errors.Is(err, state.ErrBusy) {
		return fmt.Errorf("checkout cleanup in progress for %s; retry", task.ID())
	}
	if err != nil {
		return err
	}
	defer lease.Close()
	saved, err := readTaskSession(directory, n)
	if err != nil {
		return err
	}
	source, err := findCheckout(invocation.repository, cwd, a.prefix, n, a.value("--worktree"), saved)
	if err != nil {
		return err
	}
	if source.Paths.WorkDir == "" {
		return fmt.Errorf("no checkout for %s", task.ID())
	}

	runner := streams.Runner
	if runner == nil {
		runner = process.Native{}
	}
	argv := a.positional[1:]
	result := runner.Run(ctx, process.Command{
		Args: argv, Directory: source.Paths.WorkDir,
		In: streams.In, Out: streams.Out, Err: streams.Err,
	})
	switch {
	case result.Err != nil:
		return &exitError{Code: result.Code, Err: fmt.Errorf("run %q: %w", argv[0], result.Err)}
	case result.Code < 0:
		return &exitError{Code: result.Code, Err: fmt.Errorf("%q: %s", argv[0], syscall.Signal(-result.Code))}
	case result.Code > 0:
		return &exitError{Code: result.Code, Err: fmt.Errorf("%q exited with status %d", argv[0], result.Code)}
	default:
		return nil
	}
}
