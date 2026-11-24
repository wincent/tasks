# TASKS-52: Reduce reliance on `systemGit` in tests

Status: open

## Description

Agent finding:

> Replace non-oracle `systemGit(...)` calls in `internal/cli` tests (250 call sites).
>
> In syscall traces, `systemGit` subprocesses accounted for **26%** of `TestArchiveRoundTrip/999` and **23%** of `TestRunKeepsWorkOnFailureOrInterruption/dirty`. There are ~250 call sites across `internal/cli/*_test.go` (61 in `topic_test.go`, 60 in `cleanup_branches_test.go`, 35 in `command_archive_test.go`, 26 in `workflow_test.go`). Replacing trivial setup/assertion calls (`rev-parse`, `branch`, `update-ref`, `config`, `commitImplementation`) with in-process `git.Repository` helpers — keeping `systemGit` only where testing real Git CLI interop — removes hundreds of process forks.
