# TASKS-24: Archive and unarchive individual tasks

Status: merged

## Description

Add reversible, single-ticket archiving on the `tasks` branch so old tasks stop cluttering the branch root and the default CLI working set. Archiving is independent of task status.

## Behavior

- `tasks archive NUMBER` and `tasks unarchive NUMBER` accept exactly one positive task number or full task ID, using the existing prefix rules. Each moves only the ticket Markdown file in one local metadata commit, preserving its filename, contents, ID, and status. Allow any status, but refuse to archive a task while a run or interactive task session holds its task lock.
- Use a visible `archive/` directory with one level of 1,000-number buckets. The bucket name is the largest multiple of 1,000 not greater than the task number, in decimal without padding. For example, `PROJ-42017 Title.md` moves to `archive/42000/PROJ-42017 Title.md`. Compute the bucket from the number for direct lookups.
- Leave artifacts at `storage/<TASK-ID>/`. Do not move, remove, or rewrite artifacts, saved sessions, task branches, or implementation worktrees when archiving or unarchiving.
- Keep archived tickets out of default listings, including `tasks list all`, search, recent tasks, and `tasks show` without an ID. `tasks list --include-archived` includes archived and unarchived tasks while retaining the normal status filter; `tasks list all --include-archived` includes all statuses. Mark archived rows in text and expose an `archived` field in JSON.
- An explicit read-only lookup can resolve archived tickets. In particular, `tasks show NUMBER` displays the record and indicates that it is archived. Commands that run or modify the ticket, such as `tasks run` and `tasks edit`, reject it with an actionable error: `PROJ-42 is archived; run 'tasks unarchive PROJ-42' first`.
- `tasks cleanup`, with or without an ID, can clean up eligible branches and worktrees for archived tasks using the ticket's recorded destination and the existing safety checks. An archived record must not be treated as absent and trigger the fallback to `main` for legacy branches with no record.

## Implementation and verification

- Centralize archive path and lookup rules in the store. Support both an existing metadata checkout and inspection of the local branch before a checkout exists. Keep archived numbers reserved even when the only observed record is under `archive/`.
- Use the existing metadata locking and commit machinery for an atomic move. Reject dirty paths, ambiguous IDs, and destination collisions without overwriting files or disturbing unrelated staged or unstaged changes.
- Test the round trip with and without a pre-existing metadata checkout, bucket boundaries, listing and explicit lookup behavior, run rejection, cleanup against a non-default topic, number allocation, and preservation of unrelated changes and artifacts. Update CLI help.
- No bulk archive, query parser, or older-client compatibility work in this ticket. Shared query syntax is covered separately by ADR-0004.
