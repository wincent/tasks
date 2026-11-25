# TASKS-65: Move .tasks.yml to tasks branch and add default_target_branch setting

Status: open

## Description

Move repository-level configuration (`.tasks.yml`) from `refs/heads/main` to `refs/heads/tasks`, and introduce an optional `default_target_branch` setting (defaulting to `main`) so consuming repositories do not need to use `main` as their primary implementation branch.

Putting `.tasks.yml` on `refs/heads/tasks` breaks the chicken-and-egg problem of needing configuration to find the branch that holds configuration: `refs/heads/tasks` is already a fixed, committed, worktree-independent branch shared across clones.

Existing consuming repositories (3 total) will be migrated manually in a one-time move of `.tasks.yml` from `main` to `refs/heads/tasks`, so dual-location fallback logic is not needed.

### Requirements

#### Config location and schema

- Read `.tasks.yml` from the committed tip of `refs/heads/tasks` instead of `refs/heads/main`. (`store.Scan` already ignores dotfiles, so `.tasks.yml` at the root of `refs/heads/tasks` will not interfere with `tasks check`.)
- Keep the strict, dependency-free parser (per ADR-0005) and extend `.tasks.yml` to support two top-level keys:
  - `prefix` (required): existing uppercase ASCII ticket prefix validation.
  - `default_target_branch` (optional, defaults to `main` when omitted): local implementation branch name to use when a task has no `Topic:` header.
- Validate `default_target_branch` using the same rules as a topic branch (valid Git branch name, no `refs/` prefix, not `tasks`, not in the reserved `task/` namespace, and when resolved it must be a direct non-symbolic branch).
- Reject unknown keys, duplicate keys, invalid syntax, non-regular files (e.g. symlinks), and symbolic `refs/heads/tasks` with clear setup/error messages referencing `refs/heads/tasks`.

#### Bootstrap and `tasks init`

- Allow `tasks init` to run before `.tasks.yml` is committed on `refs/heads/tasks` (it does not use `prefix`). When `.tasks.yml` is absent during `tasks init`, default the target branch to `main` for the orphan-history independence check (which already tolerates a missing target branch). This lets new repositories create an empty orphan `refs/heads/tasks` commit, run `tasks init` to set up the `tasks/` worktree, and then commit `tasks/.tasks.yml` normally.
- Commands that require `prefix` should continue to fail cleanly with updated setup instructions if `refs/heads/tasks` or its committed `.tasks.yml` is missing.

#### Replace hardcoded `main` assumptions with `default_target_branch`

- **Orphan-history independence checks** (`internal/store/store.go`, `internal/store/preview.go`): verify `refs/heads/tasks` shares no merge-base with `refs/heads/<default_target_branch>` (and that it is a direct branch if present) instead of hardcoded `refs/heads/main`.
- **Topic parsing and creation** (`internal/store/task.go`, `internal/cli/command_create.go`):
  - Stop stripping `Topic: main` inside `store.Topic()` so `Topic: main` works as a normal custom topic branch in repositories where `default_target_branch` is not `main` (e.g. `master`). Return the raw `Topic:` value (empty if omitted) and interpret empty as `default_target_branch` at the CLI/destination layer.
  - In `tasks create` / `tasks go`, omit writing a `Topic:` line when `--topic` matches `default_target_branch`.
- **Destination resolution and launch** (`internal/cli/destination.go`, `internal/cli/editor.go`):
  - Parameterize destination helpers (`taskDestination`, `tip`, `launchDestination`, `editTask`, etc.) by the configured `default_target_branch` instead of hardcoded `"main"` / `mainRef`.
  - Keep refusing to auto-create `default_target_branch` when missing, and base newly auto-created topic branches on `default_target_branch` (pinning its ref in the transaction).
  - Apply the case-insensitive exact-branch-name check uniformly for all destinations (including `default_target_branch`).

#### Simplify legacy no-store cleanup

- Previously, `tasks cleanup` supported a "legacy no-store" fallback (`store.MayHaveSharedHistory` and `inspectCleanupStore` returning a `nil` store) because `prefix` could be read from `refs/heads/main` even when `refs/heads/tasks` did not exist.
- With `.tasks.yml` on `refs/heads/tasks`, `tasks cleanup` cannot load `prefix` or `default_target_branch` when `refs/heads/tasks` is absent. Remove the now-unreachable no-store fallback (`store.MayHaveSharedHistory`, the `nil`-store path in `inspectCleanupStore` / `cleanupDestinationWithStore` / `requireUnrecordedDestination`, and obsolete no-store tests) while preserving the fallback to `default_target_branch` when `refs/heads/tasks` exists and an orphaned `task/<prefix>-<n>` branch has no ticket record (`store.ErrTaskNotFound`).

#### Future compatibility (do not implement yet)

- Do not add Git config overrides in this task. Keep the store-branch reference (`store.Branch`) cleanly centralized so that a future optional repo-level Git config setting such as `tasks.storeBranch` (evaluated via `BranchConfigurationEntries` / `CommonDir` rather than worktree-scoped `HEAD`) can override where both `.tasks.yml` and task records live without breaking `.tasks.yml`.

#### Documentation and tests

- Update CLI help text (`internal/cli/help.go`, `internal/cli/command_cleanup.go`, `internal/cli/command_go.go`, `internal/cli/command_pi.go`, `internal/cli/command_run.go`, `internal/cli/merge.go`) and `README.md` to reflect `.tasks.yml` living on `refs/heads/tasks` and document `default_target_branch`.
- Update existing test fixtures to commit `.tasks.yml` on `refs/heads/tasks`.
- Add unit and CLI tests covering:
  - `.tasks.yml` parsing with `prefix` alone (defaults to `main`), `prefix` + `default_target_branch`, quoted values, comments, duplicate/unknown keys, invalid branch names, and missing `.tasks.yml`.
  - End-to-end workflows (`init`, `create`, `edit`, `run`/`pi`, `merge`, `cleanup`, auto-creating topics) in a repository whose `default_target_branch` is not `main` (e.g. `master`), including using `--topic main` as a custom topic branch.
  - `tasks init` succeeding when `refs/heads/tasks` exists without `.tasks.yml`, followed by other commands reporting the missing `.tasks.yml` on `refs/heads/tasks`.
  - Orphan-history verification failing when `refs/heads/tasks` shares history with the configured `default_target_branch`.
