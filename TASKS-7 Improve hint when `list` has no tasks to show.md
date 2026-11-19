# TASKS-7: Improve hint when `list` has no tasks to show

Status: merged

## Description

When all tasks are done, `tasks list` prints:

```
There are no uncompleted tasks.
Run `tasks list --status done` to see completed tasks.
```

This isn't super helpful, because there may be "merged" tasks as well (or there may be no "done" tasks).

Instead, make the hint read:

```
There are no uncompleted tasks.
Run `tasks list <done|merged>` to see completed tasks.
```

or similar (I'm not sure what the best way to show "`done` or `merged`" is; we should do something conventional).
