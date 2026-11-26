# TASKS-68: Remove dead code and orphaned metadata in cli package

Status: active

## Description

A read-only dead-code sweep identified the following dead code, test-only wrappers, write-only fields, and orphaned metadata files in `internal/cli`:

- Orphaned `*.tests.json` metadata files referencing nonexistent `tasktool/internal/cli/...` paths and not referenced anywhere in the repository or CI: `internal/cli/command_show.tests.json`, `internal/cli/command_listing.tests.json`, and `internal/cli/command_search.tests.json`.
- `internal/cli/local_fixture_test.go:51`: `copyFixtureTasks` is a 63-line test helper with zero callers anywhere in the repository.
- `internal/cli/cleanup_branches.go:15`: `cleanMergedBranch` is a thin wrapper around `cleanMergedBranchWithPreview(..., nil)` that is only called from tests (`internal/cli/cleanup_branches_test.go:266,274` and `internal/cli/cleanup_destination_test.go:99`); production code calls `cleanMergedBranchWithPreview` directly (`internal/cli/command_cleanup.go:133`).
- `internal/cli/task_session.go:29`: `taskSession.Revision` (`json:"revision,omitempty"`) is written in `internal/cli/launch.go:254` and `internal/cli/merge.go:149` when persisting `claim-<n>.json`, and is never read back anywhere in Go code. Determine whether it is truly dead or intentionally persisted as human/diagnostic metadata, and remove it if dead.

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production, test, and tooling/CI references).
2. Locate the commit in Git history where the code or metadata first became dead (and note it in your summary/commit message).
3. Clean up the dead code and orphaned files, updating any affected tests. Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
