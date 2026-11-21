# TASKS-13: Avoid empty descriptions in tasks

Status: merged

## Description

When you run `tasks go "Some task title"`, it creates a task file like this:

```
# TASKS-13: Some task title

Status: open

## Description

```

In cases like this, rather that having a trailing blank line like that, we should instead produce:

```
# TASKS-13: Some task title

Status: open

## Description

None provided.
```
