# TASKS-67: Remove dead code in state, query, and signing packages

Status: active

## Description

A read-only dead-code sweep identified the following dead or test-only code across `internal/state`, `internal/query`, and `internal/adapters/signing`:

- `internal/state/clock.go` (entire file): `Monotonic()` at `internal/state/clock.go:24` and its cgo helper `task_monotonic` have zero references anywhere in production code or tests.
- `internal/state/files.go:38`: package-level `AtomicCreate` has zero references anywhere in production code or tests (callers only use `(*Batch).AtomicCreate` at `internal/state/files.go:69`).
- `internal/query/query.go:20,23,25,167,183` and `internal/query/updated.go:81`: `(*Expression).NeedsUpdated` (along with the `needsUpdated` field on `Expression` and `parser` and `p.needsUpdated = true` in `internal/query/updated.go:81`) and `(*Expression).Matches` are only referenced by tests in `internal/query/query_test.go`; production code uses `(*Expression).Evaluate` directly.
- `internal/adapters/signing/signing.go:44`: `Load` is only referenced by tests in `internal/adapters/signing/signing_test.go`; production code calls `ParsePolicy` directly.

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production and test references, and checking for any indirect or platform-specific usage).
2. Locate the commit in Git history where the code first became dead (and note it in your summary/commit message).
3. Clean up the dead code and update or remove any tests that only existed to exercise the removed APIs. Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
