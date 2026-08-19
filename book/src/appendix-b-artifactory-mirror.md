# B — Replicating FAR into an existing registry

This appendix mirrors the F5 Artifact Repository into a registry **you already
run** — a JFrog Artifactory, or IBM Cloud Container Registry. It does not cover
standing either one up; it is for the common case where the registry is already
your organization's registry of record and BNK is simply one more thing that has
to live in it.

Two targets, one set of verbs. `bom`, `diff`, `replicate` and `verify` are
identical either way — only the four lines that select the target differ. Start
with [ICR](#ibm-cloud-container-registry) if you are already on IBM Cloud, since
it needs no credentials of its own, or
[Artifactory](#jfrog-artifactory) if that is your registry of record.

Nothing here needs a Kubernetes cluster. `registry replicate` copies
registry-to-registry from wherever `roksbnkctl` runs, so the mirror can be
seeded as a supply-chain step long before a cluster exists — which is usually
the point, since the cluster is air-gapped and the mirror is what makes it
installable at all.

## IBM Cloud Container Registry

ICR is the shortest path on IBM Cloud: it reuses the workspace's own IBM Cloud
API key, so there is no second credential to create, distribute, or rotate.

### What ICR needs first

**A namespace.** ICR's namespace is the tenant unit artifacts nest under, and
`roksbnkctl` does not create it:

```bash
ibmcloud cr region-set us-east
ibmcloud cr namespace-add bnk-mirror
```

That is the only prerequisite. There is no repository to pre-create — ICR
creates repositories on first push, which is the main practical difference from
Artifactory.

### Pointing roksbnkctl at it

```bash
roksbnkctl registry target icr
roksbnkctl registry target icr_namespace bnk-mirror
```

Both remaining fields derive themselves:

| Field | Derived from | Override when |
|---|---|---|
| host | `ibmcloud.region` — `us-east` → `us.icr.io`, `eu-de` → `de.icr.io`, `eu-gb` → `uk.icr.io` | your region is not in the map, or you are deliberately mirroring to another region's registry |
| namespace | the workspace `prefix` | you want a name that is not the prefix |

Set the host explicitly with `roksbnkctl registry target icr_host de.icr.io`.

Authentication is `iamapikey` plus the workspace's IBM Cloud API key, resolved
the same way every other IBM Cloud call resolves it. Nothing further to
configure — and on the cluster side, pods pull from `*.icr.io` using the pull
secret ROKS already provides, so an ICR mirror needs no image-pull secret work
either.

### Replicating

```bash
roksbnkctl registry bom
roksbnkctl registry diff
roksbnkctl registry replicate
roksbnkctl registry verify
```

Then confirm from the IBM Cloud side, which is worth doing once because it
proves the artifacts are visible to something other than the tool that wrote
them:

```bash
ibmcloud cr images --restrict bnk-mirror
```

### Choosing between the two

| | ICR | Artifactory |
|---|---|---|
| Credential | the workspace IBM Cloud API key | a separate user + access token |
| Namespace / repository | namespace created once; repositories appear on push | a **local Docker** repository created in advance |
| TLS | publicly trusted | publicly trusted, or a private CA you supply |
| Cluster pull secret | already present on ROKS | needs configuring |
| Edition constraints | none | Docker repositories are a licensed feature |

ICR is fewer moving parts on IBM Cloud. Artifactory is the right answer when it
is already where your organization's artifacts live, or when the cluster is not
on IBM Cloud at all.

## JFrog Artifactory

### What Artifactory has to provide

Three things must exist before `roksbnkctl` is involved, and all three are
created through the Artifactory UI or its REST API. This is the manual part.

**A local Docker repository.** BNK's artifacts are OCI images and OCI-packaged
Helm charts, so the target must be a Docker repository — a *local* one, not a
remote or virtual one. Remote repositories are read-through caches of an
upstream and cannot be pushed to; a virtual repository can only be pushed to if
it has a local deployment target, so pointing at the local repository directly
is simpler and fails more clearly when permissions are wrong.

> **Artifactory OSS cannot do this.** Docker repository support is a licensed
> feature. An OSS instance has no Docker repository type at all, so there is
> nothing to point at. A Pro, Enterprise, or Cloud instance — including a trial
> — is required. This is a property of Artifactory, not of `roksbnkctl`, and it
> is worth confirming before planning an air-gap rollout around it.

**A Docker access method.** Artifactory exposes Docker repositories in one of
two ways, and which one your instance uses decides how the mirror is addressed:

| Method | Image reference | Needs |
|---|---|---|
| **Repository path** | `artifactory.example.com/bnk-mirror/f5-tmm` | nothing extra |
| **Subdomain** | `bnk-mirror.artifactory.example.com/f5-tmm` | wildcard DNS + a wildcard certificate |

`roksbnkctl` composes references as `<generic_host>/<generic_repo_prefix>/<image>`,
which is the repository-path form. On a subdomain instance, put the full
subdomain host in `generic_host` and leave `generic_repo_prefix` empty.

**A user with deploy permission.** A dedicated account with an *access token*
rather than a password is the right shape here — the token is scoped, and it can
be revoked without disturbing a human login. It needs Deploy/Cache and Read on
the target repository. Anonymous push works if your instance permits it, in
which case leave both credential fields unset.

### Pointing roksbnkctl at it

```bash
roksbnkctl registry target generic
roksbnkctl registry target generic_host       artifactory.example.com
roksbnkctl registry target generic_repo_prefix bnk-mirror
roksbnkctl registry target generic_username    bnk-mirror-bot
echo "$ARTIFACTORY_TOKEN" | roksbnkctl registry target generic_password --password-stdin
```

`--password-stdin` is the reason the token never reaches a shell history, a
process listing, or a terminal recording. Prefer it to an inline argument
everywhere, and note that it applies to the token, not to `IBMCLOUD_API_KEY` —
which this flow does not use at all, since no IBM Cloud resource is touched.

If Artifactory presents a certificate from a private CA, record the CA from the
file that issued it:

```bash
roksbnkctl registry target generic_ca /etc/pki/ca-trust/source/anchors/corp-root.crt
```

Recording it from the file rather than capturing it from the host matters: a CA
learned over the connection it is meant to authenticate proves nothing. When
`generic_ca` is set, `replicate` never dials the registry to discover trust.

### Doing the replication

```bash
roksbnkctl registry bom          # what BNK needs — offline, no registry required
roksbnkctl registry diff         # what is missing from Artifactory right now
roksbnkctl registry replicate    # copy it
roksbnkctl registry verify       # confirm every artifact is present and digest-matched
```

A successful run ends on the line that is worth putting on the screen:

```
✓ mirrored 89 artifacts into artifactory.example.com/bnk-mirror
✓ all 89 BOM artifacts present + digest-matched in the mirror
```

`verify` is the step that means anything. `replicate` reports what it pushed;
`verify` independently re-reads the BOM and checks each artifact against the
digest it should have, so a partial copy, a silently truncated layer, or a tag
that moved underneath you is caught. Treat `replicate` succeeding as progress
and `verify` succeeding as done.

`diff` before and after is what makes this legible in a demonstration: it prints
a populated list, then an empty one.

## Configuration and environment equivalents

Every field above is `config.yaml` state and can be set without the CLI.

ICR:

```yaml
registry:
  target: icr
  icr_namespace: bnk-mirror
  icr_host: us.icr.io        # optional — derived from ibmcloud.region
```

Artifactory:

```yaml
registry:
  target: generic
  generic_host: artifactory.example.com
  generic_repo_prefix: bnk-mirror
  generic_username: bnk-mirror-bot
  generic_password_b64: <base64 of the access token>
  generic_ca_b64: <base64 of the CA PEM>
```

`generic_password_b64` is **obfuscation, not encryption** — it exists so the
value survives YAML and does not trip the plaintext-secret check. Treat the file
as a secret: `chmod 600`, never commit it.

For CI, each has an environment override, so no `config.yaml` need be authored:

| Variable | Field |
|---|---|
| `ROKSBNKCTL_GENERIC_HOST` | `registry.generic_host` |
| `ROKSBNKCTL_GENERIC_REPO_PREFIX` | `registry.generic_repo_prefix` |
| `ROKSBNKCTL_GENERIC_USERNAME` | `registry.generic_username` |
| `ROKSBNKCTL_GENERIC_PASSWORD` | `registry.generic_password_b64` (raw; encoded for you) |
| `ROKSBNKCTL_GENERIC_CA_B64` | `registry.generic_ca_b64` (already base64) |
| `ROKSBNKCTL_GENERIC_CA_SHA256` | `registry.generic_ca_sha256` |

## When it goes wrong

**`UNAUTHORIZED` on push, `docker login` works by hand.** The account can read
but not deploy. Docker's error surfaces at the first blob upload, which is well
after the login it is easy to mistake for proof of access.

**`404` or `repository not found`.** Usually the repository-path/subdomain
mismatch above: a repository-path reference against a subdomain-configured
instance resolves to a repository key that does not exist. Confirm which method
the instance uses before assuming the prefix is wrong.

**`x509: certificate signed by unknown authority`.** `generic_ca` is unset or
does not chain to the certificate Artifactory is serving. Record the CA that
issued the cert actually in use, which on a load-balanced instance may not be
the one issued to the origin.

**ICR: `namespace not found`.** `roksbnkctl` does not create the namespace.
Run `ibmcloud cr namespace-add <name>` first, and check `ibmcloud cr region`
matches the region the host was derived from — a namespace created in one ICR
region is not visible from another.

**Storage.** The BOM is 89 artifacts of container images and OCI-packaged
charts. An Artifactory instance with a repository quota, or a filestore near
capacity, fails partway through `replicate` — and because the copy is
per-artifact, it fails *partway* rather than cleanly. Re-run `diff` to see what
actually landed, then `replicate` again; it copies only what is missing.
