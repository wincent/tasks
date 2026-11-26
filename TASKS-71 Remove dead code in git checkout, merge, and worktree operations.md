# TASKS-71: Remove dead code in git checkout, merge, and worktree operations

Status: open

## Description

A read-only dead-code sweep identified the following dead code, dead branches, wasted work, and test-only wrappers in `internal/git` checkout, merge, and worktree operations:

- `internal/git/operation_safety.go:180-187`: `validateCheckoutOperation` takes `existing bool`, which is `true` at both call sites (`internal/git/merge.go:213` and test-only `internal/git/index.go:281`). Furthermore, line 181 calls `changedOperationPaths(repo, base, target)` and line 183 immediately overwrites both `paths` and `err` via `sparseCheckoutPaths(repo, base, target)` (which calls `changedOperationPaths` a second time at `internal/git/sparse.go:146`), discarding the first diff and any error from it.
- `internal/git/index.go:261`: `(*Repository).CheckoutMetadata` (and its cgo helper `task_checkout` in `internal/git/index.go`) is only called from `internal/git/*_test.go` tests (it is also the only caller of `validateCheckoutOperation` other than `CheckoutImplementation`).
- `internal/git/merge.go:48`: `(*Repository).MergeIntoIndex` is only called from tests; production code only calls `(*Repository).MergeCleanIntoIndex` (`cleanOnly = true`), making `cleanOnly == false` and the conflict-retaining branch at `internal/git/merge.go:127-129` test-only.
- `internal/git/remove.go:24`: `(*Repository).RemoveCleanWorktree` is a one-line wrapper calling `RemoveWorktree(name, expected, head, false)` that is only called from tests.
- `internal/git/native.go:123,149`: `Error.Class` is written in `nativeError` and never read or formatted anywhere in production or tests.
- `internal/git/native.go:247-248,262,344`: `OpenCheckout(path, refName)` is called with `refName = ""` at all 4 production call sites (`internal/store/store.go:81`, `internal/store/preview.go:55`, and `internal/cli/worktrees.go:47,64`), so the `if refName != ""` branch and `CheckoutSummary.Reference` / `CheckoutSummary.RefErr` are only exercised by `internal/git/worktrees_test.go`.
- `internal/git/worktrees.go:21,47`: `Worktree.LockReason` is populated via `C.git_worktree_is_locked` but never read in production (only asserted in `internal/git/worktrees_test.go:41`).

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production, cgo, and test references).
2. Locate the commit in Git history where the code, field, or branch first became dead (and note it in your summary/commit message).
3. Clean up the dead code, dead branches, and unused cgo helpers, updating call sites and tests (moving any still-needed test behavior into `*_test.go` if appropriate, or dropping tests that only exercised dead production APIs). Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
