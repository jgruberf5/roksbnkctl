# Cluster-lifecycle CLI demo

The **roksbnkctl lifecycle, driven one phase at a time**, on a cluster roksbnkctl
builds and then destroys on camera. A hands-off build would just be
`roksbnkctl up`; this demo invokes every phase on its own so you can see the seams
— and see that BNK can be removed and reinstalled without the cluster ever moving.

## What it does — six phases

| # | Phase | What runs |
|---|---|---|
| 1 | One declarative `config.yaml`, then `init` | the whole input is one file; no interactive interview |
| 2 | Build the ROKS cluster | `cluster up --auto`, then `cluster config` |
| 3 | Register with BNK Forge | `bnkforge register --url … --username …` (password from `BNK_FORGE_PASSWORD`) |
| 4 | Install BIG-IP Next for Kubernetes | `bnk up --auto`, then `k get pods -n f5-bnk` + `k get licenses…` |
| 5 | The probe framework | `testing up --auto`, `test hosts add <url>`, `test` |
| 6 | Swap BNK, keep the cluster | `bnk down --auto` → (optional version bump) → `bnk up --auto` |

The demo **stops there and leaves everything running**, then reports the reachable web
UIs so you can explore. It builds a real ROKS cluster — expect **45–90 min** end to end.

## Prerequisites

**You provide** (see `.env.example`):
- an IBM Cloud **API key** (VPC + Kubernetes-Service + Transit-Gateway),
- *optionally* a **BNK Forge** account — `FORGE_URL` / `FORGE_USER` / `FORGE_PASS`
  (or the `BNK_FORGE_*` names `roksbnkctl` itself reads). Forge is used by phase 3
  and nothing else: set all three to include that phase, or none to skip it and run
  the rest. Setting only some of them is treated as a mistake and stops the demo,
  so a typo cannot quietly cost you the registration step.
- the cluster shape you want: `REGION`, `RESOURCE_GROUP`, `CLUSTER_NAME`,
  `OCP_VERSION`, `WORKERS_PER_ZONE`.

**Tools on this (Ubuntu) host:**

```bash
sudo apt-get update
sudo apt-get install -y jq curl gnupg lsb-release
# terraform — roksbnkctl shells out to it for every apply
curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
  | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null
sudo apt-get update && sudo apt-get install -y terraform
# helm — roksbnkctl shells out to it for chart / BOM resolution
curl -fsSL https://get.helm.sh/helm-v3.16.2-linux-amd64.tar.gz | tar -xz -C /tmp
sudo install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
# roksbnkctl (or point ROKSBNKCTL_BIN at a build)
#   see book Chapter 4 — Installation
```

Everything else — `kubectl`, `oc`, `ibmcloud`, `dig`, `iperf3` — is internal to
roksbnkctl. `roksbnkctl doctor` runs on camera in the preflight and reports on it.

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./cluster-lifecycle-cli-demo.sh           # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./cluster-lifecycle-cli-demo.sh` prints every command without running
it — a safe first pass to read the flow.

## Record it

```bash
./record.sh                               # → demo-video/cluster-lifecycle-cli-demo.mp4
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
../lib/check-masking.sh cluster-lifecycle-cli-demo
```

Every credential this demo is given is registered with `secret` in preflight, so
`say`/`ok`/`show` and `show_file` mask it (and its base64 form) as
`***REDACTED***`. Preflight prints `✓ secret masking active …` on camera to
confirm. See [Secrets on camera](../README.md#secrets-on-camera).

## Files

| File | What |
|---|---|
| `cluster-lifecycle-cli-demo.sh` | the demo (seven phases: init → cluster up → forge → bnk up → testing → bnk down/up → down) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The demo never tears itself down. When you have finished exploring:

```bash
./cluster-lifecycle-cli-demo.sh teardown
```

One `roksbnkctl -w <workspace> down --auto` removes **every phase** — the testing
jumphosts, BNK, and the ROKS cluster + VPC / transit gateway / registry COS that
`cluster up` provisioned. This demo **created** its cluster, so teardown destroys it;
nothing here was adopted. Teardown re-sources `.env` itself, so it runs standalone.
