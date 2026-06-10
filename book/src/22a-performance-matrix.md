# The performance matrix

[Throughput testing](./22-throughput-testing.md) runs a single measurement: one iperf3 client against one iperf3 server. `roksbnkctl test matrix` runs a whole set of them at once. You write a `matrix.yaml` listing the measurements you want — each one a row — and the command runs them all and prints the results in one report.

Reach for `test throughput` to check a single path; reach for `test matrix` to characterise a deployment — throughput, request rates, and latency from several network locations — in one repeatable command instead of a dozen steps by hand. (Don't confuse it with `roksbnkctl testing up`, which *builds* the jumphosts. `test` only runs tests.)

The matrix runs against an environment that is **already deployed**: a complete Cluster + BNK deploy with the [Testing phase](./08a-three-phase-lifecycle.md) jumphosts up, and — for the HTTP tests — the [Gateway phase](./10-deploying-bnk-trials.md) in place.

> The example numbers in this chapter are **illustrative**. The `--dry-run` plan is exercised in CI and its transcript is real, but a full live run has not yet been measured against a production ROKS cluster.

## The two families

Each row (a "cell") picks a `family`:

- **`iperf3`** — raw TCP throughput (Layer 4).
- **`l7`** — HTTP load, driven by [h2load](https://nghttp2.org/documentation/h2load-howto.html) (Layer 7).

The runner turns each cell into a tool command and runs it from wherever you point it — locally, or on a jumphost over SSH.

### `iperf3` — TCP throughput

iperf3 measures plain TCP throughput against a `host:port` target (a TCPRoute VIP). The `length` field sets the TCP block size, which stands in for message size: `"128"` for tiny messages, `"512K"` for bulk transfer. The other knobs map straight to iperf3 flags:

| Cell field | iperf3 flag | Meaning |
|---|---|---|
| `length` | `-l` | block size (`"128"`, `"512K"`) |
| `bytes` | `-n` | send a fixed number of bytes instead of running for a time |
| `duration` | `-t` | run length in seconds |
| `streams` | `-P` | parallel TCP streams |

A cell becomes, for example, `iperf3 -c 10.240.0.10 -J -p 5201 -t 30 -P 8 -l 512K`. The result reports `throughput_gbps` and `retransmits` (the same parser as [Chapter 22](./22-throughput-testing.md)).

### `l7` — HTTP load

h2load drives HTTP traffic at a URL. The scheme picks the path: an `http://` URL hits the plain HTTP route; an `https://` URL hits the TLS route, where TMM terminates TLS (h2load accepts the self-signed certificate, so there's nothing extra to configure). The response size is whatever the URL returns — the test's backend serves `/128`, `/5k`, and `/512k`, so you point a cell at the size you want.

h2load reports **requests per second**, a **transfer rate**, and request latency as **min / max / mean** (it does not report percentiles).

The `l7.mode` field is a shortcut that fills in sensible h2load flags; anything you set yourself overrides it:

| Mode | What it stresses | Defaults (when you leave the knobs unset) |
|---|---|---|
| `cps` | new connections — short connections, roughly one request each | `--h1`, `-m 1`, `-c 50`, `-n` = clients×200 |
| `tps` | sustained requests — reuse connections, many requests each | `-c 50`, `-m 100` (or `1` with `--h1`), `-n` = clients×2000 |
| `throughput` | payload — a few connections pulling a large body | `-c 8`, `-m 1`, `-n` = clients×200 |

A `cps` cell becomes `h2load -c 50 -m 1 --h1 -n 10000 http://10.240.0.10/128`; a `tps` cell written as `l7: { mode: tps, duration: 30 }` becomes `h2load -c 50 -m 100 -D 30 …`. Set any of `clients`, `streams`, `threads`, `requests`, `duration`, `http1` to override the defaults. The result reports `req_per_sec`, `throughput_mbps`, request counts (`requests_total` / `succeeded` / `failed`), status-code buckets, and `req_time_{min,max,mean}_ms`.

## The locality axis = jumphost placement

There is no "location" setting. Where the traffic comes from is simply **which jumphost a cell names as its `client`**. Each `vsi` endpoint's `target` is an SSH target name the [Testing phase registered](./15-ssh-targets.md#auto-discovery-from-terraform-outputs): `jumphost` (in the client VPC — a *different* VPC from the cluster), or `jumphost-<zone>` (in the cluster VPC, in the zone you pick).

| To measure traffic from… | Name this jumphost as the cell's client |
|---|---|
| same VPC, same zone as TMM | `jumphost-<tmm-zone>` |
| same VPC, a different zone | `jumphost-<other-zone>` |
| a different VPC | `jumphost` |

The runner runs the tool on that jumphost over SSH (see [Chapter 15](./15-ssh-targets.md) and [Chapter 16](./16-on-flag-ssh-jumphosts.md)). Both tools — iperf3 and h2load — are already installed on every jumphost, so there's nothing to set up first. A cell with no `client` runs the tool locally.

## The `matrix.yaml` schema

This is a workspace-sibling file — under `~/.roksbnkctl/<workspace>/`, next to `config.yaml` but separate from it, so you can keep and diff a campaign's grid in git on its own. The runner looks for `--file`, then `<workspace>/matrix.yaml`, then `./matrix.yaml`. It has four top-level keys, shown here against the example grid (`internal/test/testdata/matrix.example.yaml`):

```yaml
# gateway — names your already-deployed BNK gateway so the test's HTTP/TCP
# routes can attach to it. Only needed when fixtures.routes is true.
gateway:
  app_namespace: bnk-apps     # namespace the test's objects are created in
  name: bnk-gateway           # the existing Gateway the routes attach to
  http_section: http          # a listener on that Gateway, by section name
  https_section: https        # the TLS listener
  tcp_section: tcp            # the TCP listener (for the iperf3 route)

# fixtures — the temporary objects the test creates and then removes
# (kept in place if you pass --keep).
fixtures:
  iperf3_server: true         # the TCP server for the iperf3 cells
  http_backend: true          # an nginx server returning /128 /5k /512k
  routes: true                # the HTTP/TCP routes wiring the gateway to them

# endpoints — named (where, what) anchors the cells reference.
endpoints:
  vsi-same-zone: { kind: vsi, target: jumphost-jp-osa-1 }       # client in TMM's zone
  vsi-diff-zone: { kind: vsi, target: jumphost-jp-osa-3 }       # another zone, same VPC
  vsi-diff-vpc:  { kind: vsi, target: jumphost }                # the client VPC
  tmm-tcp:       { kind: address, host: 10.240.0.10, port: 5201 }  # iperf3 target
  tmm-http-128:  { kind: url, url: "http://10.240.0.10/128"  }
  tmm-https-512k:{ kind: url, url: "https://10.240.0.10/512k" }

# cells — the grid. Each is one row in the report.
cells:
  - { name: "L4 512K VSI->TMM diff-zone", family: iperf3, client: vsi-diff-zone, server: tmm-tcp, length: "512K", duration: 30, streams: 8 }
  - { name: "L7 http CPS 128B",  family: l7, client: vsi-diff-vpc, server: tmm-http-128,  l7: { mode: cps } }
  - { name: "L7 https THRU 512K", family: l7, client: vsi-diff-vpc, server: tmm-https-512k, l7: { mode: throughput, duration: 30 } }
```

### `gateway:` — the existing-gateway identity

Names your already-deployed gateway so the test's routes can attach to it. Only read when `fixtures.routes` is true.

| Field | Notes |
|---|---|
| `app_namespace` | namespace the test's objects are created in (required for `routes`) |
| `name` | the existing `Gateway` the routes attach to (required for `routes`) |
| `http_section` / `https_section` / `tcp_section` | which listeners on that Gateway the http / https / tcp routes attach to, by section name. **They must already exist.** Leave one empty to skip that route. |
| `class_name` / `controller_name` / `bnkgateway_name` / `flo_namespace` | optional descriptive fields; not needed to attach routes |

### `endpoints:` — named anchors

| `kind` | Fields | Is |
|---|---|---|
| `vsi` | `target` | a jumphost the test sends traffic *from* (the client) |
| `address` | `host`, `port` (default `5201`) | an iperf3 target — a `host:port` (an `iperf3` cell's `server`) |
| `url` | `url` | an HTTP target — a full `http(s)://` URL (an `l7` cell's `server`) |

### `cells:` — the grid

| Field | Applies to | Notes |
|---|---|---|
| `name` | both | required; the report row label and the `--only` glob target |
| `family` | both | `iperf3` \| `l7` |
| `client` | both | a `vsi` endpoint; empty → run locally |
| `server` | both | an `address` endpoint for `iperf3`, a `url` endpoint for `l7` |
| `length` / `bytes` / `duration` / `streams` | `iperf3` | iperf3 `-l` / `-n` / `-t` / `-P` |
| `l7.mode` | `l7` | `cps` \| `tps` \| `throughput` (required for `l7`) |
| `l7.{clients,streams,threads,requests,duration,http1}` | `l7` | h2load `-c` / `-m` / `-t` / `-n` / `-D` / `--h1`; override the mode defaults |

The file is checked before anything runs: a `client` must be a `vsi` with a `target`; an `iperf3` server must be an `address` with a `host`; an `l7` server must be a `url` and the cell must set a `mode`; and `routes` needs `gateway.name`, `app_namespace`, and at least one listener section.

## What the test sets up (and cleans up)

For a real run the test creates a few temporary objects, runs the measurements, then deletes them again (unless you pass `--keep`). Three toggles under `fixtures` — all skipped under `--dry-run`:

- **`iperf3_server`** — the TCP server the iperf3 cells measure against (the same one `test throughput` uses).
- **`http_backend`** — an nginx server that returns fixed-size bodies at `/128`, `/5k`, and `/512k` for the HTTP cells.
- **`routes`** — the HTTP and TCP routes (plus a TLS route and a self-signed certificate, when an https section is named) that wire your Gateway to those backends.

Everything the test creates is labelled `app.kubernetes.io/managed-by: roksbnkctl-matrix`, so cleanup is a single delete by label. The TLS certificate is self-signed and generated fresh each run — fine for a load test.

**One prerequisite for the HTTP/TCP routes:** your Gateway must already have the listener sections the routes attach to (`http` / `https` / `tcp`). The test adds the routes and backends, not the listeners — those come from the [Gateway phase](./10-deploying-bnk-trials.md). If a named section doesn't exist, the route has nowhere to attach and that cell's traffic has no path. Run with `--keep` and check the route status if a cell isn't reaching its backend.

## The workflow

### `--dry-run` — see the plan first

`--dry-run` expands the grid and prints, for each cell, the resolved client, server, and exact tool command — plus the objects it *would* create — and makes **no cluster calls**. Use it to confirm placement and commands before a long run:

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

*(Abridged; the full plan prints all ten cells of the example grid and the complete set of objects.)*

`--only '<glob>'` narrows the plan (or a real run) to cells whose name matches a glob — `--only 'L7*'` for just the HTTP rows, handy when iterating on one family.

### The real run and the report

Drop `--dry-run` against a complete deployment. The runner creates the fixtures, runs each cell from its jumphost, removes the fixtures (unless `--keep`), and prints the report. `-o` selects the shape:

- **`-o text`** (default) — the grid on stderr.
- **`-o md`** — the grid on stdout. iperf3 rows show Gbit/s and retransmits; HTTP rows show req/s, Mbit/s, and mean latency:

  ```markdown
  ## matrix (pass, 184230ms)

  | Cell | Family | Status | Throughput | req/s | mean ms | notes |
  |---|---|---|---|---|---|---|
  | L4 512K VSI->TMM diff-zone | iperf3 | pass | 9.21 Gbit/s | — | — | 312 retransmits |
  | L7 http CPS 128B VSI->TMM  | l7     | pass | 18.4 Mbit/s | 21340 | 2.34 | — |
  ```

  *(Numbers illustrative.)*

- **`-o json`** — the `roksbnkctl.v1` schema: one result per cell, each with its family-specific fields (plus the `client` / `server` it used). Exit code is **non-zero if any cell failed**, so it drops into CI:

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
| `--dry-run` | print the plan and the objects it would create; no cluster calls |
| `--keep` | leave the test's objects in place after the run |
| `-o json\|text\|md` | report shape |

## Cross-references

- [Chapter 22 — Throughput testing](./22-throughput-testing.md) — the single iperf3 measurement the matrix's `iperf3` family builds on; start there for the server-pod and SCC mechanics.
- [Chapter 15 — SSH targets](./15-ssh-targets.md#per-az-cluster-jumphosts-jumphost-zone) — the `jumphost` / `jumphost-<zone>` targets the locality axis names.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md#per-az-cluster-jumphosts) — the per-AZ jumphost fleet (with iperf3 + h2load preinstalled).
- [Chapter 17 — Execution backends](./17-execution-backends.md#ssh-backend) — how the `ssh:<target>` dispatch and the bundled tools images work.
- [Chapter 28 — Configuration reference §"matrix.yaml"](./28-configuration-reference.md#matrixyaml-the-performance-grid) — the schema as a reference table.
- [Chapter 27 — Command reference §`roksbnkctl test matrix`](./27-command-reference.md) — the generated flag surface.
