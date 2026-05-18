You are the architect agent for Sprint 11 of the roksbnkctl project. Sprint 11 lands PRD 07's `terraform.applied.tfvars` snapshot per workspace phase, cuts `v1.4.0` at the end. Your scope is `book/src/06-workspaces.md` (new subsection documenting the snapshot file), `CHANGELOG.md` (`v1.4.0` entry under `## Unreleased (v1.x)`), and `docs/PLAN.md` (new Sprint 11 section in the same shape as Sprint 10's). PRD 07 itself only gets refined if staff or validator surfaces a design gap mid-sprint.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

## Read first

- `docs/prd/07-DEPLOYED-TFVARS.md` — source of truth for Sprint 11's design. The §"Resolved design decisions" section names the four locked-in answers (apply-only trigger, destroy leaves snapshot, no `.gitignore` touching, single-variable redaction). The §"Open questions" lists one remaining item (`ops install/uninstall` snapshot — out of scope for this sprint).
- `book/src/06-workspaces.md` — current state of the chapter you're extending. Look for where the workspace dir layout is currently documented; your new subsection sits next to that.
- `CHANGELOG.md` — top of file. v1.3.0 entry is at line 7. New `## Unreleased (v1.x)` goes above v1.3.0.
- `docs/PLAN.md` — Sprint 10 section (around line 720+) for shape reference. Your Sprint 11 section follows the same structure: theme + carry-overs + risks + gate criteria + test deliverables.
- `prompts/sprint10/architect.md` — prior-sprint prompt structure; reuse the section-shape conventions.

## Coordinate with parallel agents

A **staff engineer** agent is implementing `internal/config/applied_tfvars.go` and hooking it into `internal/tf/terraform.go::Workspace.Apply`. **Do not touch files under `internal/`, `cmd/`, `internal/tf/`.**

A **validator** agent will do the regression sweep + live verify + cross-link audit on your chapter edits.

A **tech-writer** agent does read-only review at end of sprint.

## Tasks (priority order)

### 1. Chapter 6 §"`terraform.applied.tfvars` — what's deployed right now"

New subsection under chapter 6's workspace-layout section (find the existing `~/.roksbnkctl/<workspace>/` directory tree documentation; this subsection sits adjacent).

Content to cover:

- **File path**: `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` for cluster phase, `~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` for trial phase. Legacy single-state collapses to `state/` with a `phase=legacy-single` header comment.
- **What it captures**: union of `config.yaml`-derived vars + `terraform.tfvars.user` + phase overrides (cluster phase only), in canonical HCL with source-attribution comments. Alphabetic within each section.
- **Lifecycle**: written after every successful `terraform apply`. Destroy doesn't touch it — the prior `up`'s snapshot stays so re-create-from-snapshot remains an option.
- **Redaction**: `ibmcloud_api_key` is rendered as `<redacted>`. Everything else is verbatim. File mode is `0600` regardless.
- **What it's NOT**: not read by `roksbnkctl` itself as input; not a record of terraform defaults or state-derived values; not a TF_VAR_* env capture.
- **Safe-to-commit guidance**: the file is suitable for git commit alongside `config.yaml` *after* the user verifies the redaction matches their threat model. Mention that workspace dirs may contain other semi-sensitive material (`cluster-outputs.json`, the state dirs themselves) that the user should review with the same lens.
- **One worked example**: a small sample showing the source-attribution comments and the redacted API key line.

Cross-link to PRD 07 §"Design" for the format spec and PRD 04 §"Cred tmpfile-bind-mount pattern" for why the API key isn't in tfvars in the first place.

### 2. CHANGELOG `v1.4.0` entry under `## Unreleased (v1.x)`

Add a new `## Unreleased (v1.x)` section above v1.3.0 (line 7). Four subsections, mirroring the v1.3.0 entry style:

- `### Added` — `terraform.applied.tfvars` snapshot per workspace phase (the headline). Reference PRD 07.
- `### Changed` — none expected; leave the subsection out if there's nothing.
- `### Fixed` — any Sprint 10 carry-over fixes that land in this cycle (e.g., the chapter 24 tabwriter alignment polish if staff or you fold it in).
- `### Deferred` — anything carried forward (PRD 07 open question 1 — `ops install/uninstall` snapshot — explicitly listed).

Intro paragraph (~3 sentences) names the headline closure (snapshot file produced on every apply; re-create/audit/handoff scenarios become file-driven) and cross-links PRD 07 + PLAN.md §"Sprint 11" for design context.

### 3. PLAN.md Sprint 11 section

Add a `## Sprint 11` section after the Sprint 10 section. Use the Sprint 10 section as a structural template — same subsection layout:

- **Theme** — PRD 07 implementation; one-line summary.
- **Drivers / why now** — the user-side rationale (DR, audit, team handoff, re-apply scenarios). Cross-link PRD 07 §"Why."
- **Code deliverables** — three to four items, named-with-files. Staff's `internal/config/applied_tfvars.go` + the `internal/tf/terraform.go::Apply` hook + unit tests. Anything else that surfaces.
- **Test deliverables** — staff's four-shape unit tests; validator's live verify against sandbox `cluster up`; tech-writer's drift sweep.
- **Risks** — the snapshot path could race with terraform if Apply has goroutine surprises (it doesn't currently, but worth naming); the redaction-list-is-incomplete risk (mitigated by `0600` and the single-var scope); the legacy-shape merge of trial+cluster being mis-attributed.
- **Gate criteria for `v1.4.0` tag** — same shape as Sprint 10's gate: regression sweep green, live snapshot verify green, all four agents' issue files at `Status: resolved` / `wontfix` / `accepted`.

### 4. PRD 07 / chapter cross-link consistency

After staff lands the implementation, spot-check the architect-surface prose against the implemented behavior. Specifically:

- Chapter 6's sample output should byte-match what staff's `WriteAppliedTFVars` actually emits for a representative test fixture.
- PRD 07's "header comment" example (`# Generated by roksbnkctl <version> at <RFC3339 timestamp>...`) should match the literal string staff writes — adjust either PRD or chapter if staff's choice of format string differs.
- CHANGELOG bullet's user-visible claim (file at `...applied.tfvars` after every cluster/bnk up) should be verifiable via `ls` after a real apply.

If anything diverges, fix the architect-surface side (chapter / PRD / CHANGELOG) to match staff's actual output rather than asking staff to change implementation — the binary's emitted text is the source of truth.

## Issue tracking

File at `issues/issue_sprint11_architect.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Verification before reporting done

- Chapter 6 new subsection lands at a sensible position, renders cleanly under `mdbook build book/`.
- CHANGELOG `## Unreleased (v1.x)` `### Added` bullet present, intro paragraph cross-linked.
- PLAN.md Sprint 11 section follows Sprint 10's structural shape.
- `mdbook build book/` clean.
- No `internal/` / `cmd/` files touched.

## Final report

Under 200 words. Include: what landed in each of the four files (chapter 6, CHANGELOG, PLAN.md, optionally PRD 07), mdbook verdict, any findings filed against staff/validator, deferred-to-v1.4.x items (if any).
