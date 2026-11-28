# Changes

## v0.5.0 — 2026-11-28

- feat: emit [OSC 7501 terminal program status](https://mitchellh.com/writing/program-status-osc7501) (`working`, `done`, `error`, `clear`) via tmux passthrough during `tasks run` and `tasks go` invocations. [Program status reporting can be configured](https://github.com/wincent/tasks/blob/v0.5.0/docs/usage/configuration.md#terminal-status) via `terminal.status` in `$XDG_CONFIG_HOME/tasks/config.yml` and the `TASKS_TERMINAL_STATUS` environment variable.
- feat: improve readability of `tasks merge` conflict and success output.
- feat: tailor Pi launch prompts separately for interactive (`tasks pi`) and non-interactive (`tasks run`, `tasks go`) runs.

## v0.4.0 — 2026-11-26

- feat: teach `tasks tail` to infer the only active task (if there is one) when invoked without a task number.

## v0.3.0 — 2026-11-25

- feat!: move repository configuration (`.tasks.yml`) from `main` branch to `tasks` branch (users must perform this one-time manual move).
- feat: add an optional `default_target_branch` setting (defaulting to `main`) to `.tasks.yml` so repositories can target a default branch other than `main`.
- feat: teach `tasks run`, `tasks go`, and `tasks pi` to accept a `--model` option to override Pi's model (and thinking level) for that launch.
- feat: support user-defined model aliases under `models.aliases` in `$XDG_CONFIG_HOME/tasks/config.yml` (defaulting to `~/.config/tasks/config.yml`) for use with `--model`, including `:thinking` suffix overrides.
- feat: add `tasks config` command to display and validate the parsed user configuration.

## v0.2.0 — 2026-11-24

- feat: teach `tasks create` and `tasks go` to accept a `--topic` option to make subsequent commands operating on the created task target a local topic branch instead of the default (`main`) branch.
- feat: add `tasks archive` and `tasks unarchive` commands.
- feat: teach `tasks list` and `tasks search` to accept an `--include-archived` option.
- feat: add `-q`/`--query` option to `tasks list`, `tasks search`, and `tasks archive` that can be used to select tasks with a simple query language (eg. `(status:merged OR status:closed) AND updated: < 30 days ago`).
- feat: add `tasks sh` for running a command in a task worktree.
- feat: teach `tasks pi` and `tasks run` to accept a `--prompt` option, which can be used to pass additional instructions to the agent.
- feat: add `tasks st` as an alias for `tasks status`.
- feat: add `tasks version` as an alias for `tasks --version`.
- feat: handle misspelled commands automatically when it is safe to do so (eg. `tasks lsti` will autocorrect to `tasks list`).

## v0.1.0 — 2026-11-22

- Initial release.
