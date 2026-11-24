# TASKS-64: Comment why state package imports testing

Status: open

## Description

Add a brief inline comment on the `"testing"` import in `internal/state/files.go` explaining that it is imported solely for `testing.Testing()`, which is safe in production binaries since Go 1.13 (no `init()` flag registration on `flag.CommandLine`) and avoids requiring build tags for `go test ./...`. This prevents future reviews or agents from flagging the non-test `"testing"` import as a regression.
