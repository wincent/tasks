# TASKS-51: Isolate HOME and XDG_CONFIG_HOME in `TestMain`

Status: open

## Description

Agent finding:

> Isolate `HOME` and `XDG_CONFIG_HOME` in `TestMain`
>
> `validateEnvironment` intentionally forbids `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_NOSYSTEM`, so tests that don't override `HOME`/`XDG_CONFIG_HOME` inherit the developer's real personal Git config (e.g. a 300-line `~/.config/git/config` + includes on this machine) and re-read/re-assemble all ~140 entries on every `r.call()`. Pointing `HOME` and `XDG_CONFIG_HOME` at an empty temp dir in `TestMain` (for packages/tests that aren't specifically testing host config discovery) both speeds up tests dramatically and makes them more hermetic.
