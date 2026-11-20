# TASKS-11: Create a `go` subcommand for quick one-shot tasks

Status: active

## Description

Requires one argument, a task title.

Equivalent to `task create`, supplying the title and an empty body, followed by `task run <N>` on the just-created task. Designed for small, quick wins.
