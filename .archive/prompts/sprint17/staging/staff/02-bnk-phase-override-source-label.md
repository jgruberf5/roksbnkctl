---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: `terraform.applied.tfvars` section header for the Sprint 16 round-2 `bnk-phase-override.tfvars` falls through to the raw basename, breaking the human-friendly labelling sibling `cluster-phase-override.tfvars` gets'
labels: []
assignees: ''
---

## Symptom

The PRD 07 / Sprint 11 snapshot at
`~/.roksbnkctl/<workspace>/state/terraform.applied.tfvars` (trial phase,
written after a successful `up`'s second phase) renders the section
header for the Sprint 16 round-2 override file inconsistently with its
sibling. The cluster-phase override gets a human-friendly label:

```
# === from cluster-phase override ===
```

but the second/bnk-phase override is rendered with the raw basename:

```
# === from bnk-phase-override.tfvars ===
```

That divergence is a missed coherence step: `internal/config/applied_tfvars.go`'s
`sourceLabel()` switch (lines 298-310) hard-codes labels for
`terraform.tfvars`, `terraform.tfvars.user`, and
`cluster-phase-override.tfvars` only — the Sprint 16 v1.6.2 round-2
`bnk-phase-override.tfvars` was added to the apply pipeline but
`sourceLabel()` was not updated alongside.

Audit-grade tooling that greps the snapshot section headers (the
documented PRD 07 use case) sees the two architectural overrides
labelled inconsistently, and the Sprint 12 architect's
`internal/config/applied_tfvars.go::sourceLabel()` "hardcoded label set"
audit note (sprint12 architect Issue, §"Optional PRD 07 follow-up") no
longer reflects the as-landed shape.

## Reproduction

```
# 1. fresh workspace, apply both phases (so a second-phase override
#    actually gets written into terraform.applied.tfvars)
roksbnkctl ws use e2e-handoff
roksbnkctl --var-file ./terraform.tfvars up --auto

# 2. inspect the trial-phase snapshot section headers
grep '^# === from ' ~/.roksbnkctl/e2e-handoff/state/terraform.applied.tfvars

# 3. expected: every section header is a human-friendly label.
#    actual: bnk-phase-override.tfvars is the raw basename while its
#            sibling cluster-phase-override.tfvars is "cluster-phase override".
```

## Expected behavior

`sourceLabel()` returns a human-friendly string for
`bnk-phase-override.tfvars` symmetric with the cluster sibling — for
example `"bnk-phase override"`. The snapshot renders:

```
# === from bnk-phase override ===
```

The two architectural overrides now read identically in the snapshot.

## Actual behavior

`sourceLabel()` falls through to its `default:` arm
(`return filepath.Base(path)`) for `bnk-phase-override.tfvars` and the
section header is the raw basename. The rest of the snapshot (the
deduped assignments + the replay file in
`<state>/.applied-replay.tfvars`) is correct — this issue is exclusively
about the labelling.

## Environment

- `roksbnkctl version`: `v1.6.2` (the release that landed
  `bnk-phase-override.tfvars`)
- OS / arch: any — pure-Go label logic, no platform dependency
- IBM Cloud region: any (the second-phase override is written on every
  workspace that has a `cluster-outputs.json`)
- Backend: any (the snapshot writer runs after every successful apply
  regardless of backend)

## Suspect pipeline / hypotheses (optional)

1. Most likely: extend the `switch base` in
   `internal/config/applied_tfvars.go::sourceLabel()` (around line 300)
   with a `case "bnk-phase-override.tfvars": return "bnk-phase override"`
   arm, mirroring the existing `cluster-phase-override.tfvars` arm. One
   line, one matching test fixture update.

## Acceptance criteria

1. `internal/config/applied_tfvars.go::sourceLabel()` returns a
   human-friendly label string (the exact wording is integrator's
   choice — `"bnk-phase override"` is the natural symmetry with
   `"cluster-phase override"`) for input basename
   `bnk-phase-override.tfvars`.
2. After a `roksbnkctl up` against a workspace where both phases
   apply, the trial-phase `terraform.applied.tfvars` carries a
   `# === from <human label> ===` header (NOT the raw basename) for the
   bnk override.
3. Additive table-driven test in
   `internal/config/applied_tfvars_test.go` (NEW test function, NOT a
   pre-existing-test edit per the Sprint 16 parity rule) covering every
   basename `sourceLabel()` switches on — including the new
   `bnk-phase-override.tfvars` row, the existing
   `cluster-phase-override.tfvars` row, and the default-arm fallback for
   an unknown basename. The test asserts the four switch arms by name.
4. The PRD 07 user-facing description (book chapter 6 §"`terraform.applied.tfvars`
   — what's deployed right now", per CHANGELOG `v1.4.0`'s "Added"
   bullet) is reviewed for a stale list of section labels and updated
   in the same change so the documented section names match what
   roksbnkctl emits.

## Out of scope (deliberately)

- Changing the snapshot format / section ordering / dedupe behaviour —
  this is a label-only fix.
- Adding a configuration knob to override section labels —
  `sourceLabel()` stays hard-coded per PRD 07 §"Resolved design
  decisions" #4 (single literal list, no extension surface).
- Refactoring the snapshot writer or the round-3 replay derivation
  (`internal/orchestration/applied_replay.go`) — they are correct;
  this issue is purely cosmetic at the writer layer.
- Backfilling labels for any other tfvars file roksbnkctl may write in
  future cycles — scope this issue to the one missed file.

## Files likely touched

- `internal/config/applied_tfvars.go` — extend the `switch base` in
  `sourceLabel()` with one new arm.
- `internal/config/applied_tfvars_test.go` — additive new test
  function pinning every switch arm by basename.
- `book/src/06-workspaces.md` — review the §"`terraform.applied.tfvars`
  — what's deployed right now" section for any stale list of labels.

## Notes

The Sprint 16 round-2 `bnk-phase-override.tfvars` was deliberately
modelled on the existing live-proven `cluster-phase-override.tfvars`
mechanism (see `internal/orchestration/second_phase_reuse.go` lines
26-34). This issue closes the last missed coherence step: the snapshot
labelling sibling.

Related: `issues/issue_sprint12_architect.md` §"Optional PRD 07
follow-up" (the original audit note that called out `sourceLabel()`'s
hard-coded label set; this issue is the post-Sprint-16 update that the
sweep would now flag).
