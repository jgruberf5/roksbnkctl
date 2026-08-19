# B — Replicating FAR into a JFrog Artifactory

This appendix mirrors the F5 Artifact Repository into a **JFrog Artifactory you
already run**. It does not cover standing one up; that is the standalone
Artifactory demo's job.

Nothing here needs a Kubernetes cluster. `registry replicate` copies
registry-to-registry from wherever `roksbnkctl` runs, so the mirror can be seeded
long before a cluster exists — which is usually the point, since the cluster is
air-gapped and the mirror is what makes it installable at all.

Everything below has been run end to end against a self-hosted Artifactory. Where
a behaviour is edition-specific it says so, because the free and licensed
editions differ in ways that are not obvious until something returns a `400`.

## What Artifactory has to provide

**A local Docker repository.** BNK's artifacts are OCI images and OCI-packaged
Helm charts, so the target must be a Docker repository — a *local* one. A remote
repository is a read-through cache of an upstream and cannot be pushed to; a
virtual repository accepts a push only if it has a local deployment target, so
pointing at the local repository directly is simpler and fails more clearly when
permissions are wrong.

Create it in the web console: **Administration → Repositories → Add Repository →
Local Repository → Docker**. Any name works; the examples here use `bnk-mirror`.

> **Creating repositories is a console job on the free edition.** The REST API
> answers `400 This REST API is available only in Artifactory Pro`, so a
> repository cannot be created by script on JFrog Container Registry. The web UI
> works fine — it is the automated paths that are restricted. On Pro or
> Enterprise the REST call works normally.

**A credential that can deploy.** An **access token** is the right shape: it is
validated independently of the account password, so rotating the password does
not invalidate it. That matters because the same credential is handed to a
pipeline.

```bash
curl -u admin:'<password>' -X POST \
  -d username=admin -d scope=applied-permissions/user -d expires_in=0 \
  https://artifactory.example.com/artifactory/api/security/token
```

`expires_in=0` issues a non-expiring token. Omitting `scope` fails with
`Insufficient scope`, which is not obvious from the error alone.

> **A scoped user needs a licensed edition.** Users, groups and permission
> targets are Pro features; on JCR their REST APIs answer `400 … only in
> Artifactory Pro` and the corresponding screens are absent, so the mirror runs
> as `admin`. That suits a single-purpose mirror host and does not suit a shared
> registry — on Pro, create a user, grant it read and write on the repository
> through a permission, and issue that user's token instead.

## Pointing roksbnkctl at it

```bash
roksbnkctl registry target generic
roksbnkctl registry target generic_host        artifactory.example.com
roksbnkctl registry target generic_repo_prefix bnk-mirror
roksbnkctl registry target generic_username    admin

echo "$ARTIFACTORY_TOKEN" | roksbnkctl registry target generic_password --password-stdin
```

`--password-stdin` keeps the token out of shell history, out of `ps` output, and
out of any terminal recording. The password field accepts either an access token
or the account password; a token is preferable for the reason above.

**The repository name is the only thing that changes for a different
repository.** `roksbnkctl` composes references as
`<generic_host>/<generic_repo_prefix>/<image>`, so the name is a path segment —
`bom`, `diff`, `replicate`, `verify`, `list`, `prune` and `delete` are identical
whatever it is called.

That composition is Artifactory's **repository-path** method. If your instance is
configured for the **subdomain** method instead, put the full subdomain in
`generic_host` and leave `generic_repo_prefix` empty. Getting this wrong produces
a `404` that reads like a bad prefix and sends you looking in the wrong place.

If Artifactory presents a certificate from a private CA, record the CA from the
file that issued it:

```bash
roksbnkctl registry target generic_ca /etc/pki/ca-trust/source/anchors/corp-root.crt
```

Recording it from the file rather than capturing it from the host matters: a CA
learned over the connection it is meant to authenticate proves nothing. With
`generic_ca` set, `replicate` never dials the registry to discover trust. A
publicly-trusted certificate needs none of this, and `replicate` will say so —
`no private CA captured` is expected, not a warning to act on.

## Doing the replication

```bash
roksbnkctl registry bom          # what BNK needs — offline, no registry required
roksbnkctl registry diff         # what is missing from Artifactory right now
roksbnkctl registry replicate    # copy it
roksbnkctl registry verify       # confirm every artifact is present + digest-matched
```

A successful run ends on two lines worth putting on screen:

```
✓ mirrored 89 artifacts into artifactory.example.com/bnk-mirror
✓ all 89 BOM artifacts present + digest-matched in the mirror
```

**Gate on `verify`, not on `replicate` or `diff`.** `replicate` reports what it
pushed. `verify` independently re-reads the bill of materials and checks each
artifact against the digest it should have, so a partial copy or a tag that moved
underneath you is caught.

> **`diff` can be wrong when the recorded mirror is stale.** A workspace records
> what it last replicated, and that record can describe a registry that has since
> been rebuilt or replaced. In that state `diff` has been observed reporting
> *"mirror is in sync"* against an **empty** registry, while `verify` correctly
> reported every artifact missing. If a workspace has been re-pointed at a
> different registry, trust `verify`.

## Configuration and environment equivalents

Every field is `config.yaml` state:

```yaml
registry:
  target: generic
  generic_host: artifactory.example.com
  generic_repo_prefix: bnk-mirror
  generic_username: admin
  generic_password_b64: <base64 of the access token>
  generic_ca_b64: <base64 of the CA PEM>       # only for a private CA
```

`generic_password_b64` is **obfuscation, not encryption** — it exists so the
value survives YAML and does not trip the plaintext-secret check. Treat the file
as a secret: `chmod 600`, never commit it.

For CI, each has an environment override, so no `config.yaml` need be authored at
all:

| Variable | Field |
|---|---|
| `ROKSBNKCTL_GENERIC_HOST` | `registry.generic_host` |
| `ROKSBNKCTL_GENERIC_REPO_PREFIX` | `registry.generic_repo_prefix` |
| `ROKSBNKCTL_GENERIC_USERNAME` | `registry.generic_username` |
| `ROKSBNKCTL_GENERIC_PASSWORD` | `registry.generic_password_b64` (raw; encoded for you) |
| `ROKSBNKCTL_GENERIC_CA_B64` | `registry.generic_ca_b64` (already base64) |
| `ROKSBNKCTL_GENERIC_CA_SHA256` | `registry.generic_ca_sha256` |

Three more are needed for an unattended run, and are easy to miss because
`init --non-interactive` refuses without them rather than defaulting:
`ROKSBNKCTL_REGION`, `ROKSBNKCTL_RESOURCE_GROUP` and `ROKSBNKCTL_PREFIX`.

The FAR pull credential is resolved from Cloud Object Storage, which needs
`ROKSBNKCTL_COS_INSTANCE`, `ROKSBNKCTL_COS_BUCKET` and `ROKSBNKCTL_COS_REGION`.
**The bucket has no usable default** — COS names are globally unique, so every
account's is suffixed (`bnk-artifacts-<hex>`), and the built-in default belongs
to someone else. A wrong bucket fails with `AccessDenied` rather than
`NotFound`, which reads like a bad API key and sends you to check the wrong
credential.

## When it goes wrong

**`UNAUTHORIZED` on push, `docker login` works by hand.** The account can read
but not deploy. Docker surfaces this at the first blob upload, well after the
login that is easy to mistake for proof of access.

**`404` or `repository not found`.** Usually the repository-path/subdomain
mismatch above. Confirm which method the instance uses before assuming the prefix
is wrong.

**`x509: certificate signed by unknown authority`.** `generic_ca` is unset or
does not chain to the certificate actually being served — which, on a
load-balanced instance, may not be the one issued to the origin.

**Storage.** The BOM is 89 artifacts of container images and OCI-packaged charts.
An instance with a repository quota, or a filestore near capacity, fails
*partway* through `replicate` rather than cleanly. Re-run `diff` to see what
landed, then `replicate` again; it copies only what is missing.

**`no mirror recorded — nothing to delete`.** `registry delete` works from the
workspace's recorded mirror, so it cannot clean a registry it has no record of —
after the record has been cleared, or from a different workspace. Remove the
artifacts through Artifactory instead.
