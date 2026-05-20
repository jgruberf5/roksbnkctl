# Sprint 19 — validator issues (tests for `init --var-file`)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Validator owns the hermetic test surface for staff Issue 1's
> `init --var-file` flag + the opt-in gated live-verify driver
> that closes the UX gap the integrator's manual testing exposed
> in v1.6.3.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Hermetic + gated-live tests for `init --var-file`

**Severity**: medium
**Status**: open

### Motivation

Staff Issue 1 adds `roksbnkctl init --var-file <path>`. The deliverable is the test surface that proves the staff feature meets its acceptance criteria — hermetically (the file lands where the lifecycle expects + `config.yaml` is seeded) and live (a real bare `roksbnkctl plan -w <ws>` after `init --var-file` succeeds without re-supplying the var-file, closing the gap manual testing of v1.6.3 exposed).

### Hermetic tests (additive, new file)

`internal/cli/init_var_file_test.go` covers:

- **(a) Happy path**: complete `terraform.tfvars`-shaped fixture file → post-init both `state/terraform.tfvars.user` AND `state-cluster/terraform.tfvars.user` exist, mode `0600`, byte-identical to input.
- **(b) Config seeding**: tfvars sets `ibmcloud_cluster_region = "us-south"`, `openshift_cluster_name = "test-cluster"`, etc. → `config.yaml` post-init reflects them (skipped interview prompts).
- **(c) Missing file**: `--var-file /nonexistent` → non-zero exit, error names the path.
- **(d) Malformed file**: a file that doesn't parse as tfvars → non-zero exit, message points at `terraform.tfvars.example`.
- **(e) Without `--var-file`**: existing init is byte-identical (no `terraform.tfvars.user` created).

Drives the `init` cobra command via its public surface (whatever non-interactive flag staff exposes for tests — likely `--auto` or similar); no reach into private functions. Uses `t.TempDir()` for the workspace root so the test is hermetic + parallel-safe.

### Gated live-verify driver (operator-run, NOT CI)

`scripts/e2e-init-var-file.sh` (+x) mirroring `scripts/e2e-cos-bucket-get.sh`'s style:

- `set -euo pipefail`, `redact()` over every echoed command, structured log under `$LOG_DIR/init-var-file-$RUN_TS.log`, `DRY_RUN=1` walk with zero cloud calls + zero key leaks.
- `WORKSPACE` defaults to `e2e-init-vf-$RUN_TS` (throwaway). `TFVARS` defaults to `./terraform.tfvars`.
- Steps:
  - **S1**: `roksbnkctl init -w "$WORKSPACE" --var-file "$TFVARS"` (non-interactive flag if needed).
  - **S2**: bare `roksbnkctl plan -w "$WORKSPACE"` (NO `--var-file`). Expected: succeeds because `HasUserTFVars()` picks up the seeded `terraform.tfvars.user`.
- Assertions:
  - **A1**: both `terraform.tfvars.user` copies exist + mode `0600`.
  - **A2**: `config.yaml` reflects the tfvars-seeded fields.
  - **A3**: bare `plan -w "$WORKSPACE"` exits 0; run log contains the existing `→ Layering user tfvars from <path>` stderr line.
  - **A4**: planted-sentinel API-key leak scan over the run log = 0 hits.
- EXIT-trap teardown: `roksbnkctl ws delete "$WORKSPACE" --force` + `rm -rf ~/.roksbnkctl/$WORKSPACE`. No cloud teardown (this driver never runs `up`).

### Acceptance criteria

1. `go test -race ./internal/cli/` green incl. new file; pre-existing `_test.go` byte-unchanged (`git diff --stat -- '*_test.go'` shows only the new file).
2. The five sub-cases (a)–(e) are each named for the AC they cover; `go test -v` output shows them.
3. `bash -n scripts/e2e-init-var-file.sh` clean; `DRY_RUN=1` walks every step with zero cloud calls and zero key leaks (sentinel scan = 0).
4. The live driver assertion A3 verifies the actual `HasUserTFVars()` codepath fires — captured by the stderr log line, NOT just a "did `plan` exit 0" proxy.

### Out of scope

- A full `up` → `down` cycle in the driver — the gap being closed is the `init`-to-first-`up` window, so the driver tests `init` → `plan` (no `up` needed). v1.7 work can add a full lifecycle e2e if useful.
- CI-side integration of the live driver — gated live-verify only, per `live-verify-high-issues`.

### Files affected

- `internal/cli/init_var_file_test.go` (new, additive).
- `scripts/e2e-init-var-file.sh` (new, +x).

### Related

- Staff Issue 1 — the code-side; this issue's tests prove it.
- Sprint 16 round-2 option (b) (`live-verify-high-issues`) — the actionable-error gate this work makes redundant for `init --var-file` users.
- `scripts/e2e-cos-bucket-get.sh` — the reference gated-live-verify shape this driver mirrors.
