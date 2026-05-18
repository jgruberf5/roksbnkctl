# Sprint 9 — architect issues

## Issue 1: chapter-19 trusted-profile sample output is illustrative — staff must land the exact strings for the chapter quotes to match

**Severity**: medium
**Status**: open — flagged to staff
**Description**: Chapter 19's new §"Trusted-profile flow (v1.2+)" section quotes several sample-output lines that the staff implementation may not emit verbatim. The chapter prose is prescriptive on:

- The `roksbnkctl ops install --trusted-profile=auto` sample (lines `checking IAM permissions for trusted-profile provisioning ...`, `✓ iam-identity perms present (key <key-suffix>)`, `provisioning trusted profile roksbnkctl-ops-<workspace> ...`, `✓ created profile crn:v1:bluemix:public:iam-identity:::profile/iam-Profile-<id>`, `✓ linked compute resource: cluster <id> / ns roksbnkctl-ops / sa roksbnkctl-ops`, `annotated serviceaccount roksbnkctl-ops with iam.cloud.ibm.com/trusted-profile=<name>`).
- The `--trusted-profile=auto` fallback warning text (the 4-line block starting `warning: workspace API key lacks IAM \`iam-identity\` permission`).
- The `ops show` line `secret:       <none — trusted profile <name> in use>` when the trusted-profile path is in use.
- The `ops uninstall --confirm` line `✓ deleted IAM trusted profile <name>` and the best-effort warning when IAM perms have lapsed.
- The SA annotation `roksbnkctl.io/provisioned-at` (timestamp) — added alongside the `roksbnkctl.io/trusted-profile-managed: "true"` marker.

If staff diverges on any of these (e.g., the install log line uses `creating` instead of `provisioning`, or the SA marker annotation is named differently), the chapter quote needs to follow. Staff drives implementation; the architect surface here is the prose, so any divergence is a chapter follow-up.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` (architect carry if staff diverges), `internal/cli/ops.go` + `internal/exec/k8s.go` + `internal/ibm/trusted_profile.go` (staff).

**Proposed fix**: staff implements the install / uninstall log lines + SA annotations to roughly match the chapter prose; architect refines chapter samples if staff's exact wording differs after the validator's `go run ./cmd/roksbnkctl ops install --help` spot-check.

## Issue 2: chapter-14 / chapter-19 warning-text consistency

**Severity**: low
**Status**: resolved — chapter 14 wording softened to match chapter 19
**Description**: Chapter 14's three-row `--trusted-profile` flag table originally said `auto` falls back with a "one-line stderr warning"; chapter 19 quotes a 4-line block. The chapter 14 wording was carried over from PLAN.md's framing but the actual UX value of the warning is in spelling out the remediation path, which is a multi-line block. Chapter 14 reworded to "a stderr warning that names the missing perm and how to silence it (`--trusted-profile=off`)" and the §"Compatibility note" paragraph reworded from "one extra stderr warning line" to "one extra stderr warning block ... naming the fallback and how to silence it" so the two chapters are consistent.

**Files affected**: `book/src/14-credentials-resolver.md` — done.

**Proposed fix**: none — closed.

## Issue 3: PRD 04 §"Open questions" — only the first two items closed; remaining items left untouched

**Severity**: low
**Status**: resolved — no further action
**Description**: PRD 04 §"Open questions" had four items at sprint start: (1) centralized cred resolver (closed in Sprint 3), (2) trusted-profile auto-provisioning (closed this sprint), (3) kubeconfig refresh during long-running pods (still open), (4) cred TTL alignment (closed this sprint, moot under trusted-profile path).

Sprint 9 closes items 2 and 4 (the prompt's "first two items"). Item 1 was already de-facto resolved in Sprint 3 when `internal/cred/resolver.go` landed; the question's wording asks for a recommendation rather than a state-of-the-world, so its phrasing as an "open question" is harmless. Item 3 (kubeconfig refresh) is genuinely still open and is a Sprint 10+ concern (the long-lived ops pod's projected SA token rotation behaviour against the IBM Cloud SDK's session caching). The prompt explicitly says "Leave any other open questions alone" — so item 1 and 3 are unchanged.

**Files affected**: `docs/prd/04-CREDENTIALS.md` — no change beyond the two strike-throughs.

**Proposed fix**: none — closed.

## Issue 4: PLAN.md §"Sprint 9" deliverable list — no edit needed this sprint

**Severity**: low
**Status**: resolved — no change required
**Description**: PLAN.md §"Sprint 9" enumerates the code deliverables (5 items), test deliverables, documentation deliverables (chapter 14, chapter 19, PRD 04 §"Resolved in Sprint 9", CHANGELOG `v1.2.0`), gate criteria, risks, and carry-overs. Architect-scope wording (the doc deliverables block and the gate criterion mentioning "chapter 14 + 19 cross-links resolve") matches what landed this sprint byte-for-byte. No refinement edit applied.

**Files affected**: `docs/PLAN.md` — no change.

**Proposed fix**: none — closed.

## Issue 5: CHANGELOG "Deferred" subsection — flags items the architect can't fully resolve without product input

**Severity**: low
**Status**: open — for integrator awareness, not blocking
**Description**: The CHANGELOG `Deferred (v1.x roadmap, post-v1.2.0)` block lists three items:

1. Workspace-config customisation of trusted-profile policies (architect inferred from chapter 19's "post-v1.2 enhancement" line about `ibmcloud.trusted_profile.policies`).
2. Trusted-profile path for the SSH backend (architect inferred from the underlying tech — SSH targets don't have a projected k8s SA token; there's no path for this without a new design).
3. `--trusted-profile` flag on `roksbnkctl up` / `cluster up` (architect inferred — terraform HCL providers do their own auth and the trusted-profile path is exclusively for the ops pod's SA).

Items 2 and 3 are architecturally sound exclusions (terraform / SSH don't have the trust-anchor infrastructure that k8s projected tokens provide). Item 1 is more speculative — whether a future cycle adds custom-policy config under `ibmcloud.trusted_profile.policies` is a product decision the architect doesn't have authority to commit to.

**Files affected**: `CHANGELOG.md` (architect-edited; potentially needs integrator review).

**Proposed fix**: integrator reviews the Deferred block at tag time; if item 1's framing is too forward-leaning, trim to a more neutral "the v1.2 ships with minimal default policies; richer policy customisation is a future cycle" sentence. Items 2 and 3 are safe.
