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
