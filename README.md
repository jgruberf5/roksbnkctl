# awsbnkctl

A single Go binary that deploys F5 BIG-IP Next for Kubernetes (BNK) onto AWS EKS, using the AWS SDK only and an imperative phased provisioner driven by a `cluster.yaml` intent file.

## Status

Live-validated on `aws-syd-test` (ap-southeast-2). The Go-SDK phased path (`up --config`) runs Phases 00–25 — VPC, subnets, IGW, NAT, EKS control plane, node group, kubeconfig, S3 supply chain, IRSA, Multus, SR-IOV CNI, BNK activation, jumphost, forge registration — end-to-end without Terraform. Terraform has been removed from the production path and is being deleted from the repository in follow-up PRs (see [`docs/POST_TERRAFORM_DIRECTION.md`](docs/POST_TERRAFORM_DIRECTION.md)).

## How it works

1. You write (or copy from `examples/`) a `cluster.yaml` describing the cluster name, region, network layout, node group shape, BNK credentials, and optional forge handoff.
2. `awsbnkctl up --config cluster.yaml` runs Phases 00–25 sequentially via the AWS SDK. AWS resource tags are the source of truth; a local `.awsbnkctl/<name>/state.env` IDs cache is rebuildable from tags.
3. BNK subsystems (cert-manager, FLO, CNE controller, TMM) are activated by applying variant manifests from `internal/k8s/manifests/<pattern>/`, selected by `cluster.yaml: pattern:`.
4. `awsbnkctl down --config cluster.yaml --yes` tears everything down in reverse order.

No Terraform binary, no `kubectl` on the host, no `aws` CLI required — it is all in the Go binary.

## Quick start

```bash
# 1. Build the binary.
go build -o awsbnkctl ./cmd/awsbnkctl

# 2. Copy and edit the example intent file.
cp examples/syd-tracer/cluster.yaml my-cluster.yaml
#    Set: metadata.name, metadata.region, network CIDRs, cluster.nodeGroups,
#         bnk.farArchive, bnk.jwt, and (optionally) forge block.

# 3. Authenticate with AWS (standard credential chain).
export AWS_PROFILE=my-profile
aws sso login --profile $AWS_PROFILE   # if using SSO

# 4. Validate the intent file (no AWS API calls).
awsbnkctl validate my-cluster.yaml

# 5. Provision everything (add --dry-run to preview without applying).
awsbnkctl up --config my-cluster.yaml

# 6. Run the built-in traffic validation scenario.
awsbnkctl scenarios run http-routing-e2e --config my-cluster.yaml
# or the shorthand:
awsbnkctl test traffic --config my-cluster.yaml

# 7. Tear it all down.
awsbnkctl down --config my-cluster.yaml --yes
```

## cluster.yaml

The intent file is a structured YAML document validated by the binary at startup. The canonical example is [`examples/syd-tracer/cluster.yaml`](examples/syd-tracer/cluster.yaml). Key fields:

```yaml
apiVersion: awsbnkctl/v1
kind: Cluster

metadata:
  name: my-cluster          # becomes the awsbnkctl:cluster tag on every AWS resource
  region: ap-southeast-2

pattern: host-device         # data-path variant (see Patterns section)

network:
  vpcCidr: 10.0.0.0/16
  azs: [ap-southeast-2a, ap-southeast-2b]
  subnets:
    public:  [ {cidr: 10.0.1.0/24, az: ap-southeast-2a}, ... ]
    private: [ {cidr: 10.0.11.0/24, az: ap-southeast-2a}, ... ]
  dataPath:                  # host-device only: BNK ext/int VLANs
    external: {cidr: 10.0.10.0/24, az: ap-southeast-2a}
    internal: {cidr: 10.0.20.0/24, az: ap-southeast-2a}
  natGateways: 1

cluster:
  kubernetesVersion: "1.30"
  nodeGroups:
    - name: default
      instanceType: m6i.4xlarge   # ≥4 ENIs required for host-device pattern
      desiredSize: 3

bnk:
  farArchive: ./cne_pull_64.json  # path to F5 FAR pull credentials JSON
  jwt: ./license.jwt              # path to F5 subscription JWT

forge:                      # optional: register with a running bnk-forge instance
  enabled: true
  url: http://localhost:8000

tags:
  cost-center: RnD
  env: lab
```

State is written to `.awsbnkctl/<name>/state.env`. Loss of the cache is recoverable via tag-discovery.

## Patterns

`pattern: host-device` is the current supported data-path variant. TMM runs directly on EC2 host NIC interfaces via secondary ENIs (no SR-IOV vfio pass-through).

Requirements for `host-device`:

- Instance type with at least 4 ENIs (primary + EKS CNI + 2 BNK secondaries). `m5.xlarge` is the minimum; `m6i.4xlarge` is the validated BNK 2.3 Small size.
- VPC CNI prefix delegation enabled (Phase 08b) so all pods stay on the primary ENI and secondary ENIs remain available to BNK.
- `network.dataPath` block in `cluster.yaml` (Phase 03 creates the BNK ext/int subnets; Phase 17 attaches the secondary ENIs to the TMM node).
- Phase 00 preflight enforces these minimums and fails fast before any AWS writes.

## Commands

| Command | Description |
|---|---|
| `up --config <f>` | Provision everything (Phases 00–25). Add `--dry-run` to preview without applying. |
| `down --config <f> --yes` | Tear down in reverse order. Flags: `--keep-iam`, `--keep-keypair`, `--keep-forge-link`. |
| `status` | Summary of the workspace: cluster, components, per-phase deployment state. |
| `doctor` | Pre-flight check: AWS creds, reachability, BNK subsystem health. |
| `validate <path>` | Parse and validate a `cluster.yaml` with no AWS API calls. |
| `topology` | Render the cluster data-path topology (VPC, TMM VLANs, jumphost, gateways). |
| `scenarios list` | Print registered validation scenarios. |
| `scenarios run [name]` | Run a named scenario (or `--all`). Requires `--config`. |
| `scenarios clean <name>` | Invoke a scenario's cleanup hook. |
| `test traffic` | Alias for `scenarios run http-routing-e2e` — drive HTTP traffic through TMM. |
| `forge register` | Register the workspace's EKS cluster with forge (idempotent). |
| `forge status` | Show this workspace's forge registration state. |
| `forge unregister` | Remove this workspace's forge registration. |
| `k <verb> [args]` | Kubernetes passthrough — get, apply, describe, delete, logs, exec, port-forward. No host `kubectl` needed. |
| `bnk resync [httproute]` | Force the CNE controller to re-resolve stale TMM pool members. |
| `install` | Copy the running binary into a directory on PATH. |

## Forge integration

awsbnkctl is **write-only** toward forge. AWS is the source of truth; `status` and `doctor` query AWS directly and never ask forge for cluster state. The forge handoff flow:

1. Phase 09 (`Phase09ForgeRegister`) fires after EKS is active, before BNK install, so the forge GUI can surface BNK-install progress live.
2. On failure: soft-fail with 3 retries + exponential backoff. AWS infra stays up. The operator can recover with `awsbnkctl forge register --config <f>`.
3. `awsbnkctl down` calls `forge unregister` by default. `--keep-forge-link` preserves the forge project record.

Forge listens at `:8000` (REST) and `:8081` (MCP). Credentials: `AWSBNKCTL_FORGE_PASSWORD` env var (preferred) or `forge.password` in `cluster.yaml` (dev only). See [`docs/FORGE_MCP_INTEGRATION.md`](docs/FORGE_MCP_INTEGRATION.md) for the full integration design.

## What's in the repo

```
awsbnkctl/
├── cmd/awsbnkctl/          # binary entry point
├── internal/
│   ├── cli/               # cobra command tree (lifecycle, inspect, scenarios, forge, k, …)
│   ├── aws/phases/        # Phase00–Phase25 Go SDK functions
│   ├── aws/state/         # state.env IDs cache + tag-discovery
│   ├── intent/            # cluster.yaml loader + Go struct validation
│   ├── k8s/               # client-go apply wrapper + variant manifests
│   ├── scenarios/         # end-to-end validation scenario runner
│   ├── forge/             # forge REST + MCP client
│   ├── remote/            # SSH backends + EICE tunnel
│   └── exec/              # local / docker / k8s execution backends
├── pkg/bnk/               # exported BNK runtime helpers (HTTPRoute resync)
├── examples/
│   └── syd-tracer/        # reference cluster.yaml (ap-southeast-2 tracer-bullet)
└── docs/
    ├── POST_TERRAFORM_DIRECTION.md   # why Terraform was removed
    ├── FORGE_MCP_INTEGRATION.md      # forge handoff design
    ├── prd/09-SCENARIOS-FRAMEWORK.md # scenarios framework design
    └── audits/                        # live-cycle audit trail
```

## Prerequisites

- **AWS credentials** via the standard chain: env vars, `~/.aws/credentials`, SSO, IRSA, or IMDS. No other host installs required.
- **Go 1.25+** to build from source (`go build ./cmd/awsbnkctl`; the version tracks the `go` directive in `go.mod`). Pre-built binaries from goreleaser need nothing beyond the binary itself.
- **F5 BNK entitlements:** a FAR pull credentials JSON and a subscription JWT from the F5 portal, referenced in `cluster.yaml`.

There is no Terraform, no host `kubectl`, no `aws` CLI, and no `dig` required — all AWS API calls use the AWS SDK, all Kubernetes operations use `client-go`, and DNS probes use `miekg/dns`, all compiled into the single binary.

## What this is not

- Not a Terraform wrapper or authoring tool. Terraform has been removed from the production path.
- Not a general-purpose AWS CLI. awsbnkctl's scope on AWS is the BNK supply chain: EKS, VPC networking, S3, IAM, and the BNK data-plane ENI topology.
- Not a general-purpose Kubernetes CLI. `awsbnkctl k *` internalises common verbs for convenience within the BNK workspace; use `kubectl` for anything else.
- Not an arbitrary workload deployer. BNK is the workload; the test scenarios exist only to validate it.

## License

[MIT](LICENSE). The project originated as a fork of [`jgruberf5/roksbnkctl`](https://github.com/jgruberf5/roksbnkctl) (IBM Cloud ROKS) and has since been independently developed as an AWS-native tool. The MIT license attribution from the upstream project is preserved in the LICENSE file.
