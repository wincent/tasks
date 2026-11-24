# TASKS-42: Deal with staged renames in `tasks show`

Status: open

## Description

Agent finding:

> `show` treats a staged rename as two competing records
>
> After a `git mv` that changes only a ticket's title, the working tree has one ticket for that number, under the new name. The old name still exists in `HEAD` until you commit. `Store.read` treats the missing old path plus the new path as ambiguous. In a fixture, v0.1.0 showed the ticket and v0.2.0 listed it under the new title, but v0.2.0 `show 1` refused to display it.
>
> That check is useful for cleanup and mutation, where guessing at a missing record can be dangerous. `show` doesn't need to guess: it can display the one valid working-tree record and still reject two actual files with the same ID. The fix should cover both `show 1` and `show` without a number when the renamed ticket is the latest.
