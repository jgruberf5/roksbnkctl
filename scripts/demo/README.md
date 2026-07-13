# roksbnkctl demonstration video pipeline

Reproducible tooling to **record (and re-record)** four demos as time-compressed,
EN + FR narrated 1080p mp4s:

| # | Screenplay | What it demonstrates |
|---|---|---|
| 1 | `cli-demo.sh` | The **roksbnkctl CLI lifecycle** — install on a fresh Ubuntu VSI, build a ROKS cluster, register it with BNK Forge, install BNK, run the testing framework, remove/rebuild BNK, then `down` everything. |
| 2 | `ci-demo.sh` | The **tools-runner container as a CI pipeline** — the same story with *zero* host install; every step is a `docker run` of the all-in-one runner image, exactly what a CI job calls. |
| 3 | `far-replication-demo.sh` | **FAR replication into a private registry** — pull the FAR credential from IBM COS, then mirror every BNK chart and image out of `repo.f5.com` into a private OCI registry and verify each artifact by digest. The registry (a standard open-source Harbor) is built beforehand, off-camera. |
| 4 | `flp-licensed-demo.sh` | **Air-gap-style install, licensed by the F5 License Proxy** — one workspace declares a Harbor mirror + FLP licensing, then replicates FAR → Harbor, builds the cluster, installs the FLP (from Harbor), installs BNK (from Harbor, licensed by the in-cluster FLP), confirms the License CR is Active via the proxy, and `down`s everything. Nothing is pulled from `repo.f5.com` after replication. The Harbor is built beforehand, off-camera. |

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
| `flp-licensed-demo.sh [teardown]` | **on the VSI** | Demo #4 — see the table above. Targets a pre-built Harbor (`REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD` in `demo.env`); builds a cluster, installs the FLP + BNK from Harbor, licenses via the FLP. |
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

## Demo #4 — FLP-licensed install from Harbor

The full-length one: it **builds a ROKS cluster**, installs the FLP, and installs
BNK, so budget roughly **90–120 min** of live cloud (and real IBM Cloud spend).
The master `.cast` captures it once; re-cuts never need another run.

**Same Harbor prerequisites as Demo #3** — build the registry off-camera with
`scripts/deploy-far-registry.sh` and put `REGISTRY_DOMAIN` + `REGISTRY_ADMIN_PASSWORD`
in `demo.env` (see Demo #3 above). The screenplay itself replicates FAR into it on
camera, so start from an **empty** Harbor project (`registry delete --force`, or a
fresh project) so the replication has real work to show.

**Use `SRC_BUILD=1`** — FLP support is not in a public release yet, so the demo
must install the source build (`record.sh` builds `roksbnkctl.bin` from this repo
and ships it; the screenplay installs it in preference to any release).

```bash
SRC_BUILD=1 SCREENPLAY=flp-licensed-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
mv out/latest.en.mp4 out/demo4-flp.en.mp4   # keep it alongside the others
mv out/latest.fr.mp4 out/demo4-flp.fr.mp4
```

Details:

- **The install pulls from Harbor, not FAR — and nothing else does either.**
  `registry replicate` records a `registry-mirror.json` whose `ChartHost`/`ImageHost`
  point at the Harbor project; `renderBNKFields` turns that into `far_chart_repo_url` /
  `far_image_repo_url` + `use_registry_mirror = true`, and carries the Harbor
  credentials through as `registry_mirror_username` / `registry_mirror_password` so
  charts and images authenticate to the mirror with the same credentials replication
  used (no anonymous-pull / public-project requirement). Two things make the install
  genuinely disconnected:
  - the **f5-bigip-k8s-manifest is itself mirrored** (it is a BOM artifact —
    `bnkbom.ManifestChartName`), so the install never goes back to `repo.f5.com` for it;
  - roksbnkctl applies the manifest to the cluster as a **`CNEManifest` CR**. FLO
    resolves the BNK manifest by listing cluster-scoped `CNEManifest`s and matching
    `spec.version`, and only falls back to pulling it from a registry when none
    matches — so with the CR present, FLO never fetches a manifest at all.
- **FLP licensing.** `bnk.license_mode: f5licenseproxy` + the `bnk.flp` block make
  the run install the in-cluster F5 License Proxy (`flp up`) and point BNK's License
  CR at it. The cluster-wide controller trusts the proxy's CA (written to the
  `licenseserver-rootca` Secret) and brokers the entitlement through the proxy — see
  `book/src/10c-flp-licensing.md`. A subscription JWT is still required
  (`bnk.subscription_jwt_file`, default `trial.jwt`, pulled from COS like the FAR
  credential).
- **No FAR credential / JWT to supply** — same as Demo #3, roksbnkctl reads both from
  the orchestration COS bucket.
- **Teardown is two commands** (the screenplay's stage 9 does both): `flp down` first
  (the FLP is a standalone phase the composite `down` does not touch), then `down`
  removes BNK + the cluster. The closing `down` destroys the cluster on camera.

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
