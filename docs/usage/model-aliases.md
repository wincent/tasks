# Model aliases

Define aliases under `models.aliases` in your [user configuration](configuration.md#user-configuration) to use shorter names with `tasks run --model`, `tasks pi --model`, or `tasks go --model`:

```yaml
models:
  aliases:
    sol: openai-codex/gpt-6.2-sol
    fast: anthropic/claude-haiku-4-5:low
```

Pass an alias as-is or add a thinking-level suffix:

```sh
tasks run 10 --model sol
tasks run 10 --model sol:high
tasks run 10 --model fast:high
```

- An exact alias match expands to its configured target.
- With `<alias>:<thinking>`, the requested level replaces a Pi thinking-level suffix already on the target (`:off`, `:minimal`, `:low`, `:medium`, `:high`, `:xhigh`, or `:max`). Otherwise, the requested level is appended, preserving non-thinking colons in model IDs such as `ollama/llama3:8b`.
- Aliases do not chain to other aliases. Unmatched `--model` values pass through to Pi unchanged.

These commands read the user configuration only when `--model` is supplied. A missing file is treated as empty; an unreadable or invalid file causes the command to fail before changing task, worktree, or session state.
