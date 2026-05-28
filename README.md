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
- **`demo` experience.** Audience-friendly walkthrough surface with a rocket-themed launch renderer (gated on `--demo` + TTY) and protocol demos for HTTP/2 (h2c) and Diameter (L4).

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

The Go-SDK phased path runs end-to-end without Terraform: VPC, subnets, IGW, NAT, EKS control plane, node group, kubeconfig, S3 supply chain, IRSA, Multus, host-device secondary ENIs, BNK activation, jumphost, forge registration. Terraform has been removed entirely from the production path and from the repository — see [`docs/POST_TERRAFORM_DIRECTION.md`](docs/POST_TERRAFORM_DIRECTION.md).

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

`pattern: host-device` is the current supported data-path variant. TMM runs directly on EC2 host NIC interfaces via secondary ENIs (no SR-IOV `vfio` passthrough).

**Requirements:**
- Instance type with at least 4 ENIs (primary + EKS CNI + 2 BNK secondaries). `m5.xlarge` minimum; `m6i.4xlarge` is the validated BNK 2.3 *Small* size.
- VPC CNI prefix delegation (enabled automatically in Phase 08b) so pods stay on the primary ENI and secondary ENIs remain available to BNK.
- A `network.dataPath` block in `cluster.yaml` (Phase 03 creates the BNK ext/int subnets; Phase 17 attaches the secondary ENIs to the TMM node).

Phase 00 preflight enforces these minimums and fails fast before any AWS writes.

## Commands

| Command | Description |
|---|---|
| `up -f <cfg>` | Provision everything (Phases 00–25). Add `--dry-run` to preview, `--demo` for the audience-mode launch renderer. |
| `down -f <cfg> --yes` | Tear down in reverse. Flags: `--keep-iam`, `--keep-keypair`, `--keep-forge-link`. |
| `status` | Workspace summary: cluster state, BNK components, per-phase deployment. |
| `doctor` | Health check: AWS creds, reachability, BNK subsystem state. |
| `validate <path>` | Parse + validate a `cluster.yaml` (no AWS API calls). |
| `topology` | Render the cluster data-path topology (VPC, TMM VLANs, jumphost, gateways). |
| `scenarios {list,run,clean}` | Built-in data-plane traffic validation suite. |
| `demo {list,run,clean}` | Curated audience walkthrough (HTTP/2, Diameter, green scenarios). |
| `test traffic` | Shorthand for `scenarios run http-routing-e2e`. |
| `bnk resync [route]` | Force the CNE controller to re-resolve stale TMM pool members. |
| `k <verb> [args]` | Kubernetes passthrough — `get`, `apply`, `describe`, `delete`, `logs`, `exec`, `port-forward`. No host `kubectl` needed. |
| `forge {register,status,unregister}` | Optional handoff to a running [bnk-forge](docs/FORGE_MCP_INTEGRATION.md) instance. |
| `install` | Copy the running binary into a directory on PATH. |

## Demo experience

`awsbnkctl up --demo` provisions the **identical** cluster as a normal `up`, plus:

- a rocket-themed launch renderer that maps the ~30 phases into 4 stages
- pre-stages a demo client (`grpcurl`, diameter python client) on the jumphost
- tags resources `awsbnkctl:demo=true` with an absolute expiry tag
- enables `awsbnkctl demo run` to drive narrated protocol walkthroughs

```
   awsbnkctl ▸ tracer ▸ DEMO LAUNCH   T+38s
   ██████████  STAGE 1  VPC · subnets · IGW · NAT                  [Phase 00–07]  ✓ 38s
   ██████████  STAGE 2  EKS control plane                          [Phase 08–08b]  ✓ 8m12s
   ██████░░░░  STAGE 3  Nodes · kubeconfig · ENIs · jumphost       [Phase 10–18]  (current: jumphost)  ⏳ 1m04s
   ──────────  STAGE 4  BNK supply chain · activation              [Phase 11b–25]
   ──────────  ORBIT
```

Non-TTY runs (CI, piped output, `--no-color`) fall back to the plain per-phase log — byte-for-byte unchanged. See [`docs/design/specs/10-DEMO-EXPERIENCE.md`](docs/design/specs/10-DEMO-EXPERIENCE.md) for the full design.

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
    ├── POST_TERRAFORM_DIRECTION.md   # why Terraform was removed
    ├── FORGE_MCP_INTEGRATION.md      # forge handoff design
    ├── design/specs/                 # per-PRD design notes
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

See [`CONTRIBUTING.md`](CONTRIBUTING.md). For deeper architectural context, the design notes in [`docs/design/specs/`](docs/design/specs/) trace each subsystem from PRD through implementation.

## License

[MIT](LICENSE) © 2026 John Gruber
