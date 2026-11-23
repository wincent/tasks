# TASKS-23: Support topic branches with --topic

Status: active

## Description

Replace the manual topic-branch workflow documented in commit `9af05de` with a task-level setting. Choose the topic when filing the task and let later commands use it without repeating the flag.

- Accept `--topic BRANCH` on `tasks create` and `tasks go`. Record an explicit topic as `Topic: BRANCH` next to `Status:` in the task Markdown. Accept local branch names. With no topic (including `--topic main`), omit the field and keep the existing `main` behavior. Keep reading project configuration and the task prefix from local `main`.
- `tasks create` must not create a Git branch or worktree. On first `tasks run` or `tasks pi`, use the existing local topic branch, or create it from the then-current local `main` without switching the caller's checkout. Start fresh task worktrees from the topic. `tasks go --topic BRANCH` records the topic and then follows the normal run path. Reuse existing task branches and checkouts; if resuming existing work and the topic is missing, report the problem rather than silently recreating it from `main`.
- `tasks run NUMBER` automatically merges successful work into the recorded topic. `--no-merge` still leaves the work for review, and `tasks pi` still never merges. `tasks merge NUMBER` uses the recorded topic regardless of the caller's checkout, preserves the existing merge safety checks, and does not create a missing topic.
- Make `tasks cleanup` check each task branch tip against its recorded destination and preserve existing removal safeguards. If the topic is missing, keep the work; do not create the topic or fall back to `main`.
- Show the topic in launch and merge messages. Update the CLI help and replace the manual topic workflow in the README. Add coverage for deferred creation from the latest local `main`, reuse of an existing topic, automatic and explicit topic merges that leave `main` unchanged, and cleanup of topic-only work.
