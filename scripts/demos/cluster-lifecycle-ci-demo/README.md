# Demo 2 — tools-runner container as CI (`ci-demo.sh`)

The same story as Demo 1 with **zero host install** — every step is a `docker run`
of the all-in-one `roksbnkctl-tools-runner` image, exactly what a CI job calls.
Also builds a real cluster — expect **45–90 min**.

## Dependencies

**Inputs** (via `lib/prompt-inputs.sh` → `demo.env`): same as Demo 1 **minus**
`CLUSTER_NAME` — `IBMCLOUD_API_KEY`, an IBM Cloud SSH key, a **BNK Forge** account
(`FORGE_URL`/`FORGE_USER`/`FORGE_PASS`), `REGION`, `RESOURCE_GROUP`, `OCP_VERSION`,
`WORKERS_PER_ZONE`, `BNK_VERSION`. Set `CI_WORKSPACE` in `demo.env` to keep its
workspace separate from Demo 1's.

**Tools:**
- **Control host** — the shared recording pipeline
  ([`../README.md`](../README.md#install-the-pipeline-on-ubuntu-control-host)).
- **VSI** — only **Docker** (installed automatically by the screenplay via
  `get.docker.com`); it then pulls `ghcr.io/jgruberf5/roksbnkctl-tools-runner`. No
  roksbnkctl / terraform / helm on the host — that's the whole point.

To reproduce the runner steps directly on an Ubuntu host, install Docker:

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker "$USER"    # log out/in so docker runs without sudo
```

## Run

```bash
cd scripts/demos/lib
./prompt-inputs.sh && ./provision-vsi.sh up
SCREENPLAY=ci-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```
