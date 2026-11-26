# TASKS-66: Print --apply hint to stderr after tasks cleanup preview

Status: open

## Description

In preview mode (without `--apply`), `tasks cleanup` prints `Would remove ...` lines to stdout, but does not remind the user how to actually apply the removals.

Update `cleanupTask` in `internal/cli/command_cleanup.go` to print a hint to stderr after the preview pass, following these rules:

- Write the hint to `streams.Err` (stderr), keeping `streams.Out` (stdout) unchanged.
- Only print the hint in preview mode (when `--apply` is not set).
- Do not print the hint if there are no eligible removals (i.e. no `Would remove ...` lines were emitted).
- Do not print the hint unless stderr is a terminal (`terminal.IsTerminal(streams.Err)`).
- Keep the wording uniform regardless of whether `NUMBER` is supplied:
  - When there is a single eligible removal: ``Run `tasks cleanup --apply` to perform removal.``
  - When there are multiple eligible removals: ``Run `tasks cleanup --apply` to perform removals.``

Add tests covering terminal vs non-terminal stderr, single vs multiple eligible removals, zero eligible removals (no matches or all kept), `--apply`, and passing `NUMBER`.
