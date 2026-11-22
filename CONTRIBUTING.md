# Contributing

Install the [build prerequisites](README.md#prerequisites) and make sure `dprint` is on your `PATH`.

From the repository root, run `bin/test`, `bin/vet`, or `bin/check-format` to run checks individually. Run `bin/format` to format Go and Markdown files.

Before pushing, run `bin/ci` to run the same checks as GitHub Actions, including workflow lockfile verification and the release tag tests. The release tag tests (`bin/test-release-tag`) also require `jq` and `ssh-keygen`. For that check, authenticate the [GitHub CLI](https://cli.github.com/) with `gh auth login` and install the extension:

```sh
gh extension install github/gh-actions-lock --pin v0.5.0
```
