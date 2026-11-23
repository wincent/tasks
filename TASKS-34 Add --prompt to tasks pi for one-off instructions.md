# TASKS-34: Add --prompt to tasks pi for one-off instructions

Status: open

## Description

Follow up on TASKS-33, which added `--prompt` to `tasks run`. `tasks pi` already sends the standard task prompt when it opens, so typing a hint in the TUI cannot steer that first turn. Let users pass the hint at launch and then stay in the interactive session.

```sh
tasks pi 173 --prompt "Skip Chrome tests; still run the agent-worker suite."
```

- Accept optional `--prompt TEXT` on both fresh launches and resumed sessions. Use the shared launcher to append the hint under the same label as `tasks run`, without replacing the standard task prompt.
- Do not write the hint to the task file or append it again on later launches unless the flag is supplied again. It can remain in the Pi transcript as normal session history.
- Preserve behavior when the flag is absent. `tasks pi` still requires a terminal, resumes the saved session when available, and never auto-merges.
- Update `tasks pi --help` and add regression coverage for forwarding the hint in interactive mode while preserving the standard prompt and session behavior.
