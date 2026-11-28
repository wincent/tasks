# Contributing

Install the [build prerequisites](README.md#prerequisites) and make sure `dprint` is on your `PATH`.

From the repository root, run `bin/test`, `bin/vet`, or `bin/check-format` to run checks individually. Run `bin/format` to format Go and Markdown files.

Before pushing, run `bin/ci` to run the same checks as GitHub Actions, including workflow lockfile verification and the release script tests. The release tests (`bin/test-release-tag` and `bin/test-publish-release`) also require `jq`, and the tag tests require GnuPG. For the lockfile check, authenticate the [GitHub CLI](https://cli.github.com/) with `gh auth login` and install the extension:

```sh
gh extension install github/gh-actions-lock --pin v0.5.0
```

## Releasing

Releases use GitHub's [artifact attestations to establish provenance for builds](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations), using the procedure described in [ADR-0002](docs/adrs/0002-provenance-based-releases.md).

The repository settings are:

- Settings:
  - General:
    - "Enable release immutability"
  - Rulesets:
    - "Restrict creation" (tag rule):
      - Enforcement status: Active
      - Bypass list: Maintainer handle(s)
      - Tag targeting criteria: Include by pattern `v*`
      - Rules: "Restrict creation"
    - "Restrict mutation" (tag rule):
      - Enforcement status: Active
      - Bypass list: none
      - Tag targeting criteria: Include by pattern `v*`
      - Rules: "Restrict updates" and "Restrict deletions"
  - Environments:
    - "Release":
      - Selected branches and tags: `v*`
      - Environment variables:
        - `RELEASE_REPOSITORY`: `wincent/tasks`
        - `RELEASE_GPG_PUBLIC_KEYS`: combined export of authorized signing keys[^keys], set via `gpg --armor --export CA35A4528D888CDF264D0A2A4838AEDCA8CE883C 4282ED4A05CC894D53A541C3F962DC1A1941CCC4 | gh variable set RELEASE_GPG_PUBLIC_KEYS --repo wincent/tasks --env Release`

[^keys]: The keys associated with my work and personal email addresses, identified by fingerprints CA35A4528D888CDF264D0A2A4838AEDCA8CE883C and 4282ED4A05CC894D53A541C3F962DC1A1941CCC4 respectively.

The release process is triggered by pushing a signed tag pointing at a commit on `main` for which CI has previously passed:

1. Update [CHANGES.md](CHANGES.md) with the version, release date, and user-visible changes. Note that, because change text is extracted and included in release notes, **it must not include relative links** (instead, link to absolute GitHub URLs pointing at the specific tagged version of the link target).
2. Run `bin/ci` as a final preflight check.
3. Commit and push `main`.
4. **Wait for CI to pass.**
5. Tag the new version (with `git tag -s -m "v0.1.0 release" v0.1.0` or similar).
6. Push the tag (with `git push --tags`)
7. Wait for the Release workflow to publish.
