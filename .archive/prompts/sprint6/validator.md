You are the validator agent for Sprint 6 of the roksbnkctl project. Sprint 6 is **the testing sprint** — your scope is the bulk of the new code. You expand `scripts/e2e-test-backends.sh` with full **Phase I (SSH backend e2e), Phase M (automated cred audit), and Phase N (mixed-mode lifecycle)**, write a brand-new **`scripts/e2e-test-full.sh`** combined runner (A-H baseline + I-N + L-DNS, ~5-hour total), wire a **manual-trigger CI workflow** for the full driver, and add the missing **`TestProbe_TruncatedFlag`** test coverage from the Sprint 5 validator Issue 4 carry-over.

Sprint 7 cuts the **`v1.0` release tag** — your tests are the final gate signal.

Project location: `/mnt/d/project/roksbnkctl/`. Min Go: 1.25.

## Read first

- `docs/prd/05-E2E-TEST-PLAN.md` — the authoritative spec for every phase. Sprint 6 wires the previously-yellow-skipped Phase I + M (M5/M6) + N steps end-to-end. Read all of `§Phase I`, `§Phase M`, `§Phase N`.
- `docs/PLAN.md` Sprint 6 §"Test deliverables" — your acceptance criteria.
- `scripts/e2e-test-backends.sh` — Sprint 4/5's driver covering Phases K + L + L-DNS + M (subset). Sprint 6 expands to full Phase I + full Phase M (incl. M5/M6 SSH-side) + Phase N.
- `scripts/e2e-test.sh` — the baseline A-H driver that's existed since Sprint 0-1. Sprint 6's `e2e-test-full.sh` chains this driver's phases A-H, then `e2e-test-backends.sh` phases I-N + L-DNS, against the same cluster.
- `internal/test/dns_test.go` — Sprint 5 validator Issue 4 left `TestProbe_TruncatedFlag` uncovered because the staff impl auto-retries truncated UDP responses over TCP; Sprint 6 covers it via a TCP-only mock server (one of the two paths the issue proposed).
- `.github/workflows/ci.yml` — existing CI matrix + Sprint 1 integration + Sprint 3 docker-backend + Sprint 4 k8s-backend jobs. Sprint 6 adds a **manual-trigger** workflow (not a PR-gated job) for the full e2e runner.
- `prompts/sprint5/validator.md` for prompt-structure reference; `issues/resolved_sprint5_validator.md` for the explicit Sprint 6 carry-over (Issue 4 — TruncatedFlag) and the LD9 + M5/M6 Sprint-6-deferral notes.

## Coordinate with parallel agents

An architect agent is writing 9 reference / troubleshooting / glossary chapters (23, 25, 26, 27, 28, 29, 30, 31, 32) and reordering chapter 22's SCC section per Sprint 5 tech-writer Issue 14. A staff-engineer agent is writing the cobra-md + tfvars-md auto-generators (for the architect's chapter 27 + 29 outputs), refreshing the doctor command for green-by-default behaviour on a stock dev box (only `terraform` strictly required), writing `MIGRATING.md`, doing Phase N Go-side verification + small fix-ups, dropping `ENTRYPOINT ["ibmcloud"]` from `tools/docker/ibmcloud/Dockerfile` (Sprint 5 staff Issue 1 carry-over), and optionally surfacing `edns_client_subnet` in `DNSProbeResult`. **Do not touch their files.** You own all `*_test.go`, `scripts/e2e-test*.sh`, `.github/workflows/*.yml`, `cspell.json`, CONTRIBUTING.md additions, and `docs/E2E_TEST.md` updates.

## Tasks

### 1. Phase I — SSH backend full e2e (`scripts/e2e-test-backends.sh`)

PRD 05 §"Phase I" specifies the SSH backend test surface. Sprint 1 landed the binary's `--on jumphost` path; Sprint 4 landed the `--backend ssh:<target>` path. Sprint 6 wires the full e2e against a real SSH bastion.

The phase has 12 steps (I0-I11; check PRD 05 for the exact list). Add a `phase_i` function to `scripts/e2e-test-backends.sh` mirroring the existing Phase K / L / L-DNS / M shape:

```bash
phase_i() {
    local ssh_ready="$1"   # set by caller; whether an ssh:<target> is available
    if [[ "$ssh_ready" != yes ]]; then
        yellow "  ⊘ Phase I skipped — no ssh:<target> available (set ROKSBNKCTL_E2E_SSH_TARGET=<name> to enable)"
        return 0
    fi

    # I0: verify the SSH target is reachable (`roksbnkctl targets show $TARGET`)
    # I1: --on $TARGET ibmcloud iam oauth-tokens (Sprint 1 path)
    # I2: --backend ssh:$TARGET ibmcloud iam oauth-tokens (Sprint 4 path; tool already installed)
    # I3: --backend ssh:$TARGET --bootstrap iperf3 -v (apt-bootstrap path; idempotent on re-run)
    # I4: cred audit — `ssh $TARGET 'env | grep IBMCLOUD'` should NOT find the key value (the wrapper-script approach uses an env-file, sourced + traps-removed; the key isn't in process env outside the wrapper)
    # I5: wrapper-script cleanup — `ssh $TARGET ls /tmp/roksbnkctl.* 2>/dev/null` should be empty (trap-on-EXIT removed the tempdir)
    # I6: SetEnv silent-drop fallback (force-fallback via test target with AcceptEnv disabled; verify the wrapper-script path activates)
    # I7: non-Ubuntu detection (skip on Ubuntu target; only runs when ROKSBNKCTL_E2E_SSH_NON_UBUNTU=<target>)
    # I8: sudo-password-required failure (skip unless ROKSBNKCTL_E2E_SSH_NO_NOPASSWD=<target>)
    # I9: repo-unreachable failure (skip; requires network mutation)
    # I10: context-cancel (kill the SSH run mid-flight, verify cleanup happens within ~5s)
    # I11: SSH backend doctor (`roksbnkctl doctor --backend ssh:$TARGET` reports green)
}
```

The `ssh_ready` flag comes from a `preflight_ssh_target` check at the top of the driver that verifies `$ROKSBNKCTL_E2E_SSH_TARGET` is set and `roksbnkctl targets show $TARGET` succeeds. Steps I7-I9 are conditional on additional env vars so a single SSH target can satisfy the happy-path coverage; the failure-mode coverage needs purpose-built targets.

### 2. Phase M — automated cred audit (full implementation)

PRD 05 §"Phase M" lists M1-M7. Sprint 4 wired M1-M4 + M7 (no SSH targets available); Sprint 6 lands M5 + M6 now that Phase I exists, plus tightens M1-M4 with the actual implementation (Sprint 4 had stub `(dry-run)` markers in places).

For each cred-audit assertion, capture the test-time IBM Cloud API key value (a known-secret fixture, NOT the user's real key — use a dummy `e2e-audit-key-<random>` value set via `IBMCLOUD_API_KEY` env override). Run the wrapped operation. Then scan multiple inspection surfaces and assert the secret value never appears:

- **M1** — `docker history ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v>` after a `--backend docker` call: no key in any layer ENV.
- **M2** — `docker inspect <last-container>` after the same call: no key in `Config.Env` (the env is referenced, not valued).
- **M3** — `kubectl get events -n roksbnkctl-ops -o yaml` after a `--backend k8s` call: no key in any event field.
- **M4** — `kubectl logs <ops-pod> -n roksbnkctl-ops` after the same call: no key (the redactor wrapping should mask any leak).
- **M5** — `ssh $TARGET ls /tmp/roksbnkctl.* 2>/dev/null` after a `--backend ssh:$TARGET` call: empty (tempfiles cleaned up by trap).
- **M6** — `ssh $TARGET 'cat /var/log/auth.log | tail -50'` after the same call (requires `sudo` to read auth.log on most Ubuntu installs; skip-cleanly if the SSH user lacks the read): sshd shows `Accepted publickey`; if SetEnv was used, only the var name is logged, not the value.
- **M7** — `grep -rE '<known-secret>' ~/.roksbnkctl/*/state/*.log 2>/dev/null` after all phases: empty.

Each M-step fails the phase if any inspection surface contains the secret value; passes silently otherwise.

### 3. Phase N — mixed-mode lifecycle

PRD 05 §"Phase N" — verify backend transitions preserve state. Steps (PRD 05's N1-N6 or whatever it specifies; check):

- **N1**: `roksbnkctl up --backend local` (or `--backend docker` if local terraform not installed; ROKSBNKCTL_E2E_INIT_BACKEND env-keyed) — establishes initial state.
- **N2**: `roksbnkctl test throughput --backend k8s` — runs against the cluster brought up in N1. Asserts the throughput test sees the cluster from N1 (i.e., the kubeconfig and state are backend-independent).
- **N3**: `roksbnkctl ibmcloud --backend ssh:$TARGET ks cluster ls` — same cluster visible from the SSH target. Asserts the IBM Cloud API key resolved on the local host gets propagated to the SSH target's session correctly (no re-resolve loop).
- **N4**: `roksbnkctl test dns --backend k8s --gslb-compare` — multi-vantage probe across local + k8s.
- **N5**: `roksbnkctl down --backend docker` — tears down the cluster created with a different backend in N1. Asserts state-file compatibility across backends (the docker backend's bind-mount reads the .tfstate the local backend wrote in N1).
- **N6**: verify post-N5 state — `roksbnkctl ws show` reports the cluster as destroyed; no orphan resources in IBM Cloud (manual check; the e2e driver can list what should be empty and assert empty).

Skip-cleanly when prerequisites are missing (no SSH target → skip N3; no kind cluster → skip N2 + N5).

### 4. `scripts/e2e-test-full.sh` — combined runner

A new sibling driver that chains `scripts/e2e-test.sh` (baseline Phases A-H) followed by `scripts/e2e-test-backends.sh` (Phases I-N + L-DNS) against the same cluster. Shape:

```bash
#!/usr/bin/env bash
# scripts/e2e-test-full.sh — combined A-H + I-N + L-DNS e2e runner.
# ~4-6 hour wall time; intended for release branch + manual-trigger CI.
# See docs/E2E_TEST.md §"Full e2e (e2e-test-full.sh)" for env vars + cost.

set -euo pipefail

# 1. preflight (same env-var set as both driver scripts)
# 2. run scripts/e2e-test.sh (A-H baseline — brings the cluster up)
# 3. run scripts/e2e-test-backends.sh PHASE_FROM=I (resumes at I, reuses A-H's cluster)
# 4. on failure, log which phase, leave the cluster up for inspection
# 5. on success, optionally tear down (--teardown flag); default leave-up
```

Source the same `lib/` helpers; share the run-id and log file across the two drivers. PRD 05 §"Re-runnability" specifies `PHASE_FROM=<phase>` semantics; the combined driver respects that, so re-running after a partial failure resumes at the failed phase.

### 5. Manual-trigger CI workflow — `.github/workflows/e2e-full.yml`

PLAN.md §"Sprint 6 Risks" line 488 says "5 hours is too long for a PR check. Solution: gate v1.0 release branch on full e2e; PR checks run only the unit + integration tiers."

The new workflow:

```yaml
name: Full E2E
on:
  workflow_dispatch:
    inputs:
      cluster_region:
        description: 'IBM Cloud region for the test cluster'
        required: true
        default: 'us-south'
      teardown_on_success:
        description: 'Tear down the cluster after a green run'
        required: false
        default: 'true'
        type: boolean
  push:
    branches: [release/**]   # release-branch pushes run the full e2e

jobs:
  full-e2e:
    runs-on: ubuntu-latest
    timeout-minutes: 360   # 6 hours
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version-file: 'go.mod' }
      - run: go build -o roksbnkctl ./cmd/roksbnkctl
      - run: |
          export IBMCLOUD_API_KEY=${{ secrets.IBMCLOUD_API_KEY }}
          export ROKSBNKCTL=$PWD/roksbnkctl
          ./scripts/e2e-test-full.sh
```

Skip the job when secrets aren't available (forked-PR safety per Sprint 3 pattern).

### 6. `TestProbe_TruncatedFlag` coverage (Sprint 5 validator Issue 4 carry-over)

Sprint 5 left this uncovered because the staff impl auto-retries truncated UDP responses over TCP. Sprint 6 covers it via a TCP-only mock server:

```go
func TestProbe_TruncatedFlag(t *testing.T) {
    // Start a TCP-only DNS server that returns TC=1 in its single response.
    // The Probe should fall back to TCP, observe the TC=1 in the TCP response
    // (since the server can't fit the answer even in TCP), and report
    // Truncated=true in the result.
    //
    // PRD 03 §"Truncated + authoritative flags": both AA=1 and TC=1 must
    // surface in the JSON output.
    ...
}
```

Use `miekg/dns`'s server library (same fixture shape as the existing unit tests, just `Net: "tcp"` instead of `"udp"`). Assert `result.Truncated == true` post-probe.

### 7. Sprint 5 polish: update `docs/E2E_TEST.md`'s release-checklist with Sprint 6 deliverables

Sprint 5 wrote a "v0.9 release checklist" in `docs/E2E_TEST.md`. Sprint 6 adds:

- An updated "v0.9 release checklist" → "Per-release checklist" section (since v1.0 is the next gate; the checklist is now permanent reference, not version-specific).
- A new "Full e2e (`scripts/e2e-test-full.sh`)" section documenting the combined runner's env vars, expected duration, and cost.
- A "Phase I + Phase M + Phase N coverage notes" section explaining what each phase asserts + what prerequisites it needs.

### 8. cspell + CONTRIBUTING additions

cspell.json: add Sprint 6 vocabulary as you encounter (terms surfacing in chapters 23-32 + the new e2e phases). At minimum: `multipart`, `streaming`, `cobra-cli`, `mdbook`, `goreleaser`, `Mermaid`, `dogfood`, `dogfooded`, `dogfooding`, anything else.

CONTRIBUTING.md additions:

- "Running the full e2e" subsection — env vars, cost expectation, time expectation, cluster-leave-behind behaviour.
- "Adding a new e2e phase" subsection — PRD 05 → `scripts/e2e-test-backends.sh` workflow.

## Verification before reporting done

- `go build ./...` clean
- `go test ./...` clean (incl. new `TestProbe_TruncatedFlag`)
- `bash -n scripts/e2e-test-backends.sh` clean
- `bash -n scripts/e2e-test-full.sh` clean
- `DRY_RUN=1 ./scripts/e2e-test-backends.sh` shows all phases (K, L, L-DNS, M, **I**, **N**) cleanly
- `DRY_RUN=1 ./scripts/e2e-test-full.sh` shows the chained driver-of-drivers flow cleanly
- `.github/workflows/e2e-full.yml` is valid YAML and syntactically clean (`yamllint` if available)
- `gofmt -d -l .` clean for any Go file you touched

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint6_validator.md`. `Severity: roadmap` for forward-looking items.

## Final report (under 200 words)

- Files created
- Files edited
- Test results (incl. TruncatedFlag)
- Issues filed (counts by severity)
- Whether `DRY_RUN=1` on both drivers (`e2e-test-backends.sh` + `e2e-test-full.sh`) shows phases cleanly
- Anything the integrator should know (especially regarding SSH-target-required Phase I + M5/M6 + N3 — what env vars need to be set for the integrator's manual v1.0 sign-off run)

Do NOT commit. The integrator commits the aggregated work.
