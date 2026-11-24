# TASKS-46: Fix Darwin PTY close order

Status: active

## Description

Agent finding:

> Fix Darwin PTY close order (instant ~600ms saved *per PTY* — ~8–10s+ win in `internal/cli`).
>
> A syscall trace of `TestQueryArchivePreviewAndApply/changed` showed **616.6ms (66.9% of the entire test)** spent blocked inside `syscall.Close` on the PTY! On macOS, closing `slave` while `master` is still open stalls in `ttywait` for ~600ms (measured directly: **601.2ms** vs **0.1ms** when closing `master` first).
>
> `internal/cli/editor_test.go:90-92` and `internal/cli/presentation_test.go:49` already document and fix this, but **7 other `OpenPTY` sites** still have `defer master.Close()` followed by `defer slave.Close()` (LIFO closes `slave` first):
>
> - `internal/cli/command_query_test.go:150-151` (`archiveAtPrompt` — hit by 9 tests/subtests = **~5.4s wasted in this one function alone**)
> - `internal/cli/topic_test.go:299-300`
> - `internal/cli/workflow_test.go:392-393` and `internal/cli/workflow_test.go:473-474`
> - `internal/cli/render_process_test.go:56-57`
> - `internal/cli/progress_test.go:36-37`
> - `internal/cli/query_editor_oracle_test.go:41-42`
>
> Wrapping close order in a `testutil` helper (always closing `master` before `slave`) prevents future regressions too.
