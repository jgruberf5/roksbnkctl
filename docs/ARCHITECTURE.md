# Architecture

`awsbnkctl` is a single Go binary that provisions F5 BIG-IP Next for Kubernetes
(BNK) onto AWS EKS. It drives AWS directly through the AWS SDK for Go and a
sequence of imperative, idempotent **phases**, taking its instructions from a
single structured `cluster.yaml` intent file.

This document describes the design: the provisioning model, the intent format,
the phased up/down lifecycle, state management, and the host-device data-path
pattern.

---

## 1 · Design philosophy

The tool is built around four commitments:

| Layer | Decision |
|---|---|
| **AWS writes + reads** | Strict AWS SDK for Go. No shelling out to the `aws` CLI. All AWS work lives under `internal/aws/`. |
| **Provisioning shape** | Imperative, sequential phase functions — not a reconciler framework. Each phase is a plain Go function that is easy to read, log, and debug. |
| **Intent** | A structured `cluster.yaml`, validated against a Go struct. There is no intermediate variable layer between the YAML and the AWS calls. |
| **State** | AWS resource tags are the source of truth. A small per-cluster IDs cache (`state.env`) accelerates re-runs and is rebuildable from tags. |

There is **no Terraform, no tfstate, and no external IaC engine**. The binary
makes AWS API calls directly and records what it created via tags plus the local
cache. This keeps the tool a single distributable binary with one mental model
and a log-able, linear failure surface.

Kubernetes objects are applied through `client-go` (server-side apply via
`internal/k8s`), never by shelling out to `kubectl`.

---

## 2 · The `cluster.yaml` intent

A cluster is described by one Kubernetes-style YAML document
(`apiVersion: awsbnkctl/v1`, `kind: Cluster`). The schema is defined and
validated in `internal/intent/cluster.go`; runnable examples live under
`examples/`.

At a high level:

```yaml
apiVersion: awsbnkctl/v1
kind: Cluster

metadata:
  name: full-cluster          # load-bearing — see note below
  region: ap-southeast-2
  labels:                     # optional; merged into AWS resource tags
    purpose: bnk-full-cluster

pattern: host-device          # selects the data-path variant

network:
  vpcCidr: 10.0.0.0/16
  azs: [ap-southeast-2a, ap-southeast-2b]
  subnets:
    public:  [{cidr: 10.0.1.0/24, az: ap-southeast-2a}, ...]
    private: [{cidr: 10.0.11.0/24, az: ap-southeast-2a}, ...]
  dataPath:                   # required for pattern: host-device
    external: {cidr: 10.0.10.0/24, az: ap-southeast-2a}   # TMM client side
    internal: {cidr: 10.0.20.0/24, az: ap-southeast-2a}   # TMM backend side
  natGateways: 1              # 1 (cost-optimised) or len(azs) for HA

cluster:                      # EKS control plane + node groups
  kubernetesVersion: "1.30"
  nodeGroups:
    - name: default
      instanceType: m6i.4xlarge
      desiredSize: 3

bnk:                          # BNK supply-chain credentials (local file paths)
  farArchive: ./cne_pull_64.json
  jwt: ./license.jwt

addons:                       # optional; FLO is installed by default
  flo:
    enabled: true

testing:                      # optional test jumphost (off by default)
  jumphost:
    enabled: true

forge:                        # optional forge handoff (see FORGE_INTEGRATION.md)
  enabled: true

tags:                         # merged onto every AWS resource
  cost-center: RnD-AI
```

Key points:

- **`metadata.name` is load-bearing.** It becomes the value of the
  `awsbnkctl:cluster` tag on every resource and the directory name under
  `.awsbnkctl/`. It must satisfy the EKS cluster-name rules (lowercase, starts
  with a letter, 2–40 chars).
- **`network.azs` is explicit by design.** The tool does not auto-pick
  availability zones, so infrastructure is reproducible across runs.
- **Unknown fields are errors.** Strict decoding catches typos at load time
  rather than silently ignoring them.
- **Optional blocks default cleanly.** A minimal intent (network only) is valid;
  the `cluster:`, `bnk:`, `addons:`, `testing:`, and `forge:` blocks switch on
  the corresponding phases when present.

Defaults (Kubernetes version, node group sizing, TMM resources, cert-manager
version, FLO chart version, and so on) are applied at load time before
validation. The `host-device` pattern carries its own minimums — for example a
node group large enough for ≥4 ENIs and ≥3 nodes for dSSM quorum — which
preflight enforces. See `internal/intent/cluster.go` for the authoritative field
list and defaults.

---

## 3 · The phased lifecycle

Provisioning is a fixed, ordered sequence of phase functions. Each phase:

- begins with an auth check (the SSO sentinel, below);
- uses SDK clients wrapped with shared middleware;
- creates or reads AWS / Kubernetes resources, tagging everything it creates;
- writes the IDs it discovers back into the in-memory state, which is persisted
  to `state.env` as it goes.

Every phase function has the same shape and operates on the loaded intent plus a
mutable state object, so a partially completed `up` leaves a valid partial cache
behind.

### `up`

`awsbnkctl up --config cluster.yaml` runs the phases in order. The full sequence
is wired in `internal/cli/lifecycle.go`; the phase implementations live in
`internal/aws/phases/`. Conceptually it builds up in four stages:

1. **Network + IAM** — preflight, VPC, subnets, internet gateway, NAT gateway,
   route tables, and the IAM cluster/node roles + instance profile.
2. **EKS control plane** — the EKS cluster (waits until active), then VPC CNI
   prefix delegation configured before nodes join.
3. **Nodes + data path** — the managed node group, kubeconfig generation, the
   TMM node label, the host-device secondary ENIs, an optional test jumphost,
   interface discovery, and the OIDC provider + IRSA role.
4. **BNK supply chain + activation** — the EBS CSI driver and hugepages, the
   cert-manager / cert-chain foundation, FAR + license secrets, the F5 Lifecycle
   Operator (FLO) via Helm, OTEL certificates, the host-device Kubernetes
   prerequisites (network mapping, NADs, IRSA service account), the CNEInstance
   and License custom resources, data-plane VLAN/GatewayClass plumbing, a few
   best-effort heal steps, and a final activation poll followed by a postflight
   smoke check.

When `forge.enabled` is set, a forge-registration phase fires once the EKS
control plane is active — before the long BNK install — so the forge GUI can
surface install progress. See [`FORGE_INTEGRATION.md`](FORGE_INTEGRATION.md).

### `down`

`awsbnkctl down --config cluster.yaml` tears everything down in reverse order.
Kubernetes-side teardown (OTEL certs, BNK CRs, FLO, cert foundation) runs first
while the kubeconfig is still valid, followed by the AWS-side teardown (ENIs,
IRSA/OIDC, jumphost, node group, EKS cluster, IAM, route tables, NAT, IGW,
subnets, VPC).

`down` flags (defined in `internal/cli/lifecycle.go`):

| Flag | Effect |
|---|---|
| `--config` / `-f` | Path to `cluster.yaml` (required). |
| `--yes` | Skip the interactive "type `destroy` to proceed" confirmation. |
| `--auto` | Skip the destroy confirmation. |
| `--dry-run` | Print the destroy plan from state and exit; makes zero AWS mutations. |
| `--keep-forge-link` | Preserve the forge link and skip forge unregister. |
| `--keep-irsa` | Retain the OIDC provider and IRSA role for reuse across cluster iterations. |

---

## 4 · Idempotency and safety nets

The lifecycle is built to survive interruptions and repeated runs.

- **Idempotent phases.** On `up`, each phase checks for an existing resource
  (by ID from the cache or by tag) before creating one, so a re-run of a healthy
  cluster is effectively a no-op. On `down`, each phase tolerates "already gone"
  by swallowing the relevant AWS not-found error codes
  (`InvalidVpcID.NotFound`, `ResourceNotFoundException`, `NoSuchEntity`, and so
  on).

- **Post-condition waits.** Where an AWS object has a delay to becoming usable
  or fully removed, the phase waits for it: EKS cluster active/deleted, node
  group active/deleted, NAT gateway deleted, Elastic IP unassociated before
  release, ENI detached before deletion.

- **Tag-discovery fallback.** Because every resource is tagged with
  `awsbnkctl:cluster=<name>`, `down` can rediscover and clean up resources even
  if the local `state.env` cache is missing or corrupt. Tags are the source of
  truth; the cache is an optimisation.

- **SSO auth sentinel.** AWS SDK calls are wrapped with middleware that watches
  for token-expiry errors. Each phase calls a check at entry that hard-exits with
  an `aws sso login --profile <profile>` hint if the session has expired
  mid-run, rather than silently failing partway through.

- **Dry-run.** `up --dry-run` and `down --dry-run` print the planned actions
  without mutating AWS. Dry-run `up` uses a read-only state so placeholder IDs
  never pollute a real cluster's `state.env`.

---

## 5 · State management

State for a cluster lives in two places:

1. **AWS tags (truth).** Every resource carries:

   | Key | Value | Purpose |
   |---|---|---|
   | `awsbnkctl:cluster` | `<metadata.name>` | Identifies the owning cluster; drives `down` discovery. |
   | `awsbnkctl:component` | e.g. `vpc`, `subnet-public`, `eks-cluster`, `eks-nodegroup`, `irsa-cne-controller`, `jumphost-instance` | Per-resource category. |
   | `awsbnkctl:managed` | `true` | Marks the resource as awsbnkctl-managed. |
   | `Name` | `<metadata.name>-<component>` | Human-readable console label. |

   Tags from `cluster.yaml: tags:` and `metadata.labels:` are merged on top.

2. **Local IDs cache.** `.awsbnkctl/<cluster-name>/state.env` is a
   shell-source-compatible `KEY=VALUE` file recording the IDs of created
   resources (VPC ID, subnet IDs, EKS cluster ARN, OIDC provider ARN, ENI IDs,
   and so on). It is written after each successful phase, read first on `down`
   with tag-discovery as the fallback, and is git-ignored — never committed.

Other per-cluster runtime files share the same directory:

- `.awsbnkctl/<name>/kubeconfig` — generated after the EKS cluster is active.
- `.awsbnkctl/<name>/forge_link.json` — the forge project + cluster link, when
  forge integration is enabled.

---

## 6 · The BNK interface patterns

`pattern:` selects how TMM (the BIG-IP Next traffic management microkernel) gets
its data-plane network interfaces. A pattern fixes **two orthogonal axes** —
interface *topology* (single external vs external + internal) and interface
*binding* (kernel `host-device` vs SR-IOV/`vfio-pci` DPDK). Backend pods are
always reached over the CNI (`TMM_CALICO_ROUTER: default`).

| `pattern:` | Topology | Binding | Internal subnet | Min ENIs | Status |
|---|---|---|---|---|---|
| `external-only` | external only | host-device | no | 2 | supported |
| `dual-interface` | external + internal | host-device | yes | 3 | supported |
| `sriov-external` | external only | sriov / vfio-pci | no | 2 | **gated (experimental)** |

`host-device` is the **legacy alias** for `dual-interface` (normalized at load),
so existing configs keep working unchanged. The phases read intent through three
capability helpers on `Cluster` — `IsBNKPattern()`, `HasInternalInterface()`,
`DataplaneBinding()` — rather than comparing the pattern string, so a fourth
preset (e.g. dual + sriov) only needs a row in those helpers.

Common to every BNK pattern:

- `network.dataPath.external` declares the TMM client-side / ingress subnet,
  separate from the worker and EKS subnets.
- The phases label one worker node to host TMM, attach the external secondary
  ENI (device-index 3) from the external subnet to it, assign the TMM external
  SelfIP as a secondary private IP on that ENI (AWS won't route a SelfIP
  otherwise), and announce it inside the TMM pod netns via an F5 SPK VLAN CR.
- Kubernetes prerequisites — a cloud-network-mapping ConfigMap, the external
  NetworkAttachmentDefinition, and an IRSA service account — wire the
  cne-controller and TMM to the interface.

`dual-interface` additionally declares `network.dataPath.internal` (the TMM
backend-side / server-side subnet, for reaching a backend in another cluster or
location), attaches a second secondary ENI (device-index 2), and adds the
internal NAD + `int-vlan`. Single-interface patterns omit all of that and reach
in-cluster backends over the CNI; the internal block is rejected at validation.

The **ENI floor** is `primary + one TMM secondary per data-plane interface` — 2
for single-interface, 3 for dual. No dedicated EKS-CNI secondary is counted
because VPC CNI prefix delegation keeps pod IPs on the primary ENI. Preflight
also enforces CPU/memory and dSSM node-quorum minimums before provisioning. An
optional test jumphost (`testing.jumphost.enabled`) provisions a multi-ENI
instance plus an EC2 Instance Connect Endpoint inside the external data-path
subnet so operators can verify TMM SelfIP routing without standing up EC2 by
hand; it works for any BNK pattern.

`sriov-external` is reserved in the schema but **blocked at validation** pending a
live ENA/`vfio-pci` feasibility spike — SR-IOV/DPDK on AWS ENA is undocumented
and unsupported by F5 on the EKS host build. See
`docs/spikes/sriov-ena-vfio/README.md` for the go/no-go gate.

---

## 7 · Where things live

| Area | Location |
|---|---|
| Intent schema + loader + validation | `internal/intent/` |
| AWS SDK clients + provisioning phases | `internal/aws/` (phases under `internal/aws/phases/`) |
| Tag scheme + helpers | `internal/aws/tags/` |
| IDs cache reader/writer + tag-discovery | `internal/aws/state/` |
| Kubernetes apply (server-side apply via client-go) | `internal/k8s/` |
| Forge handoff | `internal/forge/` |
| CLI commands + lifecycle wiring | `internal/cli/` |
| Runnable example topologies | `examples/` |
