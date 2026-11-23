# TASKS-35: Clean branches more aggressively

Status: merged

## Description

In another repo, `tasks cleanup` is refusing to delete a couple of branches whose implementations actually have been merged into `main`:

```
Keep branch task/cp-151-1668313131: branch is not merged into local main
Keep branch task/cp-153-3310234408: branch is not merged into local main
```

I asked an agent to compare the branches with what's on `main` and it reported:

> `tasks cleanup` checks whether the **exact branch tip** is an ancestor of local `main`. It doesn't rely on `Status: merged` or check for equivalent changes.
>
> | Task | Branch tip | Equivalent commit on `main` |
> |---|---|---|
> | CP-153 (Unlicense) | `9850085` | `b0f8f66` |
> | CP-151 (npm audit) | `577e3af` | `62e14bf` |
>
> I verified that each pair has identical file contents and that neither original tip is reachable from `main`. The work is already in `main`; cleanup is keeping the branches because their commit IDs differ. Nothing was deleted.

Can we teach `tasks cleanup` to compare the file contents in cases like this and _not_ skip cleaning up the branches when it is safe to remove them?
