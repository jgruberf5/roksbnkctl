# roksbnkctl

[Read the book](https://jgruberf5.github.io/roksbnkctl/book/) — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_

A single-binary CLI for deploying F5 BIG-IP Next for Kubernetes (BNK) onto IBM Cloud ROKS, managing its IBM Cloud Object Storage supply chain, and validating the deployment with built-in connectivity, DNS, and throughput tests.

> **Status:** v1.0 — first stable release.

The book at <https://jgruberf5.github.io/roksbnkctl/book/> is the canonical user documentation: 32 chapters covering concepts, lifecycle, day-2 operations, execution backends, the GSLB-aware DNS probe, and full reference material. This README is a quickstart pointer — read the book for everything else.

## Quick start

```bash
# 1. Install the binary
go install github.com/jgruberf5/roksbnkctl/cmd/roksbnkctl@latest
#   …or grab a pre-built binary for your OS/arch from the releases page:
#   https://github.com/jgruberf5/roksbnkctl/releases/latest

# 2. Interactive setup — region, RG, cluster, OpenShift version.
roksbnkctl init

# 3. Make your IBM Cloud API key available (env, OS keychain, or
#    `roksbnkctl init` will offer to persist it for you).
export IBMCLOUD_API_KEY=...

# 4. Plan + confirm + apply + auto-fetch admin kubeconfig.
roksbnkctl up

# 5. Run the built-in DNS + connectivity + throughput tests.
roksbnkctl test
```

That's the 4-command lifecycle (`init` → `up` → `test` → `down`). See [Chapter 7 — Quick start](https://jgruberf5.github.io/roksbnkctl/book/07-quick-start.html) for the full walkthrough; [Chapter 24 — Day-2 ops](https://jgruberf5.github.io/roksbnkctl/book/24-day-2-ops.html) for what comes after.

## Install

| Option | How |
|---|---|
| **`go install`** | `go install github.com/jgruberf5/roksbnkctl/cmd/roksbnkctl@latest` (Go 1.25+) |
| **Pre-built binary** | Linux/macOS/Windows × amd64/arm64 archives + SHA256 checksums on every tagged release at <https://github.com/jgruberf5/roksbnkctl/releases>. Verify with `sha256sum -c checksums.txt`. |
| **From source** | `git clone https://github.com/jgruberf5/roksbnkctl && cd roksbnkctl && make build` (output at `bin/roksbnkctl`; see [Chapter 31 — Building from source](https://jgruberf5.github.io/roksbnkctl/book/31-building-from-source.html)). |
| **In-place upgrade** | `roksbnkctl self update` pulls the latest GitHub release, verifies the SHA256 against `checksums.txt`, and atomic-replaces the running binary. Linux/macOS only. |

A Homebrew tap is on the v1.x roadmap.

Every tagged release also attaches an offline **`roksbnkctl-book-<tag>.pdf`** — the same book as the published HTML, rendered with Mermaid diagrams pre-baked as vector SVG. Grab it from the same releases page when you want the docs without a network round-trip.

## Prerequisites

`terraform` (1.5+) on `PATH` is the only required host install. `roksbnkctl doctor` reports green on a stock dev box with terraform alone — every other tool the binary needs (`kubectl`, `oc`, `ibmcloud`, `iperf3`, `dig`) is internalised:

| Surface | Internalised path |
|---|---|
| `kubectl`, `oc` | `client-go` — `roksbnkctl k get/apply/describe/delete/logs/exec/port-forward` |
| `ibmcloud` | Bundled tools image — `--backend docker` or `--backend ssh:<target>` |
| `iperf3` | Bundled tools image — `--backend k8s` runs the throughput probe as a one-shot Job |
| `dig` | `miekg/dns` — `roksbnkctl test dns` (multi-vantage GSLB-aware probe) |

See [Chapter 17 — Execution backends](https://jgruberf5.github.io/roksbnkctl/book/17-execution-backends.html) for the full backend matrix.

## What's in this repo

```
roksbnkctl/
├── cmd/roksbnkctl/         # binary entry point
├── internal/               # Go packages (cli, tf, ibm, k8s, cred, exec, test, doctor, ...)
├── terraform/              # the HCL deployment — embedded into the binary at build time
├── tools/                  # vendored tool images + cobra-md / tfvars-md reference generators
├── book/                   # mdBook sources for https://jgruberf5.github.io/roksbnkctl/book/
├── docs/                   # PLAN.md (sprint history), prd/ (per-feature design specs)
└── scripts/                # e2e test runners
```

The Terraform that drives the deployment is embedded into the binary at build time — every tagged release ships a matched CLI + HCL pair, eliminating skew between binary and TF. `tf_source` overrides (`type: github` or `local`) let you point at a fork for testing.

## Pointers

- **Book** — <https://jgruberf5.github.io/roksbnkctl/book/> — canonical user documentation; start at the preface or jump to [Chapter 7](https://jgruberf5.github.io/roksbnkctl/book/07-quick-start.html) for the deploy walkthrough. Auto-published from `main` via GitHub Actions on every push to `book/**`. An offline PDF (`roksbnkctl-book-<tag>.pdf`) attaches to each [GitHub Release](https://github.com/jgruberf5/roksbnkctl/releases).
- **[`MIGRATING.md`](MIGRATING.md)** — upgrade notes from `bnk` (the pre-roksbnkctl bash workflow), from manual BNK deployment, and between roksbnkctl versions.
- **[`CHANGELOG.md`](CHANGELOG.md)** — per-release change log; `v1.0.0` rolls up the seven-sprint history.
- **[`docs/PLAN.md`](docs/PLAN.md)** — sprint-by-sprint development history and the v1.x deferral list.
- **[`docs/prd/`](docs/prd/)** — per-feature design rationale (cred propagation, execution backends, DNS probe, kubectl internalisation, …).
- **[`CONTRIBUTING.md`](CONTRIBUTING.md)** — how to contribute, run tests, add a chapter, and cut a release (the `make release` driver wraps the book + PDF + goreleaser snapshot + Pages preflight into one command). Failing that, file issues at <https://github.com/jgruberf5/roksbnkctl/issues>.

## What this is *not*

- Not a Terraform authoring tool. The HCL lives in this repo under [`./terraform/`](./terraform/) and is the source of truth for the deployment shape.
- Not a general-purpose IBM Cloud CLI — `ibmcloud` covers that. roksbnkctl's scope on IBM Cloud is the BNK supply chain: ROKS for the cluster, COS for prerequisite artefacts (FAR pull keys, JWT licenses), IAM for what BNK consumes.
- Not a general-purpose Kubernetes CLI — `kubectl` / `oc` cover that. The internalised `roksbnkctl k *` verbs make their workspace context easy to load without a host install.
- Not an arbitrary workload deployer. BNK is the workload; the iperf3 / nginx test fixtures exist only to validate it.

## License

[MIT](LICENSE).
