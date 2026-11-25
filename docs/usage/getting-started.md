# Getting started

After [installing `tasks`](../../README.md#install), run these commands from the root of the Git repository where you want to track tasks.

## Join existing task history

If shared task history already exists, fetch it into a local `tasks` branch. Replace `origin` with the appropriate remote, and skip the fetch if the local branch is already present:

```sh
git fetch origin refs/heads/tasks:refs/heads/tasks
tasks init
```

## Start new task history

Only use these steps if you are starting new, independent task history. Create an empty root commit without changing your checkout or index, run `tasks init` to create the `tasks/` checkout, and commit `tasks/.tasks.yml`. The empty expected old value in `git update-ref` prevents overwriting an existing branch. Replace `DEMO` with your project's [task ID prefix](configuration.md#repository-configuration):

```sh
tree=$(git hash-object -w -t tree --stdin </dev/null) &&
commit=$(git commit-tree "$tree" -m "chore: initialize task store") &&
git update-ref refs/heads/tasks "$commit" "" &&
tasks init &&
printf 'prefix: DEMO\n' > tasks/.tasks.yml &&
git -C tasks add .tasks.yml &&
git -C tasks commit -m "chore: configure tasks"
```

`tasks init` sets up or validates the local task checkout and can run before `.tasks.yml` is committed. It does not fetch, push, or reset the branch.

## Create your first task

```sh
tasks create "First task" --body "Describe the work"
tasks list
```

Run `tasks --help` for available commands or `tasks <command> --help` for command options.

## Optional: Push both branches by default

To make a plain `git push` update both your default target branch (for example, `main`) and `tasks` on `origin`, run these commands once after both branches exist locally. If Git uses a different remote or [default target branch](configuration.md#repository-configuration), replace `origin` or `main` accordingly.

```sh
git config --local --add remote.origin.push refs/heads/main:refs/heads/main
git config --local --add remote.origin.push refs/heads/tasks:refs/heads/tasks
```
