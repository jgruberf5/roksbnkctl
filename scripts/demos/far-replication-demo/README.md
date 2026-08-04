# Demo 3 — FAR replication into a private registry (`far-replication-demo.sh`)

Pull the FAR credential from IBM COS, then mirror every BNK chart and image out of
`repo.f5.com` into a private OCI registry and verify each artifact by digest. No
cluster is built — this is the supply-chain half, so it's fast.

## Dependencies

**Prerequisite — a running Harbor** (built off-camera, once):

```bash
cd scripts/demos/lib
./deploy-far-registry.sh -w prebuilt-harbor --key-name <ibm-vpc-ssh-key> --ssh-key ~/.ssh/id_rsa
# prints the registry host (REGISTRY_DOMAIN) and admin password (REGISTRY_ADMIN_PASSWORD)
```

**Inputs** (via `lib/prompt-inputs.sh` → `demo.env`):

| Input | What |
|---|---|
| `IBMCLOUD_API_KEY` | IBM Cloud API key (COS read for the FAR credential) |
| `REGISTRY_DOMAIN` / `REGISTRY_ADMIN_PASSWORD` | the Harbor above |
| `FAR_REPO_URL` | source FAR registry (default `repo.f5.com`) |
| `REGION` / `RESOURCE_GROUP` / `CLUSTER_NAME` / `BNK_VERSION` | workspace + manifest version |

**Tools:**
- **Control host** — the shared recording pipeline
  ([`../README.md`](../README.md#install-the-pipeline-on-ubuntu-control-host)).
- **VSI** — `helm`, `asciinema`, `roksbnkctl` installed automatically. The registry
  is a standard open-source Harbor (`deploy-far-registry.sh` stands it up).

## Run

```bash
cd scripts/demos/lib
./prompt-inputs.sh && ./provision-vsi.sh up
SCREENPLAY=far-replication-demo.sh ./record.sh run
./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```
