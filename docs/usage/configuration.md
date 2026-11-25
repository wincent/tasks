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

Use `$XDG_CONFIG_HOME/tasks/config.yml` when `XDG_CONFIG_HOME` is an absolute path, or `~/.config/tasks/config.yml` otherwise. This file is optional and currently supports [model aliases](model-aliases.md).

Run `tasks config` to display the parsed user configuration. It prints nothing if the file is missing or empty, and exits with an error if it cannot read or parse the file.

`config.yml` accepts a strict UTF-8 YAML subset: space-indented block mappings under `models.aliases`, plain or single/double-quoted keys and scalar values, blank lines, and `#` comments.
