# TASKS-20: Fix alignment problem in `tasks list` output

Status: open

## Description

The visual output of `tasks list all` in this repo at the time of writing is the following:

```
TASKS-1 merged  Create an ADRs folder
TASKS-2 merged  Add a project license
TASKS-3 closed  Set up CI
TASKS-4 merged  Add a `help` subcommand
TASKS-5 merged  Add `test` and `vet` scripts under `bin`
TASKS-6 merged  Use color, bold, underline, italics, and emojis in terminal output
TASKS-7 merged  Improve hint when `list` has no tasks to show
TASKS-8 merged  Make `install` wrapper
TASKS-9 merged  Document common remote set-up
TASKS-10        merged  Install into a sandbox-friendly location
TASKS-11        merged  Create a `go` subcommand for quick one-shot tasks
TASKS-12        merged  Convert UNLICENSE into UNLICENSE.md, removing hard wrapping and updating any necessary references
TASKS-13        merged  Avoid empty descriptions in tasks
TASKS-14        closed  13
TASKS-15        merged  Improve output styling
TASKS-16        merged  Be more verbose when merging
TASKS-17        merged  Add a --version flag
TASKS-18        merged  Create an ADR describing provenance-based release process
TASKS-19        merged  Improve error output
```

Note how when the task numbers grow from 1-wide to 2-wide, the second column winds up an entire tab stop over.

The goal of this task is to avoid this situation. It should instead render as:

```
TASKS-1         merged  Create an ADRs folder
TASKS-2         merged  Add a project license
TASKS-3         closed  Set up CI
TASKS-4         merged  Add a `help` subcommand
TASKS-5         merged  Add `test` and `vet` scripts under `bin`
TASKS-6         merged  Use color, bold, underline, italics, and emojis in terminal output
TASKS-7         merged  Improve hint when `list` has no tasks to show
TASKS-8         merged  Make `install` wrapper
TASKS-9         merged  Document common remote set-up
TASKS-10        merged  Install into a sandbox-friendly location
TASKS-11        merged  Create a `go` subcommand for quick one-shot tasks
TASKS-12        merged  Convert UNLICENSE into UNLICENSE.md, removing hard wrapping and updating any necessary references
TASKS-13        merged  Avoid empty descriptions in tasks
TASKS-14        closed  13
TASKS-15        merged  Improve output styling
TASKS-16        merged  Be more verbose when merging
TASKS-17        merged  Add a --version flag
TASKS-18        merged  Create an ADR describing provenance-based release process
TASKS-19        merged  Improve error output
```
