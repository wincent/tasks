# tasks

A command-line task tracker that stores tickets on a separate branch in your Git repository.

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
| `tasks config`    | Show parsed user configuration.                               |
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

## Documentation

For users:

- [Getting started](docs/usage/getting-started.md): Set up a repository and create your first task.
- [Configuration](docs/usage/configuration.md): Configure per-repository and global (user) settings.
- [CHANGES.md](CHANGES.md): Project changelog.

For developers and their agents:

- [Architecture decision records](docs/adrs/): Info on project design decisions.
- [CONTRIBUTING.md](CONTRIBUTING.md): Instructions for maintainers and contributors.
- [AGENTS.md](AGENTS.md): Agent-specific instructions.

## License

[The Unlicense](UNLICENSE.md) applies to code that the project has the rights to dedicate to the public domain. Third-party dependencies remain subject to their own license terms.
