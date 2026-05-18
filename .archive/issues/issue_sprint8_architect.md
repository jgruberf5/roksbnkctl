# Sprint 8 — architect issues

## Issue 1: chapter-10 `bnk down` sample output is illustrative — staff must implement the trailing summary line for it to match the chapter quote
**Severity**: low
**Status**: open — flagged to staff
**Description**: Chapter 10's §"`roksbnkctl bnk down`" sample output ends with two lines that the spike branch's `runBnkDown` does **not** emit:

```
✓ Trial phase destroyed. Cluster phase ~/.roksbnkctl/default/state-cluster/ is intact.
  Run `roksbnkctl bnk up` to deploy another trial against the same cluster.
```

The spike just delegates straight to `runTrialDown(cmd, nil)` after the shape check, so the user sees nothing beyond Terraform's own `Destroy complete!` line and whatever `runTrialDown` itself prints. The chapter prose promises the "Cluster phase is intact / run bnk up to redeploy" reassurance line because it's the user-facing point of the whole command — losing the cluster across a `bnk down` is exactly the v1.0.x pain point that motivates the feature, so confirming the cluster persisted is the obvious place to surface that. Staff should add the summary lines (or close-enough text) at the end of `runBnkDown`'s happy path; if they push back, file an architect follow-up to soften the chapter prose.
**Files affected**: `internal/cli/bnk_phase.go` (staff edit), `book/src/10-deploying-bnk-trials.md` (architect carry if staff declines).
**Proposed fix**: staff adds a `fmt.Fprintln(os.Stderr, ...)` block at the tail of `runBnkDown` after `runTrialDown` returns nil, matching the chapter prose. Two-line shape; references the workspace's `state-cluster/` path via `cctx`. The architect chapter quote should track whatever shape staff lands.

## Issue 2: PRD 06 refusal-text table uses single backticks inside backtick-quoted strings; chapter quotations had to switch quoting style
**Severity**: low
**Status**: resolved — chapter quotes use a mixed style that mdbook renders cleanly; no PRD edit applied
**Description**: PRD 06 §"Refusal messages" quotes the messages as a markdown table row with the message wrapped in backticks, while the message itself contains inline backtick segments (e.g. ``` `bnk up` can't isolate ``` ). That nested-backtick form rendered fine in the PRD's table but doesn't survive a copy-paste into the chapter-11 catalogue table without escape gymnastics. Chapter 11's catalogue table switched to double-backtick fencing for the outer message string where inline backticks appear inside, which mdbook+pulldown-cmark parses correctly. PRD left alone — its rendering is fine standalone. Flagged here so the next sprint's architect knows why the chapter table looks subtly different from the PRD table.
**Files affected**: `book/src/11-tearing-down.md` (chapter catalogue), `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` (no change).
**Proposed fix**: none — closed.

## Issue 3: empty-workspace `down` refusal text needs to match the catalogue exactly
**Severity**: low
**Status**: open — flagged to staff
**Description**: PRD 06 §"Refusal messages" defines `down` on Empty → `nothing to destroy in this workspace`. The spike branch's `cluster_phase.go` emits the same text for `cluster down` on Empty. Staff's composite `runDown` dispatcher (in the refactored `internal/cli/lifecycle.go`) must emit the **exact same string** for the Empty case so chapter-11's catalogue table quote stays accurate. If staff diverges (e.g. ``error: nothing to destroy in workspace "<name>"`` with workspace name interpolation), the chapter catalogue needs to follow — or staff aligns. Empirically the spike doesn't yet contain the composite dispatcher (only the phase-scoped commands), so the exact wording for `down`-on-Empty is a staff-implementation choice.
**Files affected**: `internal/cli/lifecycle.go` (staff edit), `book/src/11-tearing-down.md` §"Refusal messages — catalogue" (architect follow-up if divergence).
**Proposed fix**: staff lands the literal `errors.New("nothing to destroy in this workspace")` shape so the chapter catalogue quote stays canonical; the prose surface is already locked in.

## Issue 4: PLAN.md §"Sprint 8" deliverable list — no edit needed this sprint
**Severity**: low
**Status**: resolved — no change required
**Description**: The PLAN.md §"Sprint 8" entry already enumerates the chapter-8/10/11 edits and the CHANGELOG entry under "Documentation deliverables". The wording matches what landed this sprint. No additive edit needed.
**Files affected**: `docs/PLAN.md` (no change).
**Proposed fix**: none — closed.
