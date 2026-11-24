# TASKS-58: Fix CI failure

Status: open

## Description

```
ok  	github.com/wincent/tasks/cmd/tasks	1.073s
ok  	github.com/wincent/tasks/internal/adapters/signing	0.124s
ok  	github.com/wincent/tasks/internal/cli	4.686s
ok  	github.com/wincent/tasks/internal/git	2.585s
ok  	github.com/wincent/tasks/internal/process	0.113s
ok  	github.com/wincent/tasks/internal/query	0.005s
ok  	github.com/wincent/tasks/internal/state	0.030s
--- FAIL: TestBatchArchiveMoveChecksSharedIndexEntries (0.01s)
    commit_test.go:406: expected duplicate index path rejection, got configure user.name and user.email before task metadata commits (or supply GIT_AUTHOR_NAME/EMAIL and GIT_COMMITTER_NAME/EMAIL): resolve commit identity from configuration/environment: config value 'user.name' was not found (libgit2 -3)
FAIL
FAIL	github.com/wincent/tasks/internal/store	0.431s
ok  	github.com/wincent/tasks/internal/tail	0.158s
?   	github.com/wincent/tasks/internal/terminal	[no test files]
?   	github.com/wincent/tasks/internal/testutil	[no test files]
FAIL
Error: Process completed with exit code 1.
```
