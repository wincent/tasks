# TASKS-50: Deal with agent proposals for scaling and microoptimizations

Status: active

## Description

Agent findings:

> - **`(*Repository).HistoryPaths`** (called on every `tasks create` via `NextTaskNumber`): walks every reachable commit on `refs/heads/tasks` and `refs/remotes/*/tasks` and decodes every root tree entry across cgo without deduplicating root tree OIDs (only `archive` subtrees are tracked in `seen`). Deduplicating root tree OIDs and doing entry scanning in C (like `history_tree_changes` in `internal/git/history.go:12`) scales much better on large histories.
> - **`tail.Follow`**: scans from byte 0 using `ReadBytes('\n')` (allocating per line) and shifts the 40-element `offsets` slice with `copy` on every line. Scanning backwards from EOF (or using a ring buffer with `ReadSlice`) avoids full-file reads/allocations on large transcripts.
> - **`sparsePatterns.includes`**: converts `prefix` to `[]rune` and back to `string` inside the inner `for _, rule := range rules` loop instead of once per prefix, and rebuilds prefixes with `strings.Join` instead of slicing `path`.
> - **`(*Repository).WriteTree`**: calls `C.git_treebuilder_insert` one cgo crossing per entry (each doing `C.CString`/`C.free` + libgit2's per-entry ODB existence check). Batching inserts in C helps wide tree writes.

For each finding, validate the finding, implement a mitigation, and commit it.
