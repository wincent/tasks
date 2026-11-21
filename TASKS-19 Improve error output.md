# TASKS-19: Improve error output

Status: active

## Description

This error output:

```
tasks: tasks go expects a title, not a task reference; use 'tasks run 18' instead
```

is hard to read because `tasks go` is not delimited. It should read:

```
tasks: `tasks go` expects a title, not a task reference; use `tasks run 18` instead
```

In addition to fixing this, sweep through all existing command output to make sure that command examples are correctly delimited.
