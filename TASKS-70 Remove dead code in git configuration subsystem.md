# TASKS-70: Remove dead code in git configuration subsystem

Status: active

## Description

A read-only dead-code sweep identified the following dead or test-only code and unused parameters in the `internal/git` configuration subsystem:

- `internal/git/config_include_policy.go:459`: `quoteConfigurationSection` has zero references anywhere in production code or tests.
- `internal/git/config_include_policy.go:468`: `quoteConfigurationFile` is only referenced in one test (`internal/git/config_closure_regression_test.go:36`).
- `internal/git/config_include_policy.go:55,66,73,315`: `cloneConfigurationRecords`, `(*configurationParseCache).records`, `(*configurationParseCache).borrowRecords`, and `(*configurationFileCapture).loadRecords` are unreachable from production (production calls `borrowFile` and `(*configurationFileCapture).borrowRecords` directly); they are only exercised via tests.
- `internal/git/config_entries.go:15,113`: unexported `configurationEntry` and `(*Repository).configEntries` are only called from tests; production code uses exported `ConfigurationEntry` and `(*Repository).ConfigurationEntries`.
- `internal/git/config_hasconfig.go:19,133`: `hasconfigRecord.conditionalURL` is written at line 133 and never read in production code (only asserted in tests).
- `internal/git/config_environment.go:700`: `(*Repository).prospectiveWorktreeConfiguration(repo, name, path, branch)` never uses its `path` parameter (callers are `internal/git/worktree_config.go:233`, `internal/git/detached_worktree.go:39`, and `internal/git/native.go:732`).
- `internal/git/config_environment.go:799`: `(*environmentConfiguration).sources(files, repo)` always receives `repo = nil` in production (`internal/git/config_environment.go:811,826`); only `internal/git/config_parse_cache_test.go:457` passes a non-nil `repo`.

## Steps

For each candidate above:

1. Verify the finding in the current codebase (confirming all production and test references; note that `configurationWork` in `internal/git/config_work.go` is an intentional test diagnostic hook and is out of scope).
2. Locate the commit in Git history where the code or parameter first became dead (and note it in your summary/commit message).
3. Clean up the dead code and unused parameters, updating or moving test-only helpers into `*_test.go` where appropriate. Do not worry about maintaining backwards compatibility, as the project is not widely used at this time.
4. Verify the change by running `bin/test`, `bin/vet`, and `bin/check-format` (or `bin/ci`).
