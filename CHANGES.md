# Changes

## Unreleased

- Tasks can target a local topic branch instead of `main`. Review and merge commands use the recorded destination.
- Archive tickets without changing their status or deleting their implementation. Restore them with `tasks unarchive`. List, search, and archive previews accept Boolean queries over title, status, and the date of the last committed ticket edit.
- Inspect a task checkout with `tasks sh`, including while Pi is running. Cleanup keeps the checkout while the command is running, even if the launcher dies.
- Cleanup keeps checkouts and branches used as recorded destinations. A task is eligible only if its tip is reachable from the current destination or merging it would leave the current destination unchanged without conflicts. Cleanup keeps work when it cannot verify that safely.
- Pass one-off instructions to Pi with `--prompt` on `tasks pi` and `tasks run`. Use `tasks st` as shorthand for `tasks status`.
- Prebuilt archives include third-party notices. Both `tasks version` and `tasks --version` report the release tag in packaged builds.

**Shared stores:** Upgrade everyone who uses the store, including automation, before recording a topic or archiving a ticket. v0.1.0 can silently merge topic work into `main`, and it can emit incomplete JSON and fail when it encounters archived tickets. See [shared-store upgrades and rollbacks](README.md#shared-store-upgrades-and-rollbacks) before switching clients.

## v0.1.0 — 2026-11-22

- Initial release.
