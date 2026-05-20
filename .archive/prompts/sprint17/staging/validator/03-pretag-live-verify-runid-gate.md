---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: pre-tag gate — refuse to cut a release tag without a recorded live-verify run-id for every high-severity fix in the cut'
labels: []
assignees: ''
---

## Motivation

The `live-verify-high-issues` lesson (memory note, Sprint 16) was
*paid for in cash and time* during the `v1.6.2` cycle:
`issues/issue_sprint16_validator.md` Issue 2 round-1 passed the
hermetic gate green, was claimed `### Fixed` in the CHANGELOG, and
was caught broken by the integrator's first live `!` verify
(run-id `20260519-181511`); the `### Fixed` claim had to be reverted
and the issue reopened. The round-3 fix on Issue 3 (the
`terraform.applied.tfvars` replay path) repeated the same pattern —
hermetic-GREEN round-2 caught broken by live verify
`20260519-220236`, fixed again, verified GREEN as
`20260519-234554`.

The discipline today is fully human: the integrator's memory note
says "high-severity issues need a live `!` verify, not just
hermetic green"; nothing mechanical prevents the next tag from going
out with a `### Fixed` claim that has only hermetic evidence behind
it. The `Release` workflow (`.github/workflows/release.yml`) triggers
on `git push origin v*.*.*` and runs goreleaser unconditionally —
there is no pre-tag check that the CHANGELOG entries for the tag
have a live-verify run-id stamped against each high-severity item.
A re-run of the round-1 mistake would ship cleanly.

A small mechanical gate would close that loop: the CHANGELOG section
for the tag being cut must, for every `### Fixed` bullet linked to a
high-severity issue, carry a `live-verified run-id `<YYYYMMDD-HHMMSS>``
fragment (the shape the integrator already uses in every recent
closure — see `issues/issue_sprint16_validator.md` Issue 2 GREEN
closure 2026-05-19). The check is grep-shaped and runs both locally
(`make release-precheck`) and as the first step of
`release.yml` (before goreleaser).

## Proposed surface

No new `roksbnkctl` verb — this is release-cycle tooling. Two new
artefacts:

```
# Local pre-tag check (the integrator runs this before `git tag vX.Y.Z`):
make release-precheck TAG=v1.7.0
# Exits 0 if every high-sev issue named in the v1.7.0 CHANGELOG section
# has a run-id stamp; non-zero with the offending lines otherwise.
```

- New script `scripts/release-precheck.sh` — Bash, `set -euo
  pipefail`, mirrors the style of the existing `scripts/pre-commit.sh`.
- New `make release-precheck` target wiring the script with `TAG=`
  defaulted to the most-recent `v*.*.*` annotated tag.
- New first step in `.github/workflows/release.yml` (before the
  goreleaser step) runs the same script with `TAG=${{ github.ref_name }}`.
  Tag push fails the workflow before goreleaser publishes anything.
- The script's contract: read the `CHANGELOG.md` section between
  `## $TAG —` and the next `## ` heading; for every bullet under
  `### Fixed` (and `### Changed` if it references an `issues/`
  high-severity item), require either
  (a) an inline `live-verified run-id `<YYYYMMDD-HHMMSS>`` token, or
  (b) an inline `(hermetic-only — severity: <low|medium>)` exemption,
  or (c) a link to an `issues/issue_sprint*_*.md` Issue whose body
  carries the run-id. Any high-sev bullet missing all three is the
  failure.

## Behavior

- **Happy path (the `v1.6.2` cut as it landed):** CHANGELOG bullets
  for Issue 2 and Issue 3 both reference run-ids
  (`20260519-202202`, `20260519-234554`, `20260520-035616`). The
  check exits 0.
- **Regression simulation (the round-1 mistake):** a CHANGELOG
  `### Fixed` bullet that claims "second-phase handoff fixed" with
  no run-id and no exemption. The check exits non-zero, names the
  offending bullet by line number, and prints:
  `bullet at CHANGELOG.md:23 looks like a high-sev fix
  (issues/issue_sprint16_validator.md Issue 2) but carries no
  live-verify run-id. Add `live-verified run-id `<YYYYMMDD-HHMMSS>``
  or mark `(hermetic-only — severity: <low|medium>)` if appropriate.`
- **Low/medium-severity fix:** the bullet may carry the exemption
  fragment; the check passes. The exemption is grep-visible so
  reviewers can challenge it.
- **No `### Fixed` block:** the check exits 0 — a release that
  only changes `### Changed` (e.g. the Sprint 16 phase-1b refactor
  cut) needs no run-ids.
- **Missing tag section in CHANGELOG:** the check exits non-zero —
  "CHANGELOG has no section for $TAG; release-precheck cannot
  proceed". (Same shape as the existing CHANGELOG-completeness
  expectations.)
- **Interaction with existing global flags:** none — release tooling,
  not the binary.
- **Side-effects on filesystem / IBM Cloud account:** none — read-only
  parse of `CHANGELOG.md` and the `issues/` files.

## Acceptance criteria

1. New file `scripts/release-precheck.sh` exists, is `chmod +x`, is
   shellcheck-clean, and is `bash -n`-clean. Reads `TAG` from `$1`
   or `$TAG` env var; errors clearly on missing input.
2. New `Makefile` target `release-precheck` wires the script. Running
   `make release-precheck TAG=v1.6.2` against the current `main`
   exits 0 — every Issue 2 / Issue 3 / Issue 4 bullet in the v1.6.2
   CHANGELOG section already carries a run-id reference.
3. A deliberately-broken CHANGELOG entry on a throwaway branch (a
   `### Fixed` bullet referencing
   `issues/issue_sprint16_validator.md` Issue 2 with the run-id
   stamp stripped) makes `make release-precheck TAG=v1.6.2` exit
   non-zero with a message that names the offending line.
4. `.github/workflows/release.yml` gains a `release-precheck` step
   before the `goreleaser` step. Tag push of a tag whose CHANGELOG
   section fails the check **does not publish goreleaser artefacts**
   — the workflow fails red at the precheck step.
5. The check's grep regex tolerates the run-id formats already in
   use in the repo: `run-id `20260519-202202``,
   `run-id 20260520-035616`, and `(`20260519-234554`)` inside
   parens. Add a fixture test under
   `scripts/release-precheck_test.sh` (or a Go-fixture under
   `internal/test/release_precheck_fixture_test.go`) that pins the
   regex against at least these three real-world shapes.
6. A new short subsection in `docs/PLAN.md` §"Release process" (or
   wherever the release-cut sequence is documented) calls out the
   precheck as step 0, before `git tag`. The `live-verify-high-issues`
   memory note's prose discipline now has a mechanical backstop —
   both stay in force.
7. Regression: the round-1 mistake from the `v1.6.2` cycle — a
   `### Fixed` claim with hermetic-only evidence — would have been
   caught by this gate at `git push origin v1.6.2` time, before
   goreleaser ran.

## Out of scope (deliberately)

- Validating that the run-id *actually exists* / corresponds to a
  green `e2e-phase-handoff.sh` log on disk. That's a separate
  evidence-chain feature and would require log archival; this issue
  is just the syntactic gate (the run-id appears in the prose).
- Auto-classifying severity. The script trusts the
  `issues/issue_sprint*_*.md` ledger's `**Severity**: high` line —
  if a future contributor mislabels a high-sev issue as medium, this
  gate doesn't save them. The `no-piling-into-active-release` gate
  filed separately catches a different facet.
- Gating PR merges on this. The gate fires at tag-push time; a PR
  that lands a `### Fixed` bullet without a run-id is fine *until*
  the integrator tries to cut a tag — the run-id can be appended
  post-merge, pre-tag.
- Editing `CHANGELOG.md` itself in this issue's scope. The current
  `v1.6.2` section is already compliant; this is purely about
  catching future divergence.

## Files likely touched

- `scripts/release-precheck.sh` — new, ~60 lines bash.
- `scripts/release-precheck_test.sh` — new, regex/fixture test (or
  the Go alternative noted in criterion 5).
- `Makefile` — add `release-precheck` target, ~5 lines.
- `.github/workflows/release.yml` — insert `release-precheck` step
  before the `goreleaser` step; ~10 lines of YAML.
- `docs/PLAN.md` — short subsection under the release-process
  description naming the precheck as step 0.

## Notes

- The shape `live-verified run-id `<YYYYMMDD-HHMMSS>`` is already
  the integrator's de-facto convention — see Issue 2 GREEN closure
  (`20260519-202202`), Issue 3 round-3 closure (`20260519-234554`),
  Issue 3 option (b) closure (`20260520-035616`). This issue
  formalises that convention into a CI gate. No new vocabulary.
- The memory-note discipline (`live-verify-high-issues`) stays;
  this is a backstop, not a replacement. A future cut where the
  integrator forgets to run the live verify is caught here at
  tag-push time — the gate's failure message is the prompt to go
  run it.
- This issue's companion is `06-pretag-no-piling-checklist.md` (the
  `no-piling-into-active-release` mechanical backstop); the two
  together close both Sprint 16 lessons.
