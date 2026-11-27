# TASKS-76: Improve and differentiate Pi launch prompts for run, go, and pi

Status: open

## Description

Update the launch prompt construction in `internal/cli/launch.go` (`launchTaskWithModel`) so that interactive (`tasks pi`) and non-interactive (`tasks run` / `tasks go`) launches get tailored, clearer instructions.

### Background

Currently both interactive and non-interactive launches share the exact same prompt string (`internal/cli/launch.go:207`):

- Interactive `tasks pi` never attaches `piProgress` to stdout, so `'Needs attention:'` is never parsed and `"If no changes are needed, explain why."` is never recorded (exiting without commits marks an open/blocked/active task `blocked` rather than closing it with the summary).
- Neither prompt explicitly tells the model to stay on the current task branch or avoid editing the task Markdown file directly, even though changing branches blocks the run and leaving the `tasks/` checkout dirty breaks post-run status recording and merging.
- Both prompts refer to `"the launcher"` instead of `` `tasks` ``.

### Desired prompts

- **Interactive (`tasks pi`)**:
  - ``Read <ID> at "<path>". Work only in this checkout on the current task branch, and do not edit the task file directly. Ask if anything is unclear or needs a decision; otherwise implement the task, run proportionate checks, and commit your changes before exiting. Do not merge; `tasks` handles task status and merging.``
- **Non-interactive (`tasks run`, `tasks go`)**:
  - ``Read <ID> at "<path>". Work only in this checkout on the current task branch, and do not edit the task file directly. Implement the task, run proportionate checks, commit your changes, and summarize what changed and which checks ran. If no changes are needed, explain why. If you cannot finish, start your final response with 'Needs attention:' and explain what remains. Do not merge; `tasks` handles task status and merging.``
- Keep `--prompt TEXT` appending `\n\nAdditional instructions for this run:\n<TEXT>` unchanged for both `tasks run` and `tasks pi`.

### Tests

- Update `TestPiPromptIsPerInvocation` in `internal/cli/workflow_test.go` (which currently asserts `Needs attention:` is present in the `tasks pi` prompt) to verify the interactive prompt wording and assert that it does not contain `Needs attention:`.
- Update `TestRunPromptIsPerInvocation` in `internal/cli/workflow_test.go` to verify the tightened non-interactive prompt wording (including the branch/task-file guidance, `Needs attention:`, and `` `tasks` `` instead of `"the launcher"`).
- Check any other tests inspecting launch prompts (such as `internal/cli/command_go_test.go`) and run `bin/test`.
