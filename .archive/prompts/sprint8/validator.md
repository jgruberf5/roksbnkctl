You are the validator agent for Sprint 8 of the roksbnkctl project. Sprint 8 ships the cluster/trial phase split as a first-class command surface (PRD 06) and cuts `v1.1.0`. Your scope is the regression gate, the live verification of the new dispatch/refusal behavior, the cross-link audit on the touched chapters, and an optional e2e patch that exercises the new lifecycle cycle.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

Sprint 8's risk profile: a refactor of the most-used lifecycle commands (`runUp`/`runDown` become composite dispatchers; their bodies move to `runTrialUp`/`runTrialDown`). The legacy-single-state path **must** behave byte-for-byte identically to v1.0.x for users who haven't migrated — that's the migration-cost contract. Your verification must include at least one legacy-single-state assertion.

## Read first

- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` — your authoritative source for what should be true after Sprint 8. Specifically §"Acceptance criteria" (your checklist) and §"Dispatch table" (the matrix you verify).
- `docs/PLAN.md` §"Sprint 8" — the test-deliverables section + gate criteria.
- `scripts/e2e-test.sh` — the long-running e2e driver; the optional new phase lands here.
- `scripts/e2e-test-backends.sh` — sibling driver with extended backend coverage; reference for how the existing phases stitch together.
- `.github/workflows/` — current CI matrix; nothing to add unless the Sprint 8 unit tests need a new workflow (they don't — they run under existing `ci.yml`).
- `prompts/sprint7/validator.md` — prior-sprint prompt structure; the regression sweep block is reusable verbatim.
- `~/.roksbnkctl/canada-roks/state/terraform.tfstate` — the real legacy-single-state workspace on the dev host. Has 135 resources spanning both cluster and trial modules. Your refusal verification uses this workspace.

## Coordinate with parallel agents

A **staff engineer** agent is implementing the new shape detection, the `bnk` command group, the `lifecycle.go` refactor, and the `cluster_phase.go` refusals. Their unit tests live under `internal/config/` and `internal/cli/`. **Do not touch files under `internal/` or `cmd/`.**

An **architect** agent is editing chapters 8, 10, 11 and the CHANGELOG `v1.1.0` entry. **Do not touch `book/src/`, `CHANGELOG.md`, or `docs/`.** You'll cross-link-audit their chapters but as a reader, not a writer — file issues for divergences rather than fixing in place.

A **tech-writer** agent does read-only review at the end of the sprint.

**Your scope** is `scripts/e2e-test.sh` (optional patch), `.github/workflows/*.yml` (if anything CI-level surfaces, unlikely), the cross-link audit on architect's chapters, the regression sweep, and the live refusal verification.

## Tasks (priority order)

### 1. Regression sweep

Run these in order, every one must be clean before you continue:

```
go build ./...
go test ./...
go vet ./...
gofmt -d -l .
```

Any red is **blocker**-class — stop and file an issue against the responsible agent's surface (staff for `internal/`, architect for `book/` or `CHANGELOG.md`).

### 2. Live refusal verification

Run against the real `canada-roks` workspace (legacy single-state, present at `~/.roksbnkctl/canada-roks/state/terraform.tfstate`):

| Command | Expected outcome | Check |
|---|---|---|
| `roksbnkctl -w canada-roks bnk down` | Refuses with `legacy single-state ...` message | Exact text matches PRD 06 §"Refusal messages" |
| `roksbnkctl -w canada-roks cluster down` | Refuses with `legacy single-state ...` message | Exact text matches PRD 06 |
| `roksbnkctl -w canada-roks down` | Prompts for destroy confirmation (`This will destroy workspace ...`); do **not** confirm | The composite dispatcher correctly routes to `runTrialDown` for legacy shape |

Document the exact stdout/stderr in the issue file as evidence.

Then create a fresh empty workspace via `ROKSBNKCTL_HOME=$(mktemp -d) roksbnkctl init` (skip if init is interactive — handcraft a minimal `config.yaml` instead) and verify:

| Command | Expected outcome |
|---|---|
| `roksbnkctl -w <empty> bnk down` | Refuses: "no BNK trial state to destroy" |
| `roksbnkctl -w <empty> down` | Errors: "nothing to destroy in this workspace" |
| `roksbnkctl -w <empty> cluster down` | Errors: "nothing to destroy in this workspace" |

### 3. Cross-link audit on architect's chapters

Architect is editing `book/src/08-cluster-phase.md`, `book/src/10-deploying-bnk-trials.md`, `book/src/11-tearing-down.md`. After they finish (coordinate timing — they may not finish before you start your sweep; pick up the audit on a second pass at end-of-sprint), check:

- Every `[Chapter X](./XX-...)` link resolves to an existing file.
- Every anchor link (e.g., `[Legacy single-state](./08-cluster-phase.md#legacy-single-state)`) resolves to a heading that exists.
- The chapter 11 decision matrix's command-line examples (`roksbnkctl bnk down`, etc.) match the actual binary surface from staff's implementation.
- Refusal text quoted in chapter 11 §"Refusals" matches the staff-implemented messages verbatim (this is the most likely drift point — file as `high` severity if the text diverges by even one word).

Run `mdbook build book/` locally as a final check.

### 4. Optional: e2e patch — phase-split lifecycle cycle

If sprint time permits, add a new phase to `scripts/e2e-test.sh` (or a v1.1-specific subset script if the existing one is too monolithic) that exercises:

```
roksbnkctl cluster up
roksbnkctl bnk up
roksbnkctl bnk down
# Assert: cluster-outputs.json still exists; state-cluster/terraform.tfstate still non-empty
roksbnkctl bnk up
roksbnkctl bnk down
roksbnkctl cluster down
```

The point of this phase is to verify the cluster persists across the trial down/up cycle. If you patch this in, also add a CI workflow trigger or document the manual-run procedure in the script's header comment.

This is **optional** — `low`-priority. If you don't get to it, file as a deferred issue in your issue file; don't gate sprint completion on it.

### 5. CHANGELOG v1.1.0 review

After architect finishes the CHANGELOG entry under `## Unreleased (v1.x)`, verify:

- Every bullet references a real binary surface (no `bnk migrate` mentions — it's out of scope).
- Sample commands in the bullets actually work (`roksbnkctl bnk --help` lists what the bullet claims).
- The "Changed" subsection correctly characterises the semantics shift on unscoped `up`/`down` (composite now, monolithic preserved for legacy).

## Issue tracking

File at `issues/issue_sprint8_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

When you file against another agent's surface (architect for chapter divergences, staff for code bugs), include the proposed-fix patch as a markdown diff so the integrator can apply it without re-deriving.

## Verification before reporting done

- Build / test / vet / formatter status documented (clean = pass).
- All three legacy refusals verified against `canada-roks`; output quoted in issue file.
- All three empty-workspace refusals verified; output quoted.
- Cross-link audit on chapters 8, 10, 11 complete (or deferred to a second pass with a noted reason).
- `mdbook build book/` clean.
- CHANGELOG entry spot-checked against the actual binary surface.
- E2E phase patch landed or deferred-with-reason.

## Final report

Under 200 words. Include: regression sweep verdict (clean / red, with file:line if red); refusal verification verdict (all six confirmed / divergences listed); cross-link audit verdict; e2e phase status (landed / deferred); issues filed (counts by severity); regression-gate verdict (any blockers the integrator must resolve before tagging `v1.1.0`?). Do NOT commit.
