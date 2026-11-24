# TASKS-39: Ensure `cleanup` does not use a stale destination

Status: active

## Description

Finding from agent review:

> **Cleanup can delete work using a stale destination.** The worktree and branch removal paths lock the implementation and destination refs, but not the task metadata used to select the destination. A concurrent Git commit can retarget the ticket before deletion.
>
> Pin the metadata revision through both destructive operations.
