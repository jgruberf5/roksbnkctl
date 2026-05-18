# Sprint 11

**Theme:** PRD 07 — deployed-tfvars snapshot per workspace phase — `v1.4.0`

_Drafted from `docs/prd/07-DEPLOYED-TFVARS.md` (committed in `4b93abf` after Sprint 10's `v1.3.0` shipped). Sprint 11 lands the snapshot-on-apply behavior: after every successful `terraform apply` for a phase, write `~/.roksbnkctl/<workspace>/<state-dir>/terraform.applied.tfvars` (mode `0600`, source-attributed, alphabetic within section). One variable redacted: `ibmcloud_api_key`. Destroy flows leave the prior snapshot intact._

Headline closure for Sprint 11: a user running `roksbnkctl cluster up` (or `roksbnkctl bnk up`, or v1.0.x `roksbnkctl up` for Legacy workspaces) gets a `terraform.applied.tfvars` file in their workspace's per-phase state dir capturing the exact var-file inputs that produced the deployed state. Re-apply, audit, and team-handoff scenarios become file-driven instead of memory-driven.

The four-agent dispatch shape is the same as Sprints 1-10:

- **Architect** — `book/src/06-workspaces.md` new §"`terraform.applied.tfvars` — what's deployed right now" subsection (under Workspace layout); CHANGELOG `v1.4.0` entry under `## Unreleased (v1.x)` with the snapshot bullet under `### Added`; `docs/PLAN.md` Sprint 11 section (drafted in same shape as the Sprint 10 section: theme + carry-overs + risks + gate criteria); PRD 07 refinement only if staff or validator surfaces a design gap.
- **Staff engineer** — implement `internal/config/applied_tfvars.go` with `WriteAppliedTFVars(workspace, phase, sources []string) error` per PRD 07 §"Design"; hook the call into `internal/tf/terraform.go::Workspace.Apply` (after success, before caller's post-apply work); fail-soft on snapshot write error (log warn to stderr, don't fail the apply); unit tests against the four-shape testdata fixtures (Empty/ClusterOnly/Split/Legacy) plus a regression test that destroy flow leaves the snapshot intact.
- **Validator** — seven-step regression sweep (Sprint 9's extended gate, now-with-kind-integration-test from Sprint 10); live verify of snapshot creation against a sandbox `cluster up` (file appears at expected path with mode `0600`, contains source-attributed sections, `ibmcloud_api_key` redacted as `<redacted>`, byte-identical on re-apply with same inputs); destroy regression check; cross-link audit on architect's chapter edits.
- **Tech-writer** — read-only review at end of sprint; dogfooding loop ("I want to verify the v1.4.0 snapshot actually records what I deployed"); drift sweep between PRD 07 ↔ staff source ↔ chapter quotes ↔ CHANGELOG; launch-readiness verdict for `v1.4.0`.

The release tag itself (`v1.4.0`) is **integrator-owned** — Sprint 11 lands all the prep; the integrator runs the now-double-extended `make release` pre-tag gate, cuts the tag, kicks off goreleaser, and runs `make release-publish` to mirror assets.

## Carry-over considerations from Sprint 10

- PRD 07 §"Open questions" item 1 (should `ops install/uninstall` also snapshot?) stays out of scope per the PRD's recommendation. If a user asks for it during Sprint 11, file as a Sprint 12 / v1.5 candidate.
- Sprint 10's accepted-not-resolved staff issues (3 — fixed-20s retry backoff, no jitter; 4 — wrap stderr interleaving on triple-fail) remain `open (acceptable for v1.x)`. Validator's pass-2 live verify confirmed first-attempt success against sandbox, so the retry pathology hasn't materialized; revisit only if a real environment exceeds the 60s OIDC propagation window.
- Sprint 10's tech-writer Issue 9 (chapter 24 tabwriter alignment cosmetic) is `open` post-tag polish. Architect can fold a one-line tabwriter route-through if the chapter-6 edit pass naturally touches the area; otherwise carry into v1.4.x backlog.
