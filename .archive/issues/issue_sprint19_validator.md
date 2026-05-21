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
**Status**: resolved

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

---

### Closure — validator deliverable shipped (Sprint 19, 2026-05-20)

**Status: open — pending live `!` verify** (integrator flips to `resolved` after the live `!` cycle GREENs).

**Files shipped:**

- `internal/cli/init_var_file_test.go` (new, +260 LOC additive — no edits to any pre-existing `_test.go`).
- `scripts/e2e-init-var-file.sh` (new, +x, mirrors `scripts/e2e-cos-bucket-get.sh`'s style).

**Sub-test → acceptance-criterion map:**

| Sub-test | AC | Hermetic? | Notes |
|---|---|---|---|
| `TestInitVarFile_HappyPath_BothCopiesLand` | AC1 (both `terraform.tfvars.user` copies, mode 0600, byte-identical) | skipped without live `IBMCLOUD_API_KEY` | staff's runInit calls `ibm.Verify()` BEFORE the var-file copy; positive path needs live creds → live driver covers it |
| `TestInitVarFile_ConfigSeeding` | AC2 (`config.yaml` reflects tfvars-seeded fields) | skipped without live `IBMCLOUD_API_KEY` | same gate as above; live driver A2 covers it |
| `TestInitVarFile_MissingFile_Fails` | AC3 (non-zero exit; error names path) | **yes** | `loadInitVarFile` pre-stats; trips before any network call |
| `TestInitVarFile_MalformedFile_Fails` | AC4 (non-zero exit; error references `terraform.tfvars.example`) | **yes** | binary blob → zero recognised assignments → staff's actionable-error branch fires |
| `TestInitVarFile_NoFlagByteIdentical` | AC5 (no `--var-file` → no `terraform.tfvars.user` created) | **yes** | filesystem-state assertion; tolerates the Verify failure that the no-creds run produces |

**Hermetic gate (today):** `go test -race -run TestInitVarFile ./internal/cli/` → 3 PASS + 2 SKIP (skip messages explicitly name the live driver as the path to GREEN on the positive cases). Full `go test -race ./internal/cli/` → PASS. `go build ./...` + `go vet ./...` + `gofmt -l internal/cli/init_var_file_test.go` clean. `bash -n scripts/e2e-init-var-file.sh` clean.

**DRY_RUN verification:** `DRY_RUN=1 TFVARS=./terraform/terraform.tfvars.example bash scripts/e2e-init-var-file.sh` walks preflight → S1 (init --var-file) → A1 → A2 → S2 (bare plan) → A3 → A4 with zero cloud calls and the planted-sentinel scan = 0 hits.

**Live driver invocation (integrator-run `!`):**

```
./scripts/e2e-init-var-file.sh
# (uses repo-root ./terraform.tfvars by default; never reads the api key into argv)
```

Asserts: S1 init exits 0 → A1 both `terraform.tfvars.user` copies land at mode 0600 with sha256 byte-equal to input → A2 `config.yaml` carries the tfvars-seeded `region` / `cluster name` / `openshift_version` / `workers_per_zone` fields → S2 bare `plan -w <ws>` exits 0 → **A3 the bare-plan stderr carries the literal `→ Layering user tfvars from` line** (pins the actual `HasUserTFVars()`-true codepath, NOT a "did `plan` exit 0" proxy) → A4 planted-sentinel + API-key-head scan over the run log = 0 hits → EXIT trap removes the throwaway workspace.

**Live-`!`-verify expected shape (per `live-verify-high-issues`):** integrator runs the driver on a real workspace; on GREEN, flips this issue + staff Issue 1 from `open` to `resolved` and tags the sprint for cut. On RED, the failing assertion is named in the driver's stderr and the integrator routes a follow-up.

---

### Closure round 2 — assertion shape corrected, 2026-05-21

**Status**: resolved (live `!` GREEN, run-id `20260521-031343`).

The round-1 live `!` cycle caught two independent defects (see staff ledger's round-2 closure for the full root-cause). Validator-side fallout:

1. **A1 path expectation was wrong.** This ledger said "both `state/terraform.tfvars.user` AND `state-cluster/terraform.tfvars.user`" — that's where staff dutifully wrote, and where the validator's A1 dutifully asserted. The actual canonical path is `<workspace-root>/terraform.tfvars.user` (sibling to `config.yaml`) — one copy, serving both phases. Same root cause as staff: ledger claim made without reading `tf.Workspace.UserTFVarsPath()`'s one-line body.
2. **Validator-shipped assertion grep was too strict.** `yaml.v3` quotes string values that look like floats (`openshift_version: "4.18"`), but A2's grep patterns required bare unquoted values. Widened the four field patterns to `field:[[:space:]]*"?VALUE"?` so both quoted and bare shapes match.

**Files updated**:

- `scripts/e2e-init-var-file.sh` — A1 now asserts a single workspace-root copy AND that the stale in-state-dir paths do NOT exist (so any regression to round-1's mis-located copies trips the test); A2 grep patterns tolerate yaml.v3's optional-quote shape; GREEN banner copy updated to "lands at workspace root".
- `internal/cli/init_var_file_test.go` — happy-path now asserts the workspace-root path + the absence of the stale in-state-dir copies; no-flag parity test extended to also assert no workspace-root copy (round-1 only checked the state dirs).

**Live `!` run-id 20260521-031343 — all-GREEN**:

```
✓ S1 init -w e2e-init-vf-20260521-031343 --var-file ./terraform.tfvars
✓ A1 workspace-root terraform.tfvars.user exists + mode 0600 + byte-identical
   (no stale state-dir copies)
✓ A2 config.yaml reflects the tfvars-seeded fields
✓ S2 bare plan -w <ws> exited 0 — no --var-file re-supplied
✓ A3 bare plan log carries the user-tfvars layering line (codepath confirmed)
✓ A4 leak scan: sentinel + API-key head both absent
✓ teardown: workspace removed
DRIVER_RC=0
```

A3's `→ Layering user tfvars from /home/jgruber/.roksbnkctl/<ws>/terraform.tfvars.user` line was emitted from `internal/orchestration/lifecycle.go`'s `writeAndInit` — the very codepath this driver was authored to pin — confirming the Sprint 19 deliverable closes the v1.6.3 UX gap end-to-end.

**Discovered gap (informational, NOT blocking):** staff's shipped `runInit` calls `ibm.Verify()` before the var-file copy step, so the AC1 + AC2 hermetic positive cases cannot reach the assertion surface without live creds. The live driver covers both AC1 and AC2 end-to-end, so this is not a closure blocker — but a v1.7 follow-up could expose a `--skip-verify` (or analogous test seam) to lift the two SKIPs to PASS in CI without re-organising staff's design.

**Discipline checks:** validator did NOT commit; validator did NOT run live infra (no `roksbnkctl init` or `plan` was executed against a real cloud account from this thread); validator did NOT touch any pre-existing `_test.go` (`git status --short | grep _test.go` shows only the two new `??` entries — the validator-shipped `init_var_file_test.go` and staff's sibling helpers test).
