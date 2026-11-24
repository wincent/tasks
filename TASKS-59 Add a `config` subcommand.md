# TASKS-59: Add a `config` subcommand

Status: blocked

## Description

Initial version of this just shows the parsed contents of config, if any (ie. ~/.config/tasks/config.yml). Reports an error if it's invalid. Prints nothing if it is missing.

Main purpose is to remind users what model aliases they have configured. In the future, we may extend this to allow reading a subset of the config, or updating it, or potentially reading from or operating on the local .tasks.yml file as well; for now, though, not aiming to do any of that.
