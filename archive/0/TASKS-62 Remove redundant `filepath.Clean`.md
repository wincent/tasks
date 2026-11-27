# TASKS-62: Remove redundant `filepath.Clean`

Status: merged

## Description

Agent finding:

> `filepath.Clean(directory)` in `internal/state/files.go:95` is redundant because `filepath.Dir(path)` already returns a cleaned path.
