# TASKS-16: Be more verbose when merging

Status: active

## Description

When I run `tasks go` the final output is something like:

```
🔄 Merging TASKS-15 into local main...
✅ TASKS-15 merged into local main.
```

The problem is, if I have multiple agents working at a time, and if they take a while to implement their deliverable, by the time the above message is printed, I can't remember what "TASKS-15" actually was.

Can we be a bit more version in that kind of message and include the task title in at least one place? I am wondering whether we should show it on a separate line, either before or after the other two messages. Maybe with an "info" emoji or similar but perhaps we could also add a brief summary, ellipsized if necessary, to match the terminal width (if it is running in an interactive terminal); eg:

```
🔄 Merging TASKS-12 (Convert UNLICENSE into UNLICENSE.md,...) into local main...
✅ TASKS-12 (Convert UNLICENSE into UNLICENSE.md, re...) merged into local main.
```

I think I'm leaning towards the above approach. As always, the title should be italicized if running in an interactive window).

We should ensure we do this in all subcommands that print task references like this.
