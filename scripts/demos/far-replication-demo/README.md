# FAR-replication demo

Mirroring the **F5 Artifact Repository (FAR)** into a private OCI registry with
`roksbnkctl`. An air-gapped cluster cannot reach `repo.f5.com`, so every chart and
image a BNK install needs must first be copied into a registry you control — this
demo is that copy, end to end, verified by digest.

**No ROKS cluster is built.** `registry replicate` is a host-side
registry-to-registry copy: it reads the workspace, pulls from FAR and pushes to the
target, and never talks to a cluster. That keeps the whole demo to roughly
**20 minutes**.

## What it does — seven phases

| # | Phase | What runs |
|---|---|---|
| 1 | A mirror-only workspace | `cluster.create: false` in the seed, then `init --config-file` |
| 2 | The FAR credential lives in COS | `cos object list <bucket> --instance <instance>` |
| 3 | The bill of materials | `registry bom` — every chart and image a BNK install pulls |
| 4 | Point the workspace at the mirror | `registry target generic` + `generic_host` / `generic_repo_prefix` / `generic_username`, password on **stdin** |
| 5 | What replication would copy | `registry diff` |
| 6 | Replicate FAR into the mirror | `registry replicate --target generic` — each artifact copied **by digest** |
| 7 | Verify, then browse | `registry verify`, then `registry list` |

Nothing secret is in the seed config: the FAR credential is **named**, not
embedded. roksbnkctl downloads `bnk.far_auth_file` from the orchestration COS
bucket and extracts the service account itself (`resolveFARServiceAccount` in
`internal/cli/registry.go`, called from `buildBOM` whenever
`registry.source_service_account_b64` is empty). The bucket is in `us-south` even
when the workspace is not — `cos.DefaultBucketRegionResolver` resolves each
bucket's own region.

## Prerequisites

**A running private registry, built off-camera.** How it is built is not part of
this story — the demo just targets it. Any OCI-compliant registry works;
`../lib/deploy-far-registry.sh` stands up a standard open-source Harbor and prints
both values you need:

```bash
../lib/deploy-far-registry.sh -w prebuilt-harbor \
  --key-name <ibmcloud-vpc-ssh-key> --ssh-key ~/.ssh/id_rsa \
  --region "$REGION" --project bnk-mirror
# → REGISTRY_DOMAIN (e.g. <floating-ip>.sslip.io) and REGISTRY_ADMIN_PASSWORD
```

The registry must serve **real TLS**: `replicate` drives crane over HTTPS and no
CLI path sets `mirror.Engine.Insecure`. `deploy-far-registry.sh` gives Harbor a
Let's Encrypt cert for `<floating-ip>.sslip.io` via Caddy; it needs inbound 80/443.

**You also provide** (see `.env.example`): an IBM Cloud **API key** (COS read for
the FAR credential), and `BNK_VERSION` / `FAR_REPO_URL` / `REGISTRY_PROJECT`.

**Tools on this (Ubuntu) host:**

```bash
sudo apt-get update && sudo apt-get install -y jq curl
# helm — roksbnkctl uses it to pull the classic-Helm charts in the BOM
curl -fsSL https://get.helm.sh/helm-v3.16.2-linux-amd64.tar.gz | tar -xz -C /tmp
sudo install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
# roksbnkctl (or point ROKSBNKCTL_BIN at a build)
#   see book Chapter 4 — Installation
```

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./far-replication-demo.sh                 # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./far-replication-demo.sh` prints every command without running it — a
safe first pass to read the flow.

**Re-runs:** the registry persists between takes, so run `./far-replication-demo.sh
teardown` between shoots — it empties the mirror, so `diff` shows everything missing
again.

## Record it

```bash
./record.sh                               # → demo-video/far-replication-demo.mp4
```

`record.sh` runs the demo hands-off in a headless X terminal, captures it with ffmpeg,
then `../lib/post_10x.py` builds the final cut from the queue the demo wrote: each phase
opens on a **cleared screen with its banner held 4s**, each `roksbnkctl` command reads as
**[5s: the command] → [10× streaming] → [5s: its settled output]**, and the `replicate` window
is the only sped part. There is no voiceover — the on-screen context lines carry the
narration. Needs `Xvfb`, `xterm`, `ffmpeg`, `python3`.

The cut is entirely queue-driven, so re-timing never needs a re-record — re-run
`post_10x.py` on the saved `demo-video/demo-raw.mkv` with different `CMD_SECS` / `OUT_SECS`
/ `PHASE_SECS` / `SPEED`.

Before recording, verify no credential can reach the screen:

```bash
../lib/check-masking.sh far-replication-demo
```

Every credential this demo is given is registered with `secret` in preflight, so
`say`/`ok`/`show` and `show_file` mask it (and its base64 form) as
`***REDACTED***`. Preflight prints `✓ secret masking active …` on camera to
confirm. See [Secrets on camera](../README.md#secrets-on-camera).

## Files

| File | What |
|---|---|
| `far-replication-demo.sh` | the demo (seven phases: init → COS credential → bom → target → diff → replicate → verify + list) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The demo never tears itself down — it leaves the mirror full so you can browse it. When
you have finished:

```bash
./far-replication-demo.sh teardown
```

That deletes the artifacts this demo pushed (`registry delete --force`) and clears the
local workspace. The **registry host itself is left untouched** — it was built off-camera
and this demo does not own it. Teardown re-sources `.env` itself, so it runs standalone.

This also resets the demo for a clean re-record: `diff` shows everything missing again.
