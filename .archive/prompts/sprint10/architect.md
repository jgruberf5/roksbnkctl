You are the architect agent for Sprint 10 of the roksbnkctl project. Sprint 10 closes PRD 04's runtime cred flow (the in-pod `ibmcloud login` wrap that Sprint 9 deferred) and PRD 06's `status` integration, plus the v1.2.x chapter polish that Sprint 9 deferred. Cuts `v1.3.0` at the end. Your scope is the prose / design surface: chapter 19 + chapter 24 + chapter 14 edits, CHANGELOG `v1.3.0` entry, and PRD 04 / PRD 06 refinement only on gap surfaces.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

Sprint 10's headline reframe: the v1.2.x partial-closure callout in chapter 19 §"Trusted-profile flow (v1.2+)" goes away. v1.3.0 ships the full closure — `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` actually returns a token under `--trusted-profile=auto` instead of failing with `missing API key`. The smoke-test admonition Sprint 9 had to add comes out; the documented behavior is now the real behavior.

## Read first

- `docs/prd/04-CREDENTIALS.md` — your authoritative source for the in-pod wrap closure. The §"Resolved in Sprint 9" subsection documents the provisioning side; Sprint 10 closes the runtime side.
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"`status` command integration (Sprint 10 scope addition)" — your authoritative source for the chapter 24 status updates.
- `docs/PLAN.md` §"Sprint 10" — your authoritative deliverables list (the documentation deliverables block + gate criteria).
- `book/src/19-in-cluster-ops-pod.md` — current text has the v1.2.x partial-closure admonition (line ~166) + the smoke-test `> Heads up` guard (line ~220). Both come out.
- `book/src/14-credentials-resolver.md` — Sprint 9 tech-writer Issues 7 (wording) + 8 (section position) — your polish surface.
- `book/src/24-day-2-ops.md` — current text covers `roksbnkctl k get/apply/logs/exec/port-forward` and `roksbnkctl status`. Sprint 10 adds per-shape `status` output samples.
- `CHANGELOG.md` §"Unreleased (v1.x)" — your v1.3.0 entry lands here; the integrator renames to `## v1.3.0 — <date>` at tag time.
- `issues/issue_sprint9_tech-writer.md` — Issues 4, 7, 8, 9, 13 are explicitly deferred to Sprint 10. Read their proposed-fix sections; apply.
- `issues/resolved_sprint9_*.md` — Sprint 9 closure notes. Staff Issue 2 is the in-pod wrap deferral that Sprint 10 closes.
- `prompts/sprint9/architect.md` — prior-sprint prompt structure; verification block reusable.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the in-pod login wrap (`internal/exec/k8s.go` runOnOpsPod, conditional on the SA's trusted-profile annotation; injects `IAM_PROFILE_ID` env into the pod spec via the manifest renderer in `internal/cli/ops.go`); the per-phase status integration (`internal/cli/inspect.go::runStatus` consuming `config.DetectShape` + per-phase tfstate mtime); unit tests for both. **Do not touch any file under `internal/` or `cmd/`.**

A **validator** agent is running the full seven-step regression sweep, live-verifying the trusted-profile end-to-end against a sandbox IBM Cloud workspace (`ops install --trusted-profile=auto` → `ibmcloud iam oauth-tokens` returns fresh token), and adding the integration-test execution to the local pre-tag gate (Makefile + maybe `scripts/integration-test.sh`). **Do not touch `Makefile` or `scripts/`.**

A **tech-writer** agent does read-only review at end of sprint.

**Your scope** is `book/src/14-credentials-resolver.md`, `book/src/19-in-cluster-ops-pod.md`, `book/src/24-day-2-ops.md`, `CHANGELOG.md` (under `## Unreleased (v1.x)`), and `docs/PLAN.md` §"Sprint 10" / `docs/prd/04-CREDENTIALS.md` / `docs/prd/06-*.md` (refinement only if staff or validator surfaces a design gap).

## Tasks

### 1. Chapter 19 partial-closure removal

The v1.2.x partial-closure admonition at the top of §"Trusted-profile flow (v1.2+)" (the `> **v1.2.0 partial closure — read this first.**` block, ~12 lines) **comes out**. Replace with a single sentence noting that v1.3.0 closes both the provisioning and runtime sides; the v1.2.x partial behavior is referenced in CHANGELOG `## v1.3.0 → ### Changed` for readers who specifically want the history.

Step 5 ("Pod creation") in §"What just happened, in order" also gets reframed: the partial-closure caveat at the end ("The Sprint 10 conditional-login-wrap closure ... will switch the in-pod `ibmcloud login` ...") becomes present tense — "The in-pod `ibmcloud login` wrap detects the SA's trusted-profile annotation and runs `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` against the projected SA token; the static API key never transits the pod env."

### 2. Chapter 19 smoke-test un-guarding

§"Verifying the profile is in use" has a `> Heads up — Sprint 10 carry-over` admonition around the `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` sample. Remove the admonition entirely. The sample now becomes the canonical v1.3.0 happy path:

```bash
$ roksbnkctl --backend k8s ibmcloud iam oauth-tokens
IAM token:  Bearer eyJ…
```

Add a brief note (one or two sentences) about the OIDC issuer propagation timing — first invocation may take 30-60s after `ops install` returns because IBM IAM needs to pick up the cluster's OIDC issuer URL; staff's implementation includes a brief retry inside the wrap to absorb this. After the first successful call the wrap is cached for the pod's lifetime.

### 3. Chapter 24 — per-shape `status` output samples

Add a new section after the existing `roksbnkctl status` material covering the v1.3.0 per-shape output. Match PRD 06 §"`status` command integration"'s table format — one shape per code block, with the new per-phase lines (cluster phase + BNK trial) plus the existing context (workspace, region, cluster name, TF source, kubeconfig, cluster reachability) all in one realistic sample.

Four samples (one per shape). For ShapeLegacySingle, preserve the v1.0.x single `Last apply` line verbatim with a callout that this shape is unchanged for script compatibility — readers parsing status output of legacy workspaces continue to work.

### 4. Sprint 9 tech-writer polish (Issues 4, 7, 8, 9, 13)

Read the proposed-fix section of each issue in `issues/issue_sprint9_tech-writer.md`; apply verbatim where the proposed fix is concrete. Specifically:

- **Issue 4 (medium)**: chapter 19 §"`ops show`" shape under `--trusted-profile=auto` — replace the documented `secret: <none — trusted profile X in use>` line with what `ops show` actually emits (read `internal/cli/ops.go:340-360` to confirm the current output shape; document accordingly). Staff doesn't change `ops show` this sprint, so the chapter must match the binary as-is.
- **Issue 7 (low)**: chapter 14 §"Compatibility note" — "one extra stderr warning block" → "one extra stderr warning line" (singular, matching the single-line shape of all three warnings in staff's `internal/cli/ops.go`).
- **Issue 8 (low)**: chapter 14 §"What's new in v1.2" section position — tech-writer suggested moving it earlier in the chapter. Apply if the change is local; flag as `Status: wontfix` if it requires extensive restructuring (low-priority issue).
- **Issue 9 (medium)**: chapter 19 §"Credential propagation" v1.2 callout placement — surface the v1.2 cred-handling change at the top of the section, not buried as a mid-section note.
- **Issue 13 (low)**: chapter 19 `<workspace>` vs `sandbox-roks` placeholder consistency — pick one (the established convention elsewhere in the book is concrete real-looking names for samples, abstract `<workspace>` only in prose generalisations). Standardise.

### 5. CHANGELOG `v1.3.0` entry

Edit `CHANGELOG.md` §"Unreleased (v1.x)" — add `### Added` (status per-phase deployment), `### Changed` (in-pod login wrap is now trusted-profile-aware; status output replaces single `Last apply` for non-Legacy shapes), `### Fixed` (the five Sprint-9-deferred chapter polish issues + the v1.2.x partial-closure now fully closed), `### Deferred` (move the closed in-pod login-wrap bullet OUT; only keep items still genuinely deferred). Match the v1.2.0 entry style.

### 6. PRD 04 / PRD 06 / PLAN.md refinement

Only edit if staff or validator surfaces a design gap mid-sprint. Default: leave them alone. The Sprint-10-relevant prose was finalised at PLAN.md commit `d380332` and PRD 06 commit `4e5f103`.

## Issue tracking

File at `issues/issue_sprint10_architect.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

If you find a code-side bug (the actual stderr emissions from staff's in-pod wrap don't match what you're writing in chapter 19), file the issue against staff's surface.

## Verification before reporting done

- `mdbook build book/` succeeds locally.
- Chapter 19 partial-closure admonition gone; smoke-test un-guarded; sample output reads as v1.3.0 reality.
- Chapter 24 has per-shape `status` samples for all four shapes (Empty, ClusterOnly, Split, LegacySingle).
- Chapter 14 polish issues applied (Issues 7, 8 from Sprint 9).
- Chapter 19 polish issues applied (Issues 4, 9, 13 from Sprint 9).
- CHANGELOG entry sits under `## Unreleased (v1.x)`; in-pod login-wrap bullet removed from `### Deferred`.
- Sample command lines match `go run ./cmd/roksbnkctl <verb> --help` for the new surface.
- No edit under `internal/`, `cmd/`, `scripts/`, `.github/`, or `Makefile`.

## Final report

Under 200 words. Include: files edited (full list), files created (full list), line counts (rough), issues filed (counts by severity), anything the integrator should know before committing. Do NOT commit.
