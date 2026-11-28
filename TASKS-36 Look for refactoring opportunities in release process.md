# TASKS-36: Look for refactoring opportunities in release process

Status: open

## Description

I don't like the amount of inline shell scripting in the release workflow. See if there's a better way.

## Recommended approach

Most of `.github/workflows/release.yml` is already extracted: `verify-tag` delegates to `bin/check-release-tag` (tested by `bin/test-release-tag`), and `build` delegates to `bin/package-release` (leaving only two 2-line OS dependency installs inline). The only substantive inline shell left is the ~54-line `publish` step.

Extract the `publish` logic into `bin/publish-release` and add a mocked test script `bin/test-publish-release` (mirroring `bin/test-release-tag`), wired into `bin/ci` and `.github/workflows/ci.yml`:

- Sparse-checkout only `bin/publish-release` in `publish` at `needs.verify-tag.outputs.commit` with `persist-credentials: false`, `sparse-checkout: bin/publish-release`, and `sparse-checkout-cone-mode: false`. Because GitHub Actions already loads `.github/workflows/release.yml` from the tagged revision (see ADR-0002), this does not widen the trust boundary, and `actions/checkout@v7.0.1` is already pinned in `.github/workflows/actions.lock`.
- Keep release assets isolated under `$RUNNER_TEMP/release-assets`.
- Add a portable `sha256_file` helper (`sha256sum` with `shasum -a 256` fallback) so `bin/test-publish-release` runs locally on macOS as well as on Linux CI runners.
- Test the publish gate in CI: happy path, tag moved before or after draft creation, missing/extra/empty/symlinked artifacts, attestation failure, existing release or draft, uploaded asset digest mismatch, and non-`uploaded` asset state.
- Update `CONTRIBUTING.md` to mention `bin/test-publish-release` alongside `bin/test-release-tag`.

Explicitly out of scope:

- Replacing the release scripts with third-party GitHub Actions (conflicts with ADR-0005's minimal dependency footprint and would not cover ADR-0002's custom draft/digest/tag-recheck invariants).
- Rewriting the release scripts in Go (would require `actions/setup-go` in `verify-tag` and `publish` and replace concise CLI orchestration with verbose `os/exec` plumbing).

## Proposed diff

```diff
diff --git a/.github/workflows/release.yml b/.github/workflows/release.yml
index 9c1b2ab..0000000 100644
--- a/.github/workflows/release.yml
+++ b/.github/workflows/release.yml
@@ -109,69 +109,21 @@ jobs:
       contents: write
       attestations: read
     steps:
-      # No checkout: publication uses only the packages from this run attempt.
+      # Sparse-checkout only the publish script; release assets stay in RUNNER_TEMP.
+      - uses: actions/checkout@v7.0.1
+        with:
+          ref: ${{ needs.verify-tag.outputs.commit }}
+          persist-credentials: false
+          sparse-checkout: bin/publish-release
+          sparse-checkout-cone-mode: false
+
       - uses: actions/download-artifact@v8.0.2
         with:
           pattern: release-${{ github.run_attempt }}-*
           path: ${{ runner.temp }}/release-assets
 
       - name: Verify the complete asset set and publish
-        shell: bash
         env:
           GH_TOKEN: ${{ github.token }}
           RELEASE_COMMIT: ${{ needs.verify-tag.outputs.commit }}
           TAG_OBJECT: ${{ needs.verify-tag.outputs.tag_object }}
-        run: |
-          fail() { printf '%s\n' "$*" >&2; exit 1; }
-          check_tag() {
-            current=$(gh api "$repo/git/ref/tags/$tag" --jq '.object | select(.type == "tag") | .sha') || return 1
-            [[ "$current" == "$TAG_OBJECT" ]]
-          }
-
-          tag=${GITHUB_REF#refs/tags/}
-          repo="repos/$GITHUB_REPOSITORY"
-          check_tag || fail 'The release tag changed after verification'
-
-          cd "$RUNNER_TEMP/release-assets" || fail 'No release artifacts for this attempt; re-run all jobs'
-          shopt -s nullglob dotglob
-          targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
-          entries=(*)
-          (( ${#entries[@]} == ${#targets[@]} )) || fail 'Incomplete or unexpected release artifacts for this attempt; re-run all jobs'
-          assets=()
-          for target in "${targets[@]}"; do
-            dir="release-$GITHUB_RUN_ATTEMPT-$target"
-            asset="$dir/tasks-$target.tar.gz"
-            entries=("$dir"/*)
-            [[ ! -L "$dir" && ${#entries[@]} == 1 && -f "$asset" && ! -L "$asset" && -s "$asset" ]] || fail "Unexpected package for $target"
-            gh attestation verify "$asset" \
-              --repo "$GITHUB_REPOSITORY" \
-              --signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release.yml" \
-              --source-ref "$GITHUB_REF" \
-              --source-digest "$RELEASE_COMMIT" \
-              --deny-self-hosted-runners
-            assets+=("$asset")
-          done
-
-          # Don't reuse a draft or modify an existing release. If a publish
-          # attempt left a draft, a maintainer must remove it before retrying.
-          gh api --paginate "$repo/releases" | jq -es --arg tag "$tag" \
-            'all(.[][]; .tag_name != $tag)' >/dev/null || fail 'Could not confirm that no release or draft exists for this tag'
-          gh release create "$tag" "${assets[@]}" --repo "$GITHUB_REPOSITORY" \
-            --draft --verify-tag --title "$tag" --notes 'These binaries require shared libgit2 1.9.x at runtime. Linux binaries are built on Ubuntu 26.04 (install libgit2-1.9); macOS binaries are built on macOS 15 with Homebrew libgit2. See BUILD-INFO.txt inside each archive for the actual build toolchain and dependency versions. Verify the archive before extracting it, as described in ADR-0002.'
-
-          draft=$(gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft,databaseId --jq 'select(.isDraft) | .databaseId')
-          [[ "$draft" =~ ^[0-9]+$ ]] || fail 'Could not identify the draft release'
-          expected=$(
-            for asset in "${assets[@]}"; do
-              digest=$(sha256sum < "$asset")
-              printf '%s\tsha256:%s\n' "${asset##*/}" "${digest%% *}"
-            done | jq -Rn '[inputs | split("\t") | {name: .[0], digest: .[1]}] | sort_by(.name)'
-          )
-          gh api --paginate "$repo/releases/$draft/assets" | jq -es --argjson expected "$expected" '
-            add | all(.[]; .state == "uploaded") and (map({name, digest}) | sort_by(.name)) == $expected
-          ' >/dev/null || fail 'The draft assets do not match the verified archives'
-
-          # Release immutability and tag rulesets must already be enabled in
-          # repository settings. Check the ref again immediately before publishing.
-          check_tag || fail 'The release tag changed after verification'
-          gh api --method PATCH "$repo/releases/$draft" -F draft=false --jq '.html_url'
+        run: bin/publish-release "$RUNNER_TEMP/release-assets"
diff --git a/bin/publish-release b/bin/publish-release
new file mode 100755
index 0000000..0000000
--- /dev/null
+++ b/bin/publish-release
@@ -0,0 +1,73 @@
+#!/usr/bin/env bash
+set -euo pipefail
+
+fail() { printf '%s\n' "$*" >&2; exit 1; }
+
+[[ $# == 1 ]] || fail "Usage: $0 <assets-dir>"
+assets_dir=$1
+
+: "${GITHUB_REPOSITORY:?Set GITHUB_REPOSITORY}"
+: "${GITHUB_REF:?Set GITHUB_REF}"
+: "${GITHUB_RUN_ATTEMPT:?Set GITHUB_RUN_ATTEMPT}"
+: "${RELEASE_COMMIT:?Set RELEASE_COMMIT}"
+: "${TAG_OBJECT:?Set TAG_OBJECT}"
+
+sha256_file() {
+  local digest
+  if command -v sha256sum >/dev/null 2>&1; then
+    digest=$(sha256sum < "$1")
+  else
+    digest=$(shasum -a 256 < "$1")
+  fi
+  printf '%s\n' "${digest%% *}"
+}
+
+check_tag() {
+  current=$(gh api "$repo/git/ref/tags/$tag" --jq '.object | select(.type == "tag") | .sha') || return 1
+  [[ "$current" == "$TAG_OBJECT" ]]
+}
+
+tag=${GITHUB_REF#refs/tags/}
+repo="repos/$GITHUB_REPOSITORY"
+check_tag || fail 'The release tag changed after verification'
+
+cd "$assets_dir" || fail 'No release artifacts for this attempt; re-run all jobs'
+shopt -s nullglob dotglob
+targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
+entries=(*)
+(( ${#entries[@]} == ${#targets[@]} )) || fail 'Incomplete or unexpected release artifacts for this attempt; re-run all jobs'
+assets=()
+for target in "${targets[@]}"; do
+  dir="release-$GITHUB_RUN_ATTEMPT-$target"
+  asset="$dir/tasks-$target.tar.gz"
+  entries=("$dir"/*)
+  [[ ! -L "$dir" && ${#entries[@]} == 1 && -f "$asset" && ! -L "$asset" && -s "$asset" ]] || fail "Unexpected package for $target"
+  gh attestation verify "$asset" \
+    --repo "$GITHUB_REPOSITORY" \
+    --signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release.yml" \
+    --source-ref "$GITHUB_REF" \
+    --source-digest "$RELEASE_COMMIT" \
+    --deny-self-hosted-runners
+  assets+=("$asset")
+done
+
+# Don't reuse a draft or modify an existing release. If a publish
+# attempt left a draft, a maintainer must remove it before retrying.
+gh api --paginate "$repo/releases" | jq -es --arg tag "$tag" \
+  'all(.[][]; .tag_name != $tag)' >/dev/null || fail 'Could not confirm that no release or draft exists for this tag'
+gh release create "$tag" "${assets[@]}" --repo "$GITHUB_REPOSITORY" \
+  --draft --verify-tag --title "$tag" --notes 'These binaries require shared libgit2 1.9.x at runtime. Linux binaries are built on Ubuntu 26.04 (install libgit2-1.9); macOS binaries are built on macOS 15 with Homebrew libgit2. See BUILD-INFO.txt inside each archive for the actual build toolchain and dependency versions. Verify the archive before extracting it, as described in ADR-0002.'
+
+draft=$(gh release view "$tag" --repo "$GITHUB_REPOSITORY" --json isDraft,databaseId --jq 'select(.isDraft) | .databaseId')
+[[ "$draft" =~ ^[0-9]+$ ]] || fail 'Could not identify the draft release'
+expected=$(
+  for asset in "${assets[@]}"; do
+    printf '%s\tsha256:%s\n' "${asset##*/}" "$(sha256_file "$asset")"
+  done | jq -Rn '[inputs | split("\t") | {name: .[0], digest: .[1]}] | sort_by(.name)'
+)
+gh api --paginate "$repo/releases/$draft/assets" | jq -es --argjson expected "$expected" '
+  add | all(.[]; .state == "uploaded") and (map({name, digest}) | sort_by(.name)) == $expected
+' >/dev/null || fail 'The draft assets do not match the verified archives'
+
+# Release immutability and tag rulesets must already be enabled in
+# repository settings. Check the ref again immediately before publishing.
+check_tag || fail 'The release tag changed after verification'
+gh api --method PATCH "$repo/releases/$draft" -F draft=false --jq '.html_url'
diff --git a/bin/test-publish-release b/bin/test-publish-release
new file mode 100755
index 0000000..0000000
--- /dev/null
+++ b/bin/test-publish-release
@@ -0,0 +1,164 @@
+#!/usr/bin/env bash
+set -euo pipefail
+
+publish=$(cd "$(dirname "$0")" && pwd)/publish-release
+work=$(mktemp -d /tmp/tasks-publish-test.XXXXXX)
+trap 'rm -rf "$work"' EXIT
+
+mkdir "$work/mock"
+export PATH="$work/mock:$PATH"
+cat > "$work/mock/gh" <<'EOF'
+#!/usr/bin/env bash
+set -euo pipefail
+printf '%s\n' "$*" >> "$MOCK_LOG"
+case "$*" in
+  "api repos/test/tasks/git/ref/tags/v1.0.0 --jq .object | select(.type == \"tag\") | .sha")
+    count=$(cat "$MOCK_TAG_COUNT")
+    count=$((count + 1))
+    printf '%s\n' "$count" > "$MOCK_TAG_COUNT"
+    if (( count == 1 )); then
+      printf '%s\n' "$MOCK_TAG_FIRST"
+    else
+      printf '%s\n' "$MOCK_TAG_SECOND"
+    fi
+    ;;
+  "attestation verify "*" --repo test/tasks --signer-workflow test/tasks/.github/workflows/release.yml --source-ref refs/tags/v1.0.0 --source-digest $RELEASE_COMMIT --deny-self-hosted-runners")
+    if [[ -n "${MOCK_FAIL_ATTEST:-}" && "$3" == *"$MOCK_FAIL_ATTEST"* ]]; then
+      printf 'Attestation verification failed for %s\n' "$3" >&2
+      exit 1
+    fi
+    ;;
+  "api --paginate repos/test/tasks/releases")
+    printf '%s\n' "$MOCK_RELEASES"
+    ;;
+  "release create v1.0.0 "*" --repo test/tasks --draft --verify-tag --title v1.0.0 --notes "*)
+    ;;
+  "release view v1.0.0 --repo test/tasks --json isDraft,databaseId --jq select(.isDraft) | .databaseId")
+    printf '%s\n' "$MOCK_DRAFT_ID"
+    ;;
+  "api --paginate repos/test/tasks/releases/42/assets")
+    printf '%s\n' "$MOCK_DRAFT_ASSETS"
+    ;;
+  "api --method PATCH repos/test/tasks/releases/42 -F draft=false --jq .html_url")
+    printf '%s\n' 'https://github.com/test/tasks/releases/tag/v1.0.0'
+    ;;
+  *)
+    printf 'Unexpected gh call: %s\n' "$*" >&2
+    exit 1
+    ;;
+esac
+EOF
+chmod +x "$work/mock/gh"
+
+sha256_file() {
+  local digest
+  if command -v sha256sum >/dev/null 2>&1; then
+    digest=$(sha256sum < "$1")
+  else
+    digest=$(shasum -a 256 < "$1")
+  fi
+  printf '%s\n' "${digest%% *}"
+}
+
+export GITHUB_REPOSITORY=test/tasks
+export GITHUB_REF=refs/tags/v1.0.0
+export GITHUB_RUN_ATTEMPT=1
+export RELEASE_COMMIT=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
+export TAG_OBJECT=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
+export MOCK_LOG="$work/gh.log"
+export MOCK_TAG_COUNT="$work/tag-count"
+
+targets=(linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)
+
+setup_assets() {
+  rm -rf "$work/assets"
+  mkdir "$work/assets"
+  local items=()
+  for target in "${targets[@]}"; do
+    local dir="$work/assets/release-1-$target"
+    local asset="$dir/tasks-$target.tar.gz"
+    mkdir "$dir"
+    printf 'payload-%s\n' "$target" > "$asset"
+    items+=("$(jq -nc --arg name "tasks-$target.tar.gz" --arg digest "sha256:$(sha256_file "$asset")" '{name:$name, digest:$digest, state:"uploaded"}')")
+  done
+  export MOCK_TAG_FIRST="$TAG_OBJECT"
+  export MOCK_TAG_SECOND="$TAG_OBJECT"
+  export MOCK_FAIL_ATTEST=''
+  export MOCK_RELEASES='[]'
+  export MOCK_DRAFT_ID='42'
+  MOCK_DRAFT_ASSETS=$(printf '%s\n' "${items[@]}" | jq -cs '.')
+  export MOCK_DRAFT_ASSETS
+  : > "$MOCK_LOG"
+  printf '0\n' > "$MOCK_TAG_COUNT"
+}
+
+reject() {
+  local message=$1
+  if "$publish" "$work/assets" > "$work/log" 2>&1 || [[ $(<"$work/log") != *"$message"* ]]; then
+    printf 'Expected rejection: %s\n' "$message" >&2
+    head -50 "$work/log" >&2
+    exit 1
+  fi
+}
+
+# Happy path.
+setup_assets
+url=$("$publish" "$work/assets")
+[[ "$url" == 'https://github.com/test/tasks/releases/tag/v1.0.0' ]]
+grep -Fq 'api --method PATCH repos/test/tasks/releases/42 -F draft=false' "$MOCK_LOG"
+
+# Tag moved before publication starts.
+setup_assets
+MOCK_TAG_FIRST=cccccccccccccccccccccccccccccccccccccccc
+reject 'The release tag changed after verification'
+! grep -Fq 'release create' "$MOCK_LOG"
+
+# Missing or extra artifact directories.
+setup_assets
+rm -rf "$work/assets/release-1-darwin-amd64"
+reject 'Incomplete or unexpected release artifacts'
+
+setup_assets
+mkdir "$work/assets/extra"
+reject 'Incomplete or unexpected release artifacts'
+
+# Empty or symlinked archive.
+setup_assets
+: > "$work/assets/release-1-linux-amd64/tasks-linux-amd64.tar.gz"
+reject 'Unexpected package for linux-amd64'
+
+setup_assets
+ln -sf /dev/null "$work/assets/release-1-linux-amd64/tasks-linux-amd64.tar.gz"
+reject 'Unexpected package for linux-amd64'
+
+# Attestation verification failure.
+setup_assets
+MOCK_FAIL_ATTEST=darwin-arm64
+reject 'Attestation verification failed'
+! grep -Fq 'release create' "$MOCK_LOG"
+
+# Existing release or draft for tag.
+setup_assets
+MOCK_RELEASES='[{"tag_name":"v1.0.0"}]'
+reject 'Could not confirm that no release or draft exists for this tag'
+! grep -Fq 'release create' "$MOCK_LOG"
+
+# Uploaded asset digest mismatch or non-uploaded state.
+setup_assets
+MOCK_DRAFT_ASSETS=$(jq -c '.[0].digest = "sha256:0000"' <<< "$MOCK_DRAFT_ASSETS")
+reject 'The draft assets do not match the verified archives'
+! grep -Fq 'draft=false' "$MOCK_LOG"
+
+setup_assets
+MOCK_DRAFT_ASSETS=$(jq -c '.[0].state = "starter"' <<< "$MOCK_DRAFT_ASSETS")
+reject 'The draft assets do not match the verified archives'
+! grep -Fq 'draft=false' "$MOCK_LOG"
+
+# Tag moved between draft creation and publish.
+setup_assets
+MOCK_TAG_SECOND=cccccccccccccccccccccccccccccccccccccccc
+reject 'The release tag changed after verification'
+! grep -Fq 'draft=false' "$MOCK_LOG"
+
+printf '%s\n' 'Release publish checks passed'
diff --git a/bin/ci b/bin/ci
index 0d327e9..0000000 100755
--- a/bin/ci
+++ b/bin/ci
@@ -7,4 +7,5 @@ gh actions-lock --verify
 bin/test
 bin/vet
 bin/test-release-tag
+bin/test-publish-release
 exec bin/check-format
diff --git a/.github/workflows/ci.yml b/.github/workflows/ci.yml
index d9a54a6..0000000 100644
--- a/.github/workflows/ci.yml
+++ b/.github/workflows/ci.yml
@@ -38,6 +38,7 @@ jobs:
       - run: bin/test
       - run: bin/vet
       - run: bin/test-release-tag
+      - run: bin/test-publish-release
 
       - name: Install dprint
         run: |
diff --git a/CONTRIBUTING.md b/CONTRIBUTING.md
index 8e63a9b..0000000 100644
--- a/CONTRIBUTING.md
+++ b/CONTRIBUTING.md
@@ -4,7 +4,7 @@ Install the [build prerequisites](README.md#prerequisites) and make sure `dprint`
 
 From the repository root, run `bin/test`, `bin/vet`, or `bin/check-format` to run checks individually. Run `bin/format` to format Go and Markdown files.
 
-Before pushing, run `bin/ci` to run the same checks as GitHub Actions, including workflow lockfile verification and the release tag tests. The release tag tests (`bin/test-release-tag`) also require `jq` and GnuPG. For the lockfile check, authenticate the [GitHub CLI](https://cli.github.com/) with `gh auth login` and install the extension:
+Before pushing, run `bin/ci` to run the same checks as GitHub Actions, including workflow lockfile verification and the release script tests. The release tests (`bin/test-release-tag` and `bin/test-publish-release`) also require `jq`, and the tag tests require GnuPG. For the lockfile check, authenticate the [GitHub CLI](https://cli.github.com/) with `gh auth login` and install the extension:
 
 ```sh
 gh extension install github/gh-actions-lock --pin v0.5.0
```
