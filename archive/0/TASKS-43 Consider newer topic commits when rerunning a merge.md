# TASKS-43: Consider newer topic commits when rerunning a merge

Status: merged

## Description

Agent finding:

> Rerunning a merged topic task can miss newer topic commits
>
> Suppose a task is merged into `delivery` at commit A. The merge removes the clean worktree but keeps the task branch. Then `delivery` advances to B. If you rerun the task, `launchTask` recreates the worktree at A. It already knows how to recognize an integrated retained branch and start fresh at B, but only applies that rule to `main`.
>
> The rerun can therefore work against outdated code and, if it makes commits, fail to merge under `merge.ff=only`. I'd apply the same ancestry check to the recorded topic: start at its current tip if the old task tip is already contained there, and keep the old tip if it still has unmerged work.
