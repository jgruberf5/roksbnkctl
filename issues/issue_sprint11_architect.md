# Sprint 11 — architect issues

Sprint 11 architect-surface findings during the prose / design pass that lands [PRD 07](../docs/prd/07-DEPLOYED-TFVARS.md)'s `terraform.applied.tfvars` snapshot per workspace phase.

Surface in scope: `book/src/06-workspaces.md`, `CHANGELOG.md` (`## Unreleased (v1.x)` for `v1.4.0`), `docs/PLAN.md` (new `## Sprint 11` section), and PRD 07 refinement where staff or validator surfaces a design gap.

Severity scale: `low | medium | high | blocker`.
Status scale: `open | in-progress | resolved | wontfix`.

---

## Issue 1: chapter 6 §"`terraform.applied.tfvars` — what's deployed right now" added

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/06-workspaces.md`.

### What changed

New top-level `##` section landed between §"The on-disk layout" and §"The everyday workspace routine". Covers the file's phase-scoped paths in a three-row table (Split / ClusterOnly cluster phase, Split trial phase, LegacySingle union), the canonical-HCL contents with source-attribution comments, the lifecycle (written on every successful apply, overwritten each apply, untouched by destroy, never read by the tool), the single-variable redaction (`ibmcloud_api_key` → `<redacted>`) and `0600` mode, the explicit "what it's not" list (not an input; not Terraform defaults; not state-derived; not `TF_VAR_*` capture), the safe-to-commit guidance with the standard reminder about other semi-sensitive workspace material, and one worked example showing the header comment + source-attribution comments + redacted-key line.

Cross-links to PRD 07 §"Design" for the format spec and chapter 14 (credentials resolver) for the cred-resolver pathway that explains why the API key isn't in tfvars to begin with.

### Verification

Section renders cleanly as inspected — no broken Markdown / table syntax. Anchor target `terraform-applied-tfvars-whats-deployed-right-now` will be referenced by the CHANGELOG `v1.4.0` entry and any external follow-up readers. `mdbook build book/` not run locally (mdbook not installed in this environment); validator picks up the build verification as part of the regression sweep.

---

## Issue 2: CHANGELOG `## Unreleased (v1.x)` block added with `v1.4.0` content

**Severity**: medium
**Status**: resolved
**Files affected**: `CHANGELOG.md`.

### What changed

New `## Unreleased (v1.x)` block landed above the existing `## v1.3.0 — 2026-05-14` heading. Three-sentence intro paragraph names the snapshot file as the Sprint 11 headline closure, frames it as enabling re-create / audit / handoff workflows, and cross-links PRD 07 + PLAN.md §"Sprint 11" for design context. Two subsections: `### Added` (the `terraform.applied.tfvars` snapshot bullet, with paths, redaction note, file mode, the not-an-input invariant, and cross-link to chapter 6) and `### Deferred (v1.x roadmap, post-v1.4.0)` (the PRD 07 §"Open questions" item 1 — `ops install` / `ops uninstall` snapshot, explicitly out of scope this sprint, with a note that all prior-cycle deferred items from v1.3.0 still carry forward).

No `### Changed` or `### Fixed` subsections: PRD 07 is purely additive (no behavior change to existing flows; the file is a new output) and no Sprint 10 carry-over fixes were folded into this cycle. If validator or staff identifies a Sprint 10 carry-over polish that ships in this cycle, surface as a follow-up issue and add `### Fixed` then.

### Verification

CHANGELOG line numbering: the v1.3.0 entry that was at line 7 in the post-tag tree now starts at line 19 after the v1.4.0 block lands. Any external references to "v1.3.0 at line 7" need updating; none exist in the repo per a `grep` sweep at architect surface (only the live edits to this file).

---

## Issue 3: PLAN.md Sprint 11 section added (mirrors Sprint 10 shape)

**Severity**: medium
**Status**: resolved
**Files affected**: `docs/PLAN.md`.

### What changed

New `## Sprint 11 — PRD 07 deployed-tfvars snapshot (post-v1.3)` section landed after Sprint 10 (line ~805) and before the `## What's deliberately deferred to post-v1.0` heading. Section structure mirrors Sprint 10's: goal (one-line summary plus a brief framing), drivers / why now (cross-linking PRD 07 §"Why"), code deliverables (three-row table: `WriteAppliedTFVars` helper / `Workspace.Apply` hook / unit tests), test deliverables (staff's four-shape unit sweep, validator's live verify, tech-writer drift sweep), risks (goroutine ordering, redaction-list incompleteness, legacy-shape mis-attribution — all three per the architect brief), gate criteria (issue files resolved, regression sweep green, live snapshot verify recorded, chapter 6 + CHANGELOG final, `mdbook build` clean), and carry-overs (none — single-PRD cycle).

### Verification

Section lands at the same nesting depth as Sprint 10 and parses as Markdown — three-row code-deliverables table is well-formed, anchor `sprint-11--prd-07-deployed-tfvars-snapshot-post-v13` resolves predictably.

---

## Issue 4: PRD 07 cross-reference path corrected (`internal/exec/tf.go` → `internal/tf/terraform.go`)

**Severity**: medium
**Status**: resolved
**Files affected**: `docs/prd/07-DEPLOYED-TFVARS.md` (four occurrences).

### What changed

PRD 07 as committed (`4b93abf`) referred to the Apply wrapper as `internal/exec/tf.Workspace.Apply` and the file path as `internal/exec/tf.go` in four places: §"Trigger point" prose, §"Source ordering" implementation note, §"Acceptance criteria" bullet 2, and §"Cross-references" bullet 3. The actual wrapper lives at `internal/tf/terraform.go::Workspace.Apply` (verified: `grep -n "func.*Apply" internal/tf/terraform.go` returns line 247; `internal/exec/tf.go` does not exist). The four references now read `internal/tf/terraform.go::Workspace.Apply` / `Workspace.Apply` / `internal/tf/terraform.go` as appropriate.

This is an architect-surface fix — staff hooks the call into the actual file regardless of what PRD 07 says, but the PRD shouldn't disagree with the code path. Caught before staff dispatch so the staff prompt and PRD agree.

### Verification

`grep -n "internal/exec/tf" docs/prd/07-DEPLOYED-TFVARS.md` returns nothing post-edit. The Sprint 11 PLAN.md section already uses the correct path (`internal/tf/terraform.go::Workspace.Apply`); staff's prompt at `prompts/sprint11/staff.md` should be re-read against the corrected PRD before dispatch (if it cites the wrong path, fix there too — outside this architect's scope but flagging for the dispatcher).

---

## Issue 5: chapter 6 worked example + PRD 07 §"File contents" sample re-byte-matched to staff's actual `renderAppliedTFVars` output

**Severity**: medium
**Status**: resolved
**Files affected**: `book/src/06-workspaces.md` §"Worked example" + §"What it captures"; `docs/prd/07-DEPLOYED-TFVARS.md` §"File contents".

### What changed

After staff landed `internal/config/applied_tfvars.go` (verified at HEAD), I diffed the staff-emitted text against my initial chapter-6 / PRD-07 samples. Three divergences:

1. **Header comment shape.** Staff's `renderAppliedTFVars` writes the timestamp + phase on a single line (`# Generated by roksbnkctl <ver> at <ts> after terraform apply on phase=<phase>.`) then a second line (`# Re-generated each apply. Do not edit by hand — your changes will be overwritten.`). My initial samples had a three-line wrap with `after\n# terraform apply...` — that's the PRD's prose spec, not what the code emits. Both the chapter 6 and PRD 07 samples now match the staff two-line shape.
2. **Section labels.** Staff's `sourceLabel()` maps `terraform.tfvars` → `"config.yaml"`, `terraform.tfvars.user` → `"terraform.tfvars.user"` (no `(if present)` suffix), and `cluster-phase-override.tfvars` → `"cluster-phase override"` (no `(cluster phase only)` suffix). The "(if present)" / "(cluster phase only)" parentheticals in my initial samples were extrapolated from the PRD's contextual notes; the actual emitted headers are unadorned. Both samples now match.
3. **Missing-file handling.** Staff appends `" (missing)"` to the section label and emits an empty body when `terraform.tfvars.user` doesn't exist. This wasn't in the original chapter 6 prose — added an explicit note in §"What it captures" and the PRD 07 sample so readers aren't surprised by `# === from terraform.tfvars.user (missing) ===` in real outputs.
4. **Variable alignment.** Staff's emitter writes `name = value` with single-space padding (no column alignment); my initial sample had `cluster_name        = "..."` column-aligned. The chapter 6 sample is now unaligned to match staff's actual output.

### Verification

`grep -c "if present\|cluster phase only" book/src/06-workspaces.md` returns 0 (both parentheticals removed). `grep -c "if present\|cluster phase only" docs/prd/07-DEPLOYED-TFVARS.md` returns 0. The chapter sample now reads exactly as `cat ~/.roksbnkctl/canada-roks/state-cluster/terraform.applied.tfvars` will once staff's hook is wired through `Workspace.Apply` and a real apply runs.

If staff later changes the render string (e.g., for the redacted-line comment text — currently `  # source: cred resolver, not persisted`), this architect-surface side needs another pass. Validator's live verify against a sandbox apply will catch any remaining drift.

---

## Issue 6: chapter 6 cross-link to PRD 07 uses relative `../../docs/prd/` path — RE-EXAMINED, escalated, then **RESOLVED** (published-book 404 fixed)

**Severity**: medium (was: low)
**Status**: resolved (was: accepted → open → resolved)
**Files affected**: `book/src/06-workspaces.md` lines 53 and 74.

### Re-examination outcome (architect, post-staff)

The user asked architect to re-examine the `accepted` verdict. The re-examination found the original rationale **factually wrong** on two counts and uncovered a real bug. Flipping to `open` with a concrete fix.

### Verification data: how often each link shape actually appears in `book/src/`

A `grep -n "docs/prd/" book/src/*.md` sweep, then bucketed:

- **Relative `../../docs/prd/` shape — 1 file, 2 occurrences, both in `book/src/06-workspaces.md`** (lines 53 and 74 — the file this issue is about).
- **Absolute `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/` shape — 16 files, 69 occurrences**:
  `03-what-roksbnkctl-does.md` (1), `05-doctor.md` (1), `10-deploying-bnk-trials.md` (1), `14-credentials-resolver.md` (5), `15-ssh-targets.md` (2), `16-on-flag-ssh-jumphosts.md` (2), `17-execution-backends.md` (11), `18-choosing-backend.md` (5), `19-in-cluster-ops-pod.md` (6), `21-dns-testing-gslb.md` (6), `22-throughput-testing.md` (1), `23-e2e-test-plan.md` (15), `24-day-2-ops.md` (5), `26-troubleshooting.md` (2), `32-extending-roksbnkctl.md` (6), `preface.md` (1).

The original issue body cited `book/src/14-credentials-resolver.md` and `book/src/19-in-cluster-ops-pod.md` as examples of the `../../docs/prd/` convention. **Both files use full GitHub URLs exclusively** — they don't contain `../../docs/prd/` at all. The cited convention claim is wrong.

The convention is in fact documented explicitly in two places:

- `book/src/preface.md` line 49: *"PRD links: design documents under `docs/prd/` are linked as full GitHub URLs ... so they resolve from the published book at GitHub Pages."*
- `book/src/32-extending-roksbnkctl.md` line 110: *"PRD references use the full GitHub URL ... to avoid the published-book 404 issue surfaced in Sprint 1."*

### Verification data: what mdbook actually does with these links

A previous `book/book/html/` build is present in the working tree (built 2026-05-14 05:52, post-edit). Inspecting the rendered chapter 6 HTML:

```
$ grep -o 'href="[^"]*docs/prd/[^"]*"' book/book/html/06-workspaces.html
href="../../docs/prd/07-DEPLOYED-TFVARS.html#design"
href="../../docs/prd/04-CREDENTIALS.html"
```

mdbook rewrote `.md` → `.html` in the link target (standard mdbook behaviour for relative links). It did **not** copy the PRD source files into the output tree (`find book/book/ -path '*docs/prd*'` returns nothing — mdbook only publishes `book/src/`). The published-book URLs become:

- `<base>/../../docs/prd/07-DEPLOYED-TFVARS.html#design` — **404 on GitHub Pages** (the target HTML doesn't exist; only the `.md` source lives in the repo, and GitHub Pages serves the mdbook `html/` tree, not the repo root).
- `<base>/../../docs/prd/04-CREDENTIALS.html` — **404 on GitHub Pages** (same reason).

This is the exact failure mode that Sprint 1 surfaced and the preface convention exists to prevent. The original `accepted` rationale ("mdbook renders these as plain links to the file on disk ... they work in the rendered book") is wrong: mdbook rewrote `.md` → `.html`, the HTML doesn't exist, the published book emits two 404s.

### Proposed fix

Replace both `../../docs/prd/...` URLs in `book/src/06-workspaces.md` with the established full-GitHub-URL convention. Concrete diff:

```diff
-See [PRD 07 §"Design"](../../docs/prd/07-DEPLOYED-TFVARS.md#design) for the format spec.
+See [PRD 07 §"Design"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/07-DEPLOYED-TFVARS.md#design) for the format spec.
```

```diff
-See [PRD 04 §"Cred tmpfile-bind-mount pattern"](../../docs/prd/04-CREDENTIALS.md) for why the API key isn't in tfvars in the first place.
+See [PRD 04 §"Cred tmpfile-bind-mount pattern"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md#cred-tmpfile-bind-mount-pattern-docker-backend) for why the API key isn't in tfvars in the first place.
```

(The PRD 04 link also picks up the section anchor `#cred-tmpfile-bind-mount-pattern-docker-backend` since the prose names that exact section — matches the way chapter 14 line 254 cites the same section.)

### v1.4.0 pre-tag must-fix

This is a published-book bug — two 404s for users who follow the chapter 6 cross-links from GitHub Pages. The current sprint's headline closure (`terraform.applied.tfvars`) is the chapter where they sit, and chapter 6 is high-traffic onboarding material. Land the fix before tagging `v1.4.0`. Architect can land it directly (single-file, two-line markdown edit, no implementation risk) or hand it to the integrator's pre-tag pass — either path is fine, but it shouldn't ship as-is.

### Pre-existing-bug check

`grep -rn "\.\./\.\./docs/prd/" book/src/` after this fix should return zero matches. Validator's cross-link audit should add a regression check for any future `../../docs/prd/` recurrence — easy `mdbook build` post-check or a `scripts/check-prd-links.sh` one-liner.

### Closure

Fix landed in `book/src/06-workspaces.md` per the proposed diff above — both `../../docs/prd/...` URLs replaced with the full `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/...` convention. Post-edit verification:

- `grep -n "docs/prd/" book/src/06-workspaces.md` returns lines 53 and 74 with the absolute-URL shape.
- `grep -rn "\.\./\.\./docs/prd/" book/src/` returns zero matches across the whole `book/src/` tree.

No commit — handed to the integrator's pre-tag pass alongside the rest of the Sprint 11 working-tree edits.

---

## Issue 7: PRD 07 acceptance-criteria sprint number (`Sprint NN — TBD`) updated

**Severity**: low
**Status**: resolved
**Files affected**: `docs/prd/07-DEPLOYED-TFVARS.md` §"Acceptance criteria (Sprint 11)" heading.

### What

PRD 07 as committed has `## Acceptance criteria (Sprint NN — TBD)` — the placeholder dates from when the PRD was drafted ahead of sprint assignment. Sprint 11 is now the assigned sprint.

### Proposed fix

Rename the heading to `## Acceptance criteria (Sprint 11)`. Single-line architect-surface edit; not blocking staff. Leaving as **open** for the validator or tech-writer's end-of-sprint pass to handle as part of the cross-link audit — touching it here would race with any concurrent edit to the file. If neither agent picks it up, the architect closes it during the v1.4.0 tag-prep pass.

### Verification

Closed by the architect during the v1.4.0 pre-tag pass. Grep against the working tree:

- `grep -n "Sprint NN" docs/prd/07-DEPLOYED-TFVARS.md` returns no matches.
- `grep -n "Acceptance criteria" docs/prd/07-DEPLOYED-TFVARS.md` returns `152:## Acceptance criteria (Sprint 11)`.

This closes the corresponding hand-offs from validator Issue 7 (`issues/issue_sprint11_validator.md`) and tech-writer Issue 6 (`issues/issue_sprint11_tech-writer.md`). Rename is staged in the working tree; commit + tag remain the integrator's step.

---

## Issue 8: PRD 07 / chapter 6 cross-link consistency — byte-check completed against staff HEAD

**Severity**: low
**Status**: resolved
**Files affected**: `docs/prd/07-DEPLOYED-TFVARS.md` §"File contents" sample and `book/src/06-workspaces.md` §"Worked example".

### What

Per the architect brief §4, after staff lands `WriteAppliedTFVars`, the architect spot-checks:

- Chapter 6 sample byte-matches staff's `renderAppliedTFVars` output — **resolved by Issue 5** (header reshape, section-label dewordifying, missing-file note, value alignment).
- PRD 07's header-comment example matches the literal string staff writes — **resolved by Issue 5** (same fix applied to the PRD sample).
- CHANGELOG bullet's user-visible claim (file at `...applied.tfvars` after every cluster/bnk up) verifiable via `ls` after a real apply — **deferred to validator's live verify** (this needs a sandbox apply against IBM Cloud; out of architect's local sweep).

### Remaining work for the validator

Verify post-sandbox-apply that `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` exists with mode `0600` and the header timestamp parses as RFC3339. If anything diverges from chapter 6's claims (paths, mode, redacted-line text, section labels), file against this architect's surface for a targeted fix.
