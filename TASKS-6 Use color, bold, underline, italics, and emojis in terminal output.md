# TASKS-6: Use color, bold, underline, italics, and emojis in terminal output

Status: open

## Description

When running in an interactive terminal, use color and font-styling and emojis to make the output easier to read and parse at a glance.

Example — output after `tasks create`:

```
Creating task: Add `test` and `vet` scripts under `bin`
Write the description, then save and quit. Leave it empty to cancel.
tasks/TASKS-5 Add `test` and `vet` scripts under `bin`.md
```

The pre-editor output combines with the post-editor result (at least, I think it's post editor result) to produce a miniature "wall of text". As a user, what I really want to see is something like:

```
Created: TASKS-5 Add `test` and `vet` scripts under `bin`
```

As this is a success outcome, I think I'd want it in green, with "Created:" in bold and "TASKS-5" underlined. Perhaps we could settle on a convention of always (when the output is an interactive terminal) underlining task IDS, and rendering task titles in italics?
