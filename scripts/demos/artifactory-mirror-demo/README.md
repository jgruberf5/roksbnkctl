# Artifactory mirror demo

Mirroring the **F5 Artifact Repository (FAR)** into a **self-hosted JFrog
Artifactory** running on a single IBM Cloud VSI, and stopping there.

This is a standalone supply-chain demo. It is the Harbor story told against
Artifactory, for customers whose registry of record is Artifactory and who want
to see it work before committing to it.

## Scope

**The demo shows the mirror succeeding.** It builds no cluster and installs no
BNK. `registry replicate` is a registry-to-registry copy that never talks to
Kubernetes, which is what makes this a standalone story — and what keeps it to
about 25 minutes once the server is up.

That is a deliberate boundary, not an omission. Whether BNK then installs *from*
the mirror is the subject of the disconnected-cluster demos, which already exist
and which assume a populated mirror as their starting point.

## The two paths, side by side

The demo runs the same work twice, which is the point of phase 8:

| | Phases 3–7 | Phase 8 |
|---|---|---|
| Driven by | `roksbnkctl` CLI | the runner container |
| Settings from | `config.yaml`, built by `registry target` | `ROKSBNKCTL_*` environment variables |
| Orchestrated by | a person | an Argo workflow |
| Verbs run | `bom`, `diff`, `replicate`, `verify` | the same |

Same binary, same verbs. Only the delivery of settings changes. The interactive
path is how an operator *tests* a mirror; the container path is how it runs
unattended.

## Files

| File | What it is |
|---|---|
| `artifactory-mirror-demo.sh` | the demo, eight phases |
| `wf-artifactory-mirror.yaml` | the Argo workflow phase 8 submits — self-contained, applies to any Argo cluster |
| `guide.html` | the step-by-step customer guide, including the Artifactory UI steps |
| `.env.example` | copy to `.env` and fill in |
| `../lib/deploy-artifactory-vsi.sh` | stands the Artifactory VSI up (and tears it down) |

The VSI runs **three** containers: Artifactory, PostgreSQL, and Caddy for TLS.
The database is not optional — Artifactory's Access service refuses to start
against the embedded Derby database (`DB Type derby is not allowed`), and the
resulting failure looks like slow startup rather than a database problem:
Artifactory's own API answers on `8081` while Access never binds `8046`.

## Prerequisites

**A DNS name you control.** Artifactory is served over HTTPS by Caddy, which
obtains a certificate automatically — but only once the name resolves to the
VSI's floating IP and inbound port 80 is reachable for the ACME challenge. A
Docker client will not talk to a registry it cannot verify, so this is not
optional.

**Two things done in the Artifactory UI**, after the server is up and before the
demo runs:

1. the admin password, set on first login;
2. a **local Docker repository** named `bnk-mirror`.

These stay manual on purpose. They are what a real deployment does by hand, and
automating them away would misrepresent the work involved. `guide.html` walks
through both with the exact menu paths.

> **Artifactory OSS cannot be a BNK mirror.** Docker repository support is a
> licensed feature; an OSS instance has no Docker repository type at all. The
> VSI therefore runs **JFrog Container Registry** — the free edition built around
> Docker and Helm repositories, needing no licence key. Set `ARTIFACTORY_IMAGE`
> to the Pro image if you hold a licence; nothing downstream changes, because
> `roksbnkctl` only ever speaks the OCI registry API to it.

## Verified

Both paths have been run end to end against a real self-hosted Artifactory:

All four combinations, each reaching `mirrored 89 artifacts` →
`all 89 BOM artifacts present + digest-matched`:

| | `docker-local` | `bnk-mirror` |
|---|---|---|
| CLI (phases 3–7) | ✅ | ✅ |
| Container / Argo (phase 8) | ✅ | ✅ |

Every run started from an emptied registry, so each replicated all 89 rather
than skipping.

The container run authored no `config.yaml` at all — 15 fields applied from the
environment — which is the equivalence phase 8 exists to demonstrate.

### Which repository

Two options, one config field apart. `ART_REPO` selects:

| | Repository | Setup |
|---|---|---|
| Fully automated | `docker-local` | none — created by `config.import.yml` |
| Named | `bnk-mirror` | create it once in the console |

Both are verified: 89 artifacts, digest-matched, into each.

`docker-local` is the default because it needs no console step at all. A custom
name cannot be automated on JCR — repo creation over REST answers `400 available
only in Artifactory Pro`, and the declarative `OnboardingConfiguration` schema
has only `repoTypes`, which creates JFrog's default names. Creating one **in the
web UI works fine**; it is only the automated paths that are restricted.

Nothing else changes with a non-default repository. The name is a path segment
in the composed reference `<host>/<prefix>/<image>`, so `bom`, `diff`,
`replicate`, `verify`, `list`, `prune` and `delete` are all identical.

## Running it

```bash
export ART_DOMAIN=artifactory.example.com
export IBMCLOUD_API_KEY=...
../lib/deploy-artifactory-vsi.sh          # ~15 min, waits for HTTPS

# ... now do the two UI steps, then:
cp .env.example .env && $EDITOR .env
./artifactory-mirror-demo.sh
```

`./artifactory-mirror-demo.sh teardown` empties the mirror and leaves the server
alone. `../lib/deploy-artifactory-vsi.sh --destroy` removes the server and its
VPC.

## Recording it

Every credential passes through `secret()` before it can reach the screen, so
`IBMCLOUD_API_KEY` and the Artifactory access token render as `***REDACTED***`.
The token is supplied on **stdin** throughout — never as an argument — because an
argument is visible in `ps` output and in the recording itself.
