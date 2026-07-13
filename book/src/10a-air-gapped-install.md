# Air-gapped install: mirroring BNK into a private registry

Some ROKS environments forbid pulling install resources from external repositories
at deploy time. For these, `roksbnkctl` can **replicate every artifact BNK needs**
— the F5 charts and images, plus the non-F5 dependencies — from the F5 Artifact
Repository (FAR, `repo.f5.com`) into a **private registry you control**, and then
install BNK entirely from there. With the mirror in place, the cluster needs no
egress to `repo.f5.com`, `quay.io`, `docker.io`, or `charts.jetstack.io` to bring
up BNK.

The mirror target is a registry you own — **IBM Container Registry (ICR)**, the
default, or a **generic OCI-compliant registry** (Artifactory, Harbor, Quay,
`registry:2`). Choosing and configuring the target is its own chapter,
[Registry targets: ICR and Artifactory](./10b-registry-targets.md); this chapter
is the air-gap flow itself.

The registry surface is CRUD-shaped, modeled on the IBM COS client:

| Command | What it does |
|---|---|
| `roksbnkctl registry target` | Show or set the mirror target — `icr` / `generic` (see [Registry targets](./10b-registry-targets.md)) |
| `roksbnkctl registry bom` | Print the bill-of-materials — every chart + image BNK needs |
| `roksbnkctl registry replicate` | Mirror the BOM into the target registry |
| `roksbnkctl registry list` | What's present in the target |
| `roksbnkctl registry diff` | BOM vs. target — what's missing or stale |
| `roksbnkctl registry verify` | Every BOM artifact present + digest-matched |
| `roksbnkctl registry prune` | Remove mirrored artifacts no longer in the BOM |
| `roksbnkctl registry delete` | Delete **all** replicated artifacts from the target + clear the record |

## The bill-of-materials

The BOM comes from the **`f5-bigip-k8s-manifest`** that FAR publishes per BNK
release — the same manifest `roksbnkctl` reads for version discovery. It
enumerates every F5 chart and image (for BNK 2.3, that's 25 charts + 56 images),
tag-pinned. `roksbnkctl` unions in the two dependencies the F5 manifest does not
cover — **cert-manager** (the Jetstack chart + its quay.io images) and the
**`bitnami/kubectl`** node-labeler image — plus **the manifest chart itself**
(`release/f5-bigip-k8s-manifest`), because the install needs to read it and a
mirror that lacked it would send every install back to `repo.f5.com`. So the
mirror is complete:

```console
$ roksbnkctl registry bom
charts  repo.f5.com/charts/f5-lifecycle-operator   v2.21.13-0.0.28
charts  repo.f5.com/charts/f5-tmm                  15.430.5-0.2.157
...
images  repo.f5.com/images/tmm-img                 v10.159.3-0.1.5
images  quay.io/jetstack/cert-manager-controller   v1.17.3
images  docker.io/bitnami/kubectl                  <tag>
...
81 F5 artifacts + 6 dependency artifacts
```

## The walk-through

```console
$ roksbnkctl cluster up               # the durable cluster
$ roksbnkctl registry target icr      # choose the target (icr is the default)
$ roksbnkctl registry replicate       # mirror the BOM (one-time per BNK version)
$ roksbnkctl registry verify          # confirm completeness
$ roksbnkctl bnk up                   # installs from the mirror
```

`registry replicate` runs **after** the Cluster phase and **before** BNK. It is a
deliberate, occasional supply-chain step — it is **not** part of the composite
`roksbnkctl up` (like `gateway`, you run it explicitly). With no `--target`, it
uses the workspace's `registry.target` (default `icr`). It copies each artifact
registry-to-registry by digest, idempotently — re-running it only moves what
changed (e.g. after a BNK version bump, `registry diff` shows exactly the changed
artifacts).

## How the install reads the mirror

`registry replicate` records what it copied — and where — in
`registry-mirror.json`. The BNK phase reads that record and **redirects the
install** to the target: every chart and image resolves from your private
registry instead of FAR, so BNK comes up with no external pulls. The air-gap
acceptance is exactly this, with egress to the external registries blocked.

The target's own pull credential is wired in for you:

- **ICR** — the cluster authenticates to `*.icr.io` with `iamapikey` + the
  workspace IBM Cloud API key, for both the FLO chart pull and the image pulls.
- **Generic OCI** — chart and image pulls authenticate with the same basic-auth
  credential replication used (`registry target generic_username` /
  `generic_password`), so a private registry needs no anonymous/public project.

### Why the install never phones home

Two things have to be true for a mirrored install to be genuinely disconnected,
and `roksbnkctl` handles both:

1. **The manifest is mirrored.** `f5-bigip-k8s-manifest` is the BOM's own source,
   so it is easy to overlook — but the install reads it to derive the FLO and CIS
   chart versions. It is a normal OCI chart, so it is replicated like any other
   artifact and pulled from the mirror.
2. **FLO reads the manifest from the cluster, not a registry.** The F5 Lifecycle
   Operator resolves the BNK manifest by listing cluster-scoped **`CNEManifest`**
   CRs and matching `spec.version`; only when none matches does it fall back to
   pulling the manifest chart from the CNEInstance's `spec.registry.uri`.
   `roksbnkctl` converts the manifest into a `CNEManifest` CR and applies it before
   the CNEInstance, so FLO finds it in the cluster and **never fetches a manifest at
   all**. (Without the CR, that fallback is what breaks a mirrored install: FLO
   reports *"No CNEManifest exists which contains expected manifestVersion"* and the
   CNEInstance never reconciles.)

The per-target host, namespace/repository, and credential specifics — including
the ICR namespace and a full Artifactory walkthrough — are in
[Registry targets](./10b-registry-targets.md).

> **TLS trust.** Charts and images are pulled over the target's HTTPS endpoint.
> ICR and a public Artifactory carry publicly-trusted certificates. If your
> registry uses a custom or self-signed cert, add its CA to the trust store on the
> host running `registry replicate` (and on the cluster, for the image pulls)
> before you replicate.
