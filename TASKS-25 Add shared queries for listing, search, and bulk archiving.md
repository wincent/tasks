# TASKS-25: Add shared queries for listing, search, and bulk archiving

Status: active

## Description

Implement ADR-0004 using GitHub-style `key:value` predicates. The same query must mean the same thing in list, search, and bulk archive commands.

## CLI

- Add `-q EXPR` / `--query EXPR` to `tasks list`, `tasks search`, and `tasks archive`. Keep `tasks archive NUMBER` unchanged and make `NUMBER` and `--query` mutually exclusive for archive.
- Preserve existing status filters and archive visibility. The expression narrows the command's normal candidate set; it does not override `--status`, list's default statuses, or `--include-archived`. Use `tasks list all --query ...` to start from every unarchived status. Bulk archive only considers unarchived tickets.
- `tasks search` still requires its positional, case-insensitive title substring, and combines it with `--query` when supplied. Rename the positional metavariable from `QUERY` to `PATTERN` in `tasks search --help`, and describe it as literal text in help and validation errors. `tasks search 'status:merged'` must still search for that exact text, ignoring case.

## Syntax and parsing

- Keep each predicate in `key:value` form. Support `status:merged` (exact status), `title:"export report"` (case-insensitive title substring), and `updated` with an optional `<`, `<=`, `>`, or `>=` at the start of its value. Accept either an unquoted value without spaces or a double-quoted value with optional whitespace around the comparator and date value. For example, `updated:<2026-10-01`, `updated:"<2026-10-01"`, and `updated:"< 2026-10-01"` are equivalent. Do not use Git's full date parser.
- Accept `YYYY`, `YYYY-MM`, `YYYY-MM-DD`, `today`, and `yesterday` as UTC calendar periods. Without a comparator, match any update within that period: `updated:2026` matches the whole year, `updated:2026-10` the whole month, and `updated:today` and `updated:yesterday` their respective days. `<` means before the start of the period, `>=` means at or after the start, `<=` means before the end, and `>` means at or after the end. For example, `updated:<2026` means before 2026 and `updated:<2026-10` means before October 2026.
- Accept relative values in either dotted or quoted space-separated form: `updated:30.days.ago`, `updated:"30.days.ago"`, and `updated:"30 days ago"` are equivalent. Allow whitespace between words in quoted values. Support integer minutes, hours, days, weeks, and months, in singular or plural; use fixed durations of 60 seconds per minute, 60 minutes per hour, 24 hours per day, 7 days per week, and 30 days per month. Capture `now` once per invocation. With a comparator, compare against the exact instant `now` minus that duration: `updated:<30.days.ago` selects older updates and `updated:>30.days.ago` selects newer ones. Without a comparator, match the UTC day containing the cutoff for days, weeks, and months, or the UTC hour or minute containing it for hours or minutes.
- Support parentheses and explicit `NOT`, `AND`, and `OR`, in that precedence order. Do not support implicit `AND`. Parse once with a small Go lexer and recursive-descent parser into a typed expression. Keep evaluation independent of the CLI and Git. Reject unknown fields, invalid or overflowing values, malformed expressions, and trailing text with a useful source position.
- Derive `updated` from the last committed change to the ticket title or Markdown contents, including creation. Moving the same record into or out of the archive is not an update. Only access history for queries that use `updated`; report unavailable history rather than guessing.

## Bulk archive

- Preview matching IDs before asking for explicit confirmation in a terminal. If there is no terminal, print the preview and make no changes. Do not mutate on an empty match, a declined confirmation, or a failed or incomplete selection.
- Apply only the previewed set. Revalidate each selected ticket under the existing locking and archive safety rules, and abort the batch if any selected ticket changed or is no longer eligible. Do not rerun the query to add new matches after confirmation. No saved-plan format in this task.

Example: `tasks archive --query 'status:merged AND updated:<30.days.ago'`.

Test equivalent quoted and unquoted values, whitespace, relative units, and UTC year, month, and day boundaries (including `today` and `yesterday`) with a fixed clock. Also test comparison direction, precedence and invalid syntax, unchanged `updated` across archive/unarchive, list/search filters and literal search, and the preview/apply behavior for non-interactive runs, cancellation, and changes between preview and apply.
