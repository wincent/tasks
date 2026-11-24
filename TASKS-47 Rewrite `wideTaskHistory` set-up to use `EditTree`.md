# TASKS-47: Rewrite `wideTaskHistory` set-up to use `EditTree`

Status: merged

## Description

Agent finding:

> Rewrite `wideTaskHistory` setup to use `EditTree` (~3.2s saved in `internal/store`).
>
> `TestTaskHistoryScale` takes **3.63s** (#3 slowest test in the repo), and **3.19s (88%)** is fixture setup in `wideTaskHistory`. On each of the 80 commits it calls `WriteTree` from scratch on 302 root entries (and 999 archive entries on every even commit), resulting in ~64,000 `C.git_treebuilder_insert` calls (2.89s CPU). Changing each commit in the loop to call `f.Repository.EditTree(head, []git.TreeEdit{...})` mutates just the 1 changed entry per commit and drops `TestTaskHistoryScale` from 3.6s to 0.4s.
