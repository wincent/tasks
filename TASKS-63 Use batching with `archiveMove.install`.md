# TASKS-63: Use batching with `archiveMove.install`

Status: open

## Description

Agent findings:

> `archiveMove.install` in `internal/store/archive.go:234` still performs 2 `SyncDirectory` calls per moved task during batch archive (`options.moves` in `commitFiles`). Leaving that untouched made sense for TASKS-56's scope and kept per-move rollback simple, though batching destination-dir syncs before source unlinks would be an optional future runtime win for large bulk archives.

Assess the viability and cost of implementing the "future runtime win". Ask the user if a judgement call is needed, but otherwise, if it is a clear win, proceed directly with implementation.
