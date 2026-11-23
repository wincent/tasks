# tasks

A command-line task tracker that stores tickets on a separate branch in your Git repository.

Architectural decisions are recorded in [docs/adrs/](docs/adrs/).

## Usage

### Common commands

| Command        | Description                                                 |
| -------------- | ----------------------------------------------------------- |
| `tasks create` | Create a task.                                              |
| `tasks show`   | Show the Markdown for a task.                               |
| `tasks list`   | List tasks by status.                                       |
| `tasks run`    | Run Pi non-interactively and merge into the recorded topic. |
| `tasks pi`     | Open a task in interactive Pi without merging.              |
| `tasks merge`  | Merge into the recorded topic without pushing.              |

### Other commands

| Command           | Description                                                   |
| ----------------- | ------------------------------------------------------------- |
| `tasks archive`   | Hide tasks from the default working set.                      |
| `tasks check`     | Check task files for errors.                                  |
| `tasks cleanup`   | Preview removal of merged task checkouts and branches.        |
| `tasks close`     | Close a task with a note, without merging or discarding work. |
| `tasks comment`   | Add a Markdown comment to a task.                             |
| `tasks edit`      | Edit a task's Markdown in your editor.                        |
| `tasks go`        | Create and run a task from its title alone.                   |
| `tasks help`      | Show help for tasks or a specific command.                    |
| `tasks init`      | Set up the local tasks checkout.                              |
| `tasks search`    | Search task titles.                                           |
| `tasks sh`        | Run a command in a task's existing checkout.                  |
| `tasks st`        | Alias for `tasks status`.                                     |
| `tasks status`    | Show running agents, merges and tasks that need attention.    |
| `tasks store`     | Save or list retained task artifacts.                         |
| `tasks tail`      | Follow a task's saved Pi session.                             |
| `tasks unarchive` | Restore archived tasks to the working set.                    |
| `tasks version`   | Print the version and exit.                                   |
| `tasks worktree`  | Print the path to a task's checkout.                          |

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

Use `--topic BRANCH` when filing a task that should land somewhere other than local `main`:

```sh
tasks create "Add export support" --topic feature/export --body "Support CSV exports"
tasks run 42
```

Replace `42` with the new task's number. Or use `tasks go "Add export support" --topic feature/export` to file and run a task in one step.

The task records `Topic: feature/export`. Filing alone does not create the topic or a task checkout. On the first `tasks run` or `tasks pi`, a fresh task checkout starts from the existing local topic branch. If the topic does not exist yet, it is created from the current local `main` without switching your checkout. If the topic disappears after work has started, restore it before resuming or merging.

A successful `tasks run` merges into the recorded topic, not the branch you have checked out. To review first, use `tasks run 42 --no-merge` or `tasks pi 42`, then `tasks merge 42` when ready. `tasks cleanup` keeps work if the recorded destination is missing. Otherwise, a task branch is eligible when its tip is reachable from the destination or when the destination contains an identical tracked snapshot outside their shared history.

Without `--topic` (or with `--topic main`), the task starts from and merges into local `main`. The task prefix always comes from `.tasks.yml` on local `main`.

## License

[The Unlicense](UNLICENSE.md) applies to code that the project has the rights to dedicate to the public domain. Third-party dependencies remain subject to their own license terms.
