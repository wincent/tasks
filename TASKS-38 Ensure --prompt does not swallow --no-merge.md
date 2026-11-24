# TASKS-38: Ensure --prompt does not swallow --no-merge

Status: active

## Description

`--prompt` can swallow `--no-merge`; eg. `tasks run 1 --prompt --no-merge` proceeds instead of rejecting the missing value. A successful run can then auto-merge.

Reject option tokens as separate prompt values. Add a regression test that checks the error occurs before Pi starts.

Additionally, audit all existing commands to see if any other value-requiring options exhibit this same kind of behavior.
