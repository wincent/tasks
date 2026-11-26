# TASKS-72: Audit and clean up test-only git repository primitives

Status: blocked

## Description

A read-only dead-code sweep identified exported primitives in `internal/git` that have zero production callers and are only referenced by tests:

Used only within `internal/git/*_test.go` (strongest candidates to delete or move into `*_test.go`):

- `internal/git/native.go:96`: `ParseID` (3 test refs in `internal/git`).
- `internal/git/head.go:35`: `(*Repository).DetachHead` (1 test ref in `internal/git`).
- `internal/git/inspect.go:49`: `(*Repository).HeadWorkdir` (3 test refs in `internal/git`).

Used across multiple packages' tests (`internal/git`, `internal/store`, `internal/cli`, `internal/adapters/signing`, `cmd/tasks`) as in-process Git fixture helpers (evaluate whether each is genuinely dead or intentionally kept to avoid shelling out to `git` in tests, and whether any belong in a test-only package or file):

- `internal/git/native.go:356`: `Init` (and cgo `task_repository_init`).
- `internal/git/native.go:530`: `(*Repository).WriteBlob`.
- `internal/git/native.go:555`: `(*Repository).WriteTree` (and cgo `task_write_tree`).
- `internal/git/native.go:133`: `Locked` constant (`int(C.GIT_ELOCKED)`).
- `internal/git/head.go:24`: `(*Repository).SetHead`.
- `internal/git/inspect.go:512`: `(*Repository).ConfigBool`.
- `internal/git/inspect.go:537`: `(*Repository).SetLocalConfig`.
- `internal/git/index.go:42`: `IndexEntry.ID` (populated in `IndexEntries()`, only read in `internal/git` and `internal/store` tests).

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production and test references across all packages).
2. Locate the commit in Git history where each item was introduced or first became unused in production (and note it in your summary/commit message).
3. Clean up the dead code (removing unused APIs, moving `internal/git`-only test helpers into `*_test.go`, and deciding/acting on cross-package fixture helpers; note any intentionally retained fixture helpers clearly in the task summary). Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
