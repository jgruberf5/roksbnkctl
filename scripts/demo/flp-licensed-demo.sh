#!/usr/bin/env bash
#
# flp-licensed-demo.sh — Demo #4: a SHARED LICENSING CLUSTER.
#
# One cluster runs the F5 License Proxy and holds the only egress to F5. A second,
# air-gapped cluster installs BIG-IP Next for Kubernetes entirely from a private
# registry and licenses through that proxy — reaching neither repo.f5.com nor F5's
# licensing service.
#
#     ┌──────── services cluster (the only egress to F5) ───────┐
#     │  F5 License Proxy ──NodePort 30001──┐                   │
#     └─────────────────────────────────────┼───────────────────┘
#     ┌─────────────────────────────────────┼── app cluster ────┐
#     │  BNK + CNEInstance + CWC ───────────┘                   │
#     │      └── charts + images ── private registry (Harbor)   │
#     └────────────────────────────────────────────────────────┘
#
# Story:
#   1. replicate FAR into a private registry with roksbnkctl
#   2. deploy the FLP into the services cluster, exposed as a NodePort service
#   3. deploy BNK into the OTHER cluster — pulling from the private registry, and
#      licensing through the proxy in the first cluster
#
# BOTH ROKS CLUSTERS ARE ALREADY RUNNING and are `cluster register`ed, not created.
# Cluster creation is ~40 minutes of nothing to watch and is not what this demo is
# about; registering is instant and is a first-class roksbnkctl flow. The demo
# therefore never destroys them — teardown removes only the workloads it installed.
#
#   ./flp-licensed-demo.sh            # run the demonstration
#   ./flp-licensed-demo.sh teardown   # emergency: remove FLP + BNK (clusters stay)
#
# Inputs from demo.env: IBMCLOUD_API_KEY, REGION, RESOURCE_GROUP, BNK_VERSION,
# FAR_REPO_URL, REGISTRY_DOMAIN, REGISTRY_ADMIN_PASSWORD,
# SERVICES_CLUSTER (the running cluster that will host the FLP),
# APP_CLUSTER      (the running cluster that will get BNK),
# APP_CLUSTER_CIDR (the app cluster's subnet CIDR — opened to the FLP's NodePort).
# Optional: REGISTRY_PROJECT (default bnk-mirror), FLP_NAMESPACE, PACE.
#
# The "STAGE n/N · Title" cards (demo-lib.sh) double as chapter markers that
# postprocess.sh greps to align the EN/FR voiceover; titles must contain the
# narration keys in narration.*.txt.
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
: "${FAR_REPO_URL:?set via demo.env — the source registry to mirror FROM}"
: "${REGISTRY_DOMAIN:?set via demo.env — the private registry host}"
: "${REGISTRY_ADMIN_PASSWORD:?set via demo.env — its admin password}"
: "${SERVICES_CLUSTER:?set via demo.env — the RUNNING cluster that will host the FLP}"
: "${APP_CLUSTER:?set via demo.env — the RUNNING cluster that will get BNK}"
: "${APP_CLUSTER_CIDR:?set via demo.env — the app cluster CIDR, opened to the FLP NodePort}"

PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"
FLP_NAMESPACE="${FLP_NAMESPACE:-f5-license-proxy}"
SVC_WS="${SVC_WS:-services}"   # workspace for the FLP cluster
APP_WS="${APP_WS:-app}"        # workspace for the BNK cluster
export PATH="$HOME/.local/bin:$PATH"
SUDO=""; [[ "$(id -u)" -eq 0 ]] || SUDO="sudo"

# ── emergency teardown ───────────────────────────────────────────────────────
# Removes only what the demo installed. The two clusters were REGISTERED, never
# created, so roksbnkctl does not own them and must not destroy them.
if [[ "${1:-}" == "teardown" ]]; then
  stage "T" "Removing the workloads"
  roksbnkctl -w "$APP_WS" bnk down --auto || true
  roksbnkctl -w "$SVC_WS" flp down --auto || true
  echo; say "FLP + BNK removed. Both clusters are still running — the demo never owned them."
  exit 0
fi

# ── 1) host preparation ──────────────────────────────────────────────────────
stage "1/8" "Prepare the host"
say "roksbnkctl needs only terraform and helm locally — it internalises kubectl, oc, ibmcloud and the mirror tooling."
export DEBIAN_FRONTEND=noninteractive
run $SUDO apt-get update -qq
run $SUDO apt-get install -y -qq curl jq tar gnupg lsb-release
if ! command -v terraform >/dev/null 2>&1; then
  curl -fsSL https://apt.releases.hashicorp.com/gpg | $SUDO gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
    | $SUDO tee /etc/apt/sources.list.d/hashicorp.list >/dev/null
  run $SUDO apt-get update -qq
  run $SUDO apt-get install -y -qq terraform
fi
if ! command -v helm >/dev/null 2>&1; then
  helm_ver="$(curl -fsSL https://api.github.com/repos/helm/helm/releases/latest | jq -r .tag_name)"
  curl -fsSL "https://get.helm.sh/helm-${helm_ver}-linux-amd64.tar.gz" | tar -xz -C /tmp
  $SUDO install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
fi
if [[ -x "$SCRIPT_DIR/roksbnkctl.bin" ]]; then
  run install -D -m 0755 "$SCRIPT_DIR/roksbnkctl.bin" "$HOME/.local/bin/roksbnkctl"
else
  REL_API="https://api.github.com/repos/jgruberf5/roksbnkctl/releases/latest"
  DL_URL="$(curl -fsSL "$REL_API" | jq -r '.assets[].browser_download_url' | grep -iE 'linux.*(amd64|x86_64).*\.tar\.gz$' | head -1)"
  curl -fsSL "$DL_URL" -o /tmp/roksbnkctl.tgz && tar -xzf /tmp/roksbnkctl.tgz -C /tmp
  run "$(find /tmp -maxdepth 2 -type f -name roksbnkctl | head -1)" install
fi
hash -r
run roksbnkctl version

# ── 2) register the services cluster (already running) ───────────────────────
stage "2/8" "Register the services cluster"
say "Both ROKS clusters are already running. roksbnkctl does not have to build them — it can adopt a cluster you already own."
rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$SVC_WS"
SVC_SEED="$HOME/${SVC_WS}-config.yaml"
{
  echo "ibmcloud:"
  echo "  region: $REGION"
  echo "  resource_group: $RESOURCE_GROUP"
  echo "prefix: $SVC_WS"
  echo "cluster:"
  echo "  create: false"          # ← adopt, do not build
  echo "  name: $SERVICES_CLUSTER"
  echo "tf_source:"
  echo "  type: embedded"
  echo "bnk:"
  echo "  manifest_version: $BNK_VERSION"
  echo "  far_repo_url: $FAR_REPO_URL"
  echo "  flp:"
  echo "    namespace: $FLP_NAMESPACE"
} > "$SVC_SEED"
run cat "$SVC_SEED"
run roksbnkctl -w "$SVC_WS" init --config-file "$SVC_SEED" --override-from-env
say "Register looks the cluster up in IBM Cloud and records its identity. Nothing is provisioned."
run roksbnkctl -w "$SVC_WS" cluster register "$SERVICES_CLUSTER"

# ── 3) replicate FAR into the private registry ───────────────────────────────
stage "3/8" "Replicate FAR into the private registry"
say "Point the workspace at the private registry. The password is piped in on stdin, so it never lands in argv or shell history."
run roksbnkctl -w "$SVC_WS" registry target generic
run roksbnkctl -w "$SVC_WS" registry target generic_host "$REGISTRY_DOMAIN"
run roksbnkctl -w "$SVC_WS" registry target generic_repo_prefix "$PROJECT"
run roksbnkctl -w "$SVC_WS" registry target generic_username admin
type_cmd "printf '%s' \"\$REGISTRY_PASSWORD\" | roksbnkctl -w $SVC_WS registry target generic_password --password-stdin"
printf '%s' "$REGISTRY_ADMIN_PASSWORD" | roksbnkctl -w "$SVC_WS" registry target generic_password --password-stdin

say "roksbnkctl reads the F5 manifest and derives every chart and image the install needs — the License Proxy among them."
run roksbnkctl -w "$SVC_WS" registry bom
say "Each artifact is copied from the F5 Artifact Repository into the registry by digest. From here, nothing is pulled from F5."
run roksbnkctl -w "$SVC_WS" registry replicate --target generic
run roksbnkctl -w "$SVC_WS" registry verify

# ── 4) the F5 License Proxy, exposed as a NodePort service ───────────────────
stage "4/8" "Deploy the F5 License Proxy with NodePort access"
say "The proxy is a cluster-wide licensing broker, and it is the only thing that needs to reach F5. Deploy it once, here."
say "--add-node-port-access exposes it beyond this cluster: it puts the worker node IPs in the proxy's certificate, and opens the NodePort to the other cluster's CIDR."
run roksbnkctl -w "$SVC_WS" flp up --auto \
    --add-node-port-access --node-port-source-cidr "$APP_CLUSTER_CIDR"
say "The proxy answers on every worker node, and every one of those addresses is in its certificate."
run roksbnkctl -w "$SVC_WS" k get svc f5-license-proxy -n "$FLP_NAMESPACE"
run roksbnkctl -w "$SVC_WS" flp output

# ── 5) register the application cluster ──────────────────────────────────────
stage "5/8" "Register the application cluster"
say "A second workspace, for the second cluster — also already running, also just registered."
# The handoff between the two workspaces: the proxy's external address + its root CA.
FLP_URL="$(roksbnkctl -w "$SVC_WS" flp output flp_external_endpoint)"
FLP_CA="$(roksbnkctl -w "$SVC_WS" flp output flp_root_ca)"
rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$APP_WS"
APP_SEED="$HOME/${APP_WS}-config.yaml"
{
  echo "ibmcloud:"
  echo "  region: $REGION"
  echo "  resource_group: $RESOURCE_GROUP"
  echo "prefix: $APP_WS"
  echo "cluster:"
  echo "  create: false"
  echo "  name: $APP_CLUSTER"
  echo "tf_source:"
  echo "  type: embedded"
  echo "bnk:"
  echo "  manifest_version: $BNK_VERSION"
  echo "  far_repo_url: $FAR_REPO_URL"
  echo "  license_mode: f5licenseproxy"
  echo "  flp:"
  echo "    external:"
  echo "      url: $FLP_URL"
  echo "      root_ca_b64: $FLP_CA"
} > "$APP_SEED"
say "license_mode is f5licenseproxy, and flp.external points at the proxy in the OTHER cluster — its address and its root CA. This cluster never runs an FLP of its own."
run cat "$APP_SEED"
run roksbnkctl -w "$APP_WS" init --config-file "$APP_SEED" --override-from-env
run roksbnkctl -w "$APP_WS" cluster register "$APP_CLUSTER"

say "The same private registry — already populated, so there is nothing left to copy."
run roksbnkctl -w "$APP_WS" registry target generic
run roksbnkctl -w "$APP_WS" registry target generic_host "$REGISTRY_DOMAIN"
run roksbnkctl -w "$APP_WS" registry target generic_repo_prefix "$PROJECT"
run roksbnkctl -w "$APP_WS" registry target generic_username admin
printf '%s' "$REGISTRY_ADMIN_PASSWORD" | roksbnkctl -w "$APP_WS" registry target generic_password --password-stdin
run roksbnkctl -w "$APP_WS" registry replicate --target generic

# ── 6) install BNK from the registry, licensed by the remote proxy ───────────
stage "6/8" "Deploy BIG-IP Next from the registry"
say "Every chart and image comes from the private registry, and the CNEInstance is told to pull from it too. The cluster-wide controller licenses through the proxy in the other cluster."
run roksbnkctl -w "$APP_WS" bnk up --auto

# ── 7) confirm ───────────────────────────────────────────────────────────────
stage "7/8" "Confirm the remote FLP licensed the cluster"
say "The License custom resource: mode f5licenseproxy, state Active — licensed by a proxy that is not even in this cluster."
run roksbnkctl -w "$APP_WS" k get license -n f5-utils
say "The CNEInstance is reconciled and the dataplane is up."
run roksbnkctl -w "$APP_WS" k get cneinstance -A
run roksbnkctl -w "$APP_WS" k get pods -n f5-bnk
say "And there is no License Proxy here at all — this cluster only ever talked to the registry and to the proxy next door."
run roksbnkctl -w "$APP_WS" k get pods -n "$FLP_NAMESPACE"

# ── 8) teardown (the clusters are NOT ours to destroy) ───────────────────────
stage "8/8" "Remove the workloads, leave the clusters"
say "Both clusters were registered, never created — so roksbnkctl removes only what it installed, and leaves them running."
run roksbnkctl -w "$APP_WS" bnk down --auto
run roksbnkctl -w "$SVC_WS" flp down --auto

echo
printf '\033[1;32m✓ BIG-IP Next installed from a private registry and licensed by an F5 License Proxy in a DIFFERENT cluster — neither cluster reached repo.f5.com, and only one of them can reach F5 at all.\033[0m\n'
