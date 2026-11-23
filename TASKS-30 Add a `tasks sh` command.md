# TASKS-30: Add a `tasks sh` command

Status: merged

## Description

Purpose: run a shell command in the worktree of the specified task.

Use case example: see `git status` output in an active worktree.

```
$ tasks st                                                                                                     0.42s [main] ~/code/tasks
TASKS-29  active  Let `tasks archive` accept multiple numeric arguments
  Checkout: /Users/greg.hurrell/code/tasks/.agent-worktrees/tasks-29-1370640285
  Session: /Users/greg.hurrell/.pi/agent/sessions/dev.wincent.tasks/5b9faae7-ba9d-4441-a51d-ba57a02bd511.jsonl
$ tasks sh 29 -- git status
```

- Runs the command after the `--` in the worktree of task 29.
- Can omit the `--`, but it can be useful explicitly to separate args to the `tasks sh` subcommand from the shell commands to be executed in the worktree.
