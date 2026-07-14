# Registry targets: ICR and a private Artifactory

The [air-gapped install](./10a-air-gapped-install.md) chapter mirrors BNK into a
private registry you control. There are two targets `roksbnkctl registry
replicate` can populate:

| `registry.target` | Backend | Push / pull host |
|---|---|---|
| `icr` *(default)* | IBM Container Registry | `<region>.icr.io/<namespace>` |
| `generic` | Any OCI registry — Artifactory, Harbor, Quay, `registry:2` | `<host>/<repo-prefix>` |

Both are ordinary OCI registries: push, image-pull, and chart-pull all use the
**same host**, with the FAR category nested under a namespace
(`<host>/<ns>/images/<name>`).

## Configuring the target — `registry target`

Configure the mirror with the **`registry target`** command — no `config.yaml`
editing required. With no arguments it prints the current target and fields;
otherwise the first argument is a backend **kind** (`icr` / `generic`) or a
**field** name followed by its value. Everything it sets is written to the
workspace `config.yaml`.

```bash
roksbnkctl registry target                       # show the current target + fields
roksbnkctl registry target icr                   # select a backend kind
roksbnkctl registry target icr_namespace my-bnk  # set a field
roksbnkctl registry replicate --target generic   # or override the backend for one run
```

Recognized fields: `icr_host`, `icr_namespace`, `generic_host`,
`generic_repo_prefix`, `generic_username`, `generic_password` (the last reads
from stdin with `--password-stdin`).

## The default is ICR

An empty `registry.target` resolves to **`icr`**. Set `generic` explicitly to
mirror into an OCI-compliant registry such as Artifactory.

> **The OpenShift internal registry was removed as a target.** Earlier releases
> supported an `openshift` target that mirrored into the cluster's own internal
> registry; it has been removed in favor of ICR and OCI-compliant registries. A
> workspace still carrying `registry.target: openshift` now errors —
> `unsupported registry target "openshift" (expected icr or generic)` — switch it
> with `roksbnkctl registry target icr` (or `generic`).

### ICR configuration

```bash
roksbnkctl registry target icr
roksbnkctl registry target icr_namespace my-bnk   # optional — defaults to the workspace prefix
roksbnkctl registry target icr_host de.icr.io     # optional — derived from ibmcloud.region
```

- **Host** comes from `ibmcloud.region` (`eu-de` → `de.icr.io`, `eu-gb` →
  `uk.icr.io`, `us-south`/`us-east` → `us.icr.io`, …). Override with `icr_host`
  for any region the map doesn't cover.
- **Namespace** is the ICR tenant unit; it defaults to the workspace `prefix`.
  **Create it first** — `roksbnkctl` does not provision the ICR namespace:
  ```bash
  ibmcloud cr namespace-add my-bnk
  ```
- **Auth** is `iamapikey` + your workspace IBM Cloud API key (resolved the usual
  way). On the cluster, pods pull from `<region>.icr.io` using the ROKS global
  `*.icr.io` pull secret.

## Walkthrough: replicate FAR into a private Artifactory

A `generic` target works against any standard OCI registry. Here is the full
flow for a private **JFrog Artifactory** OCI repository.

**1 — Provision an Artifactory OCI repository.** In Artifactory, create a local
**Docker/OCI** repository (e.g. `bnk-mirror`) and an access token with push +
pull on it. Note the registry host (e.g. `acme.jfrog.io`) and the repository
key.

**2 — Configure the workspace.** Set the generic target with `registry target`,
feeding the token via stdin so it never lands in your shell history or argv:

```bash
roksbnkctl registry target generic -w prod
roksbnkctl registry target generic_host acme.jfrog.io -w prod
roksbnkctl registry target generic_repo_prefix bnk-mirror -w prod
roksbnkctl registry target generic_username ci-bot -w prod
echo "$ARTIFACTORY_TOKEN" | roksbnkctl registry target generic_password --password-stdin -w prod

roksbnkctl registry target -w prod   # confirm (the password shows as "(set)")
```

For a fully unattended (CI) workspace you can instead supply the token through
`ROKSBNKCTL_GENERIC_PASSWORD` at `init --override-from-env` time — see
[Unattended setup](./07a-unattended-setup.md).

**3 — Replicate.** Preview, then mirror the bill-of-materials into Artifactory:

```bash
roksbnkctl registry diff -w prod        # what replicate would copy
roksbnkctl registry replicate -w prod   # copy charts + images into acme.jfrog.io/bnk-mirror
roksbnkctl registry verify -w prod      # every BOM artifact present + digest-matched
```

Artifacts land at `acme.jfrog.io/bnk-mirror/{images,charts,jetstack,...}/<name>`.

**4 — Install from the mirror.** `replicate` records the target in
`registry-mirror.json`, which redirects the BNK install off `repo.f5.com` onto
the Artifactory mirror automatically:

```bash
roksbnkctl bnk up -w prod
```

The install pulls images and charts from `acme.jfrog.io/bnk-mirror`, using the same
credential you replicated with — **you do not wire the pull secret yourself**. Chart pulls
log in with `generic_username` / `generic_password`, and the pods get a `mirror-secret`
dockerconfig built from that credential, created in every namespace that pulls images
(cert-manager, the FLO/BNK namespaces, and `kube-system` for the node-labeler) and
referenced from the CNEInstance.

So a **private** repository is fine — the registry does *not* need an anonymous or public
project. The only requirement is network reachability from the cluster.

**Licensing from the same mirror.** The mirror's BOM includes `charts/f5-license-proxy`,
so the F5 License Proxy is replicated alongside BNK and can be installed from your
registry too — including the shared-licensing topology, where one cluster runs the proxy
and other clusters license through it while reaching nothing but this mirror. See
[Licensing BNK with the F5 License Proxy](./10c-flp-licensing.md).

## Removing a mirror

To wipe everything you replicated and revert the install to pulling from FAR:

```bash
roksbnkctl registry delete -w prod          # confirms first; --force to skip
```

`registry delete` removes every artifact recorded in `registry-mirror.json` from
the target (by digest) and clears the record. Artifacts that fail to delete are
kept in the record so a re-run retries exactly those. For `icr` the API key needs
**Manager** (delete) rights on the namespace; for `generic` the registry must
have deletes enabled. (To remove only artifacts that are no longer in the current
BOM, use `registry prune` instead.)
