# Demo 4 — shared licensing cluster (`flp-licensed-demo.sh`)

One cluster runs the F5 License Proxy (a NodePort service) and holds the only
egress to F5; a second, air-gapped cluster installs BNK entirely from a private
registry and licenses *through that proxy*. **Both ROKS clusters must already be
running** — the demo `cluster register`s them, it does not build them (cluster
builds are ~40 min of nothing to watch).

## Dependencies

**Prerequisites:**
- **Two running ROKS clusters** on the same Transit Gateway — a services cluster
  (`SERVICES_CLUSTER`) and an app cluster (`APP_CLUSTER`).
- **A running Harbor** — build off-camera with
  `lib/deploy-far-registry.sh` (see [Demo 3](../far-replication-demo/README.md)).

**Inputs** (via `lib/prompt-inputs.sh` → `demo.env`):

| Input | What |
|---|---|
| `IBMCLOUD_API_KEY` | IBM Cloud API key |
| `SERVICES_CLUSTER` / `APP_CLUSTER` | the two existing clusters (by name) |
| `APP_CLUSTER_CIDR` | the app cluster's pod/node CIDR (NodePort source allow-list) |
| `REGISTRY_DOMAIN` / `REGISTRY_ADMIN_PASSWORD` | the Harbor above |
| `FAR_REPO_URL` / `BNK_VERSION` / `REGION` / `RESOURCE_GROUP` | source + manifest + placement |

**Tools:**
- **Control host** — the shared recording pipeline
  ([`../README.md`](../README.md#install-the-pipeline-on-ubuntu-control-host)).
- **VSI** — `terraform`, `helm`, `asciinema`, `roksbnkctl` installed automatically.

## Run

```bash
cd scripts/demos/lib
./prompt-inputs.sh && ./provision-vsi.sh up
SCREENPLAY=flp-licensed-demo.sh ./record.sh run
SPEED=0.10 MAX_IDLE=1.5 ./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```

Teardown removes only the workloads — it never destroys the clusters, because it
never created them.
