# TASKS-53: Avoid hardcoded 300ms wait

Status: merged

## Description

Agent finding:

> **`internal/tail/tail_test.go:99`**: `TestFollowCompletesPartialRecordWithoutSwitchingSession` waits for a hardcoded `300ms` context deadline instead of canceling as soon as `"late final output"` is written.
