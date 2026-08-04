# Shared-licensing CLI demo

**One cluster runs the F5 License Proxy (FLP) and holds the only egress to F5. A
second, air-gapped cluster installs BIG-IP Next for Kubernetes entirely from a
private registry and licenses *through that proxy*.** Neither cluster reaches
`repo.f5.com`; only one of them can reach F5 at all.

```
   ┌──────── services cluster (the only egress to F5) ───────┐
   │  F5 License Proxy ──NodePort 30001──┐                   │
   └─────────────────────────────────────┼───────────────────┘
   ┌─────────────────────────────────────┼── app cluster ────┐
   │  BNK + CNEInstance + CWC ───────────┘                   │
   │      └── charts + images ── private registry (Harbor)   │
   └─────────────────────────────────────────────────────────┘
```

## What it does — six phases

| # | Phase | What runs |
|---|---|---|
| 1 | Adopt the services cluster | seed with `cluster.create: false`, `init`, then `cluster register <name>` |
| 2 | Mirror F5's registry into the private one | `registry target …` (password on stdin) → `registry bom` → `replicate` → `verify` |
| 3 | The License Proxy, reachable from next door | `flp up --auto --add-node-port-access --node-port-source-cidr …`, then `k get svc` + `flp output` |
| 4 | Adopt the app cluster, aim it at that FLP | `flp output flp_external_endpoint` / `flp_root_ca` → the second workspace's `bnk.flp.external`; `cluster register`; same registry |
| 5 | Install BNK — disconnected, licensed remotely | `bnk up --auto` |
| 6 | Prove it | `k get license -n f5-utils` (mode `f5licenseproxy`, state Active), `k get cneinstance -A`, `k get pods -n f5-bnk`, and `k get pods -n f5-license-proxy` — which **fails**, and that failure is the evidence |

The demo **stops there and leaves the FLP and BNK running**, then reports the reachable
web UIs so you can watch the app cluster stay licensed by the proxy next door.

### Both ROKS clusters must already be running

They are `cluster register`ed, **not created**. Building a ROKS cluster is ~40
minutes of nothing to watch, twice over — and adopting an existing cluster is a
first-class roksbnkctl flow that is more representative anyway. Because the demo
never creates the clusters, **it never destroys them**: `teardown` removes only the
FLP and BNK it installed.

That puts the shoot at roughly **50–70 min** (mostly `registry replicate` and
`bnk up`), against 3+ hours if it built the clusters.

## Prerequisites

**You provide** (see `.env.example`):
- an IBM Cloud **API key**,
- `SERVICES_CLUSTER` and `APP_CLUSTER` — two **running** ROKS clusters that can
  reach each other (same VPC, or a transit gateway between their VPCs),
- `APP_CLUSTER_CIDR` — **every** zone prefix of the app cluster's VPC,
  comma-separated. A multi-zone VPC carries one prefix per zone, and a pod
  scheduled in a zone you left out is silently dropped at the security group:

  ```bash
  ibmcloud is vpc-address-prefixes <vpc> --output json | jq -r '[.[].cidr]|join(",")'
  ```

- `REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD` — a **running private registry**,
  built off-camera with `../lib/deploy-far-registry.sh` (see the
  [FAR-replication demo](../far-replication-demo/README.md) for how). Start from an
  **empty** project (`roksbnkctl -w services registry delete --force`) so the
  replication has real work to show on camera.

**Tools on this (Ubuntu) host:**

```bash
sudo apt-get update
sudo apt-get install -y jq curl gnupg lsb-release
# terraform — roksbnkctl shells out to it for every apply
curl -fsSL https://apt.releases.hashicorp.com/gpg | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
  | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null
sudo apt-get update && sudo apt-get install -y terraform
# helm — chart / BOM resolution
curl -fsSL https://get.helm.sh/helm-v3.16.2-linux-amd64.tar.gz | tar -xz -C /tmp
sudo install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
# roksbnkctl (or point ROKSBNKCTL_BIN at a build)
#   see book Chapter 4 — Installation
```

## Run it (interactive)

```bash
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./shared-licensing-cli-demo.sh            # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./shared-licensing-cli-demo.sh` prints every command without running
it — a safe first pass to read the flow.

## Record it

```bash
./record.sh                               # → demo-video/shared-licensing-cli-demo.mp4
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
../lib/check-masking.sh shared-licensing-cli-demo
```

Every credential this demo is given is registered with `secret` in preflight, so
`say`/`ok`/`show` and `show_file` mask it (and its base64 form) as
`***REDACTED***`. Preflight prints `✓ secret masking active …` on camera to
confirm. See [Secrets on camera](../README.md#secrets-on-camera).

## Why it works

- **The install pulls from the registry, not FAR — and nothing else does either.**
  `registry replicate` records a `registry-mirror.json`; `renderBNKFields` turns it
  into `far_chart_repo_url` / `far_image_repo_url` + `use_registry_mirror = true`,
  and carries the registry credentials through so charts and images authenticate
  with the same ones replication used. Two further things make it genuinely
  disconnected: the **f5-bigip-k8s-manifest is itself mirrored** (it is a BOM
  artifact), and roksbnkctl applies the manifest as a **`CNEManifest` CR** — FLO
  resolves the manifest from that CR and never fetches one from a registry.
- **`flp up --add-node-port-access`** is what makes the proxy usable from the other
  cluster. The chart already ships a NodePort Service, but it hardcodes
  `externalTrafficPolicy: Local` with one replica (so only the node hosting the pod
  answers), its certificate has no **IP SANs** (so a remote controller dialling
  `https://<node-ip>:30001` fails the handshake), and ROKS workers sit in a
  security group that does not admit another cluster. The flag fixes all three.
- **The app cluster never runs `flp up`.** It points at the foreign proxy with
  `bnk.flp.external` (`url` + `root_ca_b64`, read straight out of the services
  workspace with `roksbnkctl -w services flp output <name>`). The CA is passed
  **verbatim** — it is already base64, and re-encoding it hands the CWC a corrupt CA.

See [Licensing BNK with the F5 License Proxy](../../../book/src/10c-flp-licensing.md#flow-c--a-shared-licensing-cluster).

## Files

| File | What |
|---|---|
| `shared-licensing-cli-demo.sh` | the demo (seven phases: adopt → mirror → FLP → adopt → `bnk up` → prove → remove) |
| `.env.example` | every input, with defaults |
| `record.sh` | one-line wrapper around the shared recorder (`../lib/record-demo.sh`) |

## Teardown

The demo never tears itself down. When you have finished exploring:

```bash
./shared-licensing-cli-demo.sh teardown
```

That removes **only what the demo installed** — BNK from the app cluster, the FLP from the
services cluster, and the two local workspaces. **Both ROKS clusters keep running**: they
were `cluster register`ed, never created, so roksbnkctl does not own them. The private
registry keeps its mirrored artifacts for the same reason; teardown prints the one command
that empties it if you want that too. Teardown re-sources `.env` itself, so it runs
standalone.
