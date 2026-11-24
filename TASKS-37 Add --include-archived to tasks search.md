# TASKS-37: Add --include-archived to tasks search

Status: open

## Description

Let `tasks search PATTERN --include-archived` search archived tasks as well as unarchived tasks, matching the opt-in behavior of `tasks list`. Keep archived tasks hidden by default. The flag should not change title matching, status filtering, or `--query` filtering.

- Document the flag in `tasks search --help`.
- Add regression tests showing that an archived title match is omitted by default, included with the flag, and still subject to the status and query filters.
