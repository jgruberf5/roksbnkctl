# roksbnkctl demonstration video pipeline

Reproducible tooling to **record (and re-record)** four demos as time-compressed,
EN + FR narrated 1080p mp4s:

| # | Screenplay | What it demonstrates |
|---|---|---|
| 1 | `cli-demo.sh` | The **roksbnkctl CLI lifecycle** — install on a fresh Ubuntu VSI, build a ROKS cluster, register it with BNK Forge, install BNK, run the testing framework, remove/rebuild BNK, then `down` everything. |
| 2 | `ci-demo.sh` | The **tools-runner container as a CI pipeline** — the same story with *zero* host install; every step is a `docker run` of the all-in-one runner image, exactly what a CI job calls. |
| 3 | `far-replication-demo.sh` | **FAR replication into a private registry** — pull the FAR credential from IBM COS, then mirror every BNK chart and image out of `repo.f5.com` into a private OCI registry and verify each artifact by digest. The registry (a standard open-source Harbor) is built beforehand, off-camera. |
| 4 | `flp-licensed-demo.sh` | **A shared licensing cluster** — one cluster runs the F5 License Proxy (exposed as a NodePort service) and holds the only egress to F5; a second, air-gapped cluster installs BNK entirely from a private registry and licenses *through that proxy*. Replicate FAR → registry, `flp up --add-node-port-access`, then `bnk up` in the other cluster. **Both ROKS clusters are already running and are `cluster register`ed, not created** — cluster builds are ~40 min of nothing to watch. |
| 5 | `ci-flp-demo.sh` | **Demo #4 as a CI pipeline** — the same disconnected install (private registry + remote FLP licensing), except every step is a `docker run` of the tools-runner image and the whole workspace comes from **environment variables**. No roksbnkctl, terraform, helm or kubectl on the host; no `config.yaml` templated anywhere. The handoff between the two jobs — the proxy's address and its CA — is two env vars, exactly what CI passes as job outputs. **Both ROKS clusters are already running and are `cluster register`ed, not created.** |

All four share one pipeline. The raw asciinema `.cast` is the **master** — the
mp4s are derived from it, so re-cuts (pacing, narration, language) never need
another cloud run.

## How the pipeline works

There are two machines:

- the **control host** (your workstation) runs the orchestration; and
- a throwaway **Ubuntu 24.04 VSI** in IBM Cloud, where the demo actually happens
  and where the camera points.

`record.sh` is the bridge. It copies the screenplay to the VSI, runs it under
`asciinema rec` inside `tmux` (so a long shoot survives an SSH drop), polls for
completion, and pulls the recording back into `out/`. Then `postprocess.sh`
time-compresses it and `voiceover.sh` narrates it.

Pick the screenplay with the `SCREENPLAY` environment variable. It defaults to
`cli-demo.sh`.

## Scripts

| Script | Where it runs | What it does |
|---|---|---|
| `prompt-inputs.sh` | control host | Prompts for the 10 inputs (API key, SSH key, Forge URL/user/pass, cluster shape, BNK version, FAR repo, FAR service account, mirror project). Demo #3 additionally reads `REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD` from `demo.env` (see Demo #3 below). Defaults come from an existing `config.yaml` (via `yq`) or roksbnkctl's built-in defaults. Writes `demo.env` (0600). |
| `provision-vsi.sh {up\|down\|ssh}` | control host | Creates/destroys a self-contained Ubuntu 24.04 VSI (own VPC, subnet, PGW, SG rule, floating IP). IDs recorded in `vsi-state.env`. |
| `cli-demo.sh [teardown]` | **on the VSI** | Demo #1 — see the table above. |
| `ci-demo.sh [teardown]` | **on the VSI** | Demo #2 — see the table above. |
| `far-replication-demo.sh [teardown]` | **on the VSI** | Demo #3 — see the table above. Drives `../deploy-far-registry.sh`, which provisions a *second* VSI to host Harbor. |
| `flp-licensed-demo.sh [teardown]` | **on the VSI** | Demo #4 — see the table above. Registers two ALREADY-RUNNING clusters (`SERVICES_CLUSTER` + `APP_CLUSTER` in `demo.env`) and a pre-built registry; installs the FLP in one and BNK in the other. Teardown removes only the workloads — it never destroys the clusters, because it never created them. |
| `demo-lib.sh` | on the VSI | Shared presentation helpers: stage cards + framed commands. The `STAGE n/N · Title` line doubles as the chapter marker. |
| `record.sh {run\|start\|wait\|fetch\|attach\|teardown}` | control host | Copies the screenplay + `demo.env` to the VSI, records it under `asciinema rec` in `tmux`, polls, and pulls the master `.cast` into `out/`. |
| `postprocess.sh <cast>` | control host | Idle-compresses the cast (`MAX_IDLE`), renders `agg`→gif→`ffmpeg`→`*.silent.mp4`, and extracts `*.chapters.tsv`. |
| `voiceover.sh <compressed.cast> <chapters.tsv> [langs]` | control host | Piper TTS per chapter (`narration.<lang>.txt`), inserts per-chapter holds so clips never overlap, re-renders and muxes → one mp4 per language (default `en fr`). |

## Host prerequisites

- **Control host:** `ibmcloud` CLI + `vpc-infrastructure` plugin, `jq`, `ssh`/`scp`,
  `agg` (asciinema gif renderer), `ffmpeg`, `piper` (+ voice models), Python 3.
- **VSI:** installed automatically by the screenplays (`terraform`, `helm`,
  `asciinema`, `tmux`, roksbnkctl).
- **Piper voices:** download `.onnx` + `.onnx.json` from
  <https://huggingface.co/rhasspy/piper-voices> into `voices/` (default
  `en_US-lessac-medium`, `fr_FR-siwis-medium`; override with `PIPER_MODEL_EN/FR`).

If `agg` was installed with `cargo`, make sure it is on `PATH`
(`export PATH="$HOME/.cargo/bin:$PATH"`) before running `postprocess.sh` or
`voiceover.sh`.

## Common setup (once)

```bash
cd scripts/demo
./prompt-inputs.sh          # → demo.env (0600, git-ignored)
./provision-vsi.sh up       # → fresh VSI, ids in vsi-state.env
```

---

## Demo #1 — roksbnkctl CLI

Builds a real ROKS cluster; expect **45–90 min**. The closing `down` destroys the
cluster on camera, so the demo self-cleans.

```bash
SCREENPLAY=cli-demo.sh ./record.sh run      # or just: ./record.sh run   (it's the default)
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```

## Demo #2 — tools-runner container (CI)

Same story, no host install. Also builds a real cluster, so also **45–90 min**.
Set `CI_WORKSPACE` in `demo.env` to keep its workspace separate from demo #1's.

```bash
SCREENPLAY=ci-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```

## Demo #3 — FAR replication

**No ROKS cluster is built**, so this is the fast one — roughly **20 min**.
`registry replicate` reads the workspace, pulls from FAR and pushes to the
target; it never contacts a cluster. (`registry --help` still says "needs a live
cluster" — that line is stale, left over from the removed `openshift` target.)

**Build the registry first (off-camera).** How the registry is built is not part
of this demo's story — it just needs an OCI-compliant registry to target.
`scripts/deploy-far-registry.sh` stands up a standard open-source Harbor and
prints its address + admin password; put both in `demo.env`:

```bash
../deploy-far-registry.sh -w prebuilt-harbor \
  --key-name <ibmcloud-key> --ssh-key vsi_key \
  --region "$REGION" --vpc <vpc> --subnet <subnet> --resource-group <rg> \
  --project bnk-mirror --no-configure
# it writes ~/.roksbnkctl/prebuilt-harbor/far-registry/credentials.env; copy from there:
printf 'export REGISTRY_DOMAIN=%s\n'         "<fip>.sslip.io"   >> demo.env
printf 'export REGISTRY_ADMIN_PASSWORD=%s\n' "<admin-password>" >> demo.env
```

Then record — the screenplay ships no extra files:

```bash
SCREENPLAY=far-replication-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```

Details:

- **No FAR credential to supply.** `roksbnkctl` downloads `bnk.far_auth_file`
  (default `f5-far-auth-key.tgz`) from the orchestration COS bucket and extracts
  the service account itself — see `resolveFARServiceAccount` in
  `internal/cli/registry.go`, called from `buildBOM` whenever
  `registry.source_service_account_b64` is empty. The demo shows only
  `cos object list` (the credential tarball sitting in COS); roksbnkctl consumes
  the tarball itself. Override the COS coordinates with `COS_INSTANCE` /
  `COS_BUCKET` / `FAR_AUTH_FILE` in `demo.env` if your layout differs. The bucket
  is in `us-south` even when the workspace is not —
  `cos.DefaultBucketRegionResolver` resolves each bucket's own region.
- **The registry must serve real TLS.** `replicate` drives crane over HTTPS and no
  CLI path sets `mirror.Engine.Insecure`. `deploy-far-registry.sh` gives Harbor a
  Let's Encrypt cert for `<floating-ip>.sslip.io` via Caddy; needs inbound 80/443.
- **Re-records:** the registry persists between takes, so empty the project first
  (`roksbnkctl -w <ws> registry delete --force`) or rebuild the registry, so
  `diff` shows everything missing again.

---

## Demo #4 — a shared licensing cluster

**One cluster runs the F5 License Proxy and holds the only egress to F5. A second,
air-gapped cluster installs BNK entirely from a private registry and licenses through
that proxy.** Neither cluster reaches `repo.f5.com`; only one of them can reach F5 at all.

```
   ┌──────── services cluster (the only egress to F5) ───────┐
   │  F5 License Proxy ──NodePort 30001──┐                   │
   └─────────────────────────────────────┼───────────────────┘
   ┌─────────────────────────────────────┼── app cluster ────┐
   │  BNK + CNEInstance + CWC ───────────┘                   │
   │      └── charts + images ── private registry (Harbor)   │
   └─────────────────────────────────────────────────────────┘
```

The demo shows exactly three things: **replicate FAR into a private registry**,
**deploy the FLP with a NodePort service**, and **deploy BNK into the other cluster**
using that registry and that proxy.

### Both ROKS clusters must already be running

They are `cluster register`ed, **not created**. Building a ROKS cluster is ~40 minutes
of nothing to watch, twice over — and registering an existing cluster is a first-class
roksbnkctl flow that is more representative anyway. Because the demo never creates the
clusters, **it never destroys them**: teardown removes only the FLP and BNK it installed.

That drops the shoot to roughly **50–70 min** (mostly `registry replicate` and
`bnk up`), against 3+ hours if it built the clusters.

Set both in `demo.env`, plus the app cluster's CIDR (opened to the FLP's NodePort):

```bash
printf 'export SERVICES_CLUSTER=%s\n'  "<running-cluster-that-gets-the-FLP>" >> demo.env
printf 'export APP_CLUSTER=%s\n'       "<running-cluster-that-gets-BNK>"     >> demo.env
# EVERY zone prefix of the app cluster's VPC, comma-separated. One per zone —
# omit one and a pod scheduled there is dropped at the security group.
#   ibmcloud is vpc-address-prefixes <vpc> --output json | jq -r '[.[].cidr]|join(",")'
printf 'export APP_CLUSTER_CIDR=%s\n'  "10.242.0.0/18,10.242.64.0/18,10.242.128.0/18" >> demo.env
```

The two clusters must be able to reach each other — same VPC (simplest), or a transit
gateway between their VPCs.

### The registry

Same prerequisites as Demo #3 — build it off-camera with `scripts/deploy-far-registry.sh`
and put `REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD` in `demo.env`. Start from an
**empty** project (`roksbnkctl -w <ws> registry delete --force`) so the replication has
real work to show on camera.

### Record

FLP support ships in **v1.18.0**, so the demo installs the public release by default —
which is what you want on camera: it shows a viewer exactly what they would get.

```bash
SCREENPLAY=flp-licensed-demo.sh ./record.sh run
```

Add `SRC_BUILD=1` only to demo work that is **not yet released** — it builds the current
branch and ships that binary to the VSI instead of downloading the release. Without it,
`record.sh` deliberately deletes any stale `roksbnkctl.bin` from the VSI so the screenplay
cannot silently install an old source build.

### Accelerate the long stretches

`registry replicate` and `bnk up` are long stretches of *active* output — idle
compression alone will not shorten them. Push `SPEED` (which time-compresses console
output; lower = faster) well below its 0.25 default:

```bash
SPEED=0.10 MAX_IDLE=1.5 ./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
mv out/latest.en.mp4 out/demo4-flp.en.mp4
mv out/latest.fr.mp4 out/demo4-flp.fr.mp4
```

The raw `.cast` is never modified, so re-cut at a different `SPEED` freely — no
further cloud run.

### Details

- **The install pulls from the registry, not FAR — and nothing else does either.**
  `registry replicate` records a `registry-mirror.json`; `renderBNKFields` turns it into
  `far_chart_repo_url` / `far_image_repo_url` + `use_registry_mirror = true`, and carries
  the registry credentials through so charts and images authenticate with the same ones
  replication used — the pods get a `mirror-secret` dockerconfig, so the registry stays
  **private** (no world-readable project). Two further things make it genuinely disconnected: the
  **f5-bigip-k8s-manifest is itself mirrored** (it is a BOM artifact), and roksbnkctl
  applies the manifest to the cluster as a **`CNEManifest` CR** — FLO resolves the
  manifest from that CR and never fetches one from a registry.
- **`flp up --add-node-port-access`** is what makes the proxy usable from the other
  cluster. The chart already ships a NodePort Service, but it hardcodes
  `externalTrafficPolicy: Local` with one replica (so only the node hosting the pod
  answers), its certificate has no **IP SANs** (so a remote controller dialling
  `https://<node-ip>:30001` fails the handshake), and ROKS workers sit in a security
  group that does not admit another cluster. The flag fixes all three.
- **The app cluster never runs `flp up`.** It points at the foreign proxy with
  `bnk.flp.external` (`url` + `root_ca_b64`, read straight out of the services
  workspace with `roksbnkctl -w services flp output <name>`).

See [Licensing BNK with the F5 License Proxy](../../book/src/10c-flp-licensing.md#flow-c--a-shared-licensing-cluster).

## Demo #5 — the disconnected install as a CI pipeline

**Demo #4, told the way it actually ships: as two CI jobs.** Every step is a `docker run`
of the tools-runner image, the whole workspace comes from **environment variables**, and
the handoff between the jobs — the proxy's address and its CA — is two of them, exactly
what CI passes as job outputs. Nothing is installed on the host, and no `config.yaml` is
templated anywhere.

```
   ┌─ JOB 1 · licensing cluster ─────────────┐
   │  mirror FAR → private registry          │
   │  F5 License Proxy ──NodePort 30001──┐    │
   │  outputs: flp_url, flp_ca ──────────┼──┐ │
   └─────────────────────────────────────┼──┼─┘
   ┌─ JOB 2 · application cluster ───────┼──┼─┐
   │  BNK ← charts + images ── registry  │  │ │
   │  CWC ───────────────────────────────┘  │ │  licensed remotely
   │  ROKSBNKCTL_FLP_EXTERNAL_URL / _ROOT_CA_B64 ←┘
   └─────────────────────────────────────────┘
```

Same two-cluster prerequisites as Demo #4 — both `cluster register`ed, not created; the
same `SERVICES_CLUSTER` / `APP_CLUSTER` / `APP_CLUSTER_CIDR` in `demo.env`; the same
off-camera Harbor (`REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD`). The difference is
entirely in *how* it drives roksbnkctl.

### Record

The CI/runner FLP support ships in **v1.19.0**, so the demo pulls the public runner image
by default — which is the point on camera, since a viewer sees exactly what they would
get. `RUNNER_TAG` pins which one:

```bash
SCREENPLAY=ci-flp-demo.sh RUNNER_TAG=v1.19.0 ./record.sh run
```

`ci-flp-demo.sh` runs everything through `ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG`,
so — unlike Demo #4 — there is no `SRC_BUILD`; the binary under test *is* the image.

### Accelerate + name the outputs

Same long stretches as Demo #4 (`registry replicate`, `bnk up`), so push `SPEED` the same
way:

```bash
SPEED=0.10 MAX_IDLE=1.5 ./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
mv out/latest.en.mp4 out/demo5-ci-flp-disconnected.en.mp4
mv out/latest.fr.mp4 out/demo5-ci-flp-disconnected.fr.mp4
```

### Details

- **The whole workspace is environment variables.** `init --non-interactive
  --override-from-env` builds it from `ROKSBNKCTL_*` — no file is templated. The registry
  target is `ROKSBNKCTL_REGISTRY_TARGET` / `_GENERIC_HOST` / `_GENERIC_REPO_PREFIX` /
  `_GENERIC_USERNAME` / `_GENERIC_PASSWORD`; the license mode is `ROKSBNKCTL_LICENSE_MODE`.
- **The cross-job handoff is two variables.** Job 1 prints `flp output
  flp_external_endpoint` and `flp_root_ca`; job 2 receives them as
  `ROKSBNKCTL_FLP_EXTERNAL_URL` and `ROKSBNKCTL_FLP_ROOT_CA_B64`. The CA is passed
  **verbatim** (it is already base64) — re-encoding it hands the CWC a corrupt CA.
- **`flp up` runs *inside* the container**, which it could not do before v1.19.0: the
  chart's Helm post-renderer was a generated python script, and the runner image has no
  python. roksbnkctl now post-renders its own chart (`roksbnkctl flp postrender`), so the
  container needs no interpreter.

Recorded output: **`out/demo5-ci-flp-disconnected.{en,fr}.mp4`** (EN 3:32 / FR 3:48).

See [Flow C in CI](../../book/src/10c-flp-licensing.md#flow-c-in-ci--the-runner-container-no-host-install).

---

## Finishing up

```bash
./record.sh teardown        # emergency: run the screenplay's teardown on the VSI
./provision-vsi.sh down     # destroy the demo VSI + its VPC
```

`record.sh teardown` uses whatever `SCREENPLAY` is set to, so pass the same value
you recorded with.

The outputs land as `out/latest.*`. Rename them per demo if you want to keep more
than one, e.g. `mv out/latest.en.mp4 out/demo3-far.en.mp4`.

## Re-cutting without a cloud run

This is what the master-cast design buys you:

```bash
MAX_IDLE=1.5 SPEED=0.2 ./postprocess.sh out/latest.cast              # tighter pacing
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv fr # French only
```

Edit `narration.en.txt` / `narration.fr.txt` and re-run `voiceover.sh` alone — no
re-record, and no `postprocess.sh` either, since the compressed cast is unchanged.

> `voiceover.sh` takes the **compressed cast**, not the silent mp4 — it re-renders
> the video after inserting per-chapter holds. Run it with no arguments and it
> defaults to `out/latest.compressed.cast` + the matching `chapters.tsv`.

## Narration

`narration.en.txt` / `narration.fr.txt` hold one line per chapter, formatted
`<key>|<spoken text>`. The key is matched as a **substring of the stage-card
title**, and the *first* matching line wins — so keys must stay distinct and
ordered. All three demos share the two files.

Phonetic respellings keep Piper honest: `roksbnkctl` → "rocks BNK cuddle",
`BIG-IP Next for Kubernetes` → "big IP next for cuber net tees".

## Secrets & git

`demo.env`, `vsi-state.env`, `vsi_key`, `out/`, `voices/` and `roksbnkctl.bin` are
git-ignored. `demo.env` holds the IBM Cloud API key, the Forge password and the
FAR service account (mode 0600) — never commit it.

## Known validation points (first live shoot)

Best-effort against external surfaces, confirmed on the first real run:
- `bnk-forge login` flags (separate CLI; set `BNK_FORGE_CLI_URL` to fetch it onto the VSI).
- Self-signed Forge cert: `FORGE_INSECURE=true` (default) skips TLS validation for the
  forge calls only. If `bnk-forge` names its skip flag differently, set
  `BNK_FORGE_INSECURE_FLAG=--tls-skip-verify` (or similar) in `demo.env`.
- `--auto` on `cluster/bnk/testing up` and whether `test` needs it.
- IBM `is` flag drift: `instance-create … SUBNET` positional form and
  `subnet-public-gateway-attach`.
- Optional `FORGE_PROJECT` in `demo.env` if your Forge requires a project id.

For `far-replication-demo.sh` (demo #3): the registry is built off-camera by
`deploy-far-registry.sh`, so the recorded screenplay is pure `roksbnkctl`
(`cos`, `registry bom/target/diff/replicate/verify/list`). Pre-flighted against
the live account: `init` with the mirror-only seed config; `cos object list`
against `bnk-orchestration`/`bnk-schematics-resources` (cross-region from
`eu-gb`); `registry bom` auto-resolving the FAR service account from COS — 88
artifacts (62 images, 26 charts); and a full `replicate` + `verify` into a live
Harbor.
