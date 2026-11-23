# TASKS-27: Accept natural date expressions in updated: queries

Status: merged

## Description

`tasks archive -q 'updated: < 3 days ago'` fails even though the shell passes the query correctly as one argument. Today the query parser requires a value immediately after `:`, stops unquoted values at the first space, and only recognizes double quotes. A bare `<` ends up as the misleading `invalid updated value ""`.

- Accept optional whitespace after `updated:` and after `<`, `<=`, `>`, or `>=`. Parse exactly one date expression, including unquoted `number unit ago`, without consuming a following boolean operator or closing parenthesis.
- Preserve existing compact, dotted, calendar-date, and double-quoted forms. Accept single-quoted `updated:` values too, without changing how apostrophes in `title:` values are matched. Keep existing date and cutoff semantics.
- Show a copy-pasteable example in query help. Report an actionable error such as `expected a date after '<'` for a bare comparator, and reject incomplete phrases like `updated: < 3 days`.
- Add regression tests showing that all of these query strings match the same records, including the exact cutoff boundary, and that a spaced date works in a compound query such as `(updated: < 3 days ago AND status:merged)`:

```text
updated: < 3 days ago
updated:< 3 days ago
updated:'< 3 days ago'
updated:'< 3.days.ago'
updated:'<3.days.ago'
updated:<3.days.ago
updated:"< 3 days ago"
```
