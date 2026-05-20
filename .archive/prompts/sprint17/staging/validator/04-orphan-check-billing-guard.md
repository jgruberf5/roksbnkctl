---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: `scripts/orphan-check.sh` — pre-tag / pre-commit guard against stranded IBM Cloud billable infra'
labels: []
assignees: ''
---

## Motivation

The Sprint 16 Issue 2 round-1 live verify (run-id `20260519-181511`)
stranded a full ROKS cluster + two VPCs + the transit gateway after
the `e2e-phase-handoff.sh` driver's `teardown()` ran only the trial
`down` and not `cluster down` — see
`issues/issue_sprint16_validator.md` Issue 4. The integrator caught
it because the `state-cluster` state file happened to survive the
RED run; if it hadn't, the cluster would have billed until someone
noticed (it was caught the same evening, but the failure mode is
exactly the silent-billing-leak this project should refuse to ship
near).

The fix for Issue 4 patched that one driver's `teardown()` to also
run `cluster down` and to grep the live IBM Cloud account for
`canada-*` residue (the run's GREEN closure 2026-05-19: "Live recheck
post-teardown (cluster/VPCs/TGW/COS) | 0 / 0 / 0 / 0"). But the
**routine** guard — a single command an operator can run before
committing, before tagging, or as a periodic safety check — does not
exist. The same residue check is hand-rolled inline in
`e2e-phase-handoff.sh` and is not reusable from any other driver,
not invoked by `pre-commit.sh`, not invoked by the integrator's
release-cut sequence.

A small extraction makes the residue check a first-class repo
utility, and the existing drivers shrink to one-liners that delegate
to it. The Sprint 16 stranded-cluster class becomes "the script
would have screamed at the next commit" rather than "the integrator
noticed before bedtime".

## Proposed surface

New script `scripts/orphan-check.sh` — operator-run, requires
`IBMCLOUD_API_KEY` in env (the same contract `e2e-phase-handoff.sh`
already has). Read-only against the IBM Cloud account; no
mutations.

```
# Default — checks the prefix the integrator's standing test workspace uses:
IBMCLOUD_API_KEY=... ./scripts/orphan-check.sh

# Custom prefix — for a one-off workspace name like `canada-roks`, `e2e`, etc:
IBMCLOUD_API_KEY=... PREFIX=canada-roks ./scripts/orphan-check.sh

# Dry-run shape — show the `ibmcloud` queries that would run, without calling
# the API. Same DRY_RUN convention as the e2e drivers.
IBMCLOUD_API_KEY=... DRY_RUN=1 ./scripts/orphan-check.sh
```

- `PREFIX` env (default: `e2e`, configurable) — the workspace-name
  prefix to scan. The Sprint 16 incident used `canada-*`; the
  default-driver-workspace is `e2e`.
- `IBMCLOUD_API_KEY` env — required for live mode; absent → script
  refuses with a clear "set IBMCLOUD_API_KEY or DRY_RUN=1" error.
- `DRY_RUN` env — when `1`, prints the four `ibmcloud` / `ic` /
  underlying SDK queries it would make and exits 0. Mirrors the
  existing convention.
- Exit codes: `0` clean (0/0/0/0); `1` residue found (with the full
  list printed); `2` infrastructure error (API auth fail, network).

## Behavior

- **Happy path (clean account):** script prints
  `orphan-check $PREFIX: 0 clusters / 0 VPCs / 0 TGWs / 0 COS buckets`
  and exits 0. One line of output, suitable for shoving into a
  pre-tag checklist.
- **Residue found:** script prints one line per residue resource
  (id + name + region + age in hours, where derivable), then a
  summary line `orphan-check $PREFIX: N clusters / M VPCs / P TGWs
  / Q COS buckets — RESIDUE`, exits 1. Each line is grep-able and
  copy-paste-ready into `ibmcloud ... delete` or
  `roksbnkctl cluster down`.
- **Auth failure:** script prints
  `orphan-check: IBMCLOUD_API_KEY rejected (auth failed) — check the
  key or run with DRY_RUN=1` and exits 2 — distinct from "0 / 0 /
  0 / 0" so a misconfigured CI can't pass the gate green.
- **`DRY_RUN=1`:** script prints the four query commands it would
  issue, prints `(dry-run; no API calls made)`, exits 0. No API call
  occurs (assertable: a planted `IBMCLOUD_API_KEY=invalid` exits 0
  in dry-run, not 2).
- **No key sentinel leaks:** the script must not echo the value of
  `IBMCLOUD_API_KEY` to stdout/stderr/the log. Mirrors the
  e2e-phase-handoff.sh contract; the validator-side check used a
  planted-sentinel test, this script gets the same.
- **Interaction with existing global flags:** none — script-level
  utility, not a `roksbnkctl` subcommand.
- **Side-effects on filesystem / IBM Cloud account:** *none*.
  Read-only by design. The script does not delete anything — it
  reports. The operator decides what to do with the residue list.

## Acceptance criteria

1. New file `scripts/orphan-check.sh` exists, is `chmod +x`,
   `set -euo pipefail`, `bash -n`-clean, shellcheck-clean. Helper
   functions (`log`, `red`, `green`) mirror
   `scripts/e2e-phase-handoff.sh` so a contributor reading both
   recognises the style instantly.
2. `DRY_RUN=1 ./scripts/orphan-check.sh` (no real API key) prints
   the planned queries, makes zero network calls (assertable via
   `strace -e trace=connect` or by setting a bogus key and observing
   exit 0), exits 0.
3. With a valid `IBMCLOUD_API_KEY` against an account with zero
   `$PREFIX-*` residue, the script exits 0 and prints the one-line
   clean summary.
4. With a valid `IBMCLOUD_API_KEY` against an account where a stale
   `$PREFIX-*` VPC exists (operator simulates by leaving a tiny
   throwaway VPC), the script exits 1 and prints the VPC's id,
   name, region in the listing — the same shape Issue 4's GREEN
   closure showed (`canada-*` recheck = 0/0/0/0 was that script's
   inline version; this is the extracted reusable form).
5. `scripts/e2e-phase-handoff.sh`'s `teardown()` post-residue check
   is refactored to call `./scripts/orphan-check.sh` instead of
   inlining the same four queries (one-line delegation; the
   inline-grep stays as a fallback comment). Issue 2 GREEN re-runs
   with the same outcome.
6. The script does not echo `IBMCLOUD_API_KEY`'s value: a planted
   `IBMCLOUD_API_KEY=ORPHAN_CHECK_SENTINEL_$RANDOM` followed by a
   `grep ORPHAN_CHECK_SENTINEL_$RANDOM` of stdout/stderr/the run log
   returns zero matches.
7. The script is referenced from the `release-precheck` flow (the
   companion issue) as a recommended-but-optional step 0a: "run
   `orphan-check` against your test account before cutting a tag;
   a non-empty residue list means you have a billing leak to clean
   first".

## Out of scope (deliberately)

- Auto-cleanup. The script reports, the operator decides. A future
  `--auto-clean` flag is its own issue with its own live-verify
  story; this one is read-only.
- A `roksbnkctl orphan-check` subcommand. The shape is short enough
  to stay in `scripts/`; promoting it into the binary is a different
  trade-off (cross-platform, the SDK dependency, etc.) — file as a
  follow-up if operators want it.
- Cross-account scanning. The script uses whatever account
  `IBMCLOUD_API_KEY` authenticates to. Multi-account is the
  operator's loop, not this script's.
- Region selection. The script scans all enabled regions, matching
  the e2e drivers' behavior; per-region narrowing is a follow-up.
- Pre-commit hook integration. The hook (`scripts/pre-commit.sh`)
  runs offline-fast (~30s) and cannot afford an IBM Cloud API call;
  this script is operator-run / pre-tag, not pre-commit.

## Files likely touched

- `scripts/orphan-check.sh` — new, ~120 lines bash, the script itself.
- `scripts/e2e-phase-handoff.sh` — `teardown()` post-residue assertion
  refactored to call `./scripts/orphan-check.sh`.
- `docs/E2E_TEST.md` — short subsection naming the script under the
  existing `Phase-handoff regression (Issue 2)` block; one
  paragraph.
- `docs/PLAN.md` — one bullet under the release-cut sequence, "run
  `scripts/orphan-check.sh` against the test account before
  `git tag`".

## Notes

- This issue exists because the Sprint 16 incident
  (`issues/issue_sprint16_validator.md` Issue 4) revealed how easy
  it is to strand billable IBM Cloud infra on a failed e2e run.
  Issue 4 patched the one driver; this issue extracts the residue
  check so *any* future driver — or any future operator pre-tag
  ritual — can call it.
- The script does not need to be invoked by CI. It's an operator
  utility (it makes real IBM Cloud API calls; CI cannot afford
  that). The pre-tag checklist names it; the integrator's release
  ritual runs it.
- Pairs naturally with the `release-precheck.sh` gate (issue 03):
  one catches CHANGELOG-side discipline drift, this catches
  IBM-Cloud-side residue drift. Both are read-only, both run
  pre-tag.
