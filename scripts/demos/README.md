# roksbnkctl demonstrations

Six self-contained, reproducible demos of `roksbnkctl`. Every one of them **runs on
this host** (Linux / WSL), drives real IBM Cloud infrastructure, and records itself
to an MP4 with the same tooling — so they differ only in *what* they demonstrate,
never in how they look.

| Demo | What it demonstrates |
|---|---|
| [`cluster-lifecycle-cli-demo`](cluster-lifecycle-cli-demo/) | The **lifecycle, one phase at a time** — `init` from a declarative config, `cluster up`, `bnkforge register`, `bnk up`, `testing up` + `test`, then `bnk down`/`bnk up` to swap BNK without touching the cluster. Ends with everything running; `teardown` is one `roksbnkctl down`. |
| [`cluster-lifecycle-ci-demo`](cluster-lifecycle-ci-demo/) | The same lifecycle with **zero host install** — every step is a `docker run` of the all-in-one `roksbnkctl-tools-runner` image, exactly what a CI job calls. |
| [`far-replication-demo`](far-replication-demo/) | **Mirroring the F5 Artifact Repository** into a private OCI registry — the FAR credential read from COS, `registry bom` → `target` → `diff` → `replicate` → `verify` → `list`. **No cluster is built**, so it is the fast one. |
| [`shared-licensing-cli-demo`](shared-licensing-cli-demo/) | **A shared licensing cluster** — one cluster runs the F5 License Proxy and holds the only egress to F5; a second, air-gapped cluster installs BNK entirely from a private registry and licenses *through that proxy*. Both clusters are **adopted, not created**, so `teardown` removes only the FLP and BNK. |
| [`shared-licensing-ci-demo`](shared-licensing-ci-demo/) | The same shared-licensing story as **two CI jobs** — every step a `docker run`, the whole workspace from **environment variables**, and the cross-job handoff (the proxy's URL + its CA) as two of them. |
| [`disconnected-cluster-cli-demo`](disconnected-cluster-cli-demo/) | A **fully disconnected** BNK install onto an existing private cluster over a Transit Gateway (the Appendix A topology) — a Harbor mirror VSI that is also the operator host, a standalone FLP VSI, then `bnk up` in one pass. |

`disconnected-cluster-ci-demo/` is a placeholder for the same disconnected story
driven by ArgoCD.

## Layout

Each demo is a self-contained folder with the same four things:

```
<demo>/
  <demo>.sh        the demo — phases, hands-off, DRY_RUN-able
  .env.example     every input, with defaults; copy to .env
  record.sh        one-line wrapper around lib/record-demo.sh
  README.md        what it does, what it needs, how to run and record it
```

The shared pieces live in `lib/`:

| File | What |
|---|---|
| `demo-format.sh` | the on-camera format every demo sources: `banner`/`phase`, `show`/`run`, `begin_long`/`end_long`, `say`/`note`/`ok`/`die`, `pause`, and the **secret masking** (`secret`/`redact`/`show_file`/`runmask`) |
| `check-masking.sh` | **run before every shoot** — proves no credential can reach the screen |
| `record-demo.sh` | the recorder: Xvfb + xterm + ffmpeg, then `post_10x.py` |
| `post_10x.py` | builds the final cut from the raw + queue: phase/command/output stills, 10× long windows, seekable re-encode |
| `deploy-far-registry.sh` | stands up a standalone open-source Harbor on its own VSI — the private registry the mirror/licensing demos target. Run once, off-camera. |
| `forge-register.sh` | registers a workspace's cluster with **BNK Forge v3** over its REST API (v3 is REST/UI-first and has no CLI) |
| `provision-vsi.sh {up\|down\|ssh}` | optional: a throwaway Ubuntu 24.04 VSI (own VPC, subnet, PGW, SG, floating IP) to run a demo on a clean host |

## How a demo runs

```bash
cd scripts/demos/<demo>
cp .env.example .env && $EDITOR .env      # fill in the required values
set -a; source .env; set +a
./<demo>.sh                               # AUTO_ADVANCE=1 → hands-off; set 0 to step
```

`DRY_RUN=1 ./<demo>.sh` prints every command without running it — a safe first pass
to read the flow. `AUTO_ADVANCE=0` waits for ENTER between phases, which is what you
want when presenting live.

### No demo tears itself down

Every demo **leaves its infrastructure running** and ends with a report naming each
reachable web UI, its credentials **by variable name** (never the literal value, so the
report is recording-safe), and the one command that removes everything:

```
Reachable web UIs (explore before you tear down):
  Harbor registry:  https://<host>/   (login: admin / your REGISTRY_ADMIN_PASSWORD)
  ...
Tear it all down when finished:
  ./<demo>.sh teardown
```

`./<demo>.sh teardown` removes **only what that demo created** — its workspaces, VSIs,
VPC/subnets/gateways/floating-IPs and TGW connection. Anything the demo **adopted**
(`cluster register`) or was handed (a registry built off-camera) is left untouched, and
the report says so. Teardown re-sources `.env` itself, so it runs standalone.

## How a demo records

```bash
./record.sh                               # → demo-video/<demo>.mp4
```

The demo runs hands-off inside a headless X terminal; ffmpeg captures that display;
then `post_10x.py` re-cuts the raw capture from the **queue file** the demo wrote:

| Queue row | Becomes |
|---|---|
| `PHASE MARK` | the phase banner, on a freshly **cleared screen**, held **4s** |
| `COMMAND MARK` | the `roksbnkctl` command itself — fully rendered, no output yet — held **5s** |
| `OUTPUT MARK` | that command's **settled** output, held **5s** |
| `LONG START`/`END` | the **only** sped part: 10×, so a 40-minute apply is four watchable minutes |

So every roksbnkctl command reads as **[5s: the command] → [10× streaming] → [5s: its
output]**, and each phase opens on a clean title card. Because the timeline is entirely
queue-driven, **any timing change is a `post_10x.py` re-run on the saved raw** — never a
re-record:

```bash
CMD_SECS=7 SPEED=15 python3 ../lib/post_10x.py \
  demo-video/demo-raw.mkv demo-video/phase-timestamps.txt \
  "$(cat demo-video/rec_start)" demo-video/recut.mp4
```

There is **no voiceover** and no narration files: the `say`/`note` context lines are
written straight onto the screen, in the demo, next to the command they explain. A
re-cut therefore needs nothing but a re-run of `post_10x.py` against the raw capture
in `demo-video/`.

Recording prerequisites on this host:

```bash
sudo apt-get update && sudo apt-get install -y xvfb xterm ffmpeg python3
```

## Secrets on camera

These demos are **recorded**, and a leaked API key in a published video is
unrecoverable. Masking is therefore built into the format layer, not left to each
demo to remember:

- each demo calls **`secret "$IBMCLOUD_API_KEY" …`** in its preflight, registering
  every credential it was given — **and its base64 form**, since configs carry the
  encoded form;
- from that point `banner`, `say`, `note`, `ok`, `die` and `show` run their text
  through `redact()`, so a credential is replaced with `***REDACTED***` even if a
  demo interpolates one into a message or a command line by accident;
- **`show_file <path> [drop-regex]`** is the *only* sanctioned way to put a file on
  screen — it masks as it prints. Never `run cat` or `run grep` a file that holds
  inputs. The CI demos display their whole `--env-file` this way, so the audience
  sees `IBMCLOUD_API_KEY=***REDACTED***` — which teaches the pipeline better than
  hiding the line did;
- **`runmask <cmd…>`** masks a *command's output* too. Plain `run` deliberately
  does not filter, so roksbnkctl and terraform keep their TTY colours; that is safe
  because roksbnkctl already redacts its own sensitive output (see
  `redactedVarNames` in `internal/config/applied_tfvars.go` and `--show-sensitive`
  in `internal/cli/phase_output.go`).

The demo confirms masking is live **on camera**: preflight prints
`✓ secret masking active — N credential(s) render as ***REDACTED*** on screen`.

### Verify before every shoot

```bash
./lib/check-masking.sh              # all demos; or: ./lib/check-masking.sh <demo>
```

It runs three independent checks — a static scan for unmasked file displays, a unit
test of the redaction helpers (verbatim *and* base64), and a full dry-run of every
demo with sentinel credentials whose transcript is then scanned. Exit 0 means safe
to record.

## Secrets & git

Each demo's `.env` holds its secrets (IBM Cloud API key, Forge password, registry
password) and is **git-ignored**, along with `demo-video/` and `.demo-state/` — see
`.gitignore`. Never commit them. The CI demos write short-lived `--env-file` files
into `.demo-state/` at mode 0600 and delete them at the end of the run.
`record-demo.sh` sources each demo's `.env` *inside* the recorded shell rather than
interpolating values into the `xterm` command line, so no credential lands in the
process table either.
