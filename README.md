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

## Set up a consuming repository

Repository-level settings live in a root `.tasks.yml` committed on `refs/heads/tasks`:

```yaml
prefix: DEMO
default_target_branch: main # optional; defaults to main
```

- `prefix` (required): uppercase ASCII letters and digits, starting with a letter, without the dash. If the project already has tasks, keep its existing prefix.
- `default_target_branch` (optional, defaults to `main`): the local implementation branch used when a task has no `Topic:` header (for example, `master`).

**If shared task history already exists**, fetch it into a local `tasks` branch. Replace `origin` with the appropriate remote, and skip the fetch if the local branch is already present:

```sh
git fetch origin refs/heads/tasks:refs/heads/tasks
tasks init
```

**Only if starting new, independent task history**, create an empty root commit without changing your checkout or index, run `tasks init` to create the `tasks/` checkout, and commit `tasks/.tasks.yml`. The empty expected old value in `git update-ref` prevents overwriting an existing branch:

```sh
tree=$(git hash-object -w -t tree --stdin </dev/null) &&
commit=$(git commit-tree "$tree" -m "chore: initialize task store") &&
git update-ref refs/heads/tasks "$commit" "" &&
tasks init &&
printf 'prefix: DEMO\n' > tasks/.tasks.yml &&
git -C tasks add .tasks.yml &&
git -C tasks commit -m "chore: configure tasks"
```

`tasks init` sets up or validates the local task checkout (and may be run before `.tasks.yml` is committed). It does not fetch, push, or reset the branch. Try `tasks create "First task" --body "Describe the work"` and `tasks --help`.

### Push both branches by default

To make a plain `git push` update both your default target branch (for example, `main`) and `tasks` on `origin`, run these commands once in the consuming repository after both branches exist locally. If Git uses a different remote or `default_target_branch`, replace `origin` or `main` accordingly.

```sh
git config --local --add remote.origin.push refs/heads/main:refs/heads/main
git config --local --add remote.origin.push refs/heads/tasks:refs/heads/tasks
```

## Configuration

Run `tasks config` to display the parsed user configuration from `$XDG_CONFIG_HOME/tasks/config.yml` (when `XDG_CONFIG_HOME` is an absolute path) or `~/.config/tasks/config.yml`. It prints nothing if the file is missing or empty, and exits with an error if it is invalid.

### Model aliases

You can define shorthand model aliases for `--model` (used by `tasks run`, `tasks pi`, and `tasks go`) in `$XDG_CONFIG_HOME/tasks/config.yml` when `XDG_CONFIG_HOME` is an absolute path, or `~/.config/tasks/config.yml` otherwise:

```yaml
models:
  aliases:
    sol: openai-codex/gpt-6.2-sol
    fast: datadog-anthropic/claude-haiku-4-5:low
```

Then pass an alias name with or without a thinking-level suffix:

```sh
tasks run 10 --model sol
tasks run 10 --model sol:high
tasks run 10 --model fast:high
```

- Exact alias matches expand directly to the configured target.
- When passing `<alias>:<thinking>`, if the alias target already ends with a Pi thinking-level suffix (`:off`, `:minimal`, `:low`, `:medium`, `:high`, `:xhigh`, `:max`), that suffix is replaced by `:<thinking>`; otherwise `:<thinking>` is appended, preserving non-thinking colons in model IDs such as `ollama/llama3:8b`.
- Lookups are single-step (aliases do not chain to other aliases), and unmatched `--model` values pass through to Pi unchanged.
- For launch commands, the config file is loaded only when `--model` is supplied to `tasks run`, `tasks pi`, or `tasks go`. A missing file is treated as empty; an unreadable or invalid file fails before mutating task, worktree, or session state.
- `config.yml` uses a strict UTF-8 YAML subset: space-indented block mappings under `models.aliases`, plain or single/double-quoted keys and scalar values, blank lines, and `#` comments.

## License

[The Unlicense](UNLICENSE.md) applies to code that the project has the rights to dedicate to the public domain. Third-party dependencies remain subject to their own license terms.
