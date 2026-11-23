# tasks

A command-line task tracker that stores tickets on a separate branch in your Git repository.

Architectural decisions are recorded in [docs/adrs/](docs/adrs/).

## Prerequisites

- Go 1.24 or later, a C compiler, and `pkg-config` (macOS or Linux, arm64 or amd64).
- Shared libgit2 1.9.x with headers. Make sure `pkg-config --modversion libgit2` reports 1.9.x and the shared library is available at runtime.
- Git for repository setup, and Pi (`pi` on your `PATH`) for `tasks run` and `tasks pi`.

## Install

When a prebuilt archive is available on [GitHub Releases](https://github.com/wincent/tasks/releases), [verify it before extracting](docs/adrs/0002-provenance-based-releases.md#verify-a-download). The binary still needs shared libgit2 1.9.x at runtime.

From the root of this source repository:

```sh
bin/install
```

This installs `tasks` into `~/.local/bin`. Make sure that directory is on your `PATH`.

Or install from GitHub without cloning:

```sh
go install github.com/wincent/tasks/cmd/tasks@latest
```

For the GitHub install, put your Go binary directory (`go env GOBIN`, or `$(go env GOPATH)/bin` if unset) on your `PATH`.

## Set up a consuming repository

On the consuming repo's local `main` branch, commit a root `.tasks.yml` containing, for example, `prefix: DEMO`. Use uppercase ASCII letters and digits, starting with a letter, and omit the dash. If the project already has tasks, keep its existing prefix.

**If shared task history already exists**, fetch it into a local `tasks` branch. Replace `origin` with the appropriate remote, and skip the fetch if the local branch is already present:

```sh
git fetch origin refs/heads/tasks:refs/heads/tasks
tasks init
```

**Only if starting new, independent task history**, create an empty root commit without changing your checkout or index. The empty expected old value in `git update-ref` prevents overwriting an existing branch:

```sh
tree=$(git hash-object -w -t tree --stdin </dev/null) &&
commit=$(git commit-tree "$tree" -m "chore: initialize task store") &&
git update-ref refs/heads/tasks "$commit" "" &&
tasks init
```

`tasks init` sets up or validates the local task checkout. It does not fetch, push, or reset the branch. Try `tasks create "First task" --body "Describe the work"` and `tasks --help`.

### Push both branches by default

To make a plain `git push` update both `main` and `tasks` on `origin`, run these commands once in the consuming repository after both branches exist locally. If Git uses a different remote for pushes, replace `origin` with that remote's name.

```sh
git config --local --add remote.origin.push refs/heads/main:refs/heads/main
git config --local --add remote.origin.push refs/heads/tasks:refs/heads/tasks
```

## Work on a topic branch

`tasks` is optimized for working directly on the `main` branch, but it is possible (albeit not super convenient, yet[^yet]) to use it when working on a topic branch.

[^yet]: A future version will provide some kind of affordance for working more ergonomically on topic branches.

Checking out a topic branch does not change the defaults (`tasks` reads `.tasks.yml` from local `main`, starts fresh task branches from local `main`, and merges into local `main`). If a task should land on a topic branch instead, you must use `tasks` to track and run the work, and use Git to integrate it. Avoid `tasks go`, plain `tasks run NUMBER`, and `tasks merge NUMBER` for that workflow.

For example, suppose `DEMO-42` is an existing task with no checkout yet and it needs commits from a local branch named `topic`. From the root of a standard clone, create a linked worktree for the task starting at `topic`:

```sh
git worktree add -b task/demo-42 .agent-worktrees/demo-42 topic
```

Use `task/<lowercase-prefix>-<number>` for the branch name so `tasks` recognizes it. Keep the linked worktree inside the repository container so Pi can access it. For a bare repository with sibling worktrees, place the task worktree alongside the others and pass that path to `--worktree`.

Run the task without merging. Keep `--no-merge` on any subsequent `tasks run` for this task.

```sh
tasks run 42 --no-merge --worktree .agent-worktrees/demo-42
```

For an interactive session, use `tasks pi 42 --worktree .agent-worktrees/demo-42` instead (`tasks pi` never merges).

After reviewing the committed work, merge the task branch from a clean checkout of `topic` and close the task with a note. If `topic` is already checked out in another worktree, run the merge there instead of switching branches.

```sh
git switch topic
git merge task/demo-42
tasks close 42 --body "Integrated into topic."
```

If `tasks` created the checkout, use `tasks worktree 42` to find it and `git branch --show-current` there to get its branch name. `tasks close` records the outcome without merging or removing anything. `tasks cleanup` only removes task branches and checkouts whose tips are reachable from local `main`. If the work landed only on `topic` or was squash-merged, verify it was integrated before removing the checkout and branch with Git.

## License

[The Unlicense](UNLICENSE.md) applies to code that the project has the rights to dedicate to the public domain. Third-party dependencies remain subject to their own license terms.
