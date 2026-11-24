# TASKS-56: Evaluate macOS F_FULLFSYNC test-time overhead and Sync batching

Status: merged

## Description

Re-profile `go test -count=1 ./...` after TASKS-46 through TASKS-54 have landed to determine whether macOS `F_FULLFSYNC` (invoked by Go's `os.(*File).Sync()` on Darwin) is a meaningful remaining test-time hotspot, especially under `t.Parallel()` where `F_FULLFSYNC` acts as a volume/controller-wide flush barrier on APFS.

### Background

On Darwin, `os.(*File).Sync()` calls `fcntl(fd, F_FULLFSYNC, 0)` (~4.4ms per file sync and ~1.1ms per directory sync on this machine, so ~5.5ms per `state.AtomicWrite` / `state.AtomicCreate`).

Current call sites:

- `internal/store/commit.go`: `store.commitFiles` syncs `recovery.json` via `state.AtomicWrite` (line 300), each written file via `state.AtomicWrite` / `state.AtomicCreate` (lines 421, 430; or two `state.SyncDirectory` calls per move in `internal/store/archive.go`), `indexLock.Sync()` (line 452), and `state.SyncDirectory(filepath.Dir(indexPath))` (line 461) — roughly ~16.5ms per single-file metadata commit.
- `internal/cli/task_session.go`: `writeTaskSession` calls `state.AtomicWrite` twice (lines 62, 66) — ~11ms per update.
- `internal/store/bootstrap.go`: `(*Session).ignoreCheckout` calls `file.Sync()` (line 88) — ~4.4ms.
- `internal/cli/merge.go`: writes `MERGE_MSG` via `state.AtomicWrite` (line 399) — ~5.5ms.

### Options to evaluate

1. **Runtime win (safe regardless of test changes)**: In `internal/store/commit.go` (lines 418-433), batch `state.SyncDirectory` once per unique parent directory for multi-file commits instead of syncing the same directory after every file.
2. **Test-only sync opt-in in `internal/state`** (only if profiling shows `F_FULLFSYNC` is a significant share of remaining wall time): route `bootstrap.go` and `commit.go` through a `state.SyncFile(*os.File)` helper alongside `state.SyncDirectory`, and gate skipping behind a `testing.Testing()`-guarded helper or build tag enabled from `TestMain` in `internal/cli` and `internal/store`. Keep real `Sync()` enabled in `internal/state` tests and `cmd/tasks` binary tests, still open/validate directories even when sync is skipped, and never add a runtime environment variable that could disable durability in production binaries.
3. **Zero-code-change alternative**: evaluate whether running tests with `TMPDIR` on a macOS RAM disk is preferable to adding test-only hooks.
