# TASKS-57: Allow user to define model aliases in config

Status: merged

## Description

Add support for a global config file (`$XDG_CONFIG_HOME/tasks/config.yml`, defaulting to `~/.config/tasks/config.yml`) where a user can define model aliases; eg.

```yaml
models:
  aliases:
    sol: openai-codex/gpt-6.2-sol
    fast: datadog-anthropic/claude-haiku-4-5:low
```

This allows the user to conveniently write:

```sh
tasks run 10 --model sol
tasks run 10 --model sol:high
```

instead of the long-hand form:

```sh
tasks run 10 --model openai-codex/gpt-6.2-sol
tasks run 10 --model openai-codex/gpt-6.2-sol:high
```

This is especially useful when multiple providers are authenticated in Pi (where bare fuzzy patterns like `--model sol` fail as ambiguous) or for semantic names (`fast`, `deep`) and default thinking levels.

Note that when an agent runs inside a sandbox, it may not have access to `~/.config/tasks/config.yml`. That's ok, because the only commands which use the alias config are launch commands that users run in terminals (`tasks run`, `tasks pi`, `tasks go`); agents themselves don't (or at least shouldn't) run these commands.

### Requirements

#### Config file location and XDG resolution

- Resolve the config file path as `$XDG_CONFIG_HOME/tasks/config.yml` when `XDG_CONFIG_HOME` is set and an absolute path (per the XDG Base Directory Specification); otherwise fall back to `$HOME/.config/tasks/config.yml`.
- Read `XDG_CONFIG_HOME` and `HOME` through the `IO` environment helpers in `internal/cli/env.go` (not `os.Getenv` / `os.UserHomeDir`) so `t.Parallel()` tests remain isolated.
- If the config file does not exist (`fs.ErrNotExist`), silently treat it as empty (no aliases).
- If the config file exists but cannot be read (e.g. permission error `EACCES`/`EPERM`, or the path is a directory) or fails validation/parsing, return a clear error.

#### Format and parser (no new dependencies)

- Per ADR-0005, do not add a third-party YAML dependency. Implement a small, strictly validated parser for a narrow, documented YAML subset supporting the nested `models.aliases` mapping:
  - UTF-8 text (reject invalid UTF-8), LF or CRLF line endings, blank lines, and `#` comment lines.
  - Inline `#` comments on unquoted values only when preceded by whitespace (preserving `#` inside quoted scalars).
  - Space-indented block mappings with plain, single-quoted, or double-quoted keys and scalar values.
  - Unquoted values containing colons (such as `openai-codex/gpt-6.2-sol:high`) must parse cleanly; split key/value on the first YAML mapping separator (`:` followed by whitespace or end-of-line, outside quotes).
  - Reject tabs for indentation, inconsistent indentation, flow collections (`{}` / `[]`), sequences (`- ...`), multiline scalars, anchors/aliases, and other unsupported YAML syntax with line-numbered errors.
- Apply strict schema validation:
  - Allow an empty / comments-only file, an empty `models:` section, or an empty `aliases:` section (defining zero aliases).
  - Reject unknown keys at every level (only `models` at top level, only `aliases` under `models`).
  - Reject duplicate keys at any level (including duplicate alias names).
  - Reject empty or whitespace-only alias names or target values, and reject alias names containing whitespace or `:`.

#### Alias resolution semantics

- Single-step lookup only (do not chain alias-to-alias lookups).
- Look up the `--model` value:
  - First check for an exact alias match. If matched, expand directly to the alias target.
  - Otherwise, if `--model` contains `:` with a nonempty base and suffix (`<alias>:<thinking>`), look up `<alias>`. If matched:
    - If the alias target already ends with a Pi thinking-level suffix (`:off`, `:minimal`, `:low`, `:medium`, `:high`, `:xhigh`, `:max`), replace that suffix with the CLI `:<thinking>` suffix (CLI overrides alias).
    - Otherwise, append `:<thinking>` to the alias target (preserving non-thinking colons in model IDs such as `ollama/llama3:8b`).
  - If no alias matches, forward the original `--model` value unchanged to Pi.

#### Lifecycle, lazy loading, and error handling

- Load and parse `config.yml` lazily, only when `--model` is supplied to `tasks run`, `tasks pi`, or `tasks go`. Do not read `config.yml` for other commands (`show`, `list`, `comment`, `check`, etc.) or when `--model` is omitted.
- Keep `tasks check` scoped to repository task files only (do not validate user config there).
- Validate `config.yml` and resolve `--model` before mutating any repository, task, worktree, or session state:
  - In `tasks go`, resolve before `createTask` so an invalid `config.yml` never creates a task commit.
  - In `launchTask` (`tasks run`, `tasks pi`, `tasks go`), resolve up front before creating a worktree, running hooks, writing session metadata, or marking the task `active`.

#### Documentation and tests

- Update `modelHelp` in `internal/cli/help.go` and `README.md` to document the config file path, schema, and `alias:thinking` behavior.
- Add unit tests for the config parser (valid mappings, comments, quoted/unquoted scalars with colons, empty files/sections, unknown keys, duplicate keys, empty/invalid alias names or values, bad indentation, tabs, unsupported YAML constructs, invalid UTF-8) and XDG path resolution (absolute vs. relative/unset `XDG_CONFIG_HOME`, missing file vs. unreadable file/directory).
- Add CLI launch tests for `run`, `pi`, and `go` covering alias expansion, `alias:thinking` appending, `alias:thinking` overriding a target's `:thinking` suffix, non-chaining, passthrough of unmatched models, lazy loading when `--model` is omitted (even if `config.yml` is invalid), and failing before mutating task/worktree/session state when `config.yml` is invalid.
