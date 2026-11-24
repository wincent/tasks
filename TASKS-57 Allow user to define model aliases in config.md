# TASKS-57: Allow user to define model aliases in config

Status: open

## Description

Add support for a global config file (`$XDG_CONFIG_HOME/tasks/config.yml`, defaulting to `~/.config/tasks/config.yml`) where a user can define model aliases; eg.

```
models:
  aliases:
    sol: openai-codex/gpt-6.2-sol
```

This would then allow the user to conveniently write:

```
tasks run 10 --model sol
```

instead of the long-hand form:

```
tasks run 10 --model openai-codex/gpt-6.2-sol
```

Note that when an agent runs inside a sandbox, it may not have access to `~/.config/tasks/config.yml`. That's ok, because the only commands which would make use of the alias are commands that users run in terminals (`tasks run`, `tasks pi`, `tasks go`); agents themselves don't (or at least shouldn't) run these commands.
