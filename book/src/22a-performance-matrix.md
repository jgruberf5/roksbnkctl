# The performance matrix

[Throughput testing](./22-throughput-testing.md) runs **one** measurement: an iperf3 client against an iperf3 server, picked by a couple of flags. `roksbnkctl test matrix` runs the **whole grid** — a declarative file of cells (endpoint-pair × test-family), each one a row in a single diffable report. `test throughput` is one cell; `test matrix` is the campaign. They belong adjacent: reach for `test throughput` to debug a single path, for `test matrix` to characterise a deployment the way the hand-run BNK-on-ROKS perf plan does — CPS / TPS / throughput across locality, regenerated on every BNK/ROKS revision instead of remembered as a dozen `oc get po -owide | grep … | iperf3 …` incantations.

This chapter is the user-facing surface: the two test families, the `matrix.yaml` schema, the fixtures-and-no-Terraform boundary, and the `--dry-run` → real-run → report workflow.

## What it is, and one honest divergence

The matrix turns the hand-run plan into one `matrix.yaml` plus one command, emitting a report on the `roksbnkctl.v1` schema so a campaign's numbers diff cleanly run-over-run. It runs against an **already-deployed** environment — a complete Cluster + BNK deploy, with the [Testing phase](./08a-three-phase-lifecycle.md) jumphost rig up, and (for the L7 family) the [Gateway phase](./10-deploying-bnk-trials.md) listeners in place. It is a `test` subcommand (it *runs probes*), disjoint from `roksbnkctl testing up/down` (which *provisions the rig*).

One divergence from the source plan, stated plainly so you don't read numbers that aren't there:

> **It's h2load, not OSLO.** The L7 family is driven by **h2load** (the nghttp2 load generator), not the OSLO tool the original plan used. h2load's native report gives **req/s**, a **transfer rate**, and request-time **min / max / mean** — *not* OSLO's CPS/TPS split or p50/p95/p99 percentiles. The matrix surfaces the honest h2load fields (`req_per_sec`, `throughput_mbps`, `req_time_{min,max,mean}_ms`) and **does not fabricate percentiles**. The cps / tps / throughput *modes* below are h2load flag presets shaped to *resemble* the plan's three measurement intents — they are not the plan's exact metrics. Read "CPS" as "connection-bound req/s," "TPS" as "transaction-bound req/s."

> **Live-apply is not yet validated.** The `--dry-run` plan is cluster-free and exercised in CI; the transcript below is captured verbatim from the shipped binary. The **live fixture-apply + run path has not yet been validated against a real ROKS cluster.** Treat any throughput / req/s figures in this chapter as **illustrative**. The same caveat is in the CHANGELOG.

## The two families

A cell's `family` is one of `iperf3` (L4) or `l7` (L7). The runner resolves each cell to a backend + a tool argv and dispatches it exactly like `test throughput` does — local, or `ssh:<jumphost>` (see [§ The locality axis](#the-locality-axis-jumphost-placement)).

### `iperf3` — L4 TCP throughput

iperf3 over a **TCPRoute VIP** (a `host:port` `address` endpoint). The content-size axis is the iperf3 `-l` block-size knob via the cell's `length:` field: `"128"` and `"512K"` are the L4 analog of the plan's 128 B / 512 KB payload axis. Other knobs map straight to iperf3 flags:

| Cell field | iperf3 flag | Meaning |
|---|---|---|
| `length` | `-l` | block size (`"128"`, `"512K"`) |
| `bytes` | `-n` | fixed transfer instead of a timed run |
| `duration` | `-t` | run length in seconds |
| `streams` | `-P` | parallel TCP streams |

A cell resolves to, e.g., `iperf3 -c 10.240.0.10 -J -p 5201 -t 30 -P 8 -l 512K`. The result carries `throughput_gbps` + `retransmits` (same parser as [Chapter 22](./22-throughput-testing.md)), plus `length` for the report.

### `l7` — h2load over an HTTPRoute

h2load against a `url` endpoint. The scheme selects the path: an `http://` URL exercises the cleartext HTTPRoute, an `https://` URL the **TLS-terminate-at-TMM** path (h2load speaks TLS off the scheme and doesn't hard-fail on the self-signed leaf a TMM-internal terminate presents — no TLS toggle needed). Payload (128 B / 512 KB) is the **URL path** the nginx fixture serves — `/128`, `/5k`, `/512k` — so you define one endpoint per payload+scheme.

The cell's `l7.mode` is a preset that seeds h2load flags; any low-level knob you set explicitly wins. The three modes and how they map to `-c` (clients) / `-m` (streams) / `--h1` / `-n` (requests) / `-D` (duration):

| Mode | Intent | Preset (when knobs unset) |
|---|---|---|
| `cps` | connection-bound — the plan's "CPS @ 1 request/connection" | `--h1`, `-m 1`, `-c 50`, `-n` = clients×200 (each conn serves few requests, so req/s ≈ conn/s) |
| `tps` | transaction-bound — the plan's "TPS @ 100 requests/connection" | `-c 50`, `-m 100` (h2) or `1` (`--h1`), `-n` = clients×2000 (reuse connections) |
| `throughput` | payload-bound — the plan's "throughput @ 512 KB" | `-c 8`, `-m 1`, `-n` = clients×200 (a few conns pulling a large body) |

A `cps` cell resolves to `h2load -c 50 -m 1 --h1 -n 10000 http://10.240.0.10/128`; a timed `tps` cell with `l7: { mode: tps, duration: 30 }` to `h2load -c 50 -m 100 -D 30 …`. Per-cell low-level fields (`clients`, `streams`, `threads`, `requests`, `duration`, `http1`) override the preset. The result carries `req_per_sec`, `throughput_mbps`, `requests_total` / `succeeded` / `failed`, status-code buckets, and `req_time_{min,max,mean}_ms` — the honest h2load surface, no percentiles.

## The locality axis = jumphost placement

There is **no locality enum.** Same-zone / different-zone / different-VPC is whichever `vsi` endpoint a cell names as its `client` — the topology is whatever the chosen jumphost actually is, which keeps the schema honest. A `vsi` endpoint's `target` is an SSH target name the [Testing phase auto-registered](./15-ssh-targets.md#auto-discovery-from-terraform-outputs): the singular `jumphost` (the TGW / client-VPC VSI — "different VPC"), or a per-AZ `jumphost-<zone>` (same VPC; same zone as TMM, or a different one). So:

| To measure | Name as the cell's client |
|---|---|
| same VPC, same zone as TMM | `jumphost-<tmm-zone>` |
| same VPC, different zone | `jumphost-<other-zone>` |
| different VPC | `jumphost` (the TGW jumphost) |

The runner turns the client endpoint into the backend `ssh:<target>` and dispatches the tool there — see [Chapter 15](./15-ssh-targets.md) and [Chapter 16](./16-on-flag-ssh-jumphosts.md). Both generators are **preinstalled on every jumphost** by the Testing-phase `user_data` (`iperf3` + `nghttp2-client`/h2load), so the `ssh:<target>` runs need **no `--bootstrap`**. A cell with no `client` runs the tool locally.

## The `matrix.yaml` schema

A workspace-sibling file — under `~/.roksbnkctl/<workspace>/`, next to `config.yaml`, **not part of it** (the grid is large, churns independently of deploy config, and you want to diff it in git per-campaign). The runner looks for `--file`, then `<workspace>/matrix.yaml`, then `./matrix.yaml`. It has four top-level keys, walked off the shipped `internal/test/testdata/matrix.example.yaml`:

```yaml
# gateway — IDENTITY of your already-deployed BNK gateway stack. Used ONLY
# when fixtures.routes is true, to attach route fixtures to the EXISTING
# Gateway. The runner adds Routes, never listeners.
gateway:
  app_namespace: bnk-apps     # namespace the fixtures + routes land in
  name: bnk-gateway           # the existing Gateway object (parentRefs.name)
  http_section: http          # an HTTP  listener section that already exists
  https_section: https        # an HTTPS (TLS) listener section
  tcp_section: tcp            # a TCP  listener section (for the L4 TCPRoute)

# fixtures — the ephemeral, runner-owned k8s objects (all torn down after,
# unless --keep). These are the ONLY cluster writes the matrix performs.
fixtures:
  iperf3_server: true         # L4 server (the throughput fixture) — TCPRoute backend
  http_backend: true          # nginx serving /128 /5k /512k — L7 backend
  routes: true                # apply TCPRoute + HTTPRoute(+TLS) attaching to the gateway

# endpoints — named (placement, role) anchors the cells reference.
endpoints:
  vsi-same-zone: { kind: vsi, target: jumphost-jp-osa-1 }       # client in TMM's zone
  vsi-diff-zone: { kind: vsi, target: jumphost-jp-osa-3 }       # another zone, same VPC
  vsi-diff-vpc:  { kind: vsi, target: jumphost }                # the TGW/client VPC
  tmm-tcp:       { kind: address, host: 10.240.0.10, port: 5201 }  # TCPRoute VIP:port
  tmm-http-128:  { kind: url, url: "http://10.240.0.10/128"  }
  tmm-https-512k:{ kind: url, url: "https://10.240.0.10/512k" }  # TLS terminate at TMM

# cells — the grid. Each is one row in the report.
cells:
  - { name: "L4 512K VSI->TMM diff-zone", family: iperf3, client: vsi-diff-zone, server: tmm-tcp, length: "512K", duration: 30, streams: 8 }
  - { name: "L7 http CPS 128B",  family: l7, client: vsi-diff-vpc, server: tmm-http-128,  l7: { mode: cps } }
  - { name: "L7 https THRU 512K", family: l7, client: vsi-diff-vpc, server: tmm-https-512k, l7: { mode: throughput, duration: 30 } }
```

### `gateway:` — the existing-stack identity

Names the already-deployed gateway so the route fixtures can attach to it. Used **only** when `fixtures.routes` is true.

| Field | Notes |
|---|---|
| `app_namespace` | namespace the fixtures and routes are created in (required for `routes`) |
| `name` | the existing `Gateway` object the routes' `parentRefs.name` point at (required for `routes`) |
| `http_section` / `https_section` / `tcp_section` | listener `sectionName`s on that Gateway the http / https / tcp routes bind to. **They must already exist** — the runner adds Routes, never listeners. A section left empty means "don't render that route." |
| `class_name` / `controller_name` / `bnkgateway_name` / `flo_namespace` | optional descriptive identity fields; not required to attach routes |

### `endpoints:` — named anchors

| `kind` | Fields | Resolves to |
|---|---|---|
| `vsi` | `target` | an `ssh:<target>` jumphost — a traffic-source "VSI" (the client side) |
| `address` | `host`, `port` (default `5201`) | an iperf3 TCP server, e.g. a TCPRoute VIP (an `iperf3` cell's `server`) |
| `url` | `url` | a full `http(s)://` URL — an HTTPRoute target (an `l7` cell's `server`) |

### `cells:` — the grid

| Field | Applies to | Notes |
|---|---|---|
| `name` | both | required; the report row label and the `--only` glob target |
| `family` | both | `iperf3` \| `l7` |
| `client` | both | an endpoint key of kind `vsi`; empty → run locally |
| `server` | both | an endpoint key: kind `address` for `iperf3`, kind `url` for `l7` |
| `length` / `bytes` / `duration` / `streams` | `iperf3` | iperf3 `-l` / `-n` / `-t` / `-P` |
| `l7.mode` | `l7` | `cps` \| `tps` \| `throughput` (required for `l7`) |
| `l7.{clients,streams,threads,requests,duration,http1}` | `l7` | h2load `-c` / `-m` / `-t` / `-n` / `-D` / `--h1`; override the mode preset |

`ParseMatrix` validates structurally before any run: a `client` must be a `vsi` with a `target`; an `iperf3` server must be an `address` with a `host`; an `l7` server must be a `url` with a `mode`; `fixtures.routes` requires `gateway.name` + `app_namespace` + at least one listener section.

## Fixtures and the no-Terraform boundary

The runner owns **only ephemeral objects** and **changes no Terraform**. The route fixtures **attach to your existing Gateway by name** — the runner never adds listeners and never touches the gateway phase. Three `fixtures` toggles, all skipped under `--dry-run`:

- **`iperf3_server`** — the L4 server (the same fixture `test throughput` deploys, `roksbnkctl-iperf3:5201`); the TCPRoute's backend.
- **`http_backend`** — an nginx Deployment + Service that serves fixed-size bodies at `/128`, `/5k`, `/512k` (generated at startup from `/dev/zero`); the L7 cells select a payload by URL path.
- **`routes`** — a TCPRoute, an HTTPRoute, and (when `https_section` is named) a TLS HTTPRoute plus a self-signed TLS Secret, each with `parentRefs` pointing at your Gateway's named listener sections.

Every fixture object carries `app.kubernetes.io/managed-by: roksbnkctl-matrix`, so teardown after the run (unless `--keep`) is a single label-selected delete per resource kind. The self-signed cert is an ECDSA P-256 leaf valid for a year, generated per-run — fine for a load test, which never validates the chain.

**The prerequisite this implies:** the Gateway must already expose the `http` / `https` / `tcp` listener sections the routes bind to. The runner only adds Routes + its own backend + the TLS Secret; **listener provisioning stays with you** (the [Gateway phase](./10-deploying-bnk-trials.md)). If a named section doesn't exist on the Gateway, the route attaches to nothing and the cell's traffic has no path. Use `--keep` to leave the fixtures up and inspect the route status after a run.

## The workflow

### `--dry-run` — plan + fixtures, no cluster calls

`--dry-run` expands the grid and prints the resolved (client backend, server, argv) per cell, plus the fixtures manifest it *would* apply — **without a single cluster call** (it never touches the cluster, so it's safe to capture verbatim). Confirm placement and the exact tool invocations before a long campaign:

```text
$ roksbnkctl test matrix --dry-run --file matrix.example.yaml
→ matrix file: matrix.example.yaml
matrix plan — 10 cell(s)

[1] L4 128B  VSI->TMM same-zone
    family : iperf3
    client : ssh:jumphost-jp-osa-1
    server : 10.240.0.10:5201
    run    : iperf3 -c 10.240.0.10 -J -p 5201 -t 30 -l 128

[2] L4 512K  VSI->TMM same-zone
    family : iperf3
    client : ssh:jumphost-jp-osa-1
    server : 10.240.0.10:5201
    run    : iperf3 -c 10.240.0.10 -J -p 5201 -t 30 -P 8 -l 512K

[5] L7 http CPS  128B VSI->TMM
    family : l7
    client : ssh:jumphost
    server : http://10.240.0.10/128
    run    : h2load -c 50 -m 1 --h1 -n 10000 http://10.240.0.10/128

[8] L7 https CPS  128B VSI->TMM
    family : l7
    client : ssh:jumphost
    server : https://10.240.0.10/128
    run    : h2load -c 50 -m 1 --h1 -n 10000 https://10.240.0.10/128

--- fixtures (would apply) ---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: matrix-http-backend
  namespace: bnk-apps
  labels:
    app.kubernetes.io/managed-by: roksbnkctl-matrix
...
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: matrix-httproute
  namespace: bnk-apps
spec:
  parentRefs:
  - name: bnk-gateway
    namespace: bnk-apps
    sectionName: http
  rules:
  - backendRefs:
    - name: matrix-http-backend
      port: 80
...
```

*(Cells and the fixtures manifest abridged; the full plan prints all ten cells of the example grid and the complete TCPRoute / HTTPRoute / TLS-Secret stream.)*

`--only '<glob>'` narrows the plan (or a real run) to cells whose name matches a `path.Match` glob — `--only 'L7*'` for just the L7 rows, handy when iterating on one family.

### The real run and the report

Drop `--dry-run` against a complete deployment. The runner deploys the fixtures, runs each cell over its resolved backend, tears the fixtures down (unless `--keep`), and emits the report. `-o` selects the shape:

- **`-o text`** (default) — the Markdown grid on stderr.
- **`-o md`** — the Markdown grid on stdout. iperf3 rows show Gbit/s + retransmits; l7 rows show req/s + Mbit/s + mean ms:

  ```markdown
  ## matrix (pass, 184230ms)

  | Cell | Family | Status | Throughput | req/s | mean ms | notes |
  |---|---|---|---|---|---|---|
  | L4 512K VSI->TMM diff-zone | iperf3 | pass | 9.21 Gbit/s | — | — | 312 retransmits |
  | L7 http CPS 128B VSI->TMM  | l7     | pass | 18.4 Mbit/s | 21340 | 2.34 | — |
  ```

  *(Numbers illustrative — the live run path is not yet validated; see the caveat above.)*

- **`-o json`** — additive `roksbnkctl.v1`: a `MatrixRun` with one `ProbeResult` per cell, each carrying its family-specific `Extra` (plus `client` / `server` labels). Exit code is **non-zero iff any cell failed** (CI-friendly):

  ```json
  {
    "schema": "roksbnkctl.v1",
    "command": "test",
    "suite": "matrix",
    "cells": [
      {
        "suite": "l7",
        "name": "L7 http CPS 128B VSI->TMM",
        "status": "pass",
        "extra": {
          "req_per_sec": 21340.0,
          "throughput_mbps": 18.4,
          "req_time_mean_ms": 2.34,
          "client": "ssh:jumphost",
          "server": "http://10.240.0.10/128"
        }
      }
    ],
    "overall": "pass"
  }
  ```

### Flag summary

| Flag | Effect |
|---|---|
| `--file <path>` | the matrix.yaml (default: `<workspace>/matrix.yaml`, then `./matrix.yaml`) |
| `--only <glob>` | run only cells whose name matches the glob |
| `--dry-run` | expand + print plan + fixtures; no cluster calls |
| `--keep` | leave the fixtures running after the run |
| `-o json\|text\|md` | report shape |

## Cross-references

- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — the single-cell iperf3 suite the matrix's `iperf3` family re-composes; start here for the server-pod / SCC mechanics.
- [Chapter 15 — SSH targets](./15-ssh-targets.md#per-az-cluster-jumphosts-jumphost-zone) — the `jumphost` / `jumphost-<zone>` targets the locality axis resolves to.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md#per-az-cluster-jumphosts) — the per-AZ jumphost fleet (preinstalled iperf3 + h2load).
- [Chapter 17 — Execution backends](./17-execution-backends.md#ssh-backend) — how the `ssh:<target>` dispatch + the bundled tools images work.
- [Chapter 28 — Configuration reference §"matrix.yaml"](./28-configuration-reference.md#matrixyaml-the-performance-grid) — the schema as a reference table.
- [Chapter 27 — Command reference §`roksbnkctl test matrix`](./27-command-reference.md) — the generated flag surface.
- [PRD 10 — the performance test matrix](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/10-PERF-TEST-MATRIX.md) — the design (this ships its in-scope subset; h2load substituted for OSLO).
