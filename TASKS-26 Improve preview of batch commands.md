# TASKS-26: Improve preview of batch commands

Status: active

## Description

Running `tasks archive -q "updated:<3.days.ago"` it prints:

```
Tasks to archive (9):
TASKS-1
TASKS-2
TASKS-3
TASKS-4
TASKS-5
TASKS-6
TASKS-7
TASKS-8
TASKS-9
Archive these tasks? Type yes to confirm:
```

That's not very helpful, compared to the output of `tasks list all`:

```
TASKS-1   merged  Create an ADRs folder
TASKS-2   merged  Add a project license
TASKS-3   closed  Set up CI
TASKS-4   merged  Add a `help` subcommand
TASKS-5   merged  Add `test` and `vet` scripts under `bin`
TASKS-6   merged  Use color, bold, underline, italics, and emojis in terminal output
TASKS-7   merged  Improve hint when `list` has no tasks to show
TASKS-8   merged  Make `install` wrapper
TASKS-9   merged  Document common remote set-up
```

The batch preview should use the same format and formatting as `tasks list`.
