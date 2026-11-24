# TASKS-61: Address validation divergence in `SyncDirectory`

Status: open

## Description

Agent finding:

> **Minor validation divergence in `SyncDirectory()`** (`internal/state/files.go:146`):
>
> When `syncSkipped()` is true, `SyncDirectory` checks `!info.IsDir()` and rejects regular files; when real sync is enabled, `os.Open(regularFile)` + `directory.Sync()` succeeds on macOS and Linux. Being stricter in tests is helpful for catching bugs, though checking `info.IsDir()` unconditionally (if desired) would keep both paths identical at negligible cost compared to `F_FULLFSYNC`.
