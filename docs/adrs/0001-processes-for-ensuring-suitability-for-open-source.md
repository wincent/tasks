# ADR-0001: Processes for ensuring suitability for open source

## Status

Accepted.

## Context

A clean working tree is not enough for open source publication. Git preserves old file contents and names, commit messages, and author and committer metadata. A release can disclose private information through a branch or tag even when that information is absent from the default branch.

## Decision

A maintainer assigns an agent to review the repository monthly and again before initial publication or before making additional branches or tags public. The audit is read-only by default. A maintainer approves remediation and decides whether the repository is ready to publish.

### Audit procedure

1. List the branches and tags intended for publication. Review their current tracked files and all history reachable from those refs, including deleted or renamed files, merge commits, commit metadata, and annotated tag messages and metadata. Keep the separate `tasks` branch private unless it is explicitly included in the review.
2. Search documentation, examples, tests and fixtures, source code, and configuration for machine- or user-specific details such as absolute home paths, usernames, private email addresses, and hostnames. Look for nonpublic corporate material such as internal domains and URLs, ticket references, proprietary code or data, and credentials. Review filenames and metadata as well as contents. Use local searches and secret scanning to find candidates, then manually review matches and files those tools cannot inspect. Confirm that any attribution or corporate material kept is approved for public release.
3. Report findings privately. For each finding, record its category, the affected refs and a redacted location, and a proposed fix. Record the reviewed refs and tip IDs, date, checks run, and any gaps. Do not copy sensitive values into reports, task tickets, or commit messages.

### Remediation and verification

Replace local examples with placeholders, use portable paths and synthetic fixtures, and remove nonpublic corporate material. A follow-up commit is enough only when older versions are safe to publish. If sensitive material remains in reachable history, block publication until it is removed from every affected ref. Use an interactive `git rebase` for a small, linear set of affected commits; for widespread contamination, use a history-filtering tool such as `git filter-repo` or `git filter-branch`. If credentials are found, notify their owner privately and arrange revocation or rotation; rewriting alone does not make them safe.

Get maintainer approval before rewriting history. Coordinate with collaborators and work in an isolated private copy with a private backup. Re-run the audit on the exact refs to be published, verify that superseded commits containing sensitive material are no longer reachable from those refs, and run the checks relevant to the changed files. Resolve any audit gaps and publish only the reviewed refs after maintainer sign-off. If sensitive material was already shared, treat it as an incident: a rewrite does not remove copies from clones, forks, or caches.

## Consequences

The recurring review catches disclosures that a source-only check misses. Rewrites change commit IDs and can disrupt collaborators, so the audit never rewrites or publishes history automatically.
