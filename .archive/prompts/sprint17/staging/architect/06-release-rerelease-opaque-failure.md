---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: release.yml `workflow_dispatch` re-release on an existing tag fails opaquely (raw goreleaser error, no roksbnkctl-level hint at the fix)'
labels: []
assignees: ''
---

## Symptom

`.github/workflows/release.yml`'s `workflow_dispatch` path is documented in the YAML's own header comment as the manual fallback for re-running the build against an existing tag (e.g. when the first attempt failed mid-way). In practice, against a tag that already has a published Release object (the common re-run case), `goreleaser release --clean` exits non-zero with a `Release v1.x.y already exists` (or equivalent) error from goreleaser's GitHub API client.

The failure shows up in the workflow run log as a single line buried in `goreleaser`'s stdout/stderr, with **no preflight check** in `release.yml` that detects "this tag already has a Release" and emits an actionable roksbnkctl-level hint at the integrator. There is also no documented recovery path — the integrator has to either:

- Delete the existing Release manually via `gh release delete vX.Y.Z` (which they may not realize is the fix), then re-run the workflow, or
- Add `--clean` semantics that explicitly delete the existing Release before recreating (which `--clean` *doesn't* do in goreleaser v2 — `--clean` clears the `dist/` directory locally, NOT the GitHub Release on the remote), or
- Pass extra goreleaser flags that the current `args: release --clean` doesn't.

The Sprint 16 / `v1.6.2` cycle surfaced this as an integrator pain point in the GitHub Issue #2 ("mermaid PDF") context (per the Sprint 17 README's hint: "issue #2 surfaced that the goreleaser re-release on an existing tag fails"). The defect class is failure-mode opacity: the workflow's failure log doesn't lead the operator to the fix.

## Reproduction

```
# Prerequisites: a tag that already has a published Release.
#   (Take any past green release in this repo; the cleanest demo uses a fresh
#    throwaway tag so no real Release is impacted.)

# 1. cut a throwaway tag and let release.yml run normally
git tag v0.0.0-rerun-demo
git push origin v0.0.0-rerun-demo
gh run watch --exit-status      # waits for the goreleaser job to finish (green)

# 2. confirm the Release object exists on GitHub
gh release view v0.0.0-rerun-demo --json name,tagName,publishedAt

# 3. trigger the workflow_dispatch fallback against the same tag
gh workflow run release.yml -f tag=v0.0.0-rerun-demo
gh run watch --exit-status

# 4. observe the failure
# expected (post-fix): the workflow's first step prints an actionable
#   roksbnkctl-level error like:
#     "Release v0.0.0-rerun-demo already exists on GitHub. Either
#      (a) delete it first: gh release delete v0.0.0-rerun-demo
#      (b) re-tag (e.g. v0.0.0-rerun-demo-2) and push
#      (c) re-run release.yml with input force=true to delete-and-recreate."
#   and exits non-zero before goreleaser runs (saving 3+ min of compile time).
#
# actual (pre-fix): goreleaser runs the full cross-compile (~3 min) then fails
#   at the upload step with a raw `release already exists` error. The integrator
#   has to scroll through the log to find it, then guess the right manual recovery.

# 5. cleanup
gh release delete v0.0.0-rerun-demo -y
git push --delete origin v0.0.0-rerun-demo
```

## Expected behavior

A workflow_dispatch re-run against a tag that already has a Release object either (a) preflight-fails with a clear, actionable error naming the existing Release and the three recovery paths above, or (b) succeeds by deleting and recreating the Release (guarded behind an explicit `force` input so a fat-finger dispatch can't nuke a good Release). In either case the failure mode is discoverable from the workflow run logs — the integrator does not need to read goreleaser source to figure out what happened.

## Actual behavior

Goreleaser runs the full build, then surfaces a single-line "release already exists" error from the GitHub API at upload time. The workflow's `args: release --clean` does not include `--rm-dist` / `--skip-validate` / any flag that changes this behavior; `--clean` clears the local `dist/` directory, which is unrelated to the remote-Release-exists problem. There is no preflight step on the workflow side that detects this before the 3+ minute compile.

The result is that an integrator running the documented re-release fallback at midnight after a v1.x.y release went wrong has to (a) wait 3 minutes for the failure, (b) read the goreleaser log carefully to find the one-liner, (c) guess that `gh release delete` is the fix, (d) trigger workflow_dispatch a second time, (e) wait 3 more minutes for the actual re-run. Each step is unobvious; each could be a roksbnkctl-level message in the workflow's first 30 seconds.

## Environment

- `roksbnkctl version`: any (defect is in the release workflow, not the binary).
- OS / arch: (n/a — runs on `ubuntu-latest` per the workflow).
- IBM Cloud region: (n/a)
- Backend: (n/a)
- Affected files: `.github/workflows/release.yml` (full file, especially the `workflow_dispatch` input handling).
- Tool versions in play: `goreleaser/goreleaser-action@v6`, `goreleaser ~> v2`, `gh` (server-side).

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** the `workflow_dispatch` path was added as a one-shot integrator-facing fallback under the assumption that "if the first run failed, the Release object wasn't created either, so re-running is fine". In practice goreleaser creates the Release object early (right after compile, before all assets are uploaded), so a mid-way failure leaves a partial Release that blocks every subsequent re-run. The fix is a preflight step that detects this state and chooses an actionable response.
2. Less likely: the maintainer is aware and has been recovering by hand each time, treating the opacity as "the cost of doing business". If so, criterion 1 still holds — the integrator's future-self / new team members shouldn't have to learn this by stubbing a toe on it.

## Acceptance criteria

1. A `workflow_dispatch` run against a tag that already has a Release object **exits non-zero in under 30 seconds** with a clearly-formatted error message that names (a) the existing Release tag, (b) the three recovery paths (delete, re-tag, force-recreate), and (c) the exact `gh release delete <tag> -y` and `gh workflow run release.yml -f tag=<tag> -f force=true` commands. The error is the first thing the integrator sees in the workflow log; not buried under goreleaser output.
2. The workflow grows a new `workflow_dispatch.inputs.force` boolean input (default `false`). When `true`, the preflight check still runs but it deletes the existing Release (and confirms the delete via `gh release view <tag>` returning not-found) before goreleaser runs.
3. The tag-push trigger path (`on.push.tags: ['v*.*.*']`) is unaffected — the preflight check is gated on `github.event_name == 'workflow_dispatch'` so the normal "tag-push → first-ever release" flow saves zero seconds and gains zero risk.
4. The fix is in `release.yml`, not in `.goreleaser.yml`. Goreleaser config stays unchanged; only the workflow's preflight step changes. (This keeps the goreleaser-snapshot CI gate's coverage of `.goreleaser.yml` orthogonal — no overlap in what each issue's PR touches.)
5. The reproduction's step 4 produces the post-fix message verbatim (the message text is part of the YAML and is the single source of truth — the issue's PR sets the wording).
6. Regression check: a documented note in the workflow's header comment (the existing block at lines 1–17) describes the preflight + force-recreate semantics so a future integrator reading the workflow understands the contract.

## Out of scope (deliberately)

- Automatically deleting a Release object on every workflow_dispatch run without a `force` input. Far too dangerous — a fat-finger dispatch from the Actions UI would nuke production. Force is opt-in.
- Re-architecting the release pipeline to use a single workflow that handles tag-push + re-run + retry-after-partial-failure as one state machine. Tempting, big scope; this issue tackles only the opacity bug, not the orchestration redesign.
- Re-running on the same tag in the *same* workflow run (auto-retry on transient GitHub API failures). Different defect class.
- Solving the half-published Release state in the goreleaser side (goreleaser's own `--rm-dist`-style behavior for remote artifacts). The fix lives in the workflow, not the tool.
- Touching `tools-images.yml`'s tag-push handling. Different file, different failure class, file separately if it surfaces.

## Files likely touched

- `.github/workflows/release.yml` — add a `Preflight: detect existing Release` step that runs `gh release view ${{ inputs.tag }}` and branches on the result; add the `force` input to `workflow_dispatch.inputs`; gate the preflight on `github.event_name == 'workflow_dispatch'`; update the workflow's header-comment block to describe the new semantics.
- (Optionally) `.goreleaser.yml` — no edit; mentioned as the obvious place a reader might look. The fix is deliberately not there.

## Notes

- This issue is sibling-but-distinct to the `goreleaser-snapshot` pre-tag CI gate. That gate prevents `.goreleaser.yml` defects from reaching `main`. This one fixes the `release.yml` re-run path for when a release fails mid-way despite a clean `.goreleaser.yml` (network blip, GitHub API hiccup, etc.) — i.e. for failures the snapshot job cannot prevent. Both are needed.
- The README's framing — "is the failure mode discoverable from the workflow run logs?" — answers "no" for this defect today. The acceptance criteria translate that "no" into a verifiable "yes" via the preflight message contract.
- This is a deliberately small, focused issue: one workflow file, one preflight step, one new input. Resist scope-creep on the PR — a redesign of the entire release cycle is a separate conversation.
