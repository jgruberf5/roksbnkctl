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

Select a target in the workspace config or per-run:

```yaml
registry:
  target: icr            # or generic, or openshift
```

```bash
roksbnkctl registry replicate --target generic   # --target overrides the config
```

## The default is now ICR

> **Migration note.** As of this release, an empty `registry.target` resolves to
> **`icr`**, not `openshift`. If you rely on the air-gap OpenShift internal
> registry, set it explicitly:
>
> ```yaml
> registry:
>   target: openshift
> ```
>
> Existing workspaces that mirrored into the OpenShift registry must add this
> line; otherwise `registry replicate` will target ICR.

### ICR configuration

```yaml
registry:
  target: icr
  icr_host: de.icr.io       # optional — derived from ibmcloud.region if omitted
  icr_namespace: my-bnk     # optional — defaults to the workspace prefix
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

**2 — Configure the workspace.** Set the generic target. Keep the token out of
the committed config by supplying it from the environment (see
[Unattended setup](./07a-unattended-setup.md)):

```yaml
registry:
  target: generic
  generic_host: acme.jfrog.io
  generic_repo_prefix: bnk-mirror
  generic_username: ci-bot
  generic_password_b64: ""     # ← ROKSBNKCTL_GENERIC_PASSWORD (raw token, base64-encoded)
```

```bash
export ROKSBNKCTL_GENERIC_PASSWORD="$ARTIFACTORY_TOKEN"
roksbnkctl init -w prod --config-file config.yaml --override-from-env
```

(Or set `generic_password_b64` directly — it is the base64 of the raw token,
obfuscation only; `chmod 600`, never commit.)

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
