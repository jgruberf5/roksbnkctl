# The E2E test plan

`roksbnkctl` ships a layered end-to-end test suite that exercises the full surface — install, lifecycle, four execution backends, internalised kubectl, the DNS probe, the cred-leak audit, and a mixed-mode lifecycle — against a live IBM Cloud account. This chapter is the user-facing guide: what the suite is, how to run it locally, what each phase validates, what it costs, and how it's re-run when (not if) part of it flakes.

The design rationale lives in [PRD 05](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md); read that for the *why*. This chapter is the *how* and *what*.

## What the E2E suite is

The suite is 14 automated phases organised into two tiers, plus Phase J as a manual integrator step (kubectl internalisation requires `sudo mv` of the host kubectl/oc binaries; that mutation is too disruptive to automate, so PRD 05 §J leaves it as a release-checklist item):

| Tier | Phases | What it covers | Driver script |
|---|---|---|---|
| **Baseline** | A, B, C, D, E, F, G, H | install, init, plan, up, post-apply checks, test suites, down | `scripts/e2e-test.sh` |
| **Backends + extras** | I, K, L, L-DNS, M, N | SSH backend, docker backend, k8s backend + ops pod, DNS probe with GSLB compare, cred-leak audit, mixed-mode lifecycle | `scripts/e2e-test-backends.sh` |
| **Manual** | J | kubectl internalisation (PATH-stripped, integrator-driven) | per-release checklist |

A combined driver, `scripts/e2e-test-full.sh`, runs both automated tiers in sequence: A-H first to bring up + exercise + tear down the baseline cluster, then I-N which provisions a fresh cluster via Phase N's mixed-mode-lifecycle step. The two drivers stay decoupled — each can be run standalone — at the cost of an extra cluster apply (~70min wall-time, ~5-7h combined). Cluster-sharing across the two drivers (the PRD-envisioned design) is queued for v1.x; see PRD 05 §"Test infrastructure".

### Phase coverage at a glance

| Phase | Tier | Validates | PRD |
|---|---|---|---|
| A | baseline | doctor + init | — |
| B | baseline | plan (read-only) | — |
| C | baseline | targets list, registration | [01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md) |
| D | baseline | `up` lifecycle — provision + deploy BNK | — |
| E | baseline | post-apply checks (`status`, `k get`, `logs`) | [02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md) |
| F | baseline | `test connectivity` (HTTP probes) | — |
| G | baseline | `test throughput` (iperf3) | — |
| H | baseline | `down` — destroy + cleanup | — |
| I | backends | SSH backend / `--on jumphost`, host-key TOFU | [01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md) |
| J | **manual** | kubectl internalisation (PATH-stripped — requires `sudo mv`) | [02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md) |
| K | backends | docker backend (ibmcloud + iperf3 client) | [03 § Docker](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#docker-backend) |
| L | backends | k8s backend (ops pod + ibmcloud + iperf3) | [03 § K8s](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#k8s-backend) |
| L-DNS | backends | DNS probe + GSLB cross-vantage compare | [03 § DNS](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md#dns-probe-gslb-aware) |
| M | backends | cred-leak audit (docker inspect, k8s events, ssh tempfiles) | [04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) |
| N | backends | mixed-mode lifecycle (each tool on a different backend) | all of the above |

Phase J is an integrator-driven manual step; the per-release checklist in [`docs/E2E_TEST.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/E2E_TEST.md) covers its procedure (PATH-strip kubectl + oc, then re-run the baseline driver's Phase E to confirm `roksbnkctl k get/apply/describe/...` still works against the cluster).

## How to run it locally

The three driver scripts all live under [`scripts/`](https://github.com/jgruberf5/roksbnkctl/tree/main/scripts):

```bash
# Baseline only (A-H) — ~90 minutes
./scripts/e2e-test.sh

# Backends + extras only (I-N + L-DNS) — requires a live cluster (run after
# Phase D of e2e-test.sh, or provision via `roksbnkctl up` in a separate
# workspace first)
./scripts/e2e-test-backends.sh

# Combined — A-H baseline, then I-N + L-DNS against a fresh cluster the
# backends driver brings up via Phase N's mixed-mode-lifecycle step,
# ~5-7 hours total (two separate cluster applies)
./scripts/e2e-test-full.sh
```

### Pre-requisites

| Pre-req | Required for | Notes |
|---|---|---|
| IBM Cloud account with API key | every phase | `IBMCLOUD_API_KEY` env var, or `roksbnkctl init` writes a keychain entry |
| `terraform` binary on PATH | phases B-H | the only strictly-required host tool for the baseline |
| Docker daemon | phase K | `dockerd` or `colima` or Rancher Desktop — anything that publishes a docker socket |
| `kind` binary | phase L on CI | the in-CI k8s backend uses a kind cluster; on a real run it uses the ROKS cluster from D |
| An SSH bastion or jumphost | phase I, N | provisioned automatically by phase D's terraform when `testing_create_tgw_jumphost = true` (the default) |
| Adequate disk for terraform plan output | phases B-H | ~200 MB for the embedded module's state |

Everything else (kubectl, oc, dig, iperf3) is internalised by the binary — phase J explicitly verifies the suite passes with kubectl and oc moved out of PATH.

### Resuming a partial run

Every phase is re-runnable. The driver scripts respect a `PHASE_FROM=` env var:

```bash
# Restart from phase G (skipping A-F, which already ran)
PHASE_FROM=G ./scripts/e2e-test.sh

# Same for the backend driver
PHASE_FROM=L ./scripts/e2e-test-backends.sh
```

The phase pointer is read at startup and the script fast-forwards past every step before it. Assertion phases that hit external APIs (DNS resolvers, IBM Cloud control plane) include jitter and retry on the typical transient failure shapes — short DNS timeouts, IAM 5xx blips, etc. See [PRD 05 §"Risks"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md) for the retry policy.

### Run logs

Each driver writes a single combined log per run:

- `scripts/e2e-test.sh` → `/tmp/roksbnkctl-e2e/run-<timestamp>.log`
- `scripts/e2e-test-backends.sh` → `/tmp/roksbnkctl-e2e-backends/run-<timestamp>.log`
- `scripts/e2e-test-full.sh` → both of the above (the combined runner re-uses each child driver's log directory)

Logs are preserved on both success and failure; clean them up manually when disk pressure warrants. On a CI machine that runs the suite nightly, the logs are the only forensics you get — keep them for at least 7 days. Per-phase log splitting is a v1.x consideration.

### Dry-run

`DRY_RUN=1` short-circuits every `roksbnkctl` invocation to a no-op that prints the command it would have run. Useful for re-validating the script wiring after edits without paying the 30-minute cluster-apply tax. The validator agent's "is the test plan still well-formed" check runs in this mode.

## What each phase validates

### Phase A — `init`

`roksbnkctl init` prompts for region, resource group, cluster name, and BNK version, then writes `~/.roksbnkctl/<workspace>/config.yaml`. The phase asserts the file exists, contains no plaintext API key (the rejection regex in [`internal/config/workspace.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go) catches that), and that `roksbnkctl doctor` reports green for terraform and informational for kubectl/oc.

### Phase B — `plan`

`roksbnkctl plan` runs `terraform init` (downloads providers, ~30s) and `terraform plan` (computes the resource diff, ~30-60s on a clean workspace). No infrastructure is provisioned. The phase asserts the plan reports `~77 resources to add` (the exact count is the upstream HCL's full set of cluster + cert-manager + flo + cne_instance + license + testing resources).

### Phase C — targets and registration

`roksbnkctl targets list` against a fresh workspace returns empty. `roksbnkctl cluster register <existing-cluster>` (optional, skipped if the workspace is provisioning new infra) ties an existing ROKS cluster's COS instance + bucket discovery into the workspace.

### Phase D — `up` lifecycle

The dominant cost phase. `roksbnkctl up --auto` runs `terraform apply` against the embedded HCL — provisioning ~77 resources: VPC, subnets, transit gateway, ROKS cluster, cert-manager, FLO, CNEInstance, License, jumphost. Expect 30-50 minutes on a clean apply, 5-15 minutes longer when IBM Cloud's control plane is slow. The phase asserts terraform exits zero and the admin kubeconfig was fetched and written to `$KUBECONFIG`.

Post-apply, the phase auto-registers the `jumphost` target (per [Chapter 16 §"Auto-discovery from `roksbnkctl up`"](./16-on-flag-ssh-jumphosts.md#auto-discovery-from-roksbnkctl-up)) so subsequent phases can `--on jumphost` without manual config.

### Phase E — post-apply checks

`roksbnkctl status` shows the deployed BNK components. `roksbnkctl k get nodes` lists 3 worker nodes Ready. `roksbnkctl logs flo` (the F5 Lifecycle Operator) prints recent log lines. The phase asserts each command exits zero and that the cluster's BNK install (FLO + CNE Instance + License) is in a healthy state.

### Phase F — `test connectivity`

`roksbnkctl test connectivity` walks the workspace's `test.connectivity.extra_hosts` list and probes each URL. Pass criteria: every URL returned a 2xx (or the expected status, when [Chapter 20](./20-connectivity-testing.md)'s richer assertion shape lands).

### Phase G — `test throughput`

`roksbnkctl test throughput` deploys the iperf3 server-pod fixture, runs a 30-second client measurement, and tears the fixture down. Pass criteria: bandwidth > 100 Mbps (a conservative floor — actual numbers on IBM Cloud are typically 1-5 Gbps), retransmits < 5% of streams, fixture removed afterwards.

### Phase H — `down`

`roksbnkctl down --auto` runs `terraform destroy`. Pass criteria: all ~77 resources destroyed (terraform reports `Destroy complete!`), no orphan IBM Cloud resources detectable via `roksbnkctl ibmcloud resource search`.

### Phase I — SSH backend / `--on jumphost`

`roksbnkctl exec --on jumphost -- whoami` returns `root` (the jumphost auto-provisioned with cloud-init's root user). `roksbnkctl ibmcloud --on jumphost iam oauth-tokens` validates `IBMCLOUD_API_KEY` propagation over SSH. A negative test mutates `~/.roksbnkctl/known_hosts` to a wrong fingerprint and asserts the next call exits 126 with a clear "host key mismatch" error. Phase I is the user-facing acceptance test for [PRD 01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md).

### Phase J — kubectl internalisation

The phase strips `kubectl` and `oc` out of PATH (via env-var sanitisation, not filesystem moves — see [PRD 05 §"Open questions"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md)) and verifies that `roksbnkctl k get nodes`, `roksbnkctl k apply -f`, `roksbnkctl k describe`, `roksbnkctl k exec`, `roksbnkctl k port-forward`, and `roksbnkctl k delete` all work against the cluster. A supplementary byte-equivalence step (run separately, not gated on PATH stripping) diffs `kubectl get nodes -o yaml` against `roksbnkctl k get nodes -o yaml` and asserts the diff (excluding `managedFields`, `resourceVersion`, `creationTimestamp`) is empty.

### Phase K — docker backend

`roksbnkctl ibmcloud --backend docker iam oauth-tokens` pulls the `roksbnkctl-tools-ibmcloud` image on first call and runs the ibmcloud CLI inside it. The phase asserts the API key is **not** baked into the image (via `docker history` inspection) and **not** exposed in the running container's env (via `docker inspect`). [Chapter 17 §"docker backend"](./17-execution-backends.md#docker-backend) covers the credential-passing mechanism.

### Phase L — k8s backend and ops pod

`roksbnkctl ops install` creates the `roksbnkctl-ops` namespace, deploys the long-lived ops pod, projects the IBM API key as a Kubernetes Secret, and binds the pod's ServiceAccount to a least-privilege ClusterRole. Subsequent steps run `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` (executes inside the ops pod) and `roksbnkctl test throughput --backend k8s` (the iperf3 server pod and client Job both run in-cluster). RBAC assertions confirm the SA can create Jobs in `roksbnkctl-test` but **cannot** delete Pods in `default` — least-privilege is enforced. [Chapter 19](./19-in-cluster-ops-pod.md) is the ops-pod reference.

### Phase L-DNS — DNS probe and GSLB compare

The DNS phase exercises the [`miekg/dns`-backed probe](./21-dns-testing-gslb.md):

- Single-vantage A and AAAA lookups against `8.8.8.8`
- NXDOMAIN negative test (asserts rcode=`NXDOMAIN`)
- Iterated probe (10 queries to the same server, RTT p50/p95/p99 reported)
- K8s-backend probe (runs as a Job in `roksbnkctl-test`, the binary self-execs in-cluster, RTT reflects in-cluster network path)
- `--server cluster` (uses the pod's `/etc/resolv.conf`, validates CoreDNS visibility)
- `--gslb-compare` happy path (fans out local + k8s, asserts the answer schema)
- `--gslb-compare` divergence (target a geo-resolved name where laptop and cluster IPs hit different DCs, asserts `gslb_divergence: true`)
- Docker rejection negative (asserts the parse-time rejection error for `--backend docker --target ...`)

LD9 (SSH vantage) is exercised only when a jumphost is configured; LD5-LD8 are the must-pass set.

### Phase M — cred-leak audit

Cross-cutting check that runs **after** I-L — confirms no credential value leaked during any prior phase. Concrete assertions:

- `docker history <ibmcloud-tool-image>` — no `IBMCLOUD_API_KEY=...` ENV layer
- `docker inspect <last-container>` — no API key value in env
- `kubectl get events -n roksbnkctl-ops -o yaml` — grepping for the API key value returns nothing
- `kubectl logs <ops-pod>` — grepping for the API key value returns nothing (the [redactor](./14-credentials-resolver.md#the-redactor) masks any tool output that prints it)
- `ssh jumphost ls /tmp/roksbnkctl.*` — empty (the SSH backend's trap cleans up tempfiles on exit)
- `sshd auth.log` — `Accepted publickey` lines present; the `SetEnv` var name (`IBMCLOUD_API_KEY`) is logged but **not** the value
- `~/.roksbnkctl/*/state/*.log` host-side logs — no API key value

The audit is the single most important gate on the v1.0 release. A leak in any of M1-M7 is a stop-ship. See [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) for the threat model.

### Phase N — mixed-mode lifecycle

A realistic scenario: workspace config routes each tool to its preferred backend, then a full `up` + `test` + `down` cycle runs end-to-end. Concretely, `exec.terraform=local`, `exec.ibmcloud=ssh:jumphost`, `exec.iperf3=k8s` — three different backends in one lifecycle. The phase asserts state is preserved across the per-tool dispatch (the workspace's terraform state file is touched only by the local-backend terraform; the API key projected into the k8s ops pod is the same one resolved for the SSH dispatch) and that `down` cleanly destroys everything.

## How CI runs it

`.github/workflows/ci.yml` runs **unit + integration** on every PR — `go test ./...` plus the testcontainers-go-backed integration tests. The full e2e suite is too expensive (4-6 hours, $5-10 of IBM Cloud spend per run) to gate on every PR.

A separate **manual-trigger workflow** runs `scripts/e2e-test-full.sh` on demand and on release branches. The workflow is dispatched via the GitHub Actions UI ("Run workflow") and stamps the resulting log artefacts onto the workflow run. See [`.github/workflows/e2e-full.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/e2e-full.yml) for the workflow YAML — the workflow accepts optional `cluster_region` + `teardown_on_success` inputs and runs automatically on every `release/**` branch push.

The release-cut policy is: don't tag `vX.Y.Z` until the most recent manual-trigger run on the release branch is green for **three consecutive nights**. This catches the flakes that don't reproduce locally — most of which are IBM Cloud control-plane blips rather than real regressions.

## Cost and time

A full `scripts/e2e-test-full.sh` run currently costs:

| Resource | Approximate cost (USD) |
|---|---|
| ROKS cluster (3 workers, ~5 hours uptime) | $3-6 |
| 1-2 LoadBalancer Service objects (for north-south throughput) | $0.50-1 |
| COS instance + objects for the supply chain | $0.10-0.20 |
| Egress bandwidth (throughput tests, image pulls) | $0.20-0.50 |
| **Total per run** | **$5-10** |

Per-phase time estimates:

| Phase | Wall time |
|---|---|
| A (init) | <1 minute |
| B (plan) | 1-2 minutes |
| C (targets / register) | <1 minute |
| D (up) | **30-50 minutes** (the dominant cost) |
| E (post-apply checks) | 1-2 minutes |
| F (connectivity) | 1-2 minutes |
| G (throughput) | 1-3 minutes |
| H (down) | **15-25 minutes** |
| I (SSH backend) | 2-3 minutes |
| J (kubectl internal) | 3-5 minutes |
| K (docker backend) | 3-5 minutes (first call pulls the image, +1-2 minutes) |
| L (k8s backend + ops pod) | 3-5 minutes |
| L-DNS (DNS probe + GSLB compare) | 2-4 minutes |
| M (cred audit) | <1 minute |
| N (mixed-mode lifecycle) | 30-50 minutes (full up + down again) |
| **Total** | **~4-6 hours** |

Phase N is the second-dominant cost — it runs a complete up/down cycle on top of D's. Contributors who want a shorter test loop should skip N (`PHASE_FROM=` past it) and rely on D + I-M coverage; full N is a release-gate concern, not a per-PR concern.

## Re-runnability

Every phase is re-runnable via `PHASE_FROM=`. The driver scripts are idempotent in two senses:

1. **Phase ordering**: phases later than `PHASE_FROM=<X>` run unconditionally; phases at or before `X` are skipped. The script doesn't try to remember whether earlier phases succeeded — that's the user's job (the per-phase log files are the evidence).
2. **Per-phase actions**: each phase's individual `step` calls are themselves idempotent where possible. `roksbnkctl up` on an already-applied workspace is a no-op (terraform plan reports zero changes). `roksbnkctl ops install` on an already-installed cluster is a no-op (the namespace + RBAC exist). The redactor + cred-resolver short-circuit cleanly on repeated invocations.

External-API steps include jitter+retry per [PRD 05 §"Risks"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md): DNS resolvers occasionally return SERVFAIL on first query and succeed on the second, IBM IAM occasionally 5xxs during high-load periods, and the in-cluster ops pod can take a few seconds to be `Running` after `ops install` returns. Each of these is retried with a short exponential-backoff jitter rather than failing the phase.

The intended workflow on a flake is:

```bash
# Phase L flaked on "ops pod not yet Running"
# Re-run from L; everything before is preserved
PHASE_FROM=L ./scripts/e2e-test-backends.sh
```

If the same phase flakes on consecutive `PHASE_FROM=` runs, it's a real bug — open an issue with the per-phase log attached.

## Cross-references

- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — symptom → root cause → fix entries for the failure modes phase D through phase N can surface.
- [Chapter 17 — Execution backends](./17-execution-backends.md) — the four-backend matrix that phases I, K, L exercise.
- [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) — what phase L installs and what RBAC it carries.
- [Chapter 21 — DNS testing for GSLB](./21-dns-testing-gslb.md) — the probe behaviour phase L-DNS exercises.
- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — the iperf3 fixture phase G uses.
- [PRD 05](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md) — the design spec for the suite.
