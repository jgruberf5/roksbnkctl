# Shared-licensing CI demo

The [shared-licensing CLI demo](../shared-licensing-cli-demo/README.md), told the
way it actually ships: as **two CI jobs**. Every step is a `docker run` of the
`roksbnkctl-tools-runner` image, the whole workspace comes from **environment
variables**, and no `config.yaml` is templated anywhere. Nothing is installed on
this host — no roksbnkctl, no terraform, no helm, no kubectl, no ibmcloud.

```
   ┌─ JOB 1 · licensing cluster ─────────────┐
   │  mirror FAR → private registry          │
   │  F5 License Proxy ──NodePort 30001──┐   │
   │  outputs: flp_url, flp_ca ──────────┼─┐ │
   └─────────────────────────────────────┼─┼─┘
   ┌─ JOB 2 · application cluster ───────┼─┼─┐
   │  BNK ← charts + images ── registry  │ │ │
   │  CWC ───────────────────────────────┘ │ │  licensed remotely
   │  ROKSBNKCTL_FLP_EXTERNAL_URL / _ROOT_CA_B64 ←┘
   └─────────────────────────────────────────┘
```

**The handoff between the jobs is two environment variables** — exactly what a CI
pipeline passes as job outputs. That is the point of the demo.

## What it does — seven phases

| # | Phase | What runs |
|---|---|---|
| 1 | A CI runner with nothing installed | `docker run --rm $RUNNER version` |
| 2 | Job 1: configure from the environment alone | the `.env` file **is** the CI config; `init --non-interactive --override-from-env`, then `cluster register` |
| 3 | Job 1: mirror F5's registry into the private one | `registry replicate --target generic`, `registry verify` |
| 4 | Job 1: deploy the License Proxy from the container | `flp up --auto --add-node-port-access --node-port-source-cidr …`, then `k get svc` |
| 5 | The handoff — two environment variables | `flp output flp_external_endpoint` + `flp_root_ca` → `ROKSBNKCTL_FLP_EXTERNAL_URL` + `_ROOT_CA_B64` in job 2's env |
| 6 | Job 2: install BNK, licensed from next door | `init --non-interactive --override-from-env`, `cluster register`, `registry replicate`, `bnk up --auto` |
| 7 | Prove it | `k get license -n f5-utils`, `k get pods -n f5-bnk`, and `k get pods -n f5-license-proxy` — which **fails**, and that failure is the evidence |

The pipeline **stops there and leaves the FLP and BNK running**, then reports the reachable
web UIs so you can watch the app cluster stay licensed by the proxy next door.

### Both ROKS clusters must already be running

They are `cluster register`ed, **not created** — cluster builds are ~40 minutes of
nothing to watch. `teardown` removes only the workloads the pipeline installed; it never
destroys clusters it did not create.

## Prerequisites

Same two-cluster + registry prerequisites as the CLI variant. **You provide** (see
`.env.example`):

- an IBM Cloud **API key**,
- `SERVICES_CLUSTER` and `APP_CLUSTER` — two **running** ROKS clusters that can
  reach each other (same VPC, or a transit gateway between their VPCs),
- `APP_CLUSTER_CIDR` — **every** zone prefix of the app cluster's VPC,
  comma-separated. A pod scheduled in a zone you left out is dropped at the
  security group:

  ```bash
  ibmcloud is vpc-address-prefixes <vpc> --output json | jq -r '[.[].cidr]|join(",")'
  ```

- `REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD` — a **running private registry**,
  built off-camera with `../lib/deploy-far-registry.sh` (see the
  [FAR-replication demo](../far-replication-demo/README.md)).

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
./shared-licensing-ci-demo.sh             # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./shared-licensing-ci-demo.sh` prints every command without running it
— a safe first pass to read the flow.

## Record it

```bash
./record.sh                               # → demo-video/shared-licensing-ci-demo.mp4
```

`record.sh` runs the demo hands-off in a headless X terminal, captures it with ffmpeg,
then `../lib/post_10x.py` builds the final cut from the queue the demo wrote: each phase
opens on a **cleared screen with its banner held 4s**, each `roksbnkctl` command reads as
**[5s: the command] → [10× streaming] → [5s: its settled output]**, and the long windows
(`replicate`, `flp up`, `bnk up`) are the only sped part. There is no voiceover — the
on-screen context lines carry the narration. Needs `Xvfb`, `xterm`, `ffmpeg`, `python3`.

The cut is entirely queue-driven, so re-timing never needs a re-record — re-run
`post_10x.py` on the saved `demo-video/demo-raw.mkv` with different `CMD_SECS` / `OUT_SECS`
/ `PHASE_SECS` / `SPEED`.

Before recording, verify no credential can reach the screen:

```bash
../lib/check-masking.sh shared-licensing-ci-demo
```

Every credential this demo is given is registered with `secret` in preflight, so
`say`/`ok`/`show` and `show_file` mask it (and its base64 form) as
`***REDACTED***`. Preflight prints `✓ secret masking active …` on camera to
confirm. See [Secrets on camera](../README.md#secrets-on-camera).

## Why it works

- **The whole workspace is environment variables.** `init --non-interactive
  --override-from-env` builds it from `ROKSBNKCTL_*` — no file is templated. The
  registry target is `ROKSBNKCTL_REGISTRY_TARGET` / `_GENERIC_HOST` /
  `_GENERIC_REPO_PREFIX` / `_GENERIC_USERNAME` / `_GENERIC_PASSWORD`; the license
  mode is `ROKSBNKCTL_LICENSE_MODE`. The two `.ci-*.env` files the demo writes are
  mode 0600 and land in the git-ignored state dir.
- **The cross-job handoff is two variables.** Job 1 prints `flp output
  flp_external_endpoint` and `flp_root_ca`; job 2 receives them as
  `ROKSBNKCTL_FLP_EXTERNAL_URL` and `ROKSBNKCTL_FLP_ROOT_CA_B64`. The CA is passed
  **verbatim** — it is already base64, and re-encoding it hands the CWC a corrupt CA.
- **`flp up` runs *inside* the container**, which it could not do before v1.19.0:
  the chart's Helm post-renderer was a generated python script, and the runner
  image has no python. roksbnkctl now post-renders its own chart
  (`roksbnkctl flp postrender`), so the container needs no interpreter.

See [Flow C in CI](../../../book/src/10c-flp-licensing.md#flow-c-in-ci--the-runner-container-no-host-install).

## Files

| File | What |
|---|---|
| `shared-licensing-ci-demo.sh` | the demo (seven phases, every step a `docker run` of the tools-runner image) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The pipeline never tears itself down. When you have finished exploring:

```bash
./shared-licensing-ci-demo.sh teardown
```

That removes **only what the pipeline installed** — BNK from the app cluster, the FLP from
the services cluster, the two `--env-file` files and both workspaces on the `/work` volume.
**Both ROKS clusters keep running**: they were `cluster register`ed, never created. The
private registry keeps its mirrored artifacts for the same reason; teardown prints the one
command that empties it if you want that too. Teardown rebuilds job 1's env-file from
`.env` if a run already deleted it, so it runs standalone.
