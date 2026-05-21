You are the **validator** agent for Sprint 19 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint19/README.md` — integrator decisions.
2. `issues/issue_sprint19_validator.md` Issue 1 — authoritative
   spec.
3. `scripts/e2e-cos-bucket-get.sh` — the Sprint 18 gated-live-verify
   reference shape (redact pattern, EXIT-trap teardown, structured
   logging, A1–A5 assertion style).
4. `internal/cli/init.go` — read enough to know the public surface
   you need to drive (the cobra command + the `--var-file` flag
   staff is adding).

## Tasks

### Hermetic test (additive, new file, no edits to existing tests)

`internal/cli/init_var_file_test.go` — drives `init --var-file
<tempfile> -w <name> --auto` (use `--auto` or whatever flag the
init cmd exposes for non-interactive runs in tests; staff's
implementation may add `--auto` if not already there). Cases:

- **(a) Happy path**: a complete `terraform.tfvars`-shaped file is
  passed; assert post-init that
  `~/.roksbnkctl/<name>/state/terraform.tfvars.user` AND
  `~/.roksbnkctl/<name>/state-cluster/terraform.tfvars.user` both
  exist + mode `0600` + byte-identical to the input.
- **(b) Config seeding**: the tfvars sets
  `ibmcloud_cluster_region = "us-south"`,
  `openshift_cluster_name = "test-cluster"`, etc.; assert
  `config.yaml` post-init reflects those values (the interview
  questions were skipped for the seeded fields).
- **(c) Missing file**: `--var-file /nonexistent` exits non-zero
  with an actionable error naming the path.
- **(d) Malformed file**: a file that doesn't parse as tfvars exits
  non-zero with an error pointing the operator at
  `terraform.tfvars.example`.
- **(e) Without `--var-file`**: existing init behaviour is
  byte-identical (no `terraform.tfvars.user` file is created — the
  workspace stays as today).

### Gated live-verify driver (operator-run, NOT CI)

`scripts/e2e-init-var-file.sh` (+x) mirroring
`scripts/e2e-cos-bucket-get.sh`'s style:

- `set -euo pipefail`, `redact()` over every echoed command,
  structured log under `$LOG_DIR/init-var-file-$RUN_TS.log`,
  `DRY_RUN=1` walkthrough with zero cloud calls + zero key leaks
  (planted-sentinel scan).
- `WORKSPACE` defaults to `e2e-init-vf-$RUN_TS` (auto-suffixed; the
  driver creates a clean throwaway workspace).
- `TFVARS` defaults to `./terraform.tfvars` (the integrator's repo-
  root file). The driver references the file by path; the API key
  inside is **never read into argv or stdout** — same posture as
  the Sprint 18 driver.
- Steps:
  - S1: `roksbnkctl init -w "$WORKSPACE" --var-file "$TFVARS" --auto`
    (or however staff wires the non-interactive flag).
  - S2: bare `roksbnkctl plan -w "$WORKSPACE"` (NO `--var-file`).
    **Expected to succeed** because the `init` copied the tfvars
    to `state/terraform.tfvars.user` and the lifecycle's existing
    `HasUserTFVars()` path picks it up.
  - A1: both `terraform.tfvars.user` copies exist + mode `0600`.
  - A2: `config.yaml` reflects the tfvars-seeded fields.
  - A3: bare `plan -w "$WORKSPACE"` exits 0 + the run log shows
    the user-tfvars layering line (whatever stderr the lifecycle
    emits when `HasUserTFVars()` is true — probably the existing
    `→ Layering user tfvars from <path> …` line).
  - A4: planted-sentinel API-key leak scan.
- EXIT-trap teardown: `roksbnkctl ws delete "$WORKSPACE" --force`
  + `rm -rf ~/.roksbnkctl/$WORKSPACE` (no cloud teardown needed —
  `init` doesn't provision anything; this driver never runs
  `roksbnkctl up`).

## Constraints

- **Touch only**:
  - `internal/cli/init_var_file_test.go` (new, additive).
  - `scripts/e2e-init-var-file.sh` (new, +x).
- **No edits to any pre-existing `_test.go`** (parity discipline
  carries forward from Sprint 18).
- The live driver is **opt-in**, never a CI job. No `.github/workflows`
  edits.
- Do not commit. Integrator commits.
- Coordinate with staff on the test seam: drive the same `init`
  cobra command that staff exposes (the `--var-file` flag they
  add); don't reach into private functions.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/cli/` green incl. your new file.
- `bash -n scripts/e2e-init-var-file.sh` clean.
- `DRY_RUN=1 ./scripts/e2e-init-var-file.sh` walks every step with
  zero cloud calls + zero key leaks (sentinel scan = 0 hits).
- `git diff --stat -- '*_test.go'` shows ONLY the new file.

## Issue file

Append a **Closure** section to
`issues/issue_sprint19_validator.md` documenting: which sub-tests
cover which acceptance criteria, the live driver invocation, the
DRY_RUN verification, and the integrator's expected live-`!`-verify
shape.

## Final report

≤200 words: files added, sub-test → AC map, live driver
invocation, gate results, planted-sentinel leak-scan result, and
the explicit "this sprint's feature stays `open — pending live
`!` verify`" note (the integrator runs the live cycle to close).
State explicitly: did not commit, did not run live infra.
