# Registry targets: ICR and a private Artifactory

The [air-gapped install](./10a-air-gapped-install.md) chapter mirrors BNK into
the cluster's **own OpenShift internal registry**. That is one of three targets
`roksbnkctl registry replicate` can populate:

| `registry.target` | Backend | Push / pull host |
|---|---|---|
| `icr` *(default)* | IBM Container Registry | `<region>.icr.io/<namespace>` |
| `generic` | Any OCI registry — Artifactory, Harbor, `registry:2` | `<host>/<repo-prefix>` |
| `openshift` | The cluster's internal registry (air-gap) | route + in-cluster service |

`icr` and `generic` are ordinary OCI registries: push, image-pull, and
chart-pull all use the **same host**, with the FAR category nested under a
namespace (`<host>/<ns>/images/<name>`). The `openshift` target is the special
case — its flat `<project>/<name>` registry splits push (route) from image-pull
(in-cluster service).

## Configuring the target — `registry target`

Configure the mirror with the **`registry target`** command — no `config.yaml`
editing required. With no arguments it prints the current target and fields;
otherwise the first argument is a backend **kind** (`icr` / `generic` /
`openshift`) or a **field** name followed by its value. Everything it sets is
written to the workspace `config.yaml`.

```bash
roksbnkctl registry target                       # show the current target + fields
roksbnkctl registry target icr                   # select a backend kind
roksbnkctl registry target icr_namespace my-bnk  # set a field
roksbnkctl registry replicate --target generic   # or override the backend for one run
```

Recognized fields: `icr_host`, `icr_namespace`, `generic_host`,
`generic_repo_prefix`, `generic_username`, `generic_password` (the last reads
from stdin with `--password-stdin`).

## The default is now ICR

> **Migration note.** As of this release, an empty target resolves to **`icr`**,
> not `openshift`. If you rely on the air-gap OpenShift internal registry, select
> it explicitly once:
>
> ```bash
> roksbnkctl registry target openshift
> ```
>
> Existing workspaces that mirrored into the OpenShift registry must run this;
> otherwise `registry replicate` will target ICR.

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

The install pulls images and charts from `acme.jfrog.io/bnk-mirror`. The cluster
must be able to reach Artifactory and present the same credentials (an image
pull secret for the registry); see your Artifactory + cluster networking for the
pull-secret wiring.

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
