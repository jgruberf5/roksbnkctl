---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: `roksbnkctl doctor --target <name>` renders the per-target check without the `[ssh]` backend prefix every other ssh check shows'
labels: []
assignees: ''
---

## Symptom

`roksbnkctl doctor --target jumphost` (and any other registered target)
prints the per-target `whoami` check line without the `[ssh]` backend
prefix that every other ssh-backed `doctor` check renders. Two `doctor`
lines that exercise the same ssh backend disagree on labelling — the
generic `target jumphost` row has no backend tag, while a `ssh:jumphost`
backend check elsewhere in the same `doctor` run is tagged `[ssh]`.
Cosmetic but it lies about which backend the check actually used.

The cause is a `TODO(phase3)` in
`internal/cli/meta.go:150`: `runTargetCheck` builds the
`doctor.Check{BackendName: ""}` with a comment "set 'ssh' once PRD 03
backend lands". PRD 03's ssh backend landed in `v0.9.0` (per CHANGELOG
§"`--backend ssh:<target>`"), so the TODO is stale — the literal `"ssh"`
is the correct value today.

## Reproduction

```
# 1. a workspace with at least one registered ssh target
roksbnkctl ws use canada-roks
roksbnkctl targets list             # shows "jumphost" (or similar)

# 2. run target-scoped doctor + a plain ssh-backend doctor in one shot
roksbnkctl doctor --target jumphost --backend ssh:jumphost

# 3. observe: the "target jumphost" line has no [ssh] prefix while the
#    other backend-resolved checks DO. Two rows about the same backend
#    label differently.
```

## Expected behavior

The `target jumphost` row should render with the `[ssh]` backend prefix
in `doctor`'s table output exactly the way every other ssh-backed check
does. Exit code stays 0/1 as today (semantic unchanged) — only the label
column changes.

## Actual behavior

The row has no backend prefix because
`internal/cli/meta.go:147-151`'s `runTargetCheck` builds the
`doctor.Check` with `BackendName: ""`. The PrintResults rendering then
suppresses the `[<backend>]` prefix for that single row.

## Environment

- `roksbnkctl version`: `v1.6.2` (and every prior release that shipped
  the per-target whoami check)
- OS / arch: Linux x86_64 (also affects macOS arm64 / Windows x86_64 —
  it's pure label logic)
- IBM Cloud region: n/a (label issue, no IBM Cloud call required)
- Backend: ssh:<target> (the bug is exclusively in the ssh-target check)

## Suspect pipeline / hypotheses (optional)

1. Most likely: the literal fix is `BackendName: "ssh"` on
   `internal/cli/meta.go:150`. The TODO names exactly the post-condition
   (PRD 03 backend landed) and the fix is one token; no logic change.
2. Sanity-check the rendering: `internal/doctor/print.go`'s
   `PrintResults` formats `[<BackendName>] <Name>` when `BackendName !=
   ""`; flipping the value will produce the prefix automatically.

## Acceptance criteria

1. `internal/cli/meta.go:150` sets `BackendName: "ssh"` (or, if a
   constant for the ssh backend name already exists in
   `internal/exec` / `internal/remote`, reference the constant rather
   than the literal — symmetric with the other backend-prefix sites).
2. The stale `TODO(phase3): set "ssh" once PRD 03 backend lands` comment
   is deleted at the same time (the postcondition is met; the comment is
   misleading on its own).
3. A unit test in `internal/cli/meta_test.go` (additive — new test
   function, NOT a pre-existing-test edit) builds a `runTargetCheck`
   result with a stubbed target and asserts `result.BackendName ==
   "ssh"`. Hermetic — no live ssh dial; use the same
   `remote.LoadTarget` fixture pattern existing tests use, or short-
   circuit on a `cctx == nil` / `ErrTargetNotFound` path that still
   exercises the Check construction.
4. A `doctor`-rendering snapshot test (additive) — or a one-shot
   golden-output assertion — confirms the rendered line carries the
   `[ssh]` prefix when `--target <name>` is the sole check. Pinning the
   rendered string keeps the label from silently regressing.

## Out of scope (deliberately)

- Re-shaping `doctor.Check` to carry a strongly-typed backend
  identifier — the existing `BackendName string` field is fine.
- Adding new doctor checks of any kind, or changing the per-target
  `whoami` check's behaviour. This is a label-only fix.
- Re-labelling the `--backend local` / `--backend docker` / `--backend
  k8s` doctor rows; they already carry their own backend prefixes and
  this issue is exclusively about the ssh-target row.
- Removing/changing the `errors.New("target ... not in targets")` /
  `ErrTargetNotFound` error path — distinct concern from the label.

## Files likely touched

- `internal/cli/meta.go` — the literal change (line 150) + delete the
  stale TODO comment above it.
- `internal/cli/meta_test.go` — additive unit test asserting
  `BackendName == "ssh"`.

## Notes

This is one of the last residual phase-3 follow-ups from PRD 03's ssh
backend cycle; the v0.9.0 work landed the backend itself but the
`doctor` adapter row's label was deferred behind the TODO and never
re-visited. Sprint 12-16 carries are unrelated (path/env chokepoint and
the cli decomposition); this single-token fix is independent of all
that.
