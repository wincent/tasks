# TASKS-33: Add --prompt to tasks run for per-run instructions

Status: active

## Description

When `tasks run` stops with "Needs attention", let the user retry with a hint without editing the task or switching to interactive Pi. For example, CP-173 had already been implemented and committed, but Chrome could not start in the sandbox. The user wanted to defer browser checks while leaving the separately timed-out agent-worker suite in scope.

```sh
tasks run 173 --prompt "Don't worry about Chrome tests; I'll run them separately later."
```

- Accept optional `--prompt TEXT` on `tasks run` for both first runs and retries. Resume the saved Pi session as usual when one is available.
- Append the text to the existing Pi prompt under a distinct label such as "Additional instructions for this run". Do not replace the standard prompt, write the hint into the task file, or automatically reapply it on subsequent invocations. It can remain in the Pi transcript as normal session history.
- Preserve existing behavior when the flag is absent, including task status, merge checks, and `--no-merge` behavior. The flag provides guidance to Pi, not a launcher bypass.
- Update `tasks run --help` and add regression tests for forwarding the hint while preserving the default prompt and resuming the saved session.
