# PRD 10 — Repeatable BNK-on-ROKS performance test matrix

> Prerequisites: [PRD 03 (execution backends)](./03-EXECUTION-BACKENDS.md) — the `local | docker | k8s | ssh:<target>` `Backend` interface this builds on; [PRD 09 (auto cluster jumphosts)](./09-AUTO-CLUSTER-JUMPHOSTS.md) — the per-AZ `jumphost-<zone>` SSH targets that are this matrix's "VSI" fleet; the Sprint 28 three-phase split — this design assumes the **Testing phase** (`state-testing/`) has provisioned the jumphost rig and all `up` phases (Cluster / BNK / Testing) are complete.
>
> Estimated effort: large (~2000–2800 LOC across a new matrix runner, an L7 load-gen integration, and a tmctl sampler); 3–4 weeks, stageable across sprints (see §"Staged delivery").
>
> Status: **design only.** No code is proposed for landing by this document — it captures the model, the target taxonomy, the gap analysis, and the work needed so the matrix can be slotted into a future sprint.

## Goal

Turn the ad-hoc "BNK on ROKS Testing" result grid (the hand-run iperf3 / L7 / `tmctl` campaign captured in the source PDF) into a **declarative, repeatable** roksbnkctl capability: one YAML grid + one command (`roksbnkctl test matrix`) that runs the whole campaign against an already-deployed environment and emits a PDF-shaped, machine-readable report — so the same numbers can be regenerated on every BNK/ROKS revision without an operator remembering twelve `oc get po -owide | grep ... | iperf3 ...` incantations.

## Why

The source document is a **performance characterization matrix** — a grid of throughput and TMM-CPU results across four independent axes:

| Axis | Values observed in the source campaign |
|---|---|
| **Endpoint pair** | VSI↔TMM (VIP), VSI↔VSI, pod↔pod, TMM↔app-pod, VSI↔app-pod (via NodePort / TMM-virtual / IPvLAN), VSI↔headless-endpoint |
| **Network locality** | different VPCs, same VPC / different zones, same VPC / same zone, cross-region (JP-TOK ↔ JP-OSA) |
| **VSI instance profile** | default vs `bxf-16x64` (16 vCPU / 64 GB) |
| **Test family** | iperf3 TCP throughput; L7 HTTP (CPS / TPS / payload-throughput); egress TCP via TMM |
| **Instrumentation** | TMM CPU / memory / conns sampled every 2 s via `tmctl -d blade tmm_stat -s cpu_usage_15secs,memory_total,memory_used,client_side_traffic.cur_conns` |

Today this is reproduced by hand. That is slow, non-repeatable, error-prone (the placement of each iperf3 server pod is chosen by `oc get po -owide` eyeballing), and it produces numbers that can't be diffed run-over-run. roksbnkctl already owns the rig (the Testing phase provisions the VSIs; PRD 03 owns the iperf3 engine and its backends) — the missing piece is a **driver that declares the grid once and folds the TMM CPU column in automatically.**

## Scope

This PRD targets a **representative subset** of the source grid (integrator decision — fastest path to a repeatable run; the deferred axes are additive and called out below).

### In scope

- A declarative matrix file (`matrix.yaml`) describing the grid: cells = (endpoint-pair × locality × test-family), plus per-cell duration/streams/payload knobs.
- A `roksbnkctl test matrix` runner that executes every cell and aggregates into one report.
- **Three test families**, all three confirmed in scope:
  1. **iperf3 TCP throughput matrix** — VSI↔VSI, VSI↔TMM-VIP, pod↔pod (zone-pinned), TMM↔app-pod, egress (pod→VSI via TMM).
  2. **L7 HTTP CPS / TPS / payload-throughput** — the Ingress L7 row (connections/sec at 1 RPC·128 B; transactions/sec at 100 RPC·128 B; throughput at 1 RPC·512 KB and 1 RPC·5 KB).
  3. **TMM CPU / memory / conns capture** — a `tmctl` sampler that runs *concurrently with each cell* and folds CPU% / mem / cur_conns into that cell's result, reproducing the PDF's right-hand columns.
- **Locality axis (subset):** same-VPC/same-zone, same-VPC/different-zone, different-VPC. Driven entirely off the existing per-AZ jumphost targets + the BNK service endpoints.
- A **report** shaped like the source grid: one row per cell, columns Throughput / CPU% / (CPS|TPS) — both JSON (additive on `roksbnkctl.v1`) and a text/Markdown table.

### Out of scope (deferred — additive later)

- **Cross-region** cells (JP-TOK ↔ JP-OSA). Requires the Testing phase to stand up jumphosts in a *second* region; today `testing_client_vpc_region` is a single value (`terraform/modules/testing/variables.tf:108`). Tracked as a follow-on once multi-region jumphosts exist.
- **`bxf-16x64` profile variant** rows. Requires running the same cell against a second jumphost fleet provisioned at a larger profile (`testing_jumphost_profile`). The matrix model below leaves a `profile:` slot per target group so this drops in without a schema change.
- The **IPvLAN-interface** and **headless-endpoint** VSI↔app-pod endpoint variants. NodePort and TMM-virtual VIP endpoints are in scope (they're the common cases); the other two need BNK-side fixture wiring and are deferred.
- Any change to the Testing-phase Terraform. This PRD consumes the rig as-is.

## Background — what already exists (and what's missing)

Grounding the gap analysis in the current tree:

**Reusable as-is:**

- **The iperf3 engine + backends.** `internal/test/throughput.go` runs `iperf3 -c <endpoint> -J` and parses `end.sum_received`; `internal/cli/test.go:517` (`runTestThroughputCmd`) dispatches the *client* across `local | k8s | ssh:<target>` and deploys the server in-cluster. The `Backend` interface (PRD 03) already gives us "run this tool from that network location."
- **The VSI fleet = jumphost targets.** The Testing phase provisions a TGW jumphost (client VPC) and one cluster jumphost per AZ, auto-registered as SSH targets `jumphost` and `jumphost-<zone>` from the `{zone => fip}` Terraform output (`internal/orchestration/lifecycle.go:677` / `:728`). These *are* the matrix's VSIs — `ssh:jumphost-<zone>` already addresses "a VSI in zone Z."
- **k8s exec** (`internal/cli/k_exec.go`) — the hook for running `tmctl` inside the TMM pod.
- **The result schema** (`internal/test/result.go`) — `SuiteRun` / `ProbeResult` with a free-form `Extra map[string]any`. The matrix report is additive on this (`SchemaVersion = "roksbnkctl.v1"` is preserved; new fields only).

**Gaps (the work this PRD scopes):**

1. **No VSI↔VSI or zone-pinned pod↔pod cell.** `runTestThroughputCmd` always deploys exactly one iperf3 *server* in k8s and runs the *client* elsewhere. It can't put the server on a named jumphost (VSI↔VSI), and its "east-west" is host→ClusterIP, **not** true pod-to-pod with node/zone affinity (`internal/cli/test.go:623`, and the comment there says as much). The matrix needs arbitrary (client-placement, server-placement) pairs.
2. **No endpoint taxonomy.** NodePort / TMM-virtual VIP / IPvLAN / headless aren't selectable; only LoadBalancer (north-south) and ClusterIP (east-west) are.
3. **No L7 load generator.** Nothing in the tree emits CPS / TPS / payload-throughput. Needs an HTTP/2-capable generator (candidates §"L7 family").
4. **No TMM CPU capture.** Nothing samples `tmctl` during a run and folds it into the result.
5. **No grid driver.** No way to declare the campaign once and replay it; `test throughput` is one cell per invocation.

## Design

### 1. The matrix model

A workspace-local `matrix.yaml` (under `~/.roksbnkctl/<workspace>/`, sibling to `config.yaml`) declares the grid. The runner expands it into cells and executes each. Sketch:

```yaml
# Endpoint anchors — named (placement, role) pairs the cells reference.
# "VSI" anchors resolve to ssh:<target> jumphosts already registered by
# the Testing phase; "pod"/"tmm"/"svc" anchors resolve in-cluster.
endpoints:
  vsi-osa1:   { kind: vsi, target: jumphost-jp-osa-1 }      # ssh:<target>
  vsi-osa3:   { kind: vsi, target: jumphost-jp-osa-3 }
  vsi-client: { kind: vsi, target: jumphost }               # TGW/client-VPC VSI
  app-pod-a:  { kind: pod, namespace: app-ns, selector: app=iperf3, zone: jp-osa-2 }
  app-pod-b:  { kind: pod, namespace: app-ns, selector: app=iperf3, zone: jp-osa-3 }
  tmm-vip:    { kind: tmm-virtual, namespace: app-ns, virtual: app-vs }   # TMM VIP
  app-nodeport: { kind: nodeport, namespace: app-ns, service: iperf3-np }

# Cells — the grid. Each is one row in the report.
cells:
  - name: "VSI↔TMM, same VPC/zone"
    family: iperf3
    client: vsi-osa1
    server: tmm-vip
    duration: 60
    streams: 1
    capture_tmm: true            # fold tmctl CPU/mem/conns into this row

  - name: "pod↔pod, same VPC/diff zone"
    family: iperf3
    client: app-pod-a
    server: app-pod-b
    duration: 10

  - name: "Ingress L7 HTTP — CPS"
    family: l7
    client: vsi-client
    server: tmm-vip
    l7: { mode: cps, rps: 0, payload: 128B, connections: 1, duration: 30 }
    capture_tmm: true

  - name: "Ingress L7 HTTP — TPS"
    family: l7
    client: vsi-client
    server: tmm-vip
    l7: { mode: tps, requests_per_conn: 100, payload: 128B, duration: 30 }
    capture_tmm: true

  - name: "Egress TCP — pod → remote VSI via TMM"
    family: iperf3
    client: app-pod-a
    server: vsi-client           # iperf3 server runs ON the VSI (reverse of today)
    via: tmm                      # path assertion / documentation
    capture_tmm: true
```

**Why a separate file (not `config.yaml`):** the grid is large, churns independently of deploy config, and is the kind of thing an operator wants to diff in git per-campaign. `config.yaml` stays deploy-shaped. The locality axis (same-zone / diff-zone / diff-VPC) is *implicit* in which `endpoints` a cell references — no separate locality enum needed, which keeps the schema honest (the topology is whatever the chosen jumphosts/pods actually are).

### 2. Endpoint resolution & the placement matrix

The core new primitive is **(client placement) × (server placement)**, generalizing today's "server-in-k8s, client-elsewhere." Resolution per `kind`:

| `kind` | Client side (run iperf3/L7 client here) | Server side (iperf3 server / HTTP target) |
|---|---|---|
| `vsi` | `ssh:<target>` backend (PRD 03 SSH) | iperf3 `-s` started on the VSI over SSH (new: reverse server placement) |
| `pod` | in-cluster client Job, **node-affinity pinned to `zone`** (new) | iperf3 server Deployment pinned to `zone` (new affinity) |
| `tmm-virtual` | n/a (server-only anchor) | the TMM Virtual Server VIP (resolve from BNK CR / service) |
| `nodeport` | n/a (server-only) | `<nodeIP>:<nodePort>` of the named service |

The two genuinely new capabilities behind the table:
- **iperf3 server on a VSI** (over SSH) — so VSI↔VSI and egress (pod→VSI) cells work. Mirror the existing SSH client path in `runIperf3ClientSSH` (`test.go:702`) but start `iperf3 -s -D` (or one-shot) on the target first, then run the client against its private IP.
- **Zone-pinned pod placement** — node affinity / `topology.kubernetes.io/zone` on the iperf3 server Deployment and client Job, so "same zone" vs "different zone" pod↔pod cells are deterministic instead of scheduler-luck. Extends `internal/k8s` `Iperf3Options`.

### 3. iperf3 family

Largely a re-composition of existing parts: for each iperf3 cell, resolve client+server endpoints, ensure a server is listening at the server endpoint (deploy in-cluster *or* start over SSH on a VSI), then run the client via the appropriate backend and parse `end.sum_received` (reusing `parseIperf3JSON`, `throughput.go:96`). The result already carries `throughput_gbps` + `retransmits` in `Extra`.

### 4. L7 family (new dependency)

The Ingress L7 row (CPS / TPS / payload-throughput) needs an HTTP load generator — none exists today. Requirements: HTTP/1.1 + HTTP/2, controllable connections/sec vs requests-per-connection (to separate CPS from TPS), fixed payload sizes, JSON output.

Candidate generators (decision deferred to the implementing sprint):

| Tool | Pros | Cons |
|---|---|---|
| **nighthawk** (Envoy project) | Purpose-built for CPS vs RPS separation; rich JSON | Heavier image; build/version pinning |
| **wrk2** | Tiny; constant-throughput; well understood | Lua for JSON; HTTP/1 only |
| **h2load** (nghttp2) | HTTP/2 native; CPS + TPS via `-c`/`-m`/`-n` | Output parsing less structured |

Integrate the chosen tool **exactly like iperf3** — as a `Backend`-dispatched external tool (PRD 03), with a bundled image for the k8s/ssh backends, run from the client VSI (`ssh:jumphost`) against the TMM VIP. The four PDF sub-cells map to generator flags:
- **CPS** (1 RPC, 128 B) → 1 request per connection, new conn each time.
- **TPS** (100 RPC, 128 B) → 100 requests per kept-alive connection.
- **Throughput** (1 RPC, 512 KB / 5 KB) → large/medium payload, measure MB/s.

A new `internal/test/l7.go` parses the generator's JSON into `Extra{cps, tps, throughput_mbps, p50/p95/p99_ms}`.

### 5. TMM CPU/mem capture

A sampler that runs **concurrently with each cell** and folds the result into that cell's row (the PDF's right-hand columns). Mechanism:

1. Resolve the TMM pod (`oc get po -n <ns> -l <tmm-selector>` — the PDF does `grep tmm` against the cluster default worker; we resolve by label via the existing `internal/k8s` client).
2. On a goroutine, exec into the TMM pod and run `tmctl -d blade tmm_stat -s cpu_usage_15secs,memory_total,memory_used,client_side_traffic.cur_conns` on a 2 s tick (matching the PDF's `watch -n2`), via the k8s exec path (`internal/cli/k_exec.go`).
3. Collect samples for the cell's duration; reduce to `{cpu_pct_max, cpu_pct_mean, mem_used_max, cur_conns_max}`.
4. Fold into the cell's `ProbeResult.Extra` under `tmm:{...}`.

`capture_tmm: true` per cell gates this (the pod↔pod and VSI↔VSI cells don't traverse TMM, so they leave it off — matching the PDF, where those rows have `-` in the CPU column).

### 6. CLI surface

```
roksbnkctl test matrix [--file matrix.yaml] [--only <cell-glob>] [--dry-run] [-o json|text|md]
```

- `--dry-run` — expand the grid and print the resolved (client, server, backend) plan per cell **without running** (so an operator can confirm placement before a 20-minute campaign). Mirrors the `--dry-run` discipline in `scripts/e2e-three-phase.sh`.
- `--only` — run a subset (glob on cell `name`) for iterating on one row.
- Exit non-zero if any cell fails (CI-friendly, consistent with `outputSuite`/`outputAll` in `test.go`).

This is **disjoint from the Testing phase** (`roksbnkctl testing up/down`, which *provisions* the rig) and a superset of `roksbnkctl test throughput` (one cell). The disambiguation already documented in `internal/cli/testing_phase.go:19` holds: `testing` provisions, `test` runs probes — `test matrix` is a `test` subcommand.

### 7. Report shape

Additive on `roksbnkctl.v1`. A new top-level `MatrixRun` composes per-cell `ProbeResult`s (one cell = one probe), each carrying its family-specific `Extra` plus the optional `tmm` block:

```json
{
  "schema": "roksbnkctl.v1",
  "command": "test",
  "suite": "matrix",
  "cells": [
    {
      "name": "VSI↔TMM, same VPC/zone",
      "family": "iperf3",
      "status": "pass",
      "extra": {
        "throughput_gbps": 1.30,
        "client": "ssh:jumphost-jp-osa-1",
        "server": "tmm-virtual:app-vs",
        "tmm": { "cpu_pct_max": 67.23, "cpu_pct_mean": 65.1, "cur_conns_max": 89 }
      }
    }
  ],
  "overall": "pass"
}
```

The text/`md` renderer prints the PDF's table directly (Test | Throughput | CPU% | CPS/TPS), so a campaign's output is visually diffable against the source grid.

## Staged delivery

Each stage is independently shippable and leaves `test throughput` working:

| Stage | Deliverable | Rough size |
|---|---|---|
| **S1** | `matrix.yaml` schema + parser + `--dry-run` plan printer (no execution). Proves the model. | ~400 LOC |
| **S2** | iperf3 family: VSI↔VSI (SSH server placement) + zone-pinned pod↔pod + TMM-VIP/NodePort endpoints + report. | ~800 LOC |
| **S3** | TMM `tmctl` sampler folded into S2 cells. | ~350 LOC |
| **S4** | L7 family: load-gen selection + image + CPS/TPS/payload cells. | ~700 LOC |
| **S5** (deferred) | cross-region jumphosts + `bxf-16x64` profile fleet + IPvLAN/headless endpoints. | (separate PRD) |

S1–S4 deliver the in-scope representative subset. S5 is the deferred full-parity work flagged in §Scope.

## Acceptance (in-scope subset)

1. `roksbnkctl test matrix --dry-run` expands the bundled example grid and prints a resolved (client, server, backend) plan per cell with **no cloud calls**.
2. Against a complete three-phase deployment, `roksbnkctl test matrix` runs every iperf3 + L7 cell and emits a report whose rows correspond 1:1 to the grid, with TMM CPU% populated on `capture_tmm` cells.
3. A pod↔pod "same zone" cell and a "different zone" cell land on the asserted zones (placement is deterministic, not scheduler luck).
4. The L7 CPS and TPS cells produce distinct connections/sec vs transactions/sec figures (proves the generator separates the two, as the PDF does).
5. `-o json` output validates as additive `roksbnkctl.v1` (existing CI consumers unbroken); exit code is non-zero iff any cell failed.
6. Re-running the same grid on the same deployment reproduces comparable numbers (repeatability — the whole point).

## Open questions

- **L7 generator choice** — nighthawk vs h2load vs wrk2 (S4 decision; affects image size + JSON parsing).
- **TMM pod/virtual discovery** — resolve the TMM Virtual VIP and the TMM pod label from the BNK CR, or require them in `matrix.yaml`? (Auto-discovery is nicer but couples the matrix to BNK CR shape.)
- **iperf3-server-on-VSI lifecycle** — one-shot per cell vs a long-lived `-s -D` started once and reused across cells (faster, but needs teardown discipline mirroring `teardownIperf3Best`, `test.go:767`).
