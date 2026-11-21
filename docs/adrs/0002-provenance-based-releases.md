# ADR-0002: Provenance-based releases

## Status

Proposed. This ADR defines the release process; it does not enable release automation.

## Context

Users can build `tasks` from source, but a GitHub Release would let them download a prebuilt binary. A signed Git tag identifies the source a maintainer approved, not the bytes uploaded to a release. A checksum can detect corruption but cannot, on its own, show that a trusted workflow built those bytes from the approved source.

## Decision

Require two independent checks for each release asset: a maintainer-signed Git tag that points to the approved commit, and a GitHub Actions build provenance attestation that binds the asset's SHA-256 digest to that same commit and tag ref. Use GitHub's SLSA build provenance support so CI does not need a long-lived signing key. Keep direct pushes to `main`: passing CI is a condition for releasing a commit, not for pushing it, and branch protection on `main` is not required.

### Build and publish

1. A maintainer selects a commit reachable from `main` that passed CI on `main` and is cleared for publication under [ADR-0001](0001-processes-for-ensuring-suitability-for-open-source.md). Review any release workflow changes before tagging. The maintainer creates and pushes a signed annotated version tag such as `v1.2.3`. Protect the release tag namespace so that only release maintainers can create tags and existing tags cannot be moved or deleted. Keep the private tag signing keys out of CI.
2. A version tag push triggers `.github/workflows/release.yml` on GitHub-hosted runners. Before building, verify that the tag is annotated, its signed name matches the event ref, and its signature matches the release key allowlist maintained independently of the tagged checkout. Peel the tag to its commit and require that it matches the event's source commit, is reachable from the canonical repository's current `main` tip, and has a successful run of `.github/workflows/ci.yml` triggered by a push to `main` in that repository for that exact commit. Build from a clean checkout of that commit, not the current `main` tip. Cover the release workflow's actions with the existing workflow lockfile verification.
3. Build `./cmd/tasks` for each supported release target with Go, a C compiler, and shared libgit2 1.9.x. Record the build toolchain and dependency versions and state the runtime libgit2 requirement in the release notes. Package first, then attest the exact files users will download; for an archive, attest the archive, not only the binary inside it.
4. Separate build and attestation permissions from release publication: the build job can read source and write attestations using OIDC, but cannot publish. The publish job can write release contents but cannot mint attestations. It takes only this run's packaged files, checks that the release tag still points to the verified tag object, and verifies that the complete expected asset set has attestations for that tag ref, commit, and workflow. Enable GitHub release immutability and publish the draft only after attaching the verified files. If a check fails, leave the release unpublished. To correct a published release, issue a new version instead of replacing assets or moving the tag.

### Verify a download

Fetch the version tag from the canonical `wincent/tasks` repository. Use `git verify-tag` with an independently trusted list of maintainer signing keys, and check that the signed tag name matches the requested version. After those checks pass, use the tag's peeled commit ID to verify the downloaded asset before extracting or running it:

```sh
tag=v1.2.3
asset=tasks-linux-amd64.tar.gz
ref="refs/tags/$tag"
commit=$(git rev-parse --verify "$ref^{commit}") &&
  gh attestation verify "$asset" \
    --repo wincent/tasks \
    --signer-workflow wincent/tasks/.github/workflows/release.yml \
    --source-ref "$ref" \
    --source-digest "$commit" \
    --deny-self-hosted-runners
```

Before enabling production releases, rehearse the workflow with a signed annotated test tag in a staging repository. Confirm that the attested source digest is the peeled commit ID, not the tag object ID, and that verification rejects modified assets, a different source ref or commit, and a different workflow. Confirm that unsigned tags, signatures from unapproved keys, commits outside `main` history, and commits without a successful CI run on `main` stop the workflow before publication.

## Consequences

A successful verification shows that the trusted workflow attested to these exact bytes for the commit approved by the tag signer. It does not prove that a separate build would reproduce those bytes or that the source, dependencies, or runner are uncompromised. This approach relies on GitHub's identity and attestation infrastructure and requires maintainers to manage the trusted signing keys and review changes to the release workflow. Without branch protection, changes to `main`, including workflow changes, can land without passing CI or receiving review. The release gate checks the tagged commit's CI result; it does not enforce review. Signed tags and checksums alone would be simpler, but would leave the build's origin unverified.
