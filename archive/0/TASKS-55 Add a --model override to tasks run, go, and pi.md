# TASKS-55: Add a --model override to tasks run, go, and pi

Status: merged

## Description

Add an optional `--model MODEL` flag to `tasks run`, `tasks go`, and `tasks pi` so callers can select the Pi model for a launch, including when resuming an existing task session.

- Accept Pi's native syntax, e.g. `tasks run 42 --model openai-codex/gpt-6.2-sol:high`. Forward the value unchanged before Pi's `--` prompt separator and let Pi resolve it. Ensure `go` carries the option through from task creation to launch.
- When omitted, leave the existing Pi invocation unchanged: Pi chooses the default for new sessions and tries to restore the previous model for resumed sessions. Do not add a task-level model setting, a separate `--provider` flag, or arbitrary `--` passthrough.
- Update command help and test forwarding for all three commands, including the thinking suffix and the unchanged invocation when omitted.
