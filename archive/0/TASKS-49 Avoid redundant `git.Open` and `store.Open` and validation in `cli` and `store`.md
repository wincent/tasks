# TASKS-49: Avoid redundant `git.Open` and `store.Open` and validation in `cli` and `store`

Status: merged

## Description

Agent finding:

> Redundant `git.Open`, `store.Open`, and validation passes in `cli` and `store`.
>
> - **Double `git.Open(cwd)` on every CLI command**: `cli.Run` opens a repository, loads `.tasks.yml` via `store.LoadPrefix(repo)`, and immediately calls `repo.Close()` (discarding its handle-local parse cache). Every subcommand (`withTasks`, `withTaskStore`, `mergeTask`, `cleanupTask`, `launchTask`, `editTask`) then calls `git.Open(cwd)` all over again (repeating discovery, ownership/bare/format validation, and `apply()`). Passing the open `*git.Repository` through eliminates an entire `git.Open` per invocation.
> - **Redundant calls inside `store.Open`**: calls `Location(repository)` (which calls `repository.Paths()`), then calls `repository.Paths()` again at line 88, plus `Worktrees`, `git.Open(path)`, `tasks.Paths`, `tasks.Head`, `tasks.Reference`, and `tasks.MergeBases` — ~10 config rebuilds for a single `store.Open`.
> - **Repeated validation & opens inside `store.commitFiles`**:
>   - Runs `checkSelected()` (`ValidateWorktreePaths` + `Status(true)` + `IndexEntries()`) and `ValidateWorktreeContents(contents)` *before* acquiring `index.lock` (lines 195–200), repeats both *after* acquiring `index.lock` (lines 219–224), and repeats `checkSelected()` a *third* time plus a second `store.Open(repository)` right before publication (lines 307–318). Since the session flock is already held on entry, the pre-`index.lock` pass is largely redundant.
>   - Inside `ValidateWorktreePaths` and `ValidateWorktreeContents`, `validateOperationPaths` is also called twice (once at the top, and again inside `validateWorktreeContent` / `cleanWorktreeBuffer`).
> - **O(N) index reads per batch archive**: `(*archiveMove).check` calls `store.Repository.IndexEntries()` inside the per-move loop (3x per `commitFiles`), scaling as O(moves * index_size) instead of reading the index once per check.
> - **O(branches * total_tickets) in `tasks cleanup`**: `cleanupTask` calls `cleanupDestination` and `cleanMergedBranch` -> `requireUnrecordedDestination` -> `(*Store).RecordedTopics` inside the loop over every worktree and branch. `RecordedTopics()` reads *every* committed and working-tree ticket (including all archive buckets) on every iteration. In preview mode (`!apply`), computing `RecordedTopics()` once and reusing the store avoids repeated full-store scans.
