# TASKS-1: Create an ADRs folder

Status: active

## Description

Create a `docs/adrs/` folder where we will record architectural decisions.

The first ADR, which you should create as part of this task, should be numbered ADR-0001 and titled "Processes for ensuring suitability for open source". The idea of this ADR is to describe a periodic review/audit process that an agent can carry out to identify problems that should be rectified in order to make the source code and history suitable for publication as open source. Examples of the kinds of anti-patterns to be avoided include:

- Machine and user-specific information leaking into documentation, tests, other source code, and repository history.
- Corporate information that should not appear in a public open source repo.

The remediation may include history rewriting (with straightforward `git rebase` operations) or deeper reconstruction using a tool like `git filter-branch`.
