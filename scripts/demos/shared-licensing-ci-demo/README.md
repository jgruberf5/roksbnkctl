# Demo 5 — shared licensing as CI (`ci-flp-demo.sh`)

[Demo 4](../shared-licensing-cli-demo/README.md) as a **CI pipeline** — the same
disconnected install (private registry + remote FLP licensing), except every step
is a `docker run` of the `roksbnkctl-tools-runner` image and the whole workspace
comes from **environment variables**. No roksbnkctl / terraform / helm / kubectl on
the host; no `config.yaml` templated anywhere. The two-job handoff — the proxy's
address and its CA — is two env vars, exactly what CI passes as job outputs.
**Both ROKS clusters must already be running** (`cluster register`ed, not created).

## Dependencies

**Prerequisites:** same as Demo 4 — two running ROKS clusters (`SERVICES_CLUSTER`,
`APP_CLUSTER`) on one Transit Gateway, and a running Harbor (build off-camera with
`lib/deploy-far-registry.sh`).

**Inputs** (via `lib/prompt-inputs.sh` → `demo.env`): `IBMCLOUD_API_KEY`,
`SERVICES_CLUSTER`, `APP_CLUSTER`, `APP_CLUSTER_CIDR`, `REGISTRY_DOMAIN`,
`REGISTRY_ADMIN_PASSWORD`, `BNK_VERSION`, `REGION`, `RESOURCE_GROUP`.

**Tools:**
- **Control host** — the shared recording pipeline
  ([`../README.md`](../README.md#install-the-pipeline-on-ubuntu-control-host)).
- **VSI** — only **Docker** (installed automatically via `get.docker.com`); it then
  pulls the tools-runner image. To reproduce the runner steps directly on Ubuntu:

  ```bash
  curl -fsSL https://get.docker.com | sh
  sudo usermod -aG docker "$USER"    # re-login so docker runs without sudo
  ```

## Run

```bash
cd scripts/demos/lib
./prompt-inputs.sh && ./provision-vsi.sh up
SCREENPLAY=ci-flp-demo.sh RUNNER_TAG=v1.19.0 ./record.sh run
SPEED=0.10 MAX_IDLE=1.5 ./postprocess.sh out/latest.cast
./voiceover.sh out/latest.compressed.cast out/latest.chapters.tsv
```
