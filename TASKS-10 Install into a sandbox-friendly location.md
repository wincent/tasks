# TASKS-10: Install into a sandbox-friendly location

Status: merged

## Description

Go may install executables into its bin directory, which can be outside the paths permitted by a sandbox or missing from `$PATH`.

Update `bin/install` to put `tasks` in `~/.local/bin`, a conventional user-local bin directory.
