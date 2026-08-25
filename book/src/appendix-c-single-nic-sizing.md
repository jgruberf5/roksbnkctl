# C — Single-NIC sizing

Sizing figures here come from F5's *BIG-IP Next for Kubernetes 2.3.1 on Red Hat
OpenShift on IBM Cloud — Single-NIC Sizing Guide*, which derives them from
reference testing of BNK 2.3.1/2.3.2 on ROKS. They are reproduced rather than
computed: `roksbnkctl` does not model throughput, and nothing here is our
measurement.

## Only Small and Medium are ever needed

The CNEInstance's `deploymentSize` accepts `Tiny`, `Small`, `Medium`, `Large` and
`Max`. **The reference designs use only `Small` and `Medium`.** That is the single
most useful thing to know about sizing, and it is easy to miss, because F5's
guide names three *cluster* sizes — Small, Medium and Large — and those names do
not mean the same thing as `deploymentSize`.

The Large **cluster** runs the **Medium** BNK profile. It reaches ~31 Gbit/s not
by asking BNK for a bigger profile, but by putting nine TMM pods on nine larger
nodes, three per availability zone. Capacity comes from pod count and node size,
not from a larger `deploymentSize`. So if you find yourself reaching for
`deploymentSize: Large`, the guide's answer is almost certainly more nodes
instead.

| | Small | Medium | Large |
|---|---|---|---|
| Worker nodes | 6 (2 per AZ) | 6 (2 per AZ) | 9 (3 per AZ) |
| Reference-tested flavour | `cx3d.8x20` | `cx2.16x32` | `cx2.48x96` |
| **Recommended flavour** | **`bx2.8x32`** | `cx2.16x32` | `cx2.48x96` |
| `deploymentSize` | `Small` | `Medium` | **`Medium`** |
| TMM pods | 3 (1 per AZ) | 3 (1 per AZ) | 9 (3 per AZ) |
| CPU free for applications | 38.8 % | 60 % | 82.9 % |
| L4 ingress per AZ | ~2.6 Gbit/s | ~3.5 Gbit/s | ~10.4 Gbit/s |
| L4 ingress, cluster | ~7.8 Gbit/s | ~10.4 Gbit/s | ~31 Gbit/s |

Two footnotes on the flavours. For Small, the guide recommends `bx2.8x32` over
the reference-tested `cx3d.8x20`: the tested flavour works but leaves only ~24
GiB cluster-wide for applications, and the balanced variant buys four times that
for a modest price step.

At the other end, the guide reports `cx2.8x16` as leaving **0.1 % memory free**
and not holding the platform. `roksbnkctl` does **not** refuse it — you can set
it, and the tool will build it. It is recorded here so the number is visible
before you choose, not to make the decision for you. A per-node DaemonSet floor
of ~2.9 vCPU (OpenShift/Calico, IBM observability, BNK node agents) consumes 36 %
of an 8-vCPU node before anything of yours is scheduled, which is what makes the
16 GiB variant tight and why the small flavours generally trade application CPU
for cost.

All of this is **single-NIC**. Multi-NIC is expected to change the picture,
because per-pod throughput is bounded by TMM thread count rather than by the
worker node's NIC — so higher-performance nodes become worth adding in a way they
are not here. Treat this appendix as describing the single-NIC case only, and do
not extrapolate it.

## Reference deployments

The figures above are F5's. This section is ours: one clean deployment per
sizing, on ROKS, recorded so a reader can tell what has actually been run from
what has only been reproduced from a guide.

Every run below is a fresh install onto a fresh cluster, pinned to the manifest
the BNK 2.4 IBM install guide documents — **`2.4.0-EA`**, pulled from
`repo.f5.com` — with `containerPlatform: IBM` and **one namespace** for every
component.

One trap worth naming: **BNK 2.4 is Early Access, and the ordinary production
FAR token cannot see it.** Both keys live in the same production project, so
this is a GA-versus-non-GA content split rather than a prod-versus-test one:

| FAR key | can pull |
|---|---|
| `cne-gar-pull` (standard production) | `2.3.0` only — no 2.4 version resolves |
| `non-ga-prod-pull` (non-GA production grant) | `2.4.0-EA` |

`2.4.0`, `2.4.0-GA` and `2.4.1` do not resolve with either key, so until 2.4
goes GA the non-GA production grant is required. The failure mode is
misleading: the wrong key authenticates happily and then reports
`f5-bigip-k8s-manifest:2.4.0-EA: not found`, which reads like a wrong version
rather than a wrong credential.

When 2.4 GAs this becomes a two-line change — `bnk.manifest_version` and the
key file. Nothing about the configuration shape depends on it. A run counts as verified only if all of the
following hold at once:

- exactly one `f5-*` namespace exists, and no `f5-utils`
- the `CNEManifest` version matches the reference build exactly
- every component CR reports `Available=True` — `CNEInstance`, `F5Tmm`, `CSRC`,
  `Cwc`, `DSSM`, `Coremond`, `Observer`, `RabbitMQ`
- CSRC pods are Running and the `macvlan-internal`
  NetworkAttachmentDefinition exists — CSRC creates it at runtime, so its
  absence is the visible symptom of a wrong `containerPlatform`
- the licence reports `Active`
- no pod anywhere is outside `Running`/`Completed`

`scripts/sizing/sizing-matrix.sh` builds and checks one sizing per invocation.

| | Baseline | Small cluster | Medium cluster | Large cluster |
|---|---|---|---|---|
| Worker nodes | 3 (1 per AZ) | 6 (2 per AZ) | 6 (2 per AZ) | 9 (3 per AZ) |
| Flavour | `bx2.16x64` | `bx2.8x32` | `cx2.16x32` | `cx2.48x96` |
| `deploymentSize` | `Tiny` | `Tiny` | `Tiny` | `Tiny` |
| `tmmReplicas` | 1 | 3 | 3 | 9 |
| Verified | **yes** | **yes** | **yes** | **yes** |

All four are verified: every component CR reporting `Available=True`, every TMM
pod `5/5 Running`, `macvlan-internal` present, licence `Active`, and nothing
outside `Running`/`Completed`. On the Large cluster the nine TMM pods land on
nine distinct nodes, three per availability zone, which is the anti-affinity and
topology-spread arrangement F5's reference configuration specifies.

Note the third row. **`deploymentSize` is `Tiny` everywhere** — the column
headings are *cluster* sizes, and the thing that changes between them is the
node flavour and `tmmReplicas`, not the BNK profile. The "Baseline" column is
the shape the BNK 2.4 IBM install guide itself describes (a 3-node 16-vCPU
cluster); the other three are F5's sizing-guide cluster shapes.

### `deploymentSize` stays `Tiny`; capacity comes from replicas and node size

This is the single most important thing to know about running BNK 2.4 on ROKS,
and it is easy to get wrong because F5's sizing guide names its *cluster* sizes
Small, Medium and Large — the same words `deploymentSize` uses.

**They are not the same setting.** The BNK 2.4 IBM install guide uses
`deploymentSize: "Tiny"` with `tmmReplicas: 3`, and that is what every cluster
size below runs. Capacity comes from **how many TMM pods and how big the nodes
are**, never from a larger `deploymentSize` — which is exactly the point this
appendix already makes about the Large cluster running the Medium profile.

`deploymentSize` above `Tiny` cannot run on ROKS at all, and the reason is worth
recording because the failure is silent. Every size above `Tiny` requests
hugepages, and the amount **grows** with the profile. Read from the controller-derived `F5Tmm` CR on a live 2.4 cluster by
changing only `deploymentSize`:

| `deploymentSize` | TMM cpu | TMM memory | `hugepages-2Mi` |
|---|---|---|---|
| `Tiny` | — | — | **none** |
| `Small` | 1 | 1536Mi | **4 GiB** |
| `Medium` | 4 | 2Gi | **8 GiB** |

The **Large cluster runs the Medium profile**, as the table above this section
records, so it derives the same 8 GiB and is not a separate case.

TMM validates hugepages against the 2 MB page cgroup limit when it starts, and
exits if that limit is zero:

```
<get_mem_info>  ERROR: No memory available based on 2MB page cgroup limit
<init_memory>   ERROR: Could not validate sufficient hugepages
<main>          ERROR: Failed to create cmdline args
```

It exits `code 0`, so this does not look like a crash — the pod reports
`4/5 Running` and the Deployment simply never becomes Available.

**A ROKS cluster cannot allocate hugepages.** Six approaches were measured on
live 4.21 clusters and every one fails:

| approach | result |
|---|---|
| `bnk.hugepages` (Node Tuning Operator, `[bootloader]`) | needs the Machine Config Operator; ROKS has **0 MachineConfigPools** |
| `Tuned` with `[sysctl] vm.nr_hugepages` | ROKS **deletes the CR** — created object returned, next read `NotFound` |
| privileged pod writing `/proc/sys/vm/nr_hugepages` | allocates (`HugePages_Total: 2048`) but kubelet still advertises `allocatable: 0` |
| `advanced.tmm.resources` with `hugepages-2Mi: 0` | TMM schedules, then refuses to start (above) |
| `advanced.tmm.env.TMM_MEM` | accepted, but hugepages are still required |
| `ibmcloud ks worker-pool` | no kernel-argument or sysctl option exists |

Note that `bnk.hugepages` is therefore a **no-op on ROKS**, even though it is
what the scheduling failure recommends. Setting it applies a `Tuned` CR that
ROKS removes, which surfaces as terraform reporting a provider bug.

F5's 2.3.1 sizing guide describes the reference-tested Small profile as
*"TMM: 1 thread, 1 vCPU / 1.5 GiB, hugepages disabled"*, and its TMM resource
table lists CPU and memory only. That configuration cannot be reproduced on the
2.4 build: Small derives a hugepages request, and TMM will not start without
one. Whether the 2.3.1 profile still exists in 2.4 is an open question with F5
(issue #203).

Until it is answered, treat the Small, Medium and Large rows above as F5's
published figures rather than as configurations this tool can stand up on ROKS.
`Tiny` is the only verified one, which is consistent with F5's own reference
cluster running `Tiny`.

`Tiny` is not in F5's sizing guide — it is the profile the engineering reference
cluster itself runs, recorded here as the smallest configuration known to work
end to end, not as a recommendation for production traffic.

### Two things worth knowing before you reproduce these

**Set `bnk.manifest_version` explicitly.** A workspace that leaves it unset
installs the **2.3** line, and the first sign of it is a `CNEInstance`
validation error several minutes into the apply complaining that
`deploymentSize: Tiny` is unsupported — because `Tiny` does not exist on 2.3.
The message names the size, not the version, which sends you looking in the
wrong place entirely.

**`bnk down` does not leave a clean cluster.** It returns success while leaving
the namespace and all of BNK's CRDs installed. That is harmless when
reinstalling the same release line, and actively misleading when changing
manifest versions, because the previous line's CRD schema stays in force. To
reinstall from genuinely clean state, delete the BNK namespace and the
`*.k8s.f5.com` CRDs after `bnk down` and before the next `bnk up`.

## What this looks like for a real deployment

The sizing above assumes you are building the cluster. Most deployments are not
like that. The common shape is that a platform team **already has** the VPC, the
transit gateway and the ROKS cluster, built to their own standards, and wants
`roksbnkctl` to install BNK onto it — `cluster register` rather than `cluster up`.

In that case the table is a specification to check your existing cluster
*against*, not a thing to provision. Worth confirming before installing: node
count and distribution across three AZs, the flavour's memory variant against the
"free for applications" column, and that the cluster is not on `cx2.8x16`.

Those clusters are also usually **disconnected**: no route to `repo.f5.com`, so
BNK images come from a mirrored registry and licensing goes through an FLP rather
than direct to F5. That combination — adopted cluster, adopted transit gateway,
mirrored registry, FLP licensing — is the path most installations will actually
take, and it is the one worth exercising hardest.

> **Coverage note.** The five-run validation behind the 2.4 support claim used a
> cluster this tool *created*, with connected licensing. The adopted-cluster,
> disconnected, mirrored-registry, FLP path is supported and has its own demos,
> but it has not had the same repeated-run treatment. Read the 2.4 support status
> with that distinction in mind rather than assuming it covers the shape you are
> most likely to deploy.
