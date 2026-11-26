# TASKS-69: Remove dead code and unused parameters in store package

Status: merged

## Description

A read-only dead-code sweep identified the following unused parameters and test-only functions in `internal/store`:

- `internal/store/bootstrap.go:15`: `(*Session).Ensure(who git.Signature)` never uses its `who` parameter; all 5 production callers pass `git.Signature{}` (`internal/store/archive.go:139`, `internal/store/artifacts.go:98`, `internal/cli/editor.go:243`, `internal/cli/command_init.go:21`, and `internal/cli/mutate.go:83`).
- `internal/store/store.go:66`: `openWith(_ *git.Repository, path, originalCommon string, worktrees []git.Worktree)` ignores its first parameter across all 5 call sites (`internal/store/store.go:63`, `internal/store/bootstrap.go:28`, `internal/store/number.go:64`, `internal/store/preview.go:46`, and `internal/store/signatures.go:23`).
- `internal/store/store.go:45`: `Location` is only called from tests; production code uses `locationFromPaths`.
- `internal/store/commit.go:127`: package-level `CommitFiles` is only called from tests; production code uses `(*Session).CommitFiles`.

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production and test references).
2. Locate the commit in Git history where the code or parameter first became dead (and note it in your summary/commit message).
3. Clean up the dead code and unused parameters, updating all call sites and tests. Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
