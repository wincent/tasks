# TASKS-45: Customize query help

Status: active

## Description

`tasks help list` and `tasks help search` show an example that uses `tasks archive` to demonstrate the query syntax;

```
tasks archive -q 'updated: < 3 days ago'
```

Instead, each command's help should use a command-specific example of using query syntax.
