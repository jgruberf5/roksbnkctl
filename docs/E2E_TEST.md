# End-to-end test plan

Real-system shake-out for `roksbnkctl` against a live IBM Cloud account. Validates every roksbnkctl verb against the cluster + BNK deployment that `~/bnkfun/terraform.tfvars` describes.

> **Cost warning:** A full pass provisions a 3-zone ROKS cluster, transit gateway, COS instance, and the BNK workload. End-to-end runtime is ~3-4 hours; cloud spend is whatever IBM charges for the resources during that window. Phases B and D are the expensive ones — the rest are seconds-to-minutes.

## Inputs

- `~/bnkfun/terraform.tfvars` — supplies the cluster name (`canada-roks`), region (`ca-tor`), resource group (`default`), COS instance names (`canada-roks-cos-instance` for the registry, `bnk-orchestration` for orchestration), and the `create_roks_*` flags (all true → full provisioning path).
- `IBMCLOUD_API_KEY` env var (or the `ibmcloud_api_key` in tfvars) — IAM credential.
- `terraform`, `kubectl`, `oc`, `ibmcloud`, `iperf3` on `$PATH`.

## Workspace conventions

The test uses workspace `e2e` to avoid touching the user's existing workspaces. State lives at `~/.roksbnkctl/e2e/`. The driver script (`scripts/e2e-test.sh`) deletes that workspace at the start of each pass and at the end of a successful run.

## Pass criteria

Each phase has explicit assertions — the driver script exits non-zero on the first failed assertion. Every long-running command (`up`, `down`, `cluster up`, `cluster down`) runs with `--auto`; no interactive prompts.

## Phases

### Phase A — sanity (no cloud cost; ~1 minute)

| Step | Command | Pass criterion |
|---|---|---|
| A1 | `roksbnkctl version` | exits 0; prints a version line |
| A2 | `roksbnkctl doctor` | exits 0; the credential check passes (other warnings allowed) |
| A3 | `roksbnkctl init -w e2e --auto` (no prompts; defaults pulled from tfvars + env) | exits 0; writes `~/.roksbnkctl/e2e/config.yaml` |
| A4 | `roksbnkctl ws list` | exits 0; `e2e` appears in output |
| A5 | `roksbnkctl tfvars -o /tmp/e2e-tfvars.tf` | exits 0; file exists and references `openshift_cluster_name` |

### Phase B — cluster-only lifecycle (~50 minutes)

| Step | Command | Pass criterion |
|---|---|---|
| B1 | `roksbnkctl cluster up --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Apply complete!` in stdout |
| B2 | `roksbnkctl cluster show -w e2e` | exits 0; output includes `cluster_name: canada-roks` and a `registry_cos_crn` line |
| B3 | `roksbnkctl ibmcloud ks cluster get --cluster canada-roks` | exits 0; cluster `State` is `normal` |
| B4 | `roksbnkctl kubectl get nodes` | exits 0; >= 3 nodes; all `Ready` |
| B5 | `roksbnkctl oc whoami` | exits 0; prints a username |
| B6 | (cluster stays up — Phase C uses it) | — |
| B7 | `roksbnkctl -w e2e targets list` | exits 0; output contains `jumphost` (auto-populated by `cluster up` from the upstream HCL's TGW jumphost outputs — PRD 01 §Auto-discovery) |
| B8 | `roksbnkctl -w e2e exec --on jumphost -- whoami` | exits 0; output contains `root` (the upstream HCL provisions the jumphost as root) |
| B9 | `roksbnkctl -w e2e ibmcloud --on jumphost iam oauth-tokens` | exits 0; output contains `IAM token` — validates IBMCLOUD_API_KEY env propagates over SSH |
| B10 | `roksbnkctl -w e2e ibmcloud --backend docker iam oauth-tokens` | exits 0; output contains `IAM token` — validates the **docker backend** (PRD 03 first half, Sprint 3) propagates IBMCLOUD_API_KEY without leaking it via `docker inspect`. Skipped with a yellow `⊘` when the docker daemon isn't reachable on the runner. |

Steps B7-B9 require the upstream HCL's `testing_create_tgw_jumphost` to be true (the default). When users override that to false in their tfvars, the jumphost target won't be auto-populated and B7-B9 are skipped with a yellow `⊘` marker rather than failing the phase. See `scripts/e2e-test.sh phase_B` for the gating logic.

B10 is the **Phase K-prelim** in PLAN.md's terminology — a minimal docker-backend smoke test that lands in Sprint 3 alongside the docker plumbing. The full Phase K (canonical multi-tool docker backend phase covering iperf3 + terraform) is scoped for Sprint 6.

### Phase C — register an existing cluster (~30 seconds)

Validates that `cluster register` correctly discovers and persists the identity of a cluster roksbnkctl didn't itself create.

| Step | Command | Pass criterion |
|---|---|---|
| C1 | `rm ~/.roksbnkctl/e2e/cluster-outputs.json` (simulate "I made the cluster some other way") | exits 0 |
| C2 | `roksbnkctl cluster register canada-roks --registry-cos-name canada-roks-cos-instance -w e2e` | exits 0; "✓ Wrote ~/.roksbnkctl/e2e/cluster-outputs.json" |
| C3 | `roksbnkctl cluster show -w e2e` | exits 0; same `cluster_name`, `cluster_id`, `vpc_id` as Phase B's show output |
| C4 | `roksbnkctl cluster down --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Destroy complete!` |
| C5 | `roksbnkctl cluster show -w e2e` | exits non-zero with "no cluster-outputs.json" (down deletes the file) |

### Phase D — full lifecycle (cluster + BNK; ~70 minutes)

The everyday `roksbnkctl up` happy path: TF brings up cluster + BNK in one shot with `create_roks_*=true` in tfvars.

| Step | Command | Pass criterion |
|---|---|---|
| D1 | `roksbnkctl up --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Apply complete! Resources: 77 added` (or near — TF can shift counts) |
| D2 | `roksbnkctl status -w e2e` | exits 0; reports cluster reachable |
| D3 | `roksbnkctl k get pods -n f5-bnk` | exits 0; pods listed (state checks deferred to test phase). **Sprint 2 / PRD 02**: replaces the previous `roksbnkctl kubectl get pods` passthrough with the internalised `k get` verb. |
| D3b | `env PATH=<stripped> roksbnkctl k get nodes` | exits 0; output contains `Ready`. Validates the v0.8 "no kubectl required" claim — strips every PATH entry that holds a `kubectl` or `oc` executable, then runs `roksbnkctl k get nodes` against the stripped PATH. If the in-process implementation accidentally shells out to host `kubectl`, this step fails. We use `env PATH=…` rather than `mv kubectl kubectl.hidden` so the host filesystem stays untouched (the dry-run path also remains side-effect-free). |
| D4 | `roksbnkctl logs flo` (capture 50 lines, then break) | exits 0; output lines > 0 |
| D5 | `roksbnkctl test connectivity -o json` | exits 0; `schema: roksbnkctl.v1`; all checks pass |
| D6 | `roksbnkctl test dns -o json` | exits 0; all checks pass |
| D7 | `roksbnkctl test throughput -o json` | exits 0; iperf3 pod runs and tears down |
| D8 | `roksbnkctl down --auto -w e2e --var-file ~/bnkfun/terraform.tfvars` | exits 0; `Destroy complete!` |

### Phase E — workspace ops (no cloud cost; ~10 seconds)

Run *during* Phase D's idle windows (between up and down) to amortize wall time.

| Step | Command | Pass criterion |
|---|---|---|
| E1 | `roksbnkctl ws new e2e-second` | exits 0 |
| E2 | `roksbnkctl ws list` | exits 0; both `e2e` and `e2e-second` appear |
| E3 | `roksbnkctl ws current` | exits 0; prints `e2e` (set by `init -w e2e` earlier) |
| E4 | `roksbnkctl ws use e2e-second` then `ws current` | second call prints `e2e-second` |
| E5 | `roksbnkctl ws use e2e` | exits 0 |
| E6 | `roksbnkctl ws delete e2e-second --force` | exits 0; not in `ws list` afterward |

### Phase F — COS bucket + object CRUD (no cloud cost beyond bytes stored; ~30 seconds)

Validates Resource Controller + S3 plumbing. Uses `bnk-orchestration` (the COS instance the user's tfvars references for general orchestration storage; assumed pre-existing in the account). Creates and deletes its own scratch bucket — never writes into a pre-existing bucket — so the test is fully self-contained.

| Step | Command | Pass criterion |
|---|---|---|
| F1 | `roksbnkctl cos instance list` | exits 0; `bnk-orchestration` appears |
| F2 | `roksbnkctl cos bucket list --instance bnk-orchestration` | exits 0 |
| F3 | `roksbnkctl cos bucket create roksbnkctl-e2e-<unique> --instance bnk-orchestration` | exits 0 (bucket name globally unique) |
| F4 | `roksbnkctl cos object put <bucket>/blob /tmp/blob --instance bnk-orchestration` | exits 0 |
| F5 | `roksbnkctl cos object get <bucket>/blob /tmp/blob.out --instance bnk-orchestration` | exits 0; bytes match |
| F6 | `roksbnkctl cos object delete <bucket>/blob --instance bnk-orchestration` | exits 0 |
| F7 | `roksbnkctl cos bucket delete <bucket> --instance bnk-orchestration` | exits 0 |

### Phase G — passthrough commands (no cloud cost; ~10 seconds; runs during Phase D)

Validates that `roksbnkctl` passes workspace credentials to subprocesses correctly.

| Step | Command | Pass criterion |
|---|---|---|
| G1 | `roksbnkctl ibmcloud account show` | exits 0; account info prints |
| G2 | `roksbnkctl kubectl version --client` | exits 0 |
| G3 | `roksbnkctl oc version --client` | exits 0 |
| G4 | `roksbnkctl exec env \| grep KUBECONFIG` | exits 0; KUBECONFIG points at `~/.roksbnkctl/e2e/state/kubeconfig` |

### Phase H — final cleanup (~10 seconds)

| Step | Command | Pass criterion |
|---|---|---|
| H1 | `roksbnkctl ws delete e2e --force` | exits 0; `e2e` no longer in `ws list` |
| H2 | `ls ~/.roksbnkctl/e2e 2>&1` | "No such file or directory" |

## Sprint 4 — backend matrix driver

Sprint 4 introduces a sibling driver — [`scripts/e2e-test-backends.sh`](../scripts/e2e-test-backends.sh) — that exercises the four-backend matrix from PRDs 03 + 04. Sprint 6 expanded the coverage to all PRD 05 phases:

- **Phase I** (SSH backend, full coverage — I0 through I11) per [PRD 05 §I](./prd/05-E2E-TEST-PLAN.md#phase-i--ssh-backend--on-jumphost) (Sprint 6)
- **Phase K** (docker backend, full coverage — K1 through K6) per [PRD 05 §K](./prd/05-E2E-TEST-PLAN.md#phase-k--docker-backend-ibmcloud--iperf3)
- **Phase L** (k8s backend, full coverage — L0 through L7) per [PRD 05 §L](./prd/05-E2E-TEST-PLAN.md#phase-l--k8s-backend-iperf3--ops-pod)
- **Phase L-DNS** (DNS probe + GSLB compare — LD0 through LD10) per [PRD 05 §L-DNS](./prd/05-E2E-TEST-PLAN.md#phase-l-dns--dns-probe-gslb-aware-across-backends) (Sprint 5)
- **Phase M** (cred-leak audit — M1 through M7, full coverage including M5+M6) per [PRD 05 §M](./prd/05-E2E-TEST-PLAN.md#phase-m--credential-propagation-audit) (Sprint 6 closed M5+M6)
- **Phase N** (mixed-mode lifecycle — N1 through N6) per [PRD 05 §N](./prd/05-E2E-TEST-PLAN.md#phase-n--mixed-mode-lifecycle) (Sprint 6)

The backends driver REUSES the cluster brought up by `scripts/e2e-test.sh`'s Phase D for I + K + L + L-DNS + M (with the exception of Phase N's N1 which provisions its own state to validate cross-backend lifecycle). **Run order**:

```bash
# 1. Bring up the cluster + BNK via the baseline driver:
IBMCLOUD_API_KEY=... ./scripts/e2e-test.sh                 # Phases A-H, ~3-4h

# 2. (Between Phase D's apply and final teardown) — exercise the matrix:
IBMCLOUD_API_KEY=... ./scripts/e2e-test-backends.sh        # I + K + L + L-DNS + M + N
```

Or use the **combined runner** introduced in Sprint 6 — see [§"Full e2e (e2e-test-full.sh)"](#full-e2e-e2e-test-fullsh) below for one-button coverage.

The backends driver supports the same `PHASE_FROM=` resume hook and `DRY_RUN=1` plan-rendering mode as the baseline driver. See `CONTRIBUTING.md` "Running scripts/e2e-test-backends.sh locally" for the full local-run recipe.

## Full e2e (`scripts/e2e-test-full.sh`)

Sprint 6 lands [`scripts/e2e-test-full.sh`](../scripts/e2e-test-full.sh) — the "one button" full-coverage runner that chains the baseline driver (Phases A-H) and the backends driver (Phases I + K + L + L-DNS + M + N) against the same workspace + cluster.

### When to use

- **Release-branch gate**: a `release/<v>` branch push runs this workflow automatically via [`.github/workflows/e2e-full.yml`](../.github/workflows/e2e-full.yml). Merge to `main` only after a green run.
- **Manual `v1.0` (and later) sign-off**: the integrator triggers `Full E2E` from the Actions tab before tagging a release.
- **Pre-merge feature-branch shake-out** for sweeping changes (a new backend, a kubeconfig-routing refactor, a major HCL update) — too long for PR-gated CI, useful for high-risk merges.

NOT for every PR — 4-6 hour wall time exceeds the PR check budget.

### CI preflight (workflow secret fail-fast)

`e2e-full.yml`'s first step is a preflight that fail-fasts the workflow with a
clear `missing secret X` message if either of the two required GitHub Actions
secrets is unset:

- `IBMCLOUD_API_KEY` — IBM Cloud IAM credential (the same value `~/bnkfun/terraform.tfvars` carries locally).
- `E2E_TFVARS_CONTENT` — the full `~/bnkfun/terraform.tfvars` contents **minus** the `ibmcloud_api_key` line. Multi-line value; paste the file verbatim.

The optional `E2E_SSH_TARGET` / `E2E_SSH_NON_UBUNTU` / `E2E_SSH_NO_NOPASSWD`
secrets stay optional — when unset, the workflow's Phase I + M5/M6 + N3 steps
yellow-skip without failing the run. Set them against purpose-built SSH targets
for the integrator's v1.0 sign-off run.

If preflight fails, set the missing secret in **repo settings → Actions →
secrets** and re-dispatch the workflow. The failure message names which secret
is missing — no need to dig through `roksbnkctl up` logs from 5 minutes later.

### Running

```bash
IBMCLOUD_API_KEY=... ./scripts/e2e-test-full.sh                     # full pass (cluster stays up)
IBMCLOUD_API_KEY=... ./scripts/e2e-test-full.sh --teardown          # green run tears down at end
IBMCLOUD_API_KEY=... PHASE_FROM=D ./scripts/e2e-test-full.sh        # resume baseline at D
IBMCLOUD_API_KEY=... PHASE_FROM=I ./scripts/e2e-test-full.sh        # SKIP baseline; backends only (cluster assumed up)
IBMCLOUD_API_KEY=... DRY_RUN=1 ./scripts/e2e-test-full.sh           # plan-only walkthrough
```

### Env vars

The combined runner picks up every env var the child drivers honour, plus the Phase I + M5/M6 + N3 SSH-target keys introduced in Sprint 6:

| Variable | Default | Purpose |
|---|---|---|
| `IBMCLOUD_API_KEY` | (from tfvars) | IBM Cloud IAM credential — required |
| `TFVARS` | `~/bnkfun/terraform.tfvars` | terraform inputs |
| `WORKSPACE` | `e2e` | workspace name; `~/.roksbnkctl/<ws>/` |
| `ROKSBNKCTL` | `roksbnkctl` (on PATH) | absolute path to the binary if not installed |
| `PHASE_FROM` | `A` | resume hook (A-H = baseline, I/K/L/L-DNS/M/N = backends) |
| `DRY_RUN` | `0` | plan-only (no live cloud calls) |
| `ROKSBNKCTL_E2E_SSH_TARGET` | (unset) | name of an SSH target in the workspace config; enables Phase I + M5/M6 + N3 |
| `ROKSBNKCTL_E2E_SSH_NON_UBUNTU` | (unset) | purpose-built non-Ubuntu SSH target for Phase I7 |
| `ROKSBNKCTL_E2E_SSH_NO_NOPASSWD` | (unset) | purpose-built sudo-password-required SSH target for Phase I8 |
| `ROKSBNKCTL_E2E_INIT_BACKEND` | `local` (or `docker` if no terraform) | initial backend for Phase N1 |

### Cost + duration

| Phase | Wall time | Cloud spend (USD) |
|---|---|---|
| A-H baseline (cluster up + BNK + tests + down) | ~3-4h | ~$5-8 |
| Phase I (SSH backend, against pre-existing target) | ~2-5m | $0 (no new resources) |
| Phase K (docker backend) | ~3-5m | $0 |
| Phase L (k8s backend) | ~5-10m | $0 |
| Phase L-DNS | ~1-3m | $0 |
| Phase M (cred audit) | ~1m | $0 |
| Phase N (mixed-mode; second up/down cycle) | ~70-90m | ~$3-5 |
| **Total** | **~4-6h** | **~$8-13** |

Cluster spend is bursty: most of the budget is the ROKS cluster apply + LBs during Phase D's up and Phase N's up. Phase N adds a second up/down cycle to validate cross-backend state portability — skip Phase N (set `PHASE_FROM=...` to stop before N) if cost is a concern and the integrator has manually verified the mixed-mode invariant.

### Phase I + Phase M + Phase N coverage notes

#### Phase I — SSH backend

Exercises the SSH backend introduced in Sprints 1 + 4. Requires `ROKSBNKCTL_E2E_SSH_TARGET=<name>` pointing at a target listed in the workspace's `targets:` block. Typically this is the `jumphost` target auto-populated by `cluster up` (the upstream HCL provisions a TGW jumphost when `testing_create_tgw_jumphost=true`, the default).

Step matrix (skip-clean rules in parentheses):

| Step | What it asserts | Skip-clean trigger |
|---|---|---|
| I0 | `targets show <name>` exits 0 | (none — no target means the whole phase skips) |
| I1 | Sprint 1 `--on <name>` SSH path produces an IAM token | — |
| I2 | Sprint 4 `--backend ssh:<name>` SSH path produces an IAM token | — |
| I3 | `--bootstrap iperf3 -v` apt-installs iperf3 on the target (idempotent) | yellow ⊘ on non-Ubuntu / sudo-NOPASSWD-required target |
| I4 | `env` on remote does NOT contain the API key VALUE (wrapper-script isolation) | — |
| I5 | `/tmp/roksbnkctl.*` empty on remote after the run (trap-on-EXIT cleaned up) | red ✗ if leaked |
| I6 | SetEnv silent-drop fallback (sshd AcceptEnv-disabled) | informational |
| I7 | non-Ubuntu --bootstrap rejection | yellow ⊘ unless `ROKSBNKCTL_E2E_SSH_NON_UBUNTU` set |
| I8 | sudo-password-required rejection | yellow ⊘ unless `ROKSBNKCTL_E2E_SSH_NO_NOPASSWD` set |
| I9 | repo-unreachable failure | yellow ⊘ — manual (mutates remote network) |
| I10 | Ctrl-C / SIGINT cleanup within 5s | — |
| I11 | `doctor --backend ssh:<name>` green | — |

#### Phase M — cred-leak audit (full)

Sprint 4 landed M1-M4 + M7 against the docker + k8s backends. Sprint 6 closes M5 + M6 against the SSH backend (gated on the same `ROKSBNKCTL_E2E_SSH_TARGET`):

| Step | What it asserts | Skip-clean trigger |
|---|---|---|
| M1 | `docker history` no API key in ENV layers | (skip if no docker) |
| M2 | `docker inspect` no API key in `Config.Env` | (skip if no docker) |
| M3 | `kubectl get events -n roksbnkctl-ops` no API key | — |
| M4 | ops pod logs no API key (redactor masks) | yellow ⊘ if ops pod uninstalled |
| M5 | SSH `/tmp/roksbnkctl.*` empty (cred audit lens) | yellow ⊘ unless `ROKSBNKCTL_E2E_SSH_TARGET` set |
| M6 | `/var/log/auth.log` no API key value; `Accepted publickey` lines present | yellow ⊘ on sudo-no-read |
| M7 | workspace `*.log` files no API key (state files allowed) | — |

#### Phase N — mixed-mode lifecycle

Validates that a cluster brought up via one backend can be inspected + torn down via *another* backend — the .tfstate is portable, the kubeconfig is portable, and the API key resolves consistently across backends.

| Step | What it asserts | Skip-clean trigger |
|---|---|---|
| N1 | `up --backend <init>` succeeds (default `local`, override via `ROKSBNKCTL_E2E_INIT_BACKEND`) | — |
| N2 | `test throughput --backend k8s` against the cluster from N1 | yellow ⊘ if no kube context |
| N3 | `ibmcloud --backend ssh:<target> ks cluster ls` sees the N1 cluster | yellow ⊘ unless `ROKSBNKCTL_E2E_SSH_TARGET` set |
| N4 | `test dns --backend k8s --gslb-compare` multi-vantage probe | yellow ⊘ if no kube context |
| N5 | `down --backend <other>` tears down (cross-backend state-file compat) | — |
| N6 | post-teardown: `cluster-outputs.json` removed; no orphan resources | — |

## Phase-handoff regression (Issue 2)

`scripts/e2e-phase-handoff.sh` is a **gated live-verify** driver for
`issues/issue_sprint16_validator.md` Issue 2 — the regression where the
second (bnk/testing) phase of `roksbnkctl up` re-created the cluster
VPC / transit gateway / client VPC the cluster phase already made, and
IBM Cloud rejected the duplicate names (`Provided Name (<ws>-vpc) is not
unique` / `A gateway with the same name already exists`).

### Why a separate driver, and why NOT CI

The behavior-parity gate (validator Issue 1) is GREEN and *correct* — it
proves the phase-1b refactor is behavior-identical at the unit level. It
is structurally blind to Issue 2 because no hermetic test exercises a
workspace that has already completed the cluster phase. That blind spot
is closed at two levels:

- **Hermetic** — `internal/tf/secondphase_handoff_test.go` asserts the
  second-phase tfvars contract directly (cluster outputs present →
  `use_existing_cluster_vpc = true` + `existing_cluster_vpc_id` +
  `create_roks_transit_gateway = false` + `testing_create_client_vpc =
  false`; no outputs → byte-identical create path). It runs in the
  normal `go test ./...` suite — no cloud, no key.
- **Live** — this driver, run by an operator against the real account.

Per the Sprint 16 follow-up integrator decision, e2e for this fix is
**operator-run via `!`, never a CI job**. It is intentionally NOT a
GitHub workflow and has no `workflow_dispatch` trigger — a real
`terraform apply` is real spend, so it is opt-in and self-tears-down.
The driver never echoes, logs, or scrapes the IBM Cloud API key, and
the project `./terraform.tfvars` (which holds a live key) is referenced
for structure only — its contents are never printed.

### How and when an operator runs it

After the fix has landed (and before the integrator marks Issue 2
`resolved`), an operator runs:

```bash
# Plan-only walkthrough — no cloud, no key needed (CI-safe self-check):
DRY_RUN=1 ./scripts/e2e-phase-handoff.sh

# Live verify — REAL SPEND (~$5-8, ~70+ min). Key in the ENV, not tfvars:
IBMCLOUD_API_KEY=... ./scripts/e2e-phase-handoff.sh
```

Knobs mirror `scripts/e2e-test.sh`: `TFVARS` (default
`./terraform.tfvars`), `WORKSPACE` (default `e2e-handoff`), `DRY_RUN`,
`LOG_DIR`, `ROKSBNKCTL`. The driver runs `up` end to end (cluster phase
then bnk/testing phase — the exact path Issue 2 fails on) and always
tears the workspace down via an EXIT trap so a failed run does not
strand billable infra (loud + best-effort: a teardown failure prints
the manual `down` command and a console-check reminder).

### What GREEN means

The run exits `0` and prints the GREEN banner only when **all** of:

- **A1/A1b** — the cluster phase created + tracked the cluster VPC /
  transit gateway, and `cluster-outputs.json` carries a `vpc_id` (the
  handoff data exists). Establishes the reproduction premise.
- **A2** — the second-phase state
  (`~/.roksbnkctl/<ws>/state/terraform.tfstate`) does NOT *manage* a
  duplicate `module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc`
  / `ibm_tg_gateway.transit_gateway` / `module.testing.ibm_is_vpc.client_vpc`
  (a `data` reuse lookup is fine; a `"mode": "managed"` duplicate fails
  the run).
- **A3** — the rendered second-phase `terraform.tfvars` carries
  `use_existing_cluster_vpc = true`.
- **A4** — `up` exited `0` and the run log is free of
  `is not unique` / `already exists`.

Any failed assertion exits non-zero with the failing check named. A red
A2/A3/A4 means the phase handoff is still broken — Issue 2 stays open.

## Failure recovery

If a phase fails:

1. The driver script captures the full log to `/tmp/roksbnkctl-e2e-<phase>-<timestamp>.log` and exits.
2. Cause is diagnosed from that log.
3. Code is fixed, committed with a descriptive message, pushed.
4. Binary is rebuilt and reinstalled.
5. Driver re-runs from the failing phase (state from earlier passes is preserved where reasonable; `cluster up` has its own idempotence).

This loop repeats until a full pass with zero failed assertions.

## Out of scope (this round)

- `roksbnkctl shell` — interactive subshell; can't be auto-tested without a fake TTY.
- `roksbnkctl install` — would overwrite the running binary; tested manually.
- `roksbnkctl self update` — requires a published GitHub release.
- BNK-on-existing-cluster path (cluster persists across multiple BNK trial workspaces) — that requires upstream HCL changes (gating cert_manager + testing modules behind a `deploy_cluster_services` variable) that aren't yet in this repo.

## Per-release checklist

> **History:** v0.9 was the first release to use this checklist (Sprint 5).
> Sprint 6 promoted it from a v0.9-only doc to the **permanent per-release
> gate** — every release tag (v1.0 onward) runs the same items.

Before tagging any `vX.Y` release, the integrator runs the following
manual checklist against a real BNK-deployed cluster. The automated
phases above (A-H) plus Phases I + K + L + L-DNS + M + N from
`scripts/e2e-test-backends.sh` cover most of the surface; the items
below are the ones that genuinely require a human at the helm.

### 1. Full backend-matrix e2e against a live cluster

```bash
# One-button: runs A-H + I-N + L-DNS against the same cluster
# (Sprint 6 combined runner — see §"Full e2e (e2e-test-full.sh)" above):
IBMCLOUD_API_KEY=... \
ROKSBNKCTL_E2E_SSH_TARGET=jumphost \
./scripts/e2e-test-full.sh --teardown
```

All phases must pass: I (SSH backend), K (docker), L (k8s), L-DNS, M
(cred audit, incl. M5/M6 SSH-side), N (mixed-mode). Yellow ⊘ skips
for purpose-built-target-only steps (I7, I8, I9) are acceptable; red
✗ failures are release blockers.

For backwards compatibility with the v0.9 dispatch pattern, the
chained-by-hand recipe still works:

```bash
# Step 1: baseline driver A-H (brings up a cluster, runs A-G in the
# idle window, tears down at H).
IBMCLOUD_API_KEY=... ./scripts/e2e-test.sh

# Step 2: backends driver I + K + L + L-DNS + M + N (runs against
# the same workspace; Phase N's N1 brings up its own cluster).
IBMCLOUD_API_KEY=... \
ROKSBNKCTL_E2E_SSH_TARGET=jumphost \
./scripts/e2e-test-backends.sh
```

### 2. Manual GSLB validation

The automated Phase L-DNS step LD8 doesn't assert `gslb_divergence:
true` — anycast can produce identical answers from local vs in-cluster
vantages by chance. The manual check pins the divergence behaviour
against a known-divergent target:

```bash
roksbnkctl test dns \
  --target <real-F5-BIG-IP-Next-GSLB-record> \
  --type A \
  --server <gslb-vip>:53 \
  --gslb-compare \
  -o json
```

Pass criterion: `gslb_divergence: true` AND the
`gslb_divergence_summary` field clearly explains the location-specific
records returned. If the test BNK deployment has no GSLB records
configured, fall back to a strong-DC-affinity public name (e.g.,
`www.amazon.com` via Route 53 latency-based routing) — note in the
release log that the integrator validated against the fallback target
rather than F5 BIG-IP Next.

### 3. Terraform docker-backend full lifecycle

```bash
# Fresh workspace; full plan→apply→destroy cycle entirely in container:
roksbnkctl init -w docker-tf-test --auto
roksbnkctl up --auto -w docker-tf-test \
  --backend docker \
  --var-file ~/bnkfun/terraform.tfvars

# State file must land in ~/.roksbnkctl/docker-tf-test/state/
# with the host user's UID (not root). Verify:
ls -la ~/.roksbnkctl/docker-tf-test/state/

roksbnkctl down --auto -w docker-tf-test \
  --backend docker \
  --var-file ~/bnkfun/terraform.tfvars
```

Pass criteria:
- `terraform init/plan/apply` all run inside `hashicorp/terraform:<v>`
  (verifiable via `docker ps` during apply)
- `.tfstate` written to the host bind-mount, owned by the running user
  (NOT root)
- `roksbnkctl down --backend docker` cleanly removes everything

### 4. Cred audit across all four backends

Run a known-secret IBM Cloud API key through each backend and audit the
inspection surfaces for the value. Phase M's automated checks cover
most of these; the manual check adds `~/.roksbnkctl/<ws>/state/*.log`
sweep for any roksbnkctl-internal log files that aren't yet covered:

```bash
# After Phases I-N have run with IBMCLOUD_API_KEY=<known-value>:
grep -RF "$IBMCLOUD_API_KEY" ~/.roksbnkctl/ 2>/dev/null
# Allowed: matches in *.tfstate (terraform legitimately stores the
# input variable). Forbidden: matches in any *.log file.
```

### 5. Doctor green on a stock dev box

```bash
# On a fresh dev box with only `terraform` installed (no kubectl, no
# oc, no ibmcloud, no iperf3, no dig):
roksbnkctl doctor
```

Pass criterion: `roksbnkctl doctor` exits 0; missing host tools surface
as informational notes (not warnings or errors), with the per-backend
section explaining which tools are needed for which backend.

**Sprint 6 refactor (v0.10):** `internal/doctor/doctor.go::runWithWhy`
now hard-fails only on missing `terraform`. `kubectl`, `oc`, `ibmcloud`,
`iperf3`, and `dig` are rendered as informational `✓` rows with a
detail line naming the internalised path (`--backend docker` /
`--backend k8s` / miekg-dns probe / client-go). Pre-`up` doctor runs
also render an absent kubeconfig as informational rather than a
warning, since the kubeconfig is auto-populated by `roksbnkctl up`'s
post-apply hook.

### 6. Tag and release

Once items 1-5 are green:

```bash
# Replace vX.Y with the actual release version:
git tag vX.Y
git push origin vX.Y
# GitHub Actions builds the binary + tools images + cuts the release.
```

The book under `book/src/` ships matched to the tag — every chapter
referenced from `book/src/SUMMARY.md` must be in `main` before the tag.
The `Full E2E` GitHub workflow (`.github/workflows/e2e-full.yml`)
runs on every push to a `release/<v>` branch and is the canonical
gate for whether a release branch is mergeable to `main`.
