# ADR-0004: Use a shared query language for tasks

## Status

Accepted. This ADR defines the intended behavior. The syntax and command-line interface are open.

## Context

As a project accumulates tickets, archiving them one by one becomes tedious. A selector for tickets that are merged and haven't been updated in more than 30 days would help with bulk archiving. The same selector could be useful for listing or searching tasks, so a query language specific to `archive` would create an unnecessary second way to filter tickets.

## Decision

Use one query language to select tasks for listing, search, and bulk actions such as archiving. An expression like `status:merged AND updated > 30 days` illustrates the desired selection, not a commitment to that syntax. Define whether a time predicate compares a timestamp or an elapsed age before choosing the notation. `updated` should reflect the last change to the ticket record, not a move into or out of the archive. A query filters the command's candidate set; it does not implicitly expand it to include archived tickets.

Bulk mutations should first preview the matching ticket IDs and require an explicit apply step. Applying a preview must not silently act on tickets that weren't shown. Read-only queries don't need an apply step.

## Consequences

Users can check which tickets a query matches before using it for a bulk action. Command-specific visibility rules still apply, so sharing a query language does not make archived tickets visible by default.
