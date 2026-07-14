#!/usr/bin/env bash
#
# ci-flp-demo.sh — Demo #5: the DISCONNECTED install, as a CI pipeline.
#
# Demo #4 tells this story from a laptop. This one tells it the way it actually
# ships: as two CI jobs, where every step is a `docker run` of the tools-runner
# image and the whole workspace comes from environment variables. Nothing is
# installed on the host — no roksbnkctl, no terraform, no helm, no kubectl, no
# ibmcloud — and no config.yaml is templated anywhere.
#
#     ┌─ JOB 1 · licensing cluster ────────────────────────────────┐
#     │  mirror FAR → private registry                             │
#     │  F5 License Proxy ──NodePort 30001──┐                      │
#     │  outputs: flp_url, flp_ca ──────────┼──┐                   │
#     └─────────────────────────────────────┼──┼───────────────────┘
#     ┌─ JOB 2 · application cluster ───────┼──┼───────────────────┐
#     │  BNK ← charts + images ── private registry                 │
#     │  CWC ───────────────────────────────┘  │ licensed remotely │
#     │  ROKSBNKCTL_FLP_EXTERNAL_URL / _ROOT_CA_B64 ←──────────────┘
#     └────────────────────────────────────────────────────────────┘
#
# The handoff between the jobs is TWO ENVIRONMENT VARIABLES — exactly what a CI
# pipeline passes as job outputs. That is the point of the demo.
#
# BOTH ROKS CLUSTERS ARE ALREADY RUNNING and are `cluster register`ed, not created:
# cluster builds are ~40 minutes of nothing to watch. Teardown removes only the
# workloads the pipeline installed; it never destroys clusters it did not create.
#
#   ./ci-flp-demo.sh            # run the demonstration
#   ./ci-flp-demo.sh teardown   # emergency: remove BNK + FLP (clusters stay)
#
# Inputs from demo.env: IBMCLOUD_API_KEY, REGION, RESOURCE_GROUP, BNK_VERSION,
# REGISTRY_DOMAIN, REGISTRY_ADMIN_PASSWORD, SERVICES_CLUSTER, APP_CLUSTER,
# APP_CLUSTER_CIDR (comma-separated — EVERY zone's prefix).
# Optional: RUNNER_IMAGE / RUNNER_TAG, REGISTRY_PROJECT, PACE.
#
# The "STAGE n/N · Title" cards double as chapter markers postprocess.sh greps to
# align the EN/FR voiceover; titles must contain the keys in narration.*.txt.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
trap 'echo $? > "$SCRIPT_DIR/demo_exit"' EXIT
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/demo.env}"
# shellcheck disable=SC1090
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE" || { echo "✗ $ENV_FILE not found — run ./prompt-inputs.sh first." >&2; exit 1; }
# shellcheck disable=SC1091
source "$SCRIPT_DIR/demo-lib.sh"

: "${IBMCLOUD_API_KEY:?set via demo.env}"
: "${REGION:?}"; : "${RESOURCE_GROUP:?}"; : "${BNK_VERSION:?}"
: "${REGISTRY_DOMAIN:?set via demo.env — the private registry host}"
: "${REGISTRY_ADMIN_PASSWORD:?set via demo.env — its admin password}"
: "${SERVICES_CLUSTER:?set via demo.env — the RUNNING cluster that will host the FLP}"
: "${APP_CLUSTER:?set via demo.env — the RUNNING cluster that will get BNK}"
: "${APP_CLUSTER_CIDR:?set via demo.env — the app cluster zone prefixes (comma-separated, ALL zones)}"

RUNNER_TAG="${RUNNER_TAG:-v1.19.0}"
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"
SVC_WS="${SVC_WS:-ci-svc}"     # job 1's workspace — owns the proxy
APP_WS="${APP_WS:-ci-app}"     # job 2's workspace — owns BNK
WORK="${CI_WORK:-$HOME/bnk-ci-flp-state}"   # the state volume, mounted at /work

# ── the two jobs' environments ───────────────────────────────────────────────
# This IS the CI config. Job 1 says where the registry is; job 2 says the same,
# plus how to license — and the two FLP vars it receives from job 1's outputs.
SVC_ENV="$SCRIPT_DIR/.ci-svc.env"
APP_ENV="$SCRIPT_DIR/.ci-app.env"
write_svc_env() {
  cat > "$SVC_ENV" <<EOF
IBMCLOUD_API_KEY=$IBMCLOUD_API_KEY
ROKSBNKCTL_REGION=$REGION
ROKSBNKCTL_RESOURCE_GROUP=$RESOURCE_GROUP
ROKSBNKCTL_PREFIX=$SVC_WS
ROKSBNKCTL_CLUSTER_NAME=$SERVICES_CLUSTER
ROKSBNKCTL_CLUSTER_CREATE=false
ROKSBNKCTL_REGISTRY_TARGET=generic
ROKSBNKCTL_GENERIC_HOST=$REGISTRY_DOMAIN
ROKSBNKCTL_GENERIC_REPO_PREFIX=$PROJECT
ROKSBNKCTL_GENERIC_USERNAME=admin
ROKSBNKCTL_GENERIC_PASSWORD=$REGISTRY_ADMIN_PASSWORD
EOF
  chmod 600 "$SVC_ENV"
}
SVC=(docker run --rm -v "$WORK:/work" --env-file "$SVC_ENV" "$RUNNER" -w "$SVC_WS")
APP=(docker run --rm -v "$WORK:/work" --env-file "$APP_ENV" "$RUNNER" -w "$APP_WS")

# ── emergency teardown ───────────────────────────────────────────────────────
if [[ "${1:-}" == "teardown" ]]; then
  stage "T" "Removing the workloads"
  [[ -f "$APP_ENV" ]] && { "${APP[@]}" bnk down --auto || true; }
  [[ -f "$SVC_ENV" ]] && { "${SVC[@]}" flp down --auto || true; }
  echo; say "BNK and the proxy are gone. Both clusters are still running — the pipeline never owned them."
  exit 0
fi

# ── prep (off-narrative) ─────────────────────────────────────────────────────
if ! command -v docker >/dev/null 2>&1; then
  echo "▶ Installing docker (a CI runner already provides it)…" >&2
  curl -fsSL https://get.docker.com | sh >/dev/null 2>&1
fi
docker pull -q "$RUNNER" >/dev/null 2>&1 || true
# The image runs as uid 1000 — the bind-mounted state dir must be writable, and
# both workspaces must start clean (init refuses to clobber an existing one).
mkdir -p "$WORK"; chmod 777 "$WORK"
rm -rf "$WORK/.roksbnkctl/$SVC_WS" "$WORK/.roksbnkctl/$APP_WS"
write_svc_env

# ── 1) the runner container IS roksbnkctl ────────────────────────────────────
stage "1/8" "A CI runner with nothing installed"
say "This host has no roksbnkctl, no terraform, no helm, no kubectl. Every step is one docker run of the tools-runner image — what a CI job does."
run docker run --rm "$RUNNER" version
say "It even runs the Kubernetes verbs in-process, so there is no kubectl to install and nothing to keep in step with the cluster."

# ── 2) job 1: the licensing cluster, configured from the environment ─────────
stage "2/8" "Job one — configure from the environment alone"
say "No config file is templated anywhere. The workspace IS the environment: where the cluster is, where the registry is, and the credential to reach it."
run grep -v PASSWORD "$SVC_ENV"
say "The registry password is passed by the CI secret store, never written into the pipeline."
run "${SVC[@]}" init --non-interactive --override-from-env
say "Both clusters are already running, so the pipeline adopts them rather than spending forty minutes building one."
run "${SVC[@]}" cluster register "$SERVICES_CLUSTER"

# ── 3) mirror FAR into the private registry ─────────────────────────────────
stage "3/8" "Mirror F5's registry into the private one"
say "roksbnkctl reads F5's manifest and derives every chart and image the install needs — the License Proxy among them."
run "${SVC[@]}" registry replicate --target generic
run "${SVC[@]}" registry verify

# ── 4) the proxy, reachable from the other cluster ──────────────────────────
stage "4/8" "Deploy the License Proxy from the container"
say "The proxy is the only component that needs to reach F5. Deploy it once, here, and expose it so the other cluster can license through it."
say "Pass every zone's prefix — a multi-zone VPC has one per zone, and a pod scheduled in a zone you left out is dropped at the security group."
run "${SVC[@]}" flp up --auto --add-node-port-access --node-port-source-cidr "$APP_CLUSTER_CIDR"
say "The chart needs fixing up before it will install on OpenShift, and roksbnkctl post-renders it itself — so the container needs no python, and no interpreter at all."
run "${SVC[@]}" k get svc f5-license-proxy -n f5-license-proxy

# ── 5) the handoff: two environment variables ───────────────────────────────
stage "5/8" "Hand the proxy to the next job"
say "This is the whole handoff. Two values a CI job prints as outputs: the proxy's address, and its root certificate."
FLP_URL="$("${SVC[@]}" flp output flp_external_endpoint | tr -d '\r')"
FLP_CA="$("${SVC[@]}" flp output flp_root_ca | tr -d '\r')"
type_cmd "roksbnkctl -w $SVC_WS flp output flp_external_endpoint"
printf '%s\n' "$FLP_URL"
type_cmd "roksbnkctl -w $SVC_WS flp output flp_root_ca"
printf '%s…  (%s bytes, base64)\n' "${FLP_CA:0:48}" "${#FLP_CA}"
cat > "$APP_ENV" <<EOF
IBMCLOUD_API_KEY=$IBMCLOUD_API_KEY
ROKSBNKCTL_REGION=$REGION
ROKSBNKCTL_RESOURCE_GROUP=$RESOURCE_GROUP
ROKSBNKCTL_PREFIX=$APP_WS
ROKSBNKCTL_CLUSTER_NAME=$APP_CLUSTER
ROKSBNKCTL_CLUSTER_CREATE=false
ROKSBNKCTL_REGISTRY_TARGET=generic
ROKSBNKCTL_GENERIC_HOST=$REGISTRY_DOMAIN
ROKSBNKCTL_GENERIC_REPO_PREFIX=$PROJECT
ROKSBNKCTL_GENERIC_USERNAME=admin
ROKSBNKCTL_GENERIC_PASSWORD=$REGISTRY_ADMIN_PASSWORD
ROKSBNKCTL_LICENSE_MODE=f5licenseproxy
ROKSBNKCTL_FLP_EXTERNAL_URL=$FLP_URL
ROKSBNKCTL_FLP_ROOT_CA_B64=$FLP_CA
EOF
chmod 600 "$APP_ENV"
say "Job two's environment: the same registry, plus license-mode f5licenseproxy and the proxy it just received."
run grep -vE "PASSWORD|ROOT_CA" "$APP_ENV"

# ── 6) job 2: install BNK from the registry, licensed remotely ──────────────
stage "6/8" "Job two — install BIG-IP Next, licensed from next door"
run "${APP[@]}" init --non-interactive --override-from-env
run "${APP[@]}" cluster register "$APP_CLUSTER"
say "The same private registry. Everything is already in it, so this records the mirror rather than copying anything."
run "${APP[@]}" registry replicate --target generic
say "Now install. Every chart and every image comes from the private registry, and the cluster-wide controller licenses through the proxy in the other cluster."
run "${APP[@]}" bnk up --auto

# ── 7) the proof ────────────────────────────────────────────────────────────
stage "7/8" "Confirm the pipeline licensed the cluster"
say "The License resource: mode f5licenseproxy, state Active — issued through a proxy that is not in this cluster. A CI job would gate on exactly this."
run "${APP[@]}" k get license -n f5-utils
say "And the data plane is up, pulled entirely from the private registry."
run "${APP[@]}" k get pods -n f5-bnk
say "There is no License Proxy here at all. This cluster only ever talked to the registry and to the proxy next door."
run "${APP[@]}" k get pods -n f5-license-proxy || true

# ── 8) teardown ─────────────────────────────────────────────────────────────
stage "8/8" "Remove only what the pipeline installed"
say "Both clusters were registered, never created — so the pipeline removes only what it installed."
run "${APP[@]}" bnk down --auto
run "${SVC[@]}" flp down --auto
rm -f "$SVC_ENV" "$APP_ENV"

echo
printf '\033[1;32m✓ A CI pipeline installed BIG-IP Next from a private registry and licensed it through an F5 License Proxy in a different cluster — with nothing installed on the runner, and no config file anywhere.\033[0m\n'
