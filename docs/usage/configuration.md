# Configuration

`tasks` reads shared settings from `.tasks.yml` on the repository's `tasks` branch and personal settings from your user-level `config.yml`.

## Repository configuration

Commit `.tasks.yml` at the root of the `tasks` branch (`refs/heads/tasks`), not on your implementation branch. If you use the checkout created by `tasks init`, the file is `tasks/.tasks.yml` relative to your main checkout. See [Getting started](getting-started.md) for setup instructions.

```yaml
prefix: DEMO
default_target_branch: main # optional; defaults to main
```

- `prefix` (required): the task ID prefix, such as `DEMO` for `DEMO-1`. Use uppercase ASCII letters and digits, starting with a letter, without the dash. If the project already has tasks, keep its existing prefix.
- `default_target_branch` (optional, defaults to `main`): the local implementation branch used when a task has no `Topic:` header. Set it to another branch (for example, `master`) if needed.

## User configuration

Use `$XDG_CONFIG_HOME/tasks/config.yml` when `XDG_CONFIG_HOME` is an absolute path, or `~/.config/tasks/config.yml` otherwise. This file is optional and supports [model aliases](model-aliases.md) (`models.aliases`) and [terminal status reporting](#terminal-status) (`terminal.status`).

Run `tasks config` to display the parsed user configuration. It prints nothing if the file is missing or has no configured settings, and exits with an error if it cannot read or parse the file.

`config.yml` accepts a strict UTF-8 YAML subset: space-indented block mappings under `models.aliases` and `terminal.status`, plain or single/double-quoted keys and scalar values, blank lines, and `#` comments.

### Terminal status

When running non-interactively (`tasks run` and `tasks go`, not `tasks pi`), `tasks` emits OSC 7501 program status updates wrapped in tmux DCS passthrough if stdout is a terminal, `TMUX` is nonempty, and `TERM` is not `dumb`.

To disable (or explicitly enable) OSC 7501 status emission in `config.yml`, set `terminal.status`:

```yaml
terminal:
  status: false
```

- Accepted values (case-insensitive): `true`, `1`, `on` (enable) and `false`, `0`, `off` (disable). Empty or unrecognized values fail with a parse error.
- When omitted, `terminal.status` defaults to `true`. `tasks config` renders `terminal.status` (normalized to `true` or `false`) only when explicitly set in `config.yml`.
- Override per session or per invocation with the `TASKS_TERMINAL_STATUS` environment variable, which accepts the same case-insensitive values (`true`/`1`/`on` and `false`/`0`/`off`). An unset or empty `TASKS_TERMINAL_STATUS` falls back to `config.yml`; an invalid nonempty value fails with an error.
- Precedence: `TASKS_TERMINAL_STATUS` (when nonempty) > `terminal.status` in `config.yml` > default (`true`). `TASKS_TERMINAL_STATUS` overrides `config.yml` in both directions (for example, `TASKS_TERMINAL_STATUS=1` re-enables status emission when `config.yml` sets `terminal.status: false`, and `TASKS_TERMINAL_STATUS=0` disables it when `config.yml` sets `true` or omits it).
- Enabling terminal status does not bypass terminal gating: sequences are still only emitted for `tasks run` and `tasks go` when stdout is a terminal, `TMUX` is nonempty, and `TERM` is not `dumb`.
