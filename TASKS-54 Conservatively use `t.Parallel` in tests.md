# TASKS-54: Conservatively use `t.Parallel` in tests

Status: merged

## Description

Agent proposal:

> Add `t.Parallel()` (currently 0 uses across the entire repo — likely 4x–8x wall-time speedup).
>
> There are **zero** calls to `t.Parallel()` across all ~295 test functions, and **zero** calls to `os.Chdir`/`t.Chdir`. Because `internal/cli` takes **79.6s** serially, all other CPU cores sit idle for ~65s of the test run.
>
> - **Immediate**: tests that do not mutate process env (`38` in `internal/cli`, `74` in `internal/git`, `25` in `internal/store`, plus independent subtests) can call `t.Parallel()` immediately. Shared globals in `internal/git` (`sharedConfigurationParses`, `mergeDriverRegistry`) are already mutex-protected.
> - **Unlocking the remaining `internal/cli` tests**: most of the `67` env-mutating tests in `internal/cli` only call `t.Setenv` inside `taskWorkflowFixture` (`PATH`, `HOME`, `PI_CODING_AGENT_DIR`), `editorFixture`, or `NO_COLOR`/`TERM`. Extending `cli.IO` (which already injects `Runner`) with an optional env/lookup hook would let nearly all `internal/cli` tests and subtests run in parallel.
