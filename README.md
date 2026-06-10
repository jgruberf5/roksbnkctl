# awsbnkctl

[![CI](https://github.com/JLCode-tech/awsbnkctl/actions/workflows/ci.yml/badge.svg)](https://github.com/JLCode-tech/awsbnkctl/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/JLCode-tech/awsbnkctl?include_prereleases&sort=semver)](https://github.com/JLCode-tech/awsbnkctl/releases)

A single Go binary that provisions F5 BIG-IP Next for Kubernetes (BNK) on AWS EKS — VPC, EKS cluster, node groups, IAM, secondary ENIs for the data plane, BNK install, and end-to-end traffic validation — using the AWS SDK directly.

No Terraform. No host `kubectl`. No `aws` CLI. One binary, one intent file.

```bash
awsbnkctl up   -f cluster.yaml          # provision phases 00-25
awsbnkctl demo run --all -f cluster.yaml # narrated audience walkthrough
awsbnkctl down -f cluster.yaml --yes    # tear down in reverse
```

## What this is

A purpose-built provisioner for one workload — F5 BIG-IP Next for Kubernetes — on one platform — AWS EKS. The binary owns the full lifecycle:

- **Imperative phased provisioner.** ~30 ordered phases (`Phase00Preflight` → `Phase25ActivationPoll` → `Phase13Postflight`) run via the AWS Go SDK. AWS resource tags are the source of truth; a local `state.env` cache is rebuildable from tags.
- **`cluster.yaml` intent file.** Declarative inputs (VPC, network, node group, BNK credentials) → imperative AWS calls. Validated up-front before any mutation.
- **`scenarios` framework.** Built-in end-to-end traffic validation against the provisioned cluster (5 green data-plane scenarios + a curated demo catalogue).
- **`demo` experience.** Audience-friendly walkthrough surface with a rocket-themed launch renderer (gated on `--demo` + TTY): protocol demos (HTTP/2 h2c, Diameter L4) plus **migration scenarios** that run BNK side-by-side with ingress-nginx/HAProxy (`ingress-migration`) and against an external BIG-IP VE + CIS (`bigip-cis`) — the appliance model BNK replaces.

## Quick start

```bash
# 1. Build the binary
go build -o awsbnkctl ./cmd/awsbnkctl

# 2. Copy an example and edit it
cp examples/tracer/cluster.yaml my-cluster.yaml
#    Set: metadata.name, metadata.region, network CIDRs, cluster.nodeGroups,
#         bnk.farArchive (FAR pull credentials JSON), bnk.jwt (subscription JWT).

# 3. Authenticate to AWS (standard credential chain)
export AWS_PROFILE=my-profile
aws sso login --profile $AWS_PROFILE     # if using SSO

# 4. Validate the intent file (no AWS API calls)
./awsbnkctl validate my-cluster.yaml

# 5. Provision everything (preview with --dry-run first if you like)
./awsbnkctl up -f my-cluster.yaml

# 6. Run the built-in data-plane traffic validation
./awsbnkctl scenarios run http-routing-e2e -f my-cluster.yaml

# 7. Tear it all down
./awsbnkctl down -f my-cluster.yaml --yes
```

## Status

The Go-SDK phased path runs end-to-end without Terraform: VPC, subnets, IGW, NAT, EKS control plane, node group, kubeconfig, S3 supply chain, IRSA, Multus, host-device secondary ENIs, BNK activation, jumphost, forge registration. Terraform has been removed entirely from the production path and from the repository — see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

Validated live in `ap-southeast-2` on a reference lab (BNK 2.3.0, `host-device` pattern, EKS 1.30, `m6i.4xlarge × 3` + a `t3.small` jumphost) across the full provision → traffic → demo → teardown cycle.

## `cluster.yaml`

A structured YAML document validated at startup. See [`examples/tracer/cluster.yaml`](examples/tracer/cluster.yaml) for a complete example.

```yaml
apiVersion: awsbnkctl/v1
kind: Cluster

metadata:
  name: tracer
  region: ap-southeast-2

pattern: host-device           # data-path variant (see Patterns)

network:
  vpcCidr: 10.0.0.0/16
  azs: [ap-southeast-2a, ap-southeast-2b]
  subnets:
    public:  [{cidr: 10.0.1.0/24,  az: ap-southeast-2a}, ...]
    private: [{cidr: 10.0.11.0/24, az: ap-southeast-2a}, ...]
  dataPath:                    # host-device only: BNK ext/int VLANs
    external: {cidr: 10.0.10.0/24, az: ap-southeast-2a}
    internal: {cidr: 10.0.20.0/24, az: ap-southeast-2a}
  natGateways: 1

cluster:
  kubernetesVersion: "1.30"
  nodeGroups:
    - name: default
      instanceType: m6i.4xlarge   # >=4 ENIs required for host-device
      desiredSize: 3

bnk:
  farArchive: ./cne_pull_64.json  # F5 FAR pull credentials JSON
  jwt: ./license.jwt              # F5 subscription JWT

tags:
  cost-center: RnD
  env: lab
```

Cluster state is written to `.awsbnkctl/<name>/state.env`. Loss of the local cache is recoverable via tag discovery.

## Patterns

`pattern:` selects the TMM data-plane interface topology and binding. Backend pods are always reached over the CNI; the pattern only changes how TMM gets its client-side (and optional server-side) interfaces.

| `pattern:` | Interfaces | Binding | Min ENIs | Status |
|---|---|---|---|---|
| `external-only` | external only | host-device (kernel) | 2 | supported |
| `dual-interface` | external + internal | host-device (kernel) | 3 | supported |
| `sriov-external` | external only | SR-IOV / `vfio-pci` DPDK | 2 | experimental |

`host-device` is the legacy alias for `dual-interface` (normalized at load), so existing configs keep working unchanged. `m6i.4xlarge` is the validated BNK 2.3 *Small* size; the ENI floor is `primary + one TMM secondary per data-plane interface`. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and the per-pattern configs under [`examples/`](examples/) for the full model.

**Common requirements:**
- VPC CNI prefix delegation (enabled automatically in Phase 08b) so pods stay on the primary ENI and secondary ENIs remain available to BNK.
- A `network.dataPath` block in `cluster.yaml` (Phase 03 creates the BNK subnets; Phase 17 attaches the secondary ENIs to the TMM node).

Phase 00 preflight enforces the ENI / CPU / memory minimums and fails fast before any AWS writes.

## Examples

Ready-to-edit `cluster.yaml` topologies live under [`examples/`](examples/):

| Example | What it is |
|---|---|
| [`examples/tracer/`](examples/tracer/) | Minimal VPC-only tracer-bullet (fastest smoke test) |
| [`examples/full-cluster/`](examples/full-cluster/) | Complete BNK cluster reference config |
| [`examples/external-only/`](examples/external-only/) | Single-interface `external-only` pattern |
| [`examples/sriov-external/`](examples/sriov-external/) | Experimental SR-IOV/DPDK `sriov-external` pattern |
| [`examples/demo/`](examples/demo/) | Full demo cluster + curated walkthroughs ([README](examples/demo/README.md)) |

The **demo** example also ships two migration scenarios — `demo run ingress-migration` (ingress-nginx / HAProxy / BNK side-by-side) and `demo run bigip-cis` (external BIG-IP VE + CIS, the model BNK replaces). Walkthrough: [`examples/demo/README.md`](examples/demo/README.md).

## Commands

| Command | Description |
|---|---|
| `up -f <cfg>` | Provision everything (Phases 00–25). Add `--dry-run` to preview, `--demo` for the audience-mode launch renderer. |
| `down -f <cfg> --yes` | Tear down in reverse. Flags: `--keep-irsa`, `--keep-forge-link`, `--dry-run`. |
| `status` | Workspace summary: cluster state, BNK components, per-phase deployment. |
| `doctor` | Health check: AWS creds, reachability, BNK subsystem state. |
| `validate <path>` | Parse + validate a `cluster.yaml` (no AWS API calls). |
| `topology` | Render the cluster data-path topology (VPC, TMM VLANs, jumphost, gateways). |
| `scenarios {list,run,clean}` | Built-in data-plane traffic validation suite. |
| `demo {list,run,clean}` | Curated audience walkthrough — HTTP/2, Diameter, and the `ingress-migration` / `bigip-cis` migration scenarios. |
| `test traffic` | Shorthand for `scenarios run http-routing-e2e`. |
| `bnk resync [route]` | Force the CNE controller to re-resolve stale TMM pool members. |
| `k <verb> [args]` | Kubernetes passthrough — `get`, `apply`, `describe`, `delete`, `logs`, `exec`, `port-forward`. No host `kubectl` needed. |
| `forge {register,status,unregister}` | Optional handoff to a running [bnk-forge](docs/FORGE_INTEGRATION.md) instance. |
| `install` | Copy the running binary into a directory on PATH. |

## Demo experience

`awsbnkctl up --demo` provisions the **identical** cluster as a normal `up`, plus:

- a rocket-themed launch renderer that maps the phases into 4 stages
- pre-stages a demo client (`grpcurl`, diameter python client) on the jumphost
- tags resources `awsbnkctl:demo=true` with an absolute expiry tag
- enables `awsbnkctl demo run` to drive narrated walkthroughs: protocol demos
  (HTTP/2, Diameter) and migration scenarios (`ingress-migration`, `bigip-cis` —
  see [`examples/demo/README.md`](examples/demo/README.md))

```
   awsbnkctl ▸ tracer ▸ DEMO LAUNCH   T+38s
   ██████████  STAGE 1  VPC · subnets · IGW · NAT                  [Phase 00–07]  ✓ 38s
   ██████████  STAGE 2  EKS control plane                          [Phase 08–08b]  ✓ 8m12s
   ██████░░░░  STAGE 3  Nodes · kubeconfig · ENIs · jumphost       [Phase 10–18]  (current: jumphost)  ⏳ 1m04s
   ──────────  STAGE 4  BNK supply chain · activation              [Phase 11b–25]
   ──────────  ORBIT
```

Non-TTY runs (CI, piped output, `--no-color`) fall back to the plain per-phase log — byte-for-byte unchanged.

## Architecture

```
awsbnkctl/
├── cmd/awsbnkctl/         # binary entry point
├── internal/
│   ├── cli/              # cobra command tree (up/down/status/scenarios/demo/forge/k/...)
│   ├── aws/phases/       # Phase00-Phase25 Go-SDK functions
│   ├── aws/state/        # state.env IDs cache + tag-discovery
│   ├── intent/           # cluster.yaml loader + validation
│   ├── k8s/              # client-go apply wrapper + variant manifests
│   ├── scenarios/        # data-plane traffic validation framework
│   ├── demo/             # curated demo catalogue + narration
│   ├── jumphost/         # SSH-via-EICE + ephemeral key dance
│   ├── ui/               # launch renderer (rocket-themed for --demo)
│   ├── forge/            # forge REST + MCP client
│   ├── remote/           # SSH backends + EICE tunnel
│   └── exec/             # local / docker / k8s execution backends
├── pkg/bnk/              # exported BNK runtime helpers (HTTPRoute resync)
├── examples/             # reference cluster.yaml configurations
└── docs/
    ├── ARCHITECTURE.md               # AWS-SDK phased model + cluster.yaml intent
    ├── FORGE_INTEGRATION.md          # forge handoff design
    └── upstream-issues/              # known issues / workarounds in BNK
```

## Prerequisites

- **AWS credentials** via the standard chain (env vars, `~/.aws/credentials`, SSO, IRSA, IMDS).
- **Go 1.25+** to build from source. Pre-built binaries from [releases](https://github.com/JLCode-tech/awsbnkctl/releases) need only the binary.
- **F5 BNK entitlements:** a FAR pull credentials JSON and a subscription JWT from the F5 portal, referenced in `cluster.yaml`.

No Terraform, no host `kubectl`, no `aws` CLI. All AWS API calls use the AWS SDK; all Kubernetes operations use `client-go`; DNS probes use `miekg/dns`. Everything is compiled into the single binary.

## Scope

**In scope:** F5 BIG-IP Next for Kubernetes on AWS EKS — VPC networking, IAM, secondary-ENI data-plane topology, BNK install + activation, traffic validation, optional forge handoff.

**Out of scope:** general-purpose AWS or Kubernetes CLIs. `awsbnkctl k *` covers common verbs as a convenience inside a BNK workspace; use `kubectl` for anything beyond that.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md). For deeper architectural context, see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## License

[MIT](LICENSE) © 2026 JLCode-tech
