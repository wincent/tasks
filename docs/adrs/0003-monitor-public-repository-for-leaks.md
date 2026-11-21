# ADR-0003: Monitor the public repository for leaks

## Status

Accepted. This complements the pre-publication checks in [ADR-0001](0001-processes-for-ensuring-suitability-for-open-source.md).

## Context

Once a repository is public, a push, issue comment, workflow log, or release asset can disclose information before the next release audit. Secret scanners help find credentials, but they do not reliably flag private paths, internal links, or copied transcripts.

## Decision

A maintainer owns the audit and sets a monthly reminder. Review reports of possible leaks as soon as they arrive. Enable the hosting provider's secret scanning and push protection where available, but do not rely on them in place of manual review.

### Monthly audit

1. Inventory branches and tags from the public host, not just the local clone. Record the ref names and object IDs. Use [ADR-0001](0001-processes-for-ensuring-suitability-for-open-source.md) for the first full audit. After that, review what became reachable since the last verified audit and the tracked contents at each current tip. Review all reachable history for any new or rewritten ref, or if the previous baseline is missing. Confirm that private refs, including any unaudited `tasks` branch, are not on the public host.
2. Run a local secret scanner and manually review candidate matches, changed filenames, commit messages and metadata, fixtures, and examples. Look for private paths, identities, internal domains and ticket links, proprietary material, and copied logs or agent transcripts. Inspect public issues, pull requests, workflow logs and artifacts, release notes, and downloads added or changed since the last review. If a surface cannot be inspected, record the gap rather than marking it clear.
3. Keep a private record of the date, reviewer, reviewed refs and non-Git surfaces, tools used, findings, and follow-up owners. Use redacted locations and never paste the exposed value into a public issue, commit message, or CI log.

### Respond to a leak

Notify the maintainer privately and contain the exposure where possible. Revoke or rotate exposed credentials promptly. Remove exposed content from every affected public surface and use the history rewrite and verification procedure in [ADR-0001](0001-processes-for-ensuring-suitability-for-open-source.md) when needed. Verify the fix against the public host, not just a local checkout. A rewrite cannot recall copies already in forks, clones, or caches.

## Consequences

Monthly review bounds the time between manual checks, while alerts can surface some leaks sooner. The private audit record makes it possible to resume from a known baseline without treating an incomplete check as a clean result.
