# Tasks

- This is the repo for [wincent/tasks](https://github.com/wincent/tasks) (`tasks`).
- Use `tasks` when the user requests you to create a task. Task Markdown lives on the separate `tasks` branch; use `tasks show NUMBER` to read it.
- Work in your assigned checkout. Preserve unrelated edits and don't reset, force-remove, or overwrite somebody else's work. For a task run, commit your implementation there and let `tasks` handle merging. Don't push unless asked.
- Keep task results understandable after temporary files disappear. Write a brief, self-contained summary with the relevant revision, checks and outcomes (including failures and checks not run), and important limitations. Use repository-relative paths or clear placeholders in durable records. Use `tasks store NUMBER PATH ...` for small, sanitized artifacts worth keeping, such as final benchmark results or unique reproduction inputs. Link to retained evidence with Markdown paths relative to the ticket. Don't link to ephemeral files or bypass sandbox permissions.
