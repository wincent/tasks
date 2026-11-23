# TASKS-31: Find subcommands by edit distance

Status: active

## Description

Use something like Jaro-Winkler similarity to identify misspelled commands.

eg. instead of 

```
$ tasks lsti
tasks: unknown command: lsti
```

We could print:

```
warning: subcommand `lsti` does not exist; using `list` instead.
```

We would do that only for safe, read-only subcommands (like `list`).

For most mutating commands, rather than autocorrecting, we would elevate the warning to an error and abort; eg.

```
$ tasks og "Don't just do something, stand there!"
error: subcommand `og` does not exist; did you mean `go`?
```

For some mutating commands, the ones that require user confirmation, we can proceed with the autocorrect, because if the user doesn't want the correction to apply, they can refuse confirmation; eg. `archive`:

```
$ tasks arcihve 10
warning: subcommand `arcihve` does not exist; using `archive` instead.
```

Another example of such a command is `task create`, because a user can abort creation by causing their `$EDITOR` to exit with a non-zero exit code (eg. `:cq` in Neovim).

For now, apply this Jaro-Winkler similarity testing to subcommands only. We may later decide to apply a similar treatment to command options, but that is beyond the scope of this task.
