# Sprint 8 — validator issues (resolved)

## Issue 1: pre-existing `internal/exec/` WIP fails Sprint 8 gate
**Status**: deferred to user
**Resolution**: see `resolved_sprint8_staff.md` Issue 1. Not Sprint 8 surface; the modifications were on the working tree at sprint kickoff and pre-date the sprint. The Sprint 8 integration commit covers Sprint 8 surface only; the exec WIP stays uncommitted on main for the user to triage separately (likely a `v1.0.3` patch release, per validator's preferred route). Sprint 8 gate "Sprint 8 code is `go build/test/vet/gofmt` green" is met on the Sprint 8 surface; the gate criterion against the *whole tree* is blocked by carry-in WIP, not Sprint 8 work.

## Issue 2: em-dash anchor drift on chapter 11 `Refusal messages` heading
**Status**: resolved — heading renamed per validator's recommendation
**Resolution**: applied the proposed fix verbatim:
- `book/src/11-tearing-down.md:128` heading changed from `## Refusal messages — catalogue` → `## Refusal messages catalogue`. mdbook slugifier now emits anchor `refusal-messages-catalogue` (single hyphen), matching all 5 references.
- Cross-ref display text in chapter 11 lines 26 / 69 / 196 + CHANGELOG line 248 updated to drop the em-dash for consistency with the new heading title. Chapter 10 line 272 already used a shorter display label (`§"Refusal messages"`) and needed no edit.

Other em-dash headings (`### Worked example — iterating on a BNK trial`, `## The `bnk up` / `bnk down` command group`) left alone — their references correctly use the double-hyphen anchor form per validator's audit.

## Issue 3: `pwd` confirmation
**Status**: resolved — process-hygiene note only; closed during sprint
**Resolution**: as filed.

## Issue 4: optional e2e phase deferred
**Status**: deferred to Sprint 9 (or post-v1.1.0 soak)
**Resolution**: as filed. The phase-split lifecycle cycle (`cluster up` → `bnk up` → `bnk down` → `bnk up` → `cluster down`) is not added to `scripts/e2e-test.sh` this sprint. The composite dispatcher is identity-preserving by construction (leaf helpers are the v1.0.x bodies factored out), so the risk profile of cross-cycle cluster persistence is the same as v1.0.x's risk profile of trial-state directory persistence — a filesystem concern, not a refactor concern. Track on the post-v1.0 deferral list in PLAN.md if the soak-test work picks up before Sprint 9.

## Issue 5: live refusal verification evidence
**Status**: resolved — informational log only
**Resolution**: all six refusals verified verbatim against PRD 06 §"Refusal messages"; legacy-single-state `down` prompt copy byte-for-byte identical to v1.0.x. Migration-cost contract holds. mdbook HTML build clean.
