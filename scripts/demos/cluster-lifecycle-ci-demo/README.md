# Cluster-lifecycle CI demo

The **same lifecycle** as the
[cluster-lifecycle CLI demo](../cluster-lifecycle-cli-demo/README.md), told the way
it actually ships: as a **CI pipeline**. Every step is a `docker run` of the
all-in-one `roksbnkctl-tools-runner` image — exactly what a CI job calls. Nothing
is installed on this host: no roksbnkctl, no terraform, no helm, no kubectl, no
ibmcloud.

## What it does — seven phases

| # | Phase | What runs |
|---|---|---|
| 1 | The runner container **is** roksbnkctl | `docker run --rm $RUNNER version` + `doctor` |
| 2 | Configure declaratively, on the mounted volume | one `config.yaml` on `/work`, then `init --config-file /work/config.yaml --override-from-env` |
| 3 | Build the ROKS cluster | `cluster up --auto`, then `cluster config` |
| 4 | Register with BNK Forge | `bnkforge register` — URL/user/password arrive as `BNK_FORGE_*` env |
| 5 | Install BIG-IP Next for Kubernetes | `bnk up --auto`, then `k get pods -n f5-bnk` + `k get licenses…` |
| 6 | The probe framework | `testing up --auto`, `test hosts add <url>`, `test` — the pipeline's gate |
| 7 | Swap BNK, keep the cluster | `bnk down --auto` → (optional version bump) → `bnk up --auto` |

Secrets are passed to each container **by name** (`docker run -e IBMCLOUD_API_KEY`),
so no value ever appears in `argv`. Workspace state lives in a bind-mounted `/work`
volume, so it outlives every container. The pipeline **stops there and leaves everything
running**, then reports the reachable web UIs so you can explore. It builds a real ROKS
cluster — expect **45–90 min** end to end.

## Prerequisites

**You provide** (see `.env.example`):
- an IBM Cloud **API key** (VPC + Kubernetes-Service + Transit-Gateway),
- a **BNK Forge** account — `FORGE_URL` / `FORGE_USER` / `FORGE_PASS`,
- the cluster shape: `REGION`, `RESOURCE_GROUP`, `CI_WORKSPACE`, `OCP_VERSION`,
  `WORKERS_PER_ZONE`.

`RUNNER_TAG` pins the image — **that image is the binary under test**, so there is
no local build to ship.

**Tools on this (Ubuntu) host — only Docker:**

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"    # log out/in so docker runs without sudo
```

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./cluster-lifecycle-ci-demo.sh            # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./cluster-lifecycle-ci-demo.sh` prints every command without running
it — a safe first pass to read the flow.

## Record it

```bash
./record.sh                               # → demo-video/cluster-lifecycle-ci-demo.mp4
```

`record.sh` runs the demo hands-off in a headless X terminal, captures it with ffmpeg,
then `../lib/post_10x.py` builds the final cut from the queue the demo wrote: each phase
opens on a **cleared screen with its banner held 4s**, each `roksbnkctl` command reads as
**[5s: the command] → [10× streaming] → [5s: its settled output]**, and the long deployment windows
is the only sped part. There is no voiceover — the on-screen context lines carry the
narration. Needs `Xvfb`, `xterm`, `ffmpeg`, `python3`.

The cut is entirely queue-driven, so re-timing never needs a re-record — re-run
`post_10x.py` on the saved `demo-video/demo-raw.mkv` with different `CMD_SECS` / `OUT_SECS`
/ `PHASE_SECS` / `SPEED`.

Before recording, verify no credential can reach the screen:

```bash
../lib/check-masking.sh cluster-lifecycle-ci-demo
```

Every credential this demo is given is registered with `secret` in preflight, so
`say`/`ok`/`show` and `show_file` mask it (and its base64 form) as
`***REDACTED***`. Preflight prints `✓ secret masking active …` on camera to
confirm. See [Secrets on camera](../README.md#secrets-on-camera).

## Files

| File | What |
|---|---|
| `cluster-lifecycle-ci-demo.sh` | the demo (seven phases, every step a `docker run` of the tools-runner image) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The pipeline never tears itself down. When you have finished exploring:

```bash
./cluster-lifecycle-ci-demo.sh teardown
```

One more `docker run` of the same runner image — `roksbnkctl down --auto` — removes
**every phase**: the testing jumphosts, BNK, and the ROKS cluster + VPC / transit gateway
/ registry COS that `cluster up` provisioned. This pipeline **created** its cluster, so
teardown destroys it; nothing here was adopted. Teardown re-sources `.env` itself, so it
runs standalone.
