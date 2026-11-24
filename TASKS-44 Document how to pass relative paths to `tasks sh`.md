# TASKS-44: Document how to pass relative paths to `tasks sh`

Status: active

## Description

Agent finding:

> `tasks sh` changes the child's directory, not where Go looks up its executable
>
> With `PATH=helpers:$PATH` and `review-helper` present only in the task checkout's `helpers` directory, `tasks sh 1 -- review-helper` returned `127`. `tasks sh 1 -- ./helpers/review-helper` worked. The difference is that `exec.Command` looks up a bare command name before `child.Dir` is set. An explicit path containing a slash skips that lookup and is resolved from the child's directory.
>
> I'd document the distinction and recommend explicit checkout-relative paths for repo-local tools. I wouldn't make bare names search the checkout by disabling Go's protection against implicitly running executables from relative `PATH` entries.

I pushed back against the agent that this was a rather obscure edge case, and it agreed in these terms:

> I agree this is an edge case. Someone who deliberately uses a relative `PATH` might expect `tasks sh` to behave like `cd` followed by a shell command, but I wouldn't change the implementation or document the lookup rules for that.
>
> If anything, I'd add one example to `tasks sh --help`: `tasks sh 29 -- ./scripts/check`. That shows how to run a repo-local tool without making users think about `PATH`. I wouldn't put it in the README or treat it as a release blocker.

So, let's just do that: add one brief example to the `tasks sh --help`.
