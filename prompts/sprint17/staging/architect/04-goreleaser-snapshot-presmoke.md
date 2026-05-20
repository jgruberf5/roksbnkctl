---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: ci.yml — add `goreleaser release --snapshot` pre-tag smoke job'
labels: []
assignees: ''
---

## Motivation

`ci.yml` runs `goreleaser check` (config validation) but never invokes the actual `goreleaser release` codepath. `release.yml` does, but only on a tag push — by which point a broken `.goreleaser.yml` produces a failed Release with a half-uploaded asset set and (per GitHub Issue #2's documented experience) a re-release on the same tag is opaque and painful to recover from.

A `goreleaser release --snapshot --clean` job is the canonical pre-tag smoke: it runs every step of the real release *except* the upload, on every PR and push to `main`. It catches `.goreleaser.yml` template errors (the `{{ .Tag }}` / `{{ .Version }}` / `{{ .Os }}` / `{{ .Arch }}` substitutions that `goreleaser check` validates statically but cannot exercise), cross-compile breakage (`linux/darwin/windows × amd64/arm64`), and changelog generation. It catches every defect that produced the post-tag scramble at v1.0.1's recovery cut (the `extra_files` for the PDF; the `format` → `formats` rename for goreleaser v2; the `version_template` rename) **before** they land on `main`.

Today the only check that exercises this is the integrator running `goreleaser release --snapshot` locally on their machine before tagging — undocumented, easy to skip, and depends on the integrator's local goreleaser version matching the action's pinned version. A 90-second CI job removes the foot-gun.

## Proposed surface

No `roksbnkctl` CLI change. A new job in `.github/workflows/ci.yml`:

```
# ci.yml gains a `goreleaser-snapshot` job alongside the existing `goreleaser-check` job
jobs:
  goreleaser-snapshot:
    name: goreleaser snapshot build
    runs-on: ubuntu-latest
    needs: test          # don't waste a 90s build on a tree that fails unit tests
    if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }         # goreleaser needs git history for changelog
      - uses: actions/setup-go@v5
        with: { go-version-file: go.mod, cache: true }
      - uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --snapshot --clean --skip=publish,sign
      - name: Inspect produced artefacts
        run: ls -la dist/
```

- The fork-PR guard mirrors the `integration` / `docker-backend` / `k8s-backend` jobs already in `ci.yml` — same rationale (no secrets needed; just stay polite with GHCR / Docker Hub on fork-noise).
- `--snapshot --clean --skip=publish,sign` produces the full cross-compiled archive set + checksums in `dist/` without ever calling the GitHub Releases API.

## Behavior

- **Happy path:** on every PR and main-push, the new job runs `goreleaser release --snapshot` end to end. It cross-compiles all six target tuples, generates `checksums.txt`, renders the release header/footer (with `{{ .Tag }}` resolved to the snapshot pseudo-tag), and exits 0. The `Inspect produced artefacts` step lists `dist/` so a reviewer can eyeball asset names.
- **Template break (e.g. footer references an undefined `{{ .UndefinedVar }}`):** goreleaser exits non-zero, the job goes red, the PR is unmergeable. This is the case where today's `goreleaser check` is green but the actual release would fail.
- **Cross-compile break (a new package that doesn't build on `windows/arm64`):** goreleaser surfaces the `go build` failure in the per-target output; the job goes red on the specific tuple.
- **`.goreleaser.yml`-only PR:** the new job runs (the `paths:` filter on `ci.yml` is currently empty — runs on every PR — keep it that way; cheap to run).
- **Fork PR:** the job is skipped (same `if:` guard as integration/docker/k8s).
- **No upload, no Release object, no tag side-effects.** `--skip=publish` ensures `release.yml`'s tag-push trigger is the only path that ever calls the Releases API.
- **No impact on `release.yml`.** That workflow is unchanged; this is purely additive on `ci.yml`.
- **Runtime cost:** measured (locally) ~90s on a hot Go cache, ~3 min cold. Fits inside the existing `test` job's wall time.
- **Caching:** `actions/setup-go@v5 cache: true` is enough; goreleaser's output is in `dist/` and is not cached across runs (forces a clean build, which is the whole point).
- **Global flag interaction:** none.

## Acceptance criteria

1. A PR that breaks a goreleaser template (e.g. adds `{{ .NonExistent }}` to `release.footer` in `.goreleaser.yml`) produces a red ❌ on the new `goreleaser-snapshot` job at PR time; the existing `goreleaser-check` job stays green (proving the new job catches what `check` misses).
2. A PR that introduces a windows/arm64 build break (e.g. a Go file using `//go:build !windows` excluded from windows) produces a red ❌ with the failing tuple named in the log.
3. On a clean tree (no defects) the job exits 0 in under 5 minutes on `ubuntu-latest` (cold cache); under 2 minutes on warm cache.
4. The `Inspect produced artefacts` step lists six archives + a `checksums.txt` in `dist/` (linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64, windows-arm64 — matching the `goos × goarch` matrix in `.goreleaser.yml`).
5. The job never creates a GitHub Release, never pushes a tag, never uploads to anywhere — confirmed by inspecting any run's log for absence of `Publishing release ...` / `Uploading artifact ...` lines; the `--skip=publish` flag is the load-bearing argument here.
6. The new job is added to the branch-protection required-checks list (so it can actually block a merge) — same treatment as the existing `test` / `integration` / `docker-backend` / `k8s-backend` jobs in the protected-branch policy.
7. The job is skipped on fork PRs (per the `if:` guard) and a fork-PR run does not surface a failure on the new check.

## Out of scope (deliberately)

- Signing artifacts (cosign / sigstore). `.goreleaser.yml`'s header already documents that signing is post-v1.0 deferred; introducing it here would couple two changes for no reason.
- Publishing the snapshot dist as a build artefact for download (`actions/upload-artifact@v4`). Useful in principle for hand-testing a snapshot binary on `main`, but a separate request — file if needed.
- The Homebrew tap setup. Same rationale as signing.
- Tightening `release.yml`'s post-tag flow. Filed separately as the "re-release failure-mode opacity" bug.
- Adding a windows runner to the new job. `goreleaser` cross-compiles for windows from ubuntu-latest; a native windows runner is unnecessary and expensive.

## Files likely touched

- `.github/workflows/ci.yml` — add the new `goreleaser-snapshot` job (per the YAML sketch above); keep the existing `goreleaser-check` job in place (the two are complementary: `check` is the static-validation tier, `snapshot` is the runtime tier).
- `.goreleaser.yml` — likely no change; if the `--snapshot` profile uncovers an existing defect, fix it as part of this issue's PR.

## Notes

- This issue is sibling to the "re-release opacity" bug — both are about `release.yml`'s failure-mode discoverability, approached from opposite directions. This one prevents the failure; the sibling makes the failure discoverable when prevention misses.
- The "v1.0.1 recovery cut" comments in `.goreleaser.yml` are the exact history this gate exists to prevent recurring: `extra_files` for the PDF "turned out to fail-stop the release (not warn-and-continue as the comment claimed); removed in v1.0.1's recovery cut" — a `--snapshot` job would have caught the fail-stop on the PR introducing the `extra_files` block, instead of at tag-push time.
- `release.yml`'s `workflow_dispatch` path (the documented manual fallback for re-running on an existing tag) is *not* a replacement for this: it runs the same broken config on the same tag and produces the same broken Release. This gate keeps `main` clean so that path doesn't need to fire.
