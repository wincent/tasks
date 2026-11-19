# TASKS-9: Document common remote set-up

Status: active

## Description

Provide an example in the README.md of how to set a repo so that `git push` pushes to both the `main` and the `tasks` branch by default. For example:

```
git config --local --add remote.origin.push refs/heads/main:refs/heads/main
git config --local --add remote.origin.push refs/heads/tasks:refs/heads/tasks
```
