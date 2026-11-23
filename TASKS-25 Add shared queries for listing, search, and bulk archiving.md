# TASKS-25: Add shared queries for listing, search, and bulk archiving

Status: open

## Description

Implement ADR-0004 using GitHub-style `key:value` predicates. The same query must mean the same thing in list, search, and bulk archive commands.

## CLI

- Add `-q EXPR` / `--query EXPR` to `tasks list`, `tasks search`, and `tasks archive`. Keep `tasks archive NUMBER` unchanged and make `NUMBER` and `--query` mutually exclusive for archive.
- Preserve existing status filters and archive visibility. The expression narrows the command's normal candidate set; it does not override `--status`, list's default statuses, or `--include-archived`. Use `tasks list all --query ...` to start from every unarchived status. Bulk archive only considers unarchived tickets.
- `tasks search` still requires its positional, case-insensitive title substring, and combines it with `--query` when supplied. Rename the positional metavariable from `QUERY` to `PATTERN` in `tasks search --help`, and describe it as literal text in help and validation errors. `tasks search 'status:merged'` must still search for that exact text, ignoring case.

## Syntax and parsing

- Keep each predicate in `key:value` form. Support `status:merged` (exact status), `title:"export report"` (case-insensitive title substring), and `updated:<2025-01-01` or `updated:<30.days.ago` (last update before the given cutoff). Put the optional comparison operator immediately after the colon. Support `<`, `<=`, `>`, and `>=` on `updated`; do not introduce natural-language predicates or Git's full date parser.
- Accept strict ISO calendar dates (`YYYY-MM-DD`) and dotted relative day values (`N.days.ago`). `updated:2025-01-01` and `updated:30.days.ago` without a comparator match the corresponding UTC day. For calendar dates, compare UTC dates; for relative values with a comparator, compare timestamps against `now - N * 24h`. Capture `now` once per invocation. `<` always means earlier, so `updated:<30.days.ago` selects older tickets and `updated:>30.days.ago` selects newer ones.
- Support parentheses and explicit `NOT`, `AND`, and `OR`, in that precedence order. Do not support implicit `AND`. Parse once with a small Go lexer and recursive-descent parser into a typed expression. Keep evaluation independent of the CLI and Git. Reject unknown fields, invalid or overflowing values, malformed expressions, and trailing text with a useful source position.
- Derive `updated` from the last committed change to the ticket title or Markdown contents, including creation. Moving the same record into or out of the archive is not an update. Only access history for queries that use `updated`; report unavailable history rather than guessing.

## Bulk archive

- Preview matching IDs before asking for explicit confirmation in a terminal. If there is no terminal, print the preview and make no changes. Do not mutate on an empty match, a declined confirmation, or a failed or incomplete selection.
- Apply only the previewed set. Revalidate each selected ticket under the existing locking and archive safety rules, and abort the batch if any selected ticket changed or is no longer eligible. Do not rerun the query to add new matches after confirmation. No saved-plan format in this task.

Example: `tasks archive --query 'status:merged AND updated:<30.days.ago'`.

Test date boundaries and comparison direction with a fixed clock, precedence and invalid syntax, unchanged `updated` across archive/unarchive, list/search filters and literal search, and the preview/apply behavior for non-interactive runs, cancellation, and changes between preview and apply.
