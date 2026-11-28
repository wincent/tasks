# TASKS-79: Make OSC 7501 terminal status configurable

Status: open

## Description

Allow users to disable (or explicitly re-enable) OSC 7501 program status emission (introduced in TASKS-78 / commit 75f1d46) via user configuration and an optional environment variable override.

## Requirements

### User configuration (`config.yml`)

- Support a top-level `terminal` mapping in `$XDG_CONFIG_HOME/tasks/config.yml` (or `~/.config/tasks/config.yml`), alongside the existing `models` mapping:
  ```yaml
  terminal:
    status: false
  ```
- Under `terminal`, support the key `status` (default `true` when omitted).
- Accept boolean truthy/falsy values (case-insensitive): `true`, `1`, `on` (and `false`, `0`, `off`). Reject empty or unrecognized values with a clear parse error.
- Apply the same strict validation conventions used for `models.aliases` in `internal/cli/config.go`: reject unknown keys under `terminal`, duplicate `terminal` or `status` keys, and non-mapping values for `terminal`.
- Update `tasks config` (`formatUserConfig`) to render `terminal.status` (normalized to `true` or `false`) when explicitly configured in `config.yml`, while preserving existing behavior (printing nothing when the file is missing or has no configured settings).

### Environment variable override (`TASKS_TERMINAL_STATUS`)

- Support `TASKS_TERMINAL_STATUS` as a per-session / per-invocation override accepting the same truthy/falsy values (`1`/`true`/`on` and `0`/`false`/`off`, case-insensitive).
- Treat an unset or empty `TASKS_TERMINAL_STATUS` as unspecified (fall back to `config.yml`, then default `true`).
- Reject invalid nonempty values with a clear error rather than silently ignoring typos.

### Precedence and runtime behavior

- Precedence: `TASKS_TERMINAL_STATUS` (when nonempty) > `terminal.status` in `config.yml` > default (`true`).
- An explicit override in `TASKS_TERMINAL_STATUS` works in both directions (e.g. `TASKS_TERMINAL_STATUS=1` re-enables status emission when `config.yml` sets `terminal.status: false`, and `TASKS_TERMINAL_STATUS=0` disables it when `config.yml` sets `true` or omits it).
- When resolved to `false`, `tasks run` and `tasks go` must not emit OSC 7501 sequences.
- When resolved to `true`, existing gating conditions still apply (stdout is a TTY, `TMUX` is nonempty, `TERM != "dumb"`, non-interactive `tasks run` and `tasks go` only, not `tasks pi`).
- Load user configuration once in `launchTask` and `tasks go` unconditionally (not only when `--model` is passed) so `terminal.status` takes effect without `--model` and invalid `config.yml` errors are surfaced consistently.

### Documentation and tests

- Document `terminal.status` and `TASKS_TERMINAL_STATUS` (including accepted values and precedence) in `docs/usage/configuration.md` (and any relevant command help text).
- Add tests covering `config.yml` parsing/validation/formatting (`tasks config`), `TASKS_TERMINAL_STATUS` parsing/validation/precedence in both directions, and `tasks run` / `tasks go` OSC 7501 emission gating.
