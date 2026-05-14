# Sprint 10 — architect issues

Sprint 10 architect-surface findings during the prose/design pass that closes PRD 04's runtime cred flow (the in-pod `ibmcloud login` wrap Sprint 9 deferred), lands PRD 06's `roksbnkctl status` per-phase integration, and folds the Sprint 9 tech-writer polish issues deferred to this sprint.

Surface in scope: `book/src/14-credentials-resolver.md`, `book/src/19-in-cluster-ops-pod.md`, `book/src/24-day-2-ops.md`, `CHANGELOG.md` (`## Unreleased (v1.x)` for `v1.3.0`).

Severity scale: `low | medium | high | blocker`.
Status scale: `open | in-progress | resolved | wontfix`.

---

## Issue 1: chapter 19 partial-closure admonition replaced with v1.3.0 reality

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"Trusted-profile flow (v1.2+)".

### What changed

The 12-line `> **v1.2.0 partial closure — read this first.**` admonition at the top of §"Trusted-profile flow (v1.2+)" is gone. Replaced with a single sentence noting that `v1.3.0` closes both the provisioning and runtime sides, cross-linking CHANGELOG `v1.3.0 → ### Changed` for readers who want the chronology of how the v1.2.x partial-closure resolved.

Step 5 ("Pod creation") in §"What just happened, in order" rewritten in present tense: the in-pod `ibmcloud login` wrap detects the SA's trusted-profile annotation and runs `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` against the projected SA token; the static API key never transits the pod env. The Sprint 10 future-tense framing ("will switch the in-pod login wrap …") is gone; the documented behavior is now the real behavior.

### Verification

Sprint 9 tech-writer Issue 1 (the canonical tracker for the v1.2.x partial-closure admonition) closes here. Confirmed `> **v1.2.0 partial closure`, `Sprint 10 conditional-login-wrap closure`, and `staff Issue 2` strings are all gone from chapter 19 (final state for v1.3.0).

---

## Issue 2: chapter 19 §"Verifying the profile is in use" smoke test un-guarded

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"Verifying the profile is in use".

### What changed

The `> Heads up — Sprint 10 carry-over` admonition (the 9-line block around the `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` sample) is gone. The sample now reads as the canonical v1.3.0 happy path:

```bash
$ roksbnkctl --backend k8s ibmcloud iam oauth-tokens
IAM token:  Bearer eyJ…
```

Added two-sentence note covering the OIDC issuer URL propagation window (30–60s on first invocation after `ops install` returns; staff's wrap absorbs this via a brief retry; subsequent calls cache the wrap's auth state for the pod's lifetime). The propagation-timing prose stays — it's real behavior validators will observe — but it no longer pretends to be a Sprint 10 carry-over.

Sprint 9 tech-writer Issue 10 (smoke-test failure documentation) closes as a consequence of staff's in-pod wrap closure landing.

---

## Issue 3: chapter 24 `roksbnkctl status` per-shape output samples added

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` (new top-of-chapter section).

### What changed

Chapter 24's title has always listed `status, logs, k get/apply/exec` but the chapter body had no actual `roksbnkctl status` section — only passing references. Added a new §"`roksbnkctl status`" before §"The `k` command tree" with:

- The shape-independent header block (workspace, region, resource group, cluster identity, TF source, kubeconfig path, cluster reachability).
- One sample per workspace shape: `ShapeEmpty`, `ShapeClusterOnly`, `ShapeSplit`, `ShapeLegacySingle`. Format matches PRD 06 §"`status` command integration"'s table.
- `ShapeLegacySingle` sample preserves the v1.0.x `Last apply:` line verbatim with a script-compat callout — readers parsing the line on a legacy workspace continue to work; new script targets should use the per-phase lines for non-Legacy shapes or switch to `roksbnkctl cluster show` + `bnk show` for a structured read.
- Cross-link to PRD 06 §"`status` command integration" for design rationale.

Pairs with staff's `runStatus` implementation in `internal/cli/inspect.go` and validator's `status` invocations against the four-shape fixture set.

---

## Issue 4: Sprint 9 tech-writer Issue 4 — `ops show` shape under `--trusted-profile=auto`

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"`roksbnkctl ops show`".

### What changed

The `ops show` excerpt now matches what `runOpsShow` (`internal/cli/ops.go:340-360`) actually emits — two independent lines: `trusted-profile: <profile-id>` (or `(none — static-key Secret path)`) and `secret: roksbnkctl-ibm-creds (rotated …)`. The prescriptive `secret: <none — trusted profile X in use>` form Sprint 9 documented is gone (the binary never emitted that form).

Block comment expanded from four lines to five to describe the new `trusted-profile:` line; the existing `secret:` description updated to note the Secret manifest is always rendered (empty data under trusted-profile success, populated under the static-key paths) and the `(missing: …)` form is only the cluster-side-missing case.

---

## Issue 5: Sprint 9 tech-writer Issue 7 — chapter 14 "warning block" → "warning line"

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/14-credentials-resolver.md` §"Compatibility note".

### What changed

One-word edit: "one extra stderr warning block" → "one extra stderr warning line". Matches the single-line shape of all three fallback-warning emissions in `internal/cli/ops.go:272/293/305`.

---

## Issue 6: Sprint 9 tech-writer Issue 8 — chapter 14 §"What's new in v1.2" section position

**Severity**: low
**Status**: wontfix
**Files affected**: `book/src/14-credentials-resolver.md` §"What's new in v1.2: the cred-tmpfile and trusted-profile paths".

### Why wontfix

Tech-writer's Issue 8 proposed two routes: Route A (leave as-is — the "What's new in v1.2" framing tells the reader they're reading delta material; useful for v1.0.x → v1.2.x upgraders); Route B (fold the v1.2 material into §"Backend-specific cred propagation" by reshaping the docker paragraph and embedding the `--trusted-profile=auto|on|off` table as a sub-table). Route B is the structural fix tech-writer flagged as "scales better as v1.3+ landings come."

Route B is not local — it requires re-shaping the existing backend-matrix table, threading the docker tmpfile-pattern prose through the docker row, and embedding the trusted-profile flag table under the k8s row, all without losing the v1.2-was-the-pivot framing that's relevant to upgraders. The chapter is otherwise version-agnostic; carving out the v1.2 callout into the backend matrix would require version-tagging the entire table to preserve the "what's new" surface for v1.3 and beyond.

Sprint 10's scope explicitly flags Issue 8 as `wontfix` if the change requires extensive restructuring (low-priority issue). Logged here as deferred to a future polish cycle (likely v1.4 or v2.0 when the chapter gets a broader restructure pass).

Carried into CHANGELOG `v1.3.0 §"Deferred"` for visibility.

---

## Issue 7: Sprint 9 tech-writer Issue 9 — chapter 19 §"Credential propagation" v1.2 callout placement

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"4. Create or update the credential Secret".

### What changed

Added a `> **v1.2+ note.**` block at the top of §"4. Create or update the credential Secret" mirroring the existing one in §"Credential propagation". Names the static-key Secret as the `--trusted-profile=off` / `auto`-fallback path; cross-links to §"Trusted-profile flow (v1.2+)" for the auto-success path; mentions the empty-data placeholder + `IAM_PROFILE_ID` env var under trusted-profile success.

Closes the discoverability stuck-point tech-writer flagged: a reader skimming §"`roksbnkctl ops install`" hits the trusted-profile cross-link in step 4 rather than only catching it after §"Wait for readiness".

---

## Issue 8: Sprint 9 tech-writer Issue 13 — `<workspace>` vs `sandbox-roks` placeholder consistency

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` (all concrete profile-name samples).

### What changed

Standardized concrete sample names from `sandbox-roks` → `canada-roks` (4 occurrences). Matches the book's established convention: chapter 9 uses `canada-roks` extensively in worked examples; abstract `<workspace>` reserved for prose generalizations (lines 190, 192 of chapter 19 unchanged — they describe the name-construction rule, not a concrete sample). `sandbox-roks` doesn't appear elsewhere in the book.

`runOpsShow` sample line in §"`roksbnkctl ops show`" also uses `canada-roks` for consistency with the rest of chapter 19.

---

## Issue 9: CHANGELOG `v1.3.0` entry under `## Unreleased (v1.x)`

**Severity**: medium
**Status**: resolved
**Files affected**: `CHANGELOG.md`.

### What changed

Added a new `## Unreleased (v1.x)` section (there was no prior Unreleased block; the integrator renames it to `## v1.3.0 — <date>` at tag time per the established v1.2.x cadence). Four subsections:

- `### Added` — `roksbnkctl status` per-phase deployment.
- `### Changed` — in-pod `ibmcloud login` wrap is now trusted-profile-aware (the runtime closure); `roksbnkctl status` output replaces single `Last apply` for non-Legacy shapes.
- `### Fixed` — five Sprint-9-deferred polish items (tech-writer Issues 4, 7, 9, 13) plus the v1.2.x partial-closure full closure (chapter 19 admonition + smoke-test guard removed).
- `### Deferred (v1.x roadmap, post-v1.3.0)` — five items: workspace-config trusted-profile policy customisation, trusted-profile for SSH backend, `--trusted-profile` on `up`/`cluster up`, long-running pod kubeconfig refresh, chapter 14 §"What's new in v1.2" section position (the wontfix from Issue 6 above).

The in-pod `ibmcloud login` wrap bullet from v1.2.0's `### Deferred` block is **removed** as expected — it's no longer deferred. Style matches the v1.2.0 entry (prose intro paragraph, four subsections in `Added` / `Changed` / `Fixed` / `Deferred` order, PRD + PLAN cross-links in the intro, file-path references in the bullets).

---

## Issue 10: PRD 04 / PRD 06 / PLAN.md refinement

**Severity**: low
**Status**: resolved (no changes needed)

### Status

Sprint 10 scope explicitly defers PRD 04, PRD 06, and PLAN.md to "only edit if staff or validator surfaces a design gap mid-sprint." No mid-sprint surface from staff or validator landed in the architect inbox during this pass. Per the prompt, the Sprint-10-relevant prose was finalized at PLAN.md commit `d380332` and PRD 06 commit `4e5f103`; no further refinement needed.

If staff's implementation surfaces an unexpected design gap (e.g., the in-pod wrap's retry semantics don't match the design's "brief retry to absorb 30-60s OIDC propagation" wording), this issue gets reopened in-sprint.

---

## Summary

| Severity | Count |
|---|---|
| blocker | 0 |
| high | 0 |
| medium | 6 |
| low | 4 |

| Status | Count |
|---|---|
| resolved | 9 |
| wontfix | 1 |
| open | 0 |
| in-progress | 0 |

All Sprint 10 architect-scope deliverables landed. No code-side bugs surfaced during the prose pass that warranted filing against staff or validator. Chapter 19 partial-closure admonition + smoke-test guard removed; chapter 24 per-shape `status` samples added; chapter 14 polish issues 7 applied; chapter 19 polish issues 4, 9, 13 applied; chapter 14 Issue 8 marked wontfix per Sprint 10 scope ("flag as `Status: wontfix` if it requires extensive restructuring (low-priority issue)"); CHANGELOG `v1.3.0` entry sits under `## Unreleased (v1.x)`; in-pod login-wrap bullet removed from `### Deferred`.

---

## Remediation pass — tech-writer review (issues/issue_sprint10_tech-writer.md)

Second-pass entries continuing the numbering. Each entry closes (or defers) a tech-writer issue filed against the architect surface.

---

## Issue 11: chapter 24 `TF source: embedded@v1.3.0` drift across all four shape samples (closes tech-writer Issue 2)

**Severity**: high
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` §"`roksbnkctl status`" (header sample + four per-shape samples).

### What changed

The five `TF source: embedded@v1.3.0` lines in the chapter 24 status samples (one in the shape-independent header sample plus one each in `ShapeEmpty`, `ShapeClusterOnly`, `ShapeSplit`) replaced with `jgruberf5/ibmcloud_terraform_bigip_next_for_kubernetes_2_3@v1.3.0` — the canonical `Type: github` shape from `tfSourceDescription` (`internal/cli/inspect.go:258-267`). The `ShapeLegacySingle` sample's `embedded@v1.0.0` replaced with `(unset)` (the actual binary output for `Type: embedded` or empty `Type` per the `default` branch).

Added a short paragraph under the header sample documenting the three real renderings: `github` → `<Repo>@<Ref>`, `local` → `local:<Path>`, `embedded`/unset → `(unset)`. The chapter samples now match what readers will actually see on their terminal verbatim.

Tech-writer Issue 2 was scored `high` per the prompt's "stdout/stderr verbatim" rule. Closes here.

---

## Issue 12: chapter 19 retry-failure stderr text drift (closes tech-writer Issue 6)

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"End-to-end smoke test" paragraph (post-§"Trusted-profile flow (v1.2+)").

### What changed

The "if your first smoke test errors with `failed to assume trusted profile`" sentence replaced with the actual wrap prefix `trusted-profile login failed after 3 attempts: <captured-stderr>` (verified against `internal/exec/k8s.go:81`). Added the retry shape (`3-attempt × 20s-backoff = up to ~40s of waiting inside the wrap`) and a note that the captured stderr will include the underlying `ibmcloud login` diagnostic (typically the "Unable to authenticate" / FAILED banner shape) so the reader knows what to grep for on the terminal.

The "give IAM a few more seconds and re-run" remediation prose preserved — it's correct.

---

## Issue 13: CHANGELOG `### Changed` missing the `make release` integration-test execution gate bullet (closes tech-writer Issue 13)

**Severity**: medium
**Status**: resolved
**Files affected**: `CHANGELOG.md` `## Unreleased (v1.x) → ### Changed`.

### What changed

Added a new bullet under `### Changed` documenting validator's `make release` step-4 hardening: kind-cluster bring-up via `scripts/integration-test.sh`, `go test -tags integration` execution against `internal/exec/...` + `internal/remote/...`, kind-missing warning + confirmation prompt, docker-daemon-unreachable hard abort with remediation hint, `SKIP_INTEGRATION_TEST=1` explicit bypass. Cross-links PLAN.md §"Sprint 10 → Code deliverable 3" (the framing source) and the `make integration-test` standalone target.

Verified `scripts/integration-test.sh` exists (8008 bytes), is executable, and is wired into `Makefile:205-206` (`integration-test:` target) and `Makefile:268-302` (`release:` step 4). The bullet matches option-a in PLAN.md's framing.

---

## Issue 14: CHANGELOG intro "five Sprint-9-deferred polish issues" miscounts vs `### Fixed`'s four bullets (closes tech-writer Issue 8)

**Severity**: low
**Status**: resolved
**Files affected**: `CHANGELOG.md` `## Unreleased (v1.x)` intro paragraph.

### What changed

The intro paragraph reframed: "folds the five tech-writer polish issues deferred from Sprint 9" → "folds four of the five tech-writer polish issues deferred from Sprint 9 (the fifth — chapter 14 §"What's new in v1.2" section position — is deferred again as a v1.x polish item; see `### Deferred` below)". The arithmetic now reconciles with `### Fixed`'s four Sprint-9-polish bullets and `### Deferred`'s chapter-14-position entry.

---

## Issue 15: chapter 24 cross-links missing chapters 10/11 (closes tech-writer Issue 7)

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` §"Cross-references" + inline links in `ShapeClusterOnly` / `ShapeSplit` sample prose.

### What changed

Cross-references section grew two entries — Chapter 10 (Deploying BNK trials on top — the verb that advances `ShapeClusterOnly` → `ShapeSplit`) and Chapter 11 (Tearing down — independent teardown of the cluster and trial phases). Chapter 8 entry, formerly only present as an inline link inside the prose, now also surfaces in the cross-references section so a reader doing a chapter-bottom scan finds the phase concept directly. PRD 06 §"`status` command integration" entry added too.

In the per-shape sample prose: the `roksbnkctl bnk up` / `roksbnkctl bnk down` mentions in the `ShapeClusterOnly` and `ShapeSplit` paragraphs now link to chapter 10 / chapter 11 directly so a reader who lands on chapter 24 to make sense of why their status output changed can navigate to the per-phase verb chapter in one click.

Filename references verified against actual book/src/ contents (`10-deploying-bnk-trials.md`, `11-tearing-down.md`).

---

## Issue 16: chapter 24 intro frames day-2 around kubectl-equivalent verbs only; `status` is now the chapter's first verb (closes tech-writer Issue 15)

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` intro paragraph (line 3).

### What changed

Intro paragraph now opens with "It opens with [`roksbnkctl status`](#roksbnkctl-status) — the workspace-level read of what's deployed — then covers the per-resource verbs: …". The previous "Most day-2 work is the small stuff: read pod state, tail logs, …" framing now sits as the second beat of the same sentence, scoped to the per-resource verbs (which is the surface Sprint 2's `client-go` internalisation actually covered — the `status` verb is shape-aware Go code outside that internalisation, so the framing distinction matters).

The mention of Sprint 2's `client-go` internalisation now reads "internalises all the per-resource verbs into native Go" — scoping it to the verbs it actually covers, not the new `status` line.

---

## Issue 17: chapter 24 `Cluster:` label reuse (identity vs reachability) flagged as confusing to first readers (closes tech-writer Issue 12)

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` §"`roksbnkctl status`" (one-paragraph addition under the header sample).

### What changed

Added a one-paragraph callout under the shape-independent header sample documenting that the two `Cluster:` lines are by design — the first is cluster *identity* (which cluster you're targeting; "(attach existing)" vs "(create new)" disambiguator) and the second is cluster *reachability* (live node-count + ready-count from an API call). The label reuse is intentional ("both are about the cluster"); the right-hand column disambiguates. Reader who notices the duplicate label now has the explanation inline rather than having to dig into chapter 5 or 6 for it.

Tech-writer flagged this as `optional`. Bundled into the same edit pass as Issue 11's TF-source paragraph since both sit in the same chapter 24 header-sample explanation block — efficient to land together.

---

## Issue 18: PRD 04 §"Resolved in Sprint 10" section addition (defers tech-writer Issue 11)

**Severity**: low
**Status**: wontfix

### Why wontfix

Tech-writer's Issue 11 explicitly tags itself as optional ("Optional; v1.4 cycle is fine.") and acknowledges that the Sprint 10 architect briefing explicitly defers PRD 04 / PRD 06 / PLAN.md edits to "only edit if staff or validator surfaces a design gap mid-sprint." No design gap surfaced. PRD-history completeness is a developer-surface concern not a user-surface one; CHANGELOG carries the full chronology (`v1.3.0 → ### Changed` documents the in-pod login wrap closure as the runtime-side of PRD 04's trusted-profile work). The reader tracing the design history through PRD 04 §"Resolved in Sprint 9" will still find the cross-link to CHANGELOG via the section's existing pointer.

Deferred to a v1.4 (or whenever the next PRD-touching cycle lands) — at which point the PRD 04 history can grow §"Resolved in Sprint 10" alongside whatever new sprint's resolution is being documented, in one consistent pass.

Carried into CHANGELOG `v1.3.0 §"Deferred"` is **not** needed — this is a PRD-history footnote, not a user-facing deferred feature. The existing `### Deferred` block stays focused on user-visible items.

---

## Issue 19: chapter 19 §"5. Create the Pod" pod-spec YAML doesn't show the `env:` block with `HOME` / `IAM_PROFILE_ID` (defers tech-writer Issue 14)

**Severity**: low
**Status**: wontfix (deferred to post-v1.3.0)

### Why wontfix

Tech-writer's Issue 14 tags itself as optional ("Optional; v1.4 cycle is fine.") and explicitly notes it's pre-existing chapter shape (the `env:` block has been absent since the chapter was first written; Sprint 10 made the underlying manifest's env block grow with `IAM_PROFILE_ID` but the chapter sample didn't grow). The prose at line 195 already mentions `IAM_PROFILE_ID` in passing under §"What just happened, in order" → step 5 — the reader gets the concept even if the §"5" YAML sample doesn't show it inline.

Adding the `env:` block to §"5. Create the Pod" requires showing both `HOME: /tmp` (always present since v1.2.1) and a conditional `IAM_PROFILE_ID: <profile-id>` (only present under `--trusted-profile=auto|on` success) — which means either two side-by-side YAML samples or a single sample with a comment-annotated conditional block. The former bloats the §"5" section; the latter introduces YAML-sample-as-conditional-template which the chapter doesn't use elsewhere. Either route is bigger than a local fix and would benefit from being landed in a broader chapter-polish pass.

Carried into CHANGELOG `### Deferred (v1.x roadmap, post-v1.3.0)` as a v1.x polish item alongside the chapter 14 §"What's new in v1.2" position deferral — see the bullet below.

---

## Summary (cumulative, first two passes)

| Severity | Count |
|---|---|
| blocker | 0 |
| high | 1 |
| medium | 8 |
| low | 9 |

| Status | Count |
|---|---|
| resolved | 15 |
| wontfix | 3 |
| open | 0 |
| in-progress | 0 |

Remediation pass closed all pre-tag must-fix tech-writer issues (2, 6, 13) and four of six post-tag polish issues (7, 8, 12, 15). Two polish issues deferred as wontfix (11 PRD-history footnote, 14 §"5" YAML expansion) — both tech-writer-flagged as optional with v1.4-cycle acceptable. Book builds clean (`make book BOOK_BACKEND=docker` exit 0; HTML + PDF rendered without broken-link warnings).

No new findings against staff or validator surface during this remediation pass — the architect-side fixes were all prose-only and didn't surface implementation or CI gaps. The Issue 19 deferral (chapter 19 §"5" YAML expansion) is added to CHANGELOG `### Deferred` for visibility.

---

## Remediation pass — validator review (issues/issue_sprint10_validator.md)

Third-pass entries continuing the numbering. Each entry closes a validator-surface finding raised during live-sandbox verification against `canada-roks` (ROKS cluster `bnk-demo`, region `ca-tor`). The Sprint 10 prompt's exception clause for PRD/PLAN edits ("only edit if staff or validator surfaces a design gap mid-sprint") applied — validator's Issue 1 (the in-pod login wrap flag mismatch) is a design gap surfaced mid-sprint, so PLAN.md fell back into architect scope for this pass.

---

## Issue 20: chapter 19 `ops show` sample shows profile NAME but binary emits Profile-uuid (closes validator Issue 2)

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"`roksbnkctl ops show`" (sample line 316), §"Verifying the profile is in use" (SA-annotation sample line 209), §"What each line surfaces" entry 4 (prose).

### What changed

Validator's live `ops show` against `canada-roks` returned `trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320` — the IBM IAM Profile-uuid, not the friendly `roksbnkctl-ops-canada-roks` name. Tracing back through `internal/cli/ops.go`: `runOpsInstall` (line 226) annotates the SA with `tp.ID` (the Profile-uuid), and `runOpsShow` (line 348) reads that annotation verbatim. The binary's chosen shape (the canonical IBM IAM identifier) is right for audit-trail purposes — Option A from validator's writeup. Architect surface adopts:

- §"`roksbnkctl ops show`" sample (line 316) — `trusted-profile: roksbnkctl-ops-canada-roks` → `trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320`.
- §"Verifying the profile is in use" SA-annotation YAML sample (line 209) — `iam.cloud.ibm.com/trusted-profile: roksbnkctl-ops-canada-roks            # ← the profile name` → `iam.cloud.ibm.com/trusted-profile: Profile-ccba11f2-3b1f-4b1a-b8a4-aeed2b7b3320  # ← the IBM IAM Profile ID`.
- §"What each line surfaces" entry 4 (prose) — extended with a clarification that the value is the IBM IAM Profile ID (Profile-uuid form), why (canonical identifier, grep-friendly against IBM Cloud IAM audit logs), and a cross-reference to the `✓ Provisioned IAM trusted profile …` install line (line 176) which carries both the friendly name and the parenthetical Profile-uuid form.

Validator's Issue 2 closes here.

---

## Issue 21: chapter 24 `ShapeLegacySingle` status sample omits `(<age> ago)` suffix (closes validator Issue 3)

**Severity**: low
**Status**: resolved
**Files affected**: `book/src/24-day-2-ops.md` §"`roksbnkctl status`" → `ShapeLegacySingle` sample.

### What changed

Validator's live `roksbnkctl status` against `canada-roks` emitted `Last apply:      2026-05-13 13:30:36 UTC  (12h4m38s ago)`; the chapter sample at line 98 showed only the timestamp column. Sample updated:

```diff
-Last apply:       2026-05-13 14:15:01 MST
+Last apply:       2026-05-13 14:15:01 MST  (4h22m18s ago)
```

The `MST` timezone is left as-is (illustrative; the binary uses the host's local timezone via `info.ModTime().Format(…)` — the live-observed `UTC` was a WSL2 `TZ=UTC` host). The age suffix matches the format string at `internal/cli/inspect.go:200` (`fmt.Fprintf(tw, "Last apply:\t%s\t(%s ago)\n", …)`).

Validator's Issue 3 closes here.

---

## Issue 22: chapter 19 §"Pod creation" + PLAN.md + CHANGELOG flag-reference drift (validator Issue 1 fallout)

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"What just happened, in order" → step 5 ("Pod creation"); `docs/PLAN.md` §"Sprint 10 → Code deliverables" row 1 (line 766) + §"Risks" (line 794); `CHANGELOG.md` `## Unreleased (v1.x) → ### Changed` (line 17).

### What changed

Validator's blocker (Issue 1) traced the in-pod login failure to a non-existent CLI flag: the implementation called `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"`, but `ibmcloud 2.43.0` (the version baked into `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev`) doesn't define that flag. The correct invocation is `ibmcloud login -a https://cloud.ibm.com --cr-token @<path-to-projected-SA-token> --profile <profile-id>`. Staff is landing the implementation fix in parallel (`internal/exec/k8s.go::ibmcloudLoginWrapScript`, `internal/exec/k8s_install.yaml` projected SA-token volume injection, `internal/exec/k8s_test.go` assertion-shape update). Stale flag references on the architect surface need to match the new shape.

Four locations updated:

1. **Chapter 19 §"What just happened, in order" → step 5 ("Pod creation"), line 195** — prose described the wrap as `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"`. Replaced with the full `-a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet` invocation, with a note that the projected SA-token volume (audience `iam`, mounted at `/var/run/secrets/tokens/token`) is also added to the pod spec at install time and that IBM IAM validates that JWT against the trusted profile's `ROKS_SA` claim link (the link `internal/ibm/trusted_profile.go::ensureLink` provisions). The `--apikey` branch description stays unchanged (correct for the static-key path).

2. **PLAN.md §"Sprint 10 → Code deliverables" row 1, line 766** — flag reference in the deliverable description updated to the same `--cr-token @<path> --profile` shape. The `Files` column now mentions `internal/exec/k8s_install.yaml`'s edit covers both env injection AND the projected-token volume — matching what staff is actually landing.

3. **PLAN.md §"Risks", line 794** — `The pod's first `ibmcloud login --trusted-profile-id` may fail …` → `The pod's first `ibmcloud login --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"` may fail …`. The OIDC propagation-window narrative stays — that's real behavior staff's retry absorbs.

4. **CHANGELOG `## Unreleased (v1.x) → ### Changed`, line 17** — the v1.3.0 in-pod-wrap bullet updated to the full new invocation shape, with the projected SA-token volume + `ROKS_SA` claim-link explanation woven in. The bullet now matches what `runOnOpsPod` actually emits at runtime once staff's fix lands.

**Explicitly NOT touched** (per validator's writeup + the Sprint 10 prompt):

- `internal/exec/k8s_install.yaml:157` block comment — staff's surface.
- The v1.2.0 historical `### Deferred` block at `CHANGELOG.md:105` — immutable history (the Sprint-10 forward-looking bullet there described the *intended* runtime closure at Sprint 9 close, with the old flag name; rewriting it would falsify the v1.2.0 chronology that v1.2.x existed under).

### Verification

`grep -rn "trusted-profile-id" book/src/ docs/ CHANGELOG.md` returns exactly one hit — the v1.2.0 §"Deferred" historical block on `CHANGELOG.md:105` — as expected. All architect-surface references to the deprecated flag are gone.

Validator's Issue 1 fallout closes here; the headline Issue 1 itself stays open against staff until their fix lands and validator re-runs the live verification.

---

## Summary (cumulative, all three passes)

| Severity | Count |
|---|---|
| blocker | 0 |
| high | 1 |
| medium | 10 |
| low | 10 |

| Status | Count |
|---|---|
| resolved | 18 |
| wontfix | 3 |
| open | 0 |
| in-progress | 0 |

Third remediation pass closed validator's three architect-surface findings (Issues 2, 3, and Issue 1 fallout). Validator's blocker Issue 1 itself is staff-surface and remains open pending staff's k8s.go + k8s_install.yaml fix. No new findings against staff or validator surface during this pass — the architect edits were prose + sample updates that match what the binary already emits (Issues 20, 21) and forward-match what staff is actively landing (Issue 22). Book builds clean (`make book BOOK_BACKEND=docker` exit 0; HTML rendered without broken-link warnings); `grep -rn "trusted-profile-id" book/src/ docs/ CHANGELOG.md` returns only the v1.2.0 historical block as expected.
