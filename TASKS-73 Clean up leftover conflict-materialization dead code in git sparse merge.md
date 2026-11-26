# TASKS-73: Clean up leftover conflict-materialization dead code in git sparse merge

Status: merged

## Description

Removing `(*Repository).MergeIntoIndex` in `4efc14c` (TASKS-71) left behind cascade dead code in `internal/git` that only existed to materialize conflict markers and conflict index stages into the worktree and index. Because `(*Repository).MergeCleanIntoIndex` (`internal/git/merge.go:50-124`) is now the sole caller of `withSparseMergePlan` and unconditionally returns `ErrMergeConflict` before touching the index or worktree whenever `C.git_index_has_conflicts(planned) != 0`, the following code is now dead or vestigial:

- `internal/git/sparse_merge.go:161-199`: `validateSparseConflicts` runs inside `withSparseMergePlan` before the callback checks `git_index_has_conflicts(planned)`. It preflights `working-tree-encoding` on conflict-marker bytes that `MergeCleanIntoIndex` will never materialize, and can even mask `ErrMergeConflict` with an encoding error on a conflicted file.
- `internal/git/sparse.go:391-405` and `internal/git/sparse.go:468-470`: `copySparseNames` copies index resolve-undo/name entries (`git_index_name_entrycount` / `git_index_name_add`) in `copySparseIndex` and `applySparseIndex`, which is unreachable without conflict-retaining merges.
- `internal/git/sparse.go:456-462`: the conflict-stage branch (`git_index_conflict_add`) in `applySparseIndex` is unreachable because `checkoutPreservingSparse` reads clean trees and `MergeCleanIntoIndex` returns `ErrMergeConflict` before calling `applySparseIndex`.
- `internal/git/sparse.go:13` and `internal/git/merge.go:22`: `GIT_CHECKOUT_ALLOW_CONFLICTS` in `task_sparse_checkout` and `task_merge` is leftover from conflict-retaining merges.
- `internal/git/sparse.go:291-316`: evaluate whether the `git_index_has_conflicts(after)` check in `MergeCleanIntoIndex` can move before `planSparseIndex` (confirming first that dirty-worktree, staged-change, external-merge-driver, and external-filter preflight precedence tests stay satisfied) so the `conflict` branch and comment in `planSparseIndex` can also be removed.

Minor test follow-up from the same dead-code sweep (TASKS-72):

- `internal/git/config_environment_test.go:304`: `configBool(repo, "tasktool.missing")` now only tests the test helper's synthetic `NotFound` error rather than production code; either remove the tautological check or assert the relevant production API.

## Steps

1. Audit each item above against current production callers, cgo calls, and tests (especially preflight error-precedence tests in `internal/git/operation_safety_test.go`, `internal/git/merge_preflight_quarantine_test.go`, `internal/git/sparse_transitions_test.go`, `internal/git/rerere_noninterference_test.go`, and `internal/git/encoding*_test.go`).
2. Remove the dead conflict-materialization code and flags (`validateSparseConflicts`, `copySparseNames`, `git_index_conflict_add` in `applySparseIndex`, `GIT_CHECKOUT_ALLOW_CONFLICTS`, and if safe, the `conflict` handling in `planSparseIndex`), and add/update a test if helpful (for example, confirming that a conflicted merge on a `working-tree-encoding` file returns `ErrMergeConflict` rather than an encoding error).
3. Clean up the tautological `configBool(repo, "tasktool.missing")` assertion in `internal/git/config_environment_test.go:304`.
4. Verify with `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
