# TASKS-60: Deal with partial failures in `Batch.SyncDirectories`

Status: merged

## Description

Agent finding:

> **Partial failure state in `Batch.SyncDirectories()`** (`internal/state/files.go:83`):
>
> If a batch has multiple directories `[dirA, dirB]` and `dirA` succeeds while `dirB` fails, `SyncDirectories()` returns early without clearing or advancing past `dirA`. All current callers discard `batch` on error so this has no practical impact today, but either advancing `batch.directories` as each directory succeeds (and removing it from `batch.seen`) or clearing via `defer` would avoid stale entries if a caller ever retries or reuses a `Batch`.
