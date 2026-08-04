# Demo 1 — roksbnkctl CLI lifecycle (`cli-demo.sh`)

Install roksbnkctl on a fresh Ubuntu VSI, build a ROKS cluster, register it with
BNK Forge, install BNK, run the testing framework, remove/rebuild BNK, then `down`
everything. **Builds a real cluster — expect 45–90 min.**

## Dependencies

**Inputs** (collected by `lib/prompt-inputs.sh` → `demo.env`):

| Input | What |
|---|---|
| `IBMCLOUD_API_KEY` | IBM Cloud API key with VPC + Kubernetes-Service + Transit-Gateway access |
| SSH key | a key registered in IBM Cloud VPC (for the demo VSI) |
| `FORGE_URL` / `FORGE_USER` / `FORGE_PASS` | a **BNK Forge** account |
| `REGION` / `RESOURCE_GROUP` / `CLUSTER_NAME` / `OCP_VERSION` / `WORKERS_PER_ZONE` | cluster shape |
| `BNK_VERSION` | the FAR manifest version to install |

**Tools:**
- **Control host** — the shared recording pipeline. Install it on Ubuntu per
  [`../README.md`](../README.md#install-the-pipeline-on-ubuntu-control-host).
- **VSI** — `terraform`, `helm`, `asciinema`, `roksbnkctl` are installed
  automatically by the screenplay. Nothing to install by hand.

## Run

```bash
cd scripts/demos/lib
./prompt-inputs.sh && ./provision-vsi.sh up
SCREENPLAY=cli-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```
