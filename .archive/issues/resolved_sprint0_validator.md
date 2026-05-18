# Sprint 0 — validator issues, resolution notes

Resolution log for `issue_sprint0_validator.md`. One actionable bug
(fixed), one informational (resolved at filing time), one roadmap entry
(no action — informs Sprint 1+ planning).

## Issue 1 (stale `bnkctl` reference at docs/E2E_TEST.md:46) — fixed

**Resolution**: one-character edit on line 46 of `docs/E2E_TEST.md` —
`bnkctl` → `roksbnkctl`. Cosmetic; no runtime impact. The script
`scripts/e2e-test.sh` itself was already clean (validator confirmed
this during the audit).

**Status**: ✅ resolved
**Files touched**: `docs/E2E_TEST.md` (1 line)
**Commit**: lands in the Sprint 0 integration commit

## Issue 2 (`BNKCTL_HOME` env var) — N/A at filing time

**Resolution**: confirmed there's no `*_HOME` environment variable
referenced in `scripts/e2e-test.sh` or anywhere else. The "rename
BNKCTL_HOME → ROKSBNKCTL_HOME" line in the validator brief was a
holdover from an earlier draft where such a var existed; it was removed
before the rename and the brief wasn't updated. No action.

**Status**: ✅ resolved at filing time as "N/A — variable doesn't exist"

## Issue: future testing improvements (roadmap) — informs Sprint 1+ planning

**Resolution**: not a bug; not a Sprint 0 deliverable. The package-
by-package coverage table is preserved here as a reference for Sprint
1+ test planning. Each PRD already plans testing additions per the
roadmap entry; this issue is a confirmation that the planned additions
target the right packages.

**Highlights for sprint planners**:

- Sprint 1 (PRD 01 — SSH client): the `internal/remote` testcontainers-
  go work is the highest-ROI testing add. Already in PLAN.md Sprint 1.
- Sprint 2 (PRD 02 — kubectl): `internal/k8s` gets `client-go fake`
  unit tests. Already in PLAN.md Sprint 2.
- Sprint 3 (PRD 03 — backends): `internal/exec` argv-builder tests +
  cred resolver in `internal/cred`. Already in PLAN.md Sprint 3.
- Sprint 4 (PRD 03 cont'd): `internal/exec/k8s` against `kind` (or
  testcontainers-go's k3s module). Already in PLAN.md Sprint 4.
- Sprint 5 (PRD 03 — DNS): `internal/test/dns` with miekg's stub server.
  Already in PLAN.md Sprint 5.

No work changes — this issue confirms the plan is aligned with the
actual coverage gaps.

**Status**: ✅ resolved as "informs Sprint 1+ planning; planned additions
already in PLAN.md cover the gaps"
