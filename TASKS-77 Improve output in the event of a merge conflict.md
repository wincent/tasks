# TASKS-77: Improve output in the event of a merge conflict

Status: active

## Description

Compare the final output of a `tasks run` invocation which failed to integrate back into `main` due to a merge conflict:

```
🔄 Merging EXAMPLE-225 (Some task description) into local main...
tasks: EXAMPLE-225 needs attention: merge conflict; main is unchanged. In /home/user/code/project/.agent-worktrees/cp-225-4202455728, merge main, resolve and commit, then retry
Retry with `tasks merge 225`
Checkout: /home/user/code/project/.agent-worktrees/cp-225-4202455728; session: /home/user/.pi/agent/sessions/dev.wincent.tasks/73862a7f-a293-4f58-afa4-10c854d23795.jsonl
```

With the final output of a successful `tasks run`:

```
🔄 Merging EXAMPLE-226 (Other task description) into local main...
✅ EXAMPLE-226 (Other task description) merged into local main.
Session: /home/user/.pi/agent/sessions/dev.wincent.tasks/a4f1fd0c-f1ab-4214-a3c0-9a91ca70737f.jsonl
```

It is clear at a glance that the latter succeeded due to the ✅ emoji. The merge-conflict case, however, is _not_ clear at a glance. Ideally, instead, we would:

- Not show the "tasks:" prefix before the essential information in the line.
- Make the line stand out with an emoji indicating that `tasks` was blocked (eg. 🚫).
- When outputting to an interactive terminal, underline the critical part of the message (ie. "merge conflict; main is unchanged").
- Avoid the redundancy of "then retry" and "Retry with"; perhaps we should combine those into a single "then retry with `tasks merge 225`).
- Drop the line with "Checkout:" and "session:" info, as the checkout directory was already listed above, and I don't think I've ever looked up a session JSONL file directly.

(For the same reason, in the success case, we can probably afford to drop the "Session:" line, as I don't recall ever having accessed one of these session files after the fact.)
