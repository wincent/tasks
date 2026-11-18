# tasks

A command-line task tracker that stores tickets on a separate branch in your Git repository.

Architectural decisions are recorded in [docs/adrs/](docs/adrs/).

## Prerequisites

- Go 1.24 or later, a C compiler, and `pkg-config` (macOS or Linux, arm64 or amd64).
- Shared libgit2 1.9.x with headers. Make sure `pkg-config --modversion libgit2` reports 1.9.x and the shared library is available at runtime.
- Git for repository setup, and Pi (`pi` on your `PATH`) for `tasks run` and `tasks pi`.

## Install

From the root of this source repository:

```sh
go install ./cmd/tasks
```

Put your Go binary directory (`go env GOBIN`, or `$(go env GOPATH)/bin` if unset) on your `PATH`.

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
