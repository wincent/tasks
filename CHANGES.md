# Changes

## v0.2.0 — 2026-11-24

- feat: teach `tasks create` and `tasks go` to accept a `--topic` option to make subsequent commands operating on the created task target a local topic branch instead of the default (`main`) branch.
- feat: add `tasks archive` and `tasks unarchive` commands.
- feat: teach `tasks list` and `tasks search` to accept an `--include-archived` option.
- feat: add `-q`/`--query` option to `tasks list`, `tasks search`, and `tasks archive` that can be used to select tasks with a simple query language (eg. `(status:merged OR status:closed) AND updated: < 30 days ago`).
- feat: add `tasks sh` for running a command in a task worktree.
- feat: teach `tasks pi` and `tasks run` to accept a `--prompt` option, which can be used to pass additional instructions to the agent.
- feat: add `tasks st` as an alias for `tasks status`.
- feat: add `tasks version` as an alias for `tasks --version`.

## v0.1.0 — 2026-11-22

- Initial release.
