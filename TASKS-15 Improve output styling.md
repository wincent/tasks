# TASKS-15: Improve output styling

Status: active

## Description

A `tasks run` invocation might end with this output (synthetic example):

```
Running Pi...
Needs attention: TASKS-14 has an empty description. Please clarify what should be implemented.

The checkout is clean. No tests ran and nothing was changed.
tasks: TASKS-14 needs attention: the task has an empty description.
Checkout: .worktrees/TASKS-14 session: sessions/example.jsonl
Continue with tasks run 14
```

That is rather a "wall of text", and hard to see what is actually required there at a glance.

Let's make "Needs attention:" bold and underlined.

Separately, I accidentally created that empty task by running `tasks go 13`, which means "create a task with title '13' and run it". I meant to execute `tasks run 13` instead. Let's handle this as a special case: if somebody passes a numeric argument or a ticket ID (eg. "TASK-13") to `tasks go`), we should refuse and print a hint to do `tasks run 13` instead.
