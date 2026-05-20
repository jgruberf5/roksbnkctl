# Sprint 12

**Theme:** patch cycle — `--var-file` relative-path resolution + v1.4.x tech-writer polish nudges — `v1.4.1`

_Drafted from `issues/issue_sprint12_staff.md` (pre-seeded post-v1.4.0). Sprint 12 is a patch-release cycle: no new PRDs, no design surface to spec. The headline closure is the `--var-file` relative-path bug surfaced during the v1.4.0 user-side live verify (validator Sprint 11 Issue 2 §"Out-of-band action for the user")._

Headline closure for Sprint 12: a user running `roksbnkctl up --var-file=./terraform.tfvars --auto` from a directory containing `terraform.tfvars` gets the file consumed by terraform, instead of the current "Failed to read variables file: Given variables file `./terraform.tfvars` does not exist" because terraform's CWD is the state dir, not the shell PWD.

The four-agent dispatch shape mirrors Sprints 1-11, scaled down for patch scope:

- **Architect** — `CHANGELOG.md` new `## Unreleased (v1.x)` block above `## v1.4.0` for the `v1.4.1` patch entry; `docs/PLAN.md` Sprint 12 section (much shorter than Sprint 11's — single-bug scope); two small chapter 6 nudges deferred from Sprint 11 tech-writer Issues 2 + 4 (defaults caveat in §"Worked example" and cred-resolver URL clarification near the redacted-line callout). No PRD authoring; the staff issue file is the design surface.
- **Staff engineer** — implement the `resolveVarFiles` helper per `issues/issue_sprint12_staff.md` Issue 1 §"Proposed fix"; wire into the five `flagVarFiles` consumption points in `internal/cli/lifecycle.go`, `internal/cli/cluster_phase.go`, `internal/cli/bnk_phase.go`; unit test in `internal/cli/lifecycle_test.go` covering absolute pass-through / relative resolved against CWD / missing-file error message; close `issue_sprint12_staff.md` Issue 1.
- **Validator** — seven-step regression sweep (matches Sprint 11's gate, with the docker-backend kind-integration-test step intact); reproduce the bug per `issues/issue_sprint12_staff.md` §"Reproduce" and confirm staff's fix makes it pass; cross-link audit on architect's CHANGELOG + chapter edits; flag any analogous shell-CWD-vs-state-dir gotchas in other path-shaped flags the sweep happens to surface.
- **Tech-writer** — read-only review at end of sprint; drift sweep between staff source ↔ CHANGELOG `v1.4.1` bullet ↔ chapter 6 (the bug isn't user-doc-surface, but the chapter nudges are); dogfooding the now-working `--var-file=./...` flow against the chapter 6 worked example.

The release tag itself (`v1.4.1`) is **integrator-owned** — Sprint 12 lands all the prep; integrator runs the `make release` pre-tag gate, cuts the tag, kicks off goreleaser, and runs `make release-publish` to mirror assets.

## Carry-over considerations from Sprint 11

- Sprint 11 tech-writer Issues 2 and 4 (chapter 6 discoverability nudges) ride along under architect scope this cycle — small enough to fold into the patch without scope bloat.
- `resolved_sprint10_*.md` and `A_Project_Managers_Guide_to_Agentic_Developed_Products.pdf` + `NEW_PROJECT_STARTING_POINT.md` + `make_PM_Guide_book_pdf.sh` remain untracked in the working tree from prior sessions — out of scope for Sprint 12; integrator decides what (if anything) to fold in during the v1.4.1 tag-cut commit.
- Validator's live `cluster up` against `canada-roks` from Sprint 11 Issue 2 — the user has now run it (which is how this bug surfaced). The four post-apply observations listed in that issue still apply for v1.4.1's verify; the `--var-file=./...` repro is the additional check.
