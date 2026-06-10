# Air-gapped install: mirroring BNK into the cluster registry

Some ROKS environments forbid pulling install resources from external repositories
at deploy time. For these, `roksbnkctl` can **replicate every artifact BNK needs**
— the F5 charts and images, plus the non-F5 dependencies — from the F5 Artifact
Repository (FAR, `repo.f5.com`) into the cluster's **own OpenShift internal
registry**, and then install BNK entirely from there. With the mirror in place, the
cluster needs no egress to `repo.f5.com`, `quay.io`, `docker.io`, or
`charts.jetstack.io` to bring up BNK.

The registry surface is CRUD-shaped, modeled on the IBM COS client:

| Command | What it does |
|---|---|
| `roksbnkctl registry bom` | Print the bill-of-materials — every chart + image BNK needs |
| `roksbnkctl registry replicate` | Mirror the BOM into the target registry |
| `roksbnkctl registry list` | What's present in the target |
| `roksbnkctl registry diff` | BOM vs. target — what's missing or stale |
| `roksbnkctl registry verify` | Every BOM artifact present + digest-matched |
| `roksbnkctl registry prune` | Remove mirrored artifacts no longer in the BOM |

## The bill-of-materials

The BOM comes from the **`f5-bigip-k8s-manifest`** that FAR publishes per BNK
release — the same manifest `roksbnkctl` reads for version discovery. It
enumerates every F5 chart and image (for BNK 2.3, that's 25 charts + 56 images),
tag-pinned. `roksbnkctl` unions in the two dependencies the F5 manifest does not
cover — **cert-manager** (the Jetstack chart + its quay.io images) and the
**`bitnami/kubectl`** node-labeler image — so the mirror is complete:

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
$ roksbnkctl cluster up                              # the durable cluster
$ roksbnkctl registry replicate --target openshift   # mirror the BOM (one-time per BNK version)
$ roksbnkctl registry verify                          # confirm completeness
$ roksbnkctl bnk up                                   # installs from the internal registry
```

`registry replicate` runs **after** the Cluster phase and **before** BNK. It is a
deliberate, occasional supply-chain step — it is **not** part of the composite
`roksbnkctl up` (like `gateway`, you run it explicitly). It copies each artifact
registry-to-registry by digest, idempotently — re-running it only moves what
changed (e.g. after a BNK version bump, `registry diff` shows exactly the changed
artifacts).

Once the mirror is populated, the BNK phase reads it and redirects the install: the
container images resolve from the cluster's internal registry, and BNK comes up
with no external pulls. The Stage acceptance is exactly this, with egress to the
external registries blocked.

## How it works: the registry's two faces

The OpenShift internal registry is reachable two ways, and `roksbnkctl` uses both:

- **Charts** are pulled by the in-process helm provider on the `roksbnkctl` host,
  over the registry's external **route** (`oci://<route>/<ns>/charts/…`).
- **Images** are pulled by the cluster's pods over the in-cluster **service**
  (`image-registry.openshift-image-registry.svc:5000/<ns>/images/…`), authorized by
  a `system:image-puller` role binding — **no image pull secret**.

`registry replicate` bootstraps this for you: it enables the registry's default
route, creates a mirror project, mints a push credential, and binds the pull RBAC.

> **Custom ingress certificate.** The host pulls charts over the route, trusting the
> cluster's `*.apps` ingress certificate. ROKS apps domains carry a publicly-trusted
> cert by default. If your cluster uses a custom or self-signed ingress cert, add its
> CA to the host's trust store before `registry replicate`.

## Other targets

The OpenShift internal registry is the first-class target. The mirror is built
against a pluggable `RegistryTarget`, so IBM Container Registry (`icr.io`) and a
generic private OCI registry (Harbor / Artifactory / Quay) are natural follow-on
targets — `--target` selects which.
