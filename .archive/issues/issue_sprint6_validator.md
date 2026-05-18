# Sprint 6 — validator issue log

Sprint 6 is the **testing sprint** and the final pre-`v1.0` gate. Issues
filed by the validator agent during dispatch — the integrator triages
and resolves at integration time; resolutions land in
`resolved_sprint6_validator.md`.

Format matches prior sprints. `Severity: roadmap` is reserved for non-
blocking forward-looking observations; `low/medium/high/blocker` for
actionable findings.

## Issue 1 (LOW — DRY_RUN walkthroughs not executable in validator sandbox)

**Severity**: low (sandboxing artefact, not a code defect)

**Status**: filed; integrator verification step

**Description**: The Sprint 6 deliverables include the verification
step "`DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases (K,
L, L-DNS, M, **I**, **N**) cleanly" and "`DRY_RUN=1
./scripts/e2e-test-full.sh` shows the chained driver-of-drivers flow
cleanly". Neither could be run at validator-dispatch time — the agent
sandbox actively denies any `bash`, `sh`, `python3`, or `env`
invocation that would execute the scripts (this is a tightening from
Sprint 5's sandbox; even read-only `bash -n` is denied).

The static checks I COULD run are green:

- `go build ./...` clean
- `go test -race ./...` clean (incl. new `TestProbe_TruncatedFlag`)
- `gofmt -d -l .` clean
- `go vet ./...` clean
- The YAML workflow file parses cleanly via an in-process `gopkg.in/yaml.v3`
  unmarshal (verified via a throwaway tool removed before commit)

The integrator should run:

```bash
DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true \
  ROKSBNKCTL_E2E_SSH_TARGET=jumphost \
  ./scripts/e2e-test-backends.sh
```

and confirm the phase headers appear in order: **I** → K → L → L-DNS
→ M → **N**. With `ROKSBNKCTL_E2E_SSH_TARGET=jumphost`, Phase I
should render in dry-run mode (the `SSH_READY=dry` short-circuit
covers the `targets show` preflight).

Then:

```bash
DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true \
  ./scripts/e2e-test-full.sh
```

should render `═══ baseline driver — Phases A-H ═══` followed by
`═══ backends driver — Phases I + K + L + L-DNS + M + N ═══`.

**Files affected**: `scripts/e2e-test-backends.sh`, `scripts/e2e-test-full.sh`
(both static — `bash -n` syntactic structure verified by manual review)

**Proposed fix**: integrator runs the two DRY_RUN walkthroughs at
integration time and confirms phase ordering.

## Issue 2 (LOW — Phase I context-cancel uses sleep-poll, not `wait -t`)

**Severity**: low (cosmetic; works correctly today)

**Status**: filed as roadmap

**Description**: `scripts/e2e-test-backends.sh::phase_I` step I10
verifies SIGINT cleanup within 5s. The implementation uses a
`while kill -0 "$pid" 2>/dev/null && [[ "$waited" -lt 6 ]]; do
sleep 1; done` polling loop because Bash's `wait -t <secs> <pid>`
form is only available in Bash 5.2+. The github-hosted ubuntu-latest
runner ships Bash 5.1; switching to `wait -t` would break the
release-branch CI workflow.

The polling loop is correct; it just has ~1s granularity. The 5s
budget has 4-5s of headroom either way.

**Files affected**: `scripts/e2e-test-backends.sh:phase_I:I10`

**Proposed fix**: when the github-hosted runner image picks up Bash
5.2 (anticipated by GA Ubuntu 24.10 cycle), refactor to `wait -t`.
Roadmap only.

## Issue 3 (LOW — Phase I9 repo-unreachable always yellow-⊘)

**Severity**: low (documented PRD 05 §I as manual)

**Status**: filed; intentional design

**Description**: Phase I step I9 ("repo-unreachable failure") is
always yellow-⊘'d by the driver. The test requires mutating the
remote target's network (severing DNS to apt repositories, or
flipping `/etc/apt/sources.list`) — this is too destructive to
automate inside the e2e driver because the mutation persists across
test runs.

PRD 05 §I documents I9 as a manual verification step; the driver
emits a yellow-⊘ with a pointer to the manual procedure. The
integrator's `v1.0` sign-off should include a manual I9 run against
a purpose-built SSH target where the remote's apt sources can be
toggled without consequence.

**Files affected**: `scripts/e2e-test-backends.sh:phase_I:I9`

**Proposed fix**: documented as manual; consider a `tools/manual-tests/`
directory in v1.x with a step-by-step recipe for the integrator.

## Issue 4 (LOW — `e2e-test-full.sh` runs baseline A-H sequentially, not interleaved with backends)

**Severity**: low (design tradeoff; documented in script)

**Status**: filed; intentional design

**Description**: PRD 05 §"Test infrastructure" envisions the backends
driver running *between* the baseline's Phase D apply and its final
Phase H teardown, sharing one cluster apply across both drivers. The
Sprint 6 combined runner doesn't achieve that — instead it runs:

1. The baseline driver A-H to completion (which destroys the cluster
   in Phase D's D8 + removes the workspace in Phase H).
2. The backends driver I + K + L + L-DNS + M + N, which then needs
   to provision its own cluster via Phase N's N1 (the "mixed-mode
   lifecycle" step).

The total wall-time is ~4-6h (vs. the PRD's envisioned ~3-4h with
cluster-sharing). The benefit of the simpler chained design: the two
drivers remain decoupled — each can be run standalone, and a failure
in one doesn't entangle the cluster state of the other.

The PRD-envisioned design would require either (a) splitting the
baseline driver's Phase H out into a `phase_H_only.sh` invoked by
the combined runner after the backends driver, OR (b) adding a
`SKIP_TEARDOWN=1` env-var to the baseline driver that omits D8 + H,
deferring teardown to the combined runner.

**Files affected**: `scripts/e2e-test-full.sh`, `scripts/e2e-test.sh`
(if option (b) is preferred)

**Proposed fix**: keep the current design for v1.0 — it's simpler and
the wall-time hit is bounded. Revisit in v1.x if CI minute budgets
become a concern.

## Issue 5 (LOW — `e2e-full.yml` workflow assumes E2E_TFVARS_CONTENT secret exists)

**Severity**: low (workflow doesn't fail if secret is empty)

**Status**: filed; integrator setup step

**Description**: `.github/workflows/e2e-full.yml` reads
`secrets.E2E_TFVARS_CONTENT` to append per-account terraform inputs
(cluster name, resource group, COS instance names) to the rendered
terraform.tfvars. If the secret isn't set in the repo settings, the
workflow will run with only `ibmcloud_api_key` + `region` in the
tfvars — terraform will fail with "Missing required argument" for
the cluster name + RG.

The workflow doesn't error on the secret being unset (the
`[ -n "${E2E_TFVARS_CONTENT}" ]` guard short-circuits), so the
failure surfaces at `roksbnkctl up` time rather than at workflow
preflight.

**Files affected**: `.github/workflows/e2e-full.yml`

**Proposed fix**: integrator pre-flight checklist (before triggering
`Full E2E` for the first time):

1. Set `IBMCLOUD_API_KEY` secret in repo settings.
2. Set `E2E_TFVARS_CONTENT` secret with the full
   `~/bnkfun/terraform.tfvars` contents (minus the `ibmcloud_api_key`
   line — that's rendered separately).
3. Optionally set `E2E_SSH_TARGET`, `E2E_SSH_NON_UBUNTU`,
   `E2E_SSH_NO_NOPASSWD` for Phase I + M5/M6 + N3 SSH coverage.

A future polish pass (v1.1?) could add an early step that fails the
workflow if the required secrets are unset, with a clear error
message pointing at this issue's setup checklist.

## Issue 6 (ROADMAP — `Truncated` not user-facing yet)

**Severity**: roadmap (carry-over from Sprint 5 validator Issue 4)

**Status**: resolved at validator-run time

**Description**: Sprint 5 validator Issue 4 noted that
`TestProbe_TruncatedFlag` was dropped from coverage because the staff
impl auto-retries truncated UDP responses over TCP. The integrator's
resolution note suggested revisiting in v1.x "if `Truncated` becomes
user-facing via a CLI flag."

Sprint 6 closes the test-coverage half of that issue: the new
`TestProbe_TruncatedFlag` in `internal/test/dns_test.go` uses a
dual-stack mock server (both UDP + TCP listeners on the same port,
both returning TC=1) so the truncated flag projects through the
TCP retry and lands `result.Truncated == true`.

The "user-facing CLI flag" half remains a v1.x consideration — no
flag exists today that distinguishes truncated answers in user-facing
output. The JSON `roksbnkctl.dns.v1.vantage` schema already carries
`"truncated": bool`; consumers reading the JSON get the signal.

**Status**: ✅ test-coverage half closed; flag-surface half remains
roadmap.

## Issue 7 (ROADMAP — Phase N5 cross-backend down may fail if state was written by docker bind-mount with different UID)

**Severity**: roadmap (well-known docker terraform-backend UID gotcha)

**Status**: filed for awareness

**Description**: Phase N step N5 tears down via a DIFFERENT backend
than N1's up (the point of the step is to validate state-file
portability). If N1 used `--backend docker` and the docker terraform
container ran as root (the upstream image's default), the `.tfstate`
on the host bind-mount lands root-owned. A subsequent `--backend
local` invocation from the host user would fail with EACCES on the
state-lock file.

The Sprint 5 staff `tools/docker/terraform/Dockerfile` runs as
`--user $(id -u):$(id -g)` to avoid this — but if any future change
flips that, Phase N5 surfaces the regression immediately.

This isn't a Sprint 6 blocker (the driver doesn't run live without
the integrator's manual trigger), but it's the kind of issue that
the v1.0 sign-off integrator should know to watch for in the run log.

**Files affected**: `scripts/e2e-test-backends.sh:phase_N:N5`,
`tools/docker/terraform/Dockerfile` (the cross-cutting concern)

**Proposed fix**: documented as a v1.0 sign-off watchpoint; no code
change in Sprint 6.

## Summary

7 issues filed: 5 LOW, 2 ROADMAP. None are v1.0 release-blockers.

The integrator's Sprint 6 closing tasks:

1. Run the two DRY_RUN walkthroughs (Issue 1) and confirm phase
   ordering.
2. Set up the `E2E_TFVARS_CONTENT` secret before the first `Full
   E2E` workflow run (Issue 5).
3. The `v1.0` manual sign-off run should set
   `ROKSBNKCTL_E2E_SSH_TARGET` (and ideally the NON_UBUNTU +
   NO_NOPASSWD purpose-built targets for full I7-I8 coverage).

All static checks (build, vet, fmt, test) are green at validator-run
time including the new `TestProbe_TruncatedFlag` carried over from
Sprint 5.
