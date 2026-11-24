# TASKS-48: Reduce or eliminate cost of per-operation Git configuration rebuilds

Status: open

## Description

Agent finding:

> Per-operation Git configuration rebuild (biggest runtime bottleneck).
>
> In `internal/cli` profiles, `(*Repository).callNative` -> `(*environmentConfiguration).apply` accounts for **41% of CPU time** (0.5ms per `r.call`). On every `r.call(...)`, it:
>
> - invokes `C.task_config_find` 3 times (system, XDG, global),
> - runs `filepath.EvalSymlinks` + `os.Open` + `Stat` + `io.ReadAll` + `Close` on every config file and include (`(*configurationFileCapture).borrowRecords`),
> - walks the include graph twice in `(*hasconfigEvaluation).evaluateEach`,
> - builds a brand-new C `task_config_overlay` via one cgo call + `realloc(overlay->entries, (count+1)*...)` + 4 `C.CString`/`C.free` calls **per config entry** (`configurationNativeRecords`, `task_config_append`). On this host (`~/.config/git/config` has ~300 lines), that is **~140 entries / 6 file reads per `r.call`**.
>
> Concrete ways to cut this while preserving the repo's config-freshness and security guarantees:
>
> - **Use `r.callNative(..., false, ...)` for pure immutable ODB operations**: `RootTreeID`, `TreeChanges`, `CommitInfo`, and `HistoryRecord` already skip config refresh. Other pure object/graph methods (`Tree`, `TreeAt`, `Entry`, `Blob`, `IsAncestor`, `MergeBases`, `WriteBlob`, `WriteTree`, `WriteCommit`, `EditTree`) still go through `r.call(...)`.
> - **Preallocate/batch `configurationNativeRecords` or reuse the immutable `*C.git_config`**: `task_config_overlay` is already immutable and reference-counted (`atomic_size_t references`). Preallocating `task_config_new(len(records))` eliminates O(N) `realloc`s; batching record transfer into one cgo call removes N cgo crossings; and caching the last built `*C.git_config` on `environmentConfiguration` keyed by `(branch, captured file identities/bytes)` skips native overlay reconstruction completely when config hasn't changed.
> - **Stop paying `apply()` twice around `validateIndex()` and `TreeAt()`**:
>   - `(*Repository).validateIndex` calls `r.Paths()` (full `apply()` #1) right before `Status`, `IndexEntries`, `ValidateWorktreePaths`, `ValidateWorktreeContents`, `EditIndexFile`, and `AddWorktree` enter their own `r.call(...)` (full `apply()` #2). Reading `C.git_repository_path(repo)` inside the same `r.call` halves config work for all of them.
>   - `(*Repository).TreeAt` calls `r.Entry` and then `r.call("read subtree", ...)` — two separate `r.call`s where one suffices.
> - **Avoid recompiling a regex on every `includeIf` check**: `configurationWildmatch` runs `regexp.Compile` on every `gitdir:`, `onbranch:`, and `hasconfig:` match during every `apply()`. Caching compiled wildmatch patterns (or matching directly without `regexp`) avoids repeated compilation.
