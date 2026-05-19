# Sprint 16 — validator resolution log

## Issue 2 — closure deliverables → **integrated** (Issue 2 stays open, live-`!`-gated)

**Disposition: accepted, integrated as-is.**

**What landed.**

- _Hermetic regression test_ —
  `internal/tf/secondphase_handoff_test.go` (new, additive). Pins the
  cross-agent seam `tf.RenderTFVarsWithClusterOutputs`: cluster
  outputs with a `VPCID` → second-phase tfvars carry
  `use_existing_cluster_vpc = true` + `existing_cluster_vpc_id` +
  `create_roks_transit_gateway = false` + `testing_create_client_vpc =
  false`; no outputs → byte-identical create-path parity; empty
  `VPCID` → defensive create path; also asserts no `api_key` leak in
  the rendered body. `go test -race ./internal/tf/` green within the
  full suite (exit 0).
- _Gated live-verify driver_ — `scripts/e2e-phase-handoff.sh` (new):
  operator-run via `!` only, `set -euo pipefail`, `DRY_RUN=1` support,
  EXIT-trap self-teardown, real `up` (cluster then bnk phase) + the
  four Issue-2 assertions, never echoes/logs the API key or
  `./terraform.tfvars` contents. No `.github/workflows` change.
- `docs/E2E_TEST.md` §"Phase-handoff regression (Issue 2)" added
  (operator runbook + GREEN criteria).

**Integrator checks.** `bash -n scripts/e2e-phase-handoff.sh` clean;
header correctly marks real-spend / opt-in / not-CI; `git diff --stat
-- '*_test.go'` shows only the new file (parity gate intact — Sprint
14/15 guards byte-unchanged & green in the `-race` run).

**Status: Issue 2 remains `open — pending live \`!\` verify`.** Per
`live-verify-high-issues` + README decision 3, the `high`-severity
Issue 2 is **not** closed on the hermetic GREEN. The fix + the
regression test + the live driver are integrated; the live `!` run on a
real account and the final flip to `resolved` are integrator/
operator-owned and not done in this dispatch. The validator correctly
did not mark it resolved.
