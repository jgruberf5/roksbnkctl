#!/usr/bin/env bash
#
# flp-licensed-demo.sh — Demo #4: an end-to-end air-gap-style install where BNK
# pulls every chart and image from a private Harbor and is licensed by an
# in-cluster F5 License Proxy (FLP). Runs ON the demo VSI (copied there with
# demo.env + demo-lib.sh by record.sh) and is captured by asciinema, exactly
# like the other screenplays.
#
# Story — start with nothing in IBM Cloud except a running Harbor (built
# off-camera, empty). One workspace declares the Harbor mirror, FLP licensing,
# and the cluster shape, then:
#   replicate FAR → Harbor → build the ROKS cluster → install the FLP (from
#   Harbor) → install BIG-IP Next for Kubernetes (from Harbor, licensed by the
#   FLP) → confirm the License CR is Active via the proxy → down everything.
#
# No artifact is ever pulled from repo.f5.com after replication, and no
# subscription call ever leaves the cluster — the FLP brokers licensing on the
# cluster's behalf. The closing `down` destroys the cluster ON CAMERA.
#
#   ./flp-licensed-demo.sh            # run the full demonstration
#   ./flp-licensed-demo.sh teardown   # emergency teardown (roksbnkctl down)
#
# Inputs come from demo.env (written by prompt-inputs.sh): IBMCLOUD_API_KEY,
# REGION, RESOURCE_GROUP, CLUSTER_NAME, OCP_VERSION, WORKERS_PER_ZONE,
# BNK_VERSION, FAR_REPO_URL, REGISTRY_DOMAIN, REGISTRY_ADMIN_PASSWORD.
# Optional: FLP_NAMESPACE (default f5-license-proxy), FLP_CHART_VERSION
# (default 1.29.0-0.10.28), REGISTRY_PROJECT (default bnk-mirror),
# COS_INSTANCE / COS_BUCKET / FAR_AUTH_FILE (COS coordinates for the FAR
# credential + subscription JWT), SUBSCRIPTION_JWT_FILE (default trial.jwt),
# PREFIX (default = CLUSTER_NAME), PACE.
#
# The FAR pull credential + subscription JWT are NOT inputs — roksbnkctl reads
# them straight from the orchestration COS bucket (see far-replication-demo.sh).
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
: "${REGION:?}"; : "${RESOURCE_GROUP:?}"; : "${CLUSTER_NAME:?}"
: "${OCP_VERSION:?}"; : "${WORKERS_PER_ZONE:?}"; : "${BNK_VERSION:?}"
: "${FAR_REPO_URL:?set via demo.env — the source registry to mirror FROM}"
: "${REGISTRY_DOMAIN:?set via demo.env — the private Harbor host (e.g. 1.2.3.4.sslip.io). Build one with scripts/deploy-far-registry.sh.}"
: "${REGISTRY_ADMIN_PASSWORD:?set via demo.env — the Harbor admin password (deploy-far-registry.sh prints it)}"

# The FAR credential + subscription JWT live in COS, not demo.env. roksbnkctl
# downloads them itself (see far-replication-demo.sh). Override only if the COS
# layout differs from the orchestration defaults.
COS_INSTANCE="${COS_INSTANCE:-bnk-orchestration}"
COS_BUCKET="${COS_BUCKET:-bnk-schematics-resources}"
FAR_AUTH_FILE="${FAR_AUTH_FILE:-f5-far-auth-key.tgz}"
SUBSCRIPTION_JWT_FILE="${SUBSCRIPTION_JWT_FILE:-trial.jwt}"

FLP_NAMESPACE="${FLP_NAMESPACE:-f5-license-proxy}"
FLP_CHART_VERSION="${FLP_CHART_VERSION:-1.29.0-0.10.28}"
PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"

WS="$CLUSTER_NAME"
PREFIX="${PREFIX:-$CLUSTER_NAME}"
export PATH="$HOME/.local/bin:$PATH"
SUDO=""; [[ "$(id -u)" -eq 0 ]] || SUDO="sudo"

# ── emergency teardown (a normal run ends with `down` on camera) ──────────────
# The FLP is a standalone phase (state-flp/) the composite `down` does not touch,
# and it must go before the cluster is destroyed, or its state is orphaned.
if [[ "${1:-}" == "teardown" ]]; then
  stage "T" "Destroying the workspace"
  roksbnkctl -w "$WS" flp down --auto || true
  roksbnkctl -w "$WS" down --auto || true
  echo; say "Teardown complete."
  exit 0
fi

# ── 1) host preparation ──────────────────────────────────────────────────────
stage "1/9" "Prepare the host"
say "roksbnkctl only needs terraform and helm installed locally — it internalises kubectl, oc, ibmcloud, and the mirror tooling."
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
run terraform version
if ! command -v helm >/dev/null 2>&1; then
  helm_ver="$(curl -fsSL https://api.github.com/repos/helm/helm/releases/latest | jq -r .tag_name)"
  curl -fsSL "https://get.helm.sh/helm-${helm_ver}-linux-amd64.tar.gz" | tar -xz -C /tmp
  $SUDO install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
fi
run helm version --short

# ── 2) install roksbnkctl ────────────────────────────────────────────────────
stage "2/9" "Install roksbnkctl"
if [[ -x "$SCRIPT_DIR/roksbnkctl.bin" ]]; then
  say "Installing a locally-built roksbnkctl (source build under test)."
  run install -D -m 0755 "$SCRIPT_DIR/roksbnkctl.bin" "$HOME/.local/bin/roksbnkctl"
else
  say "One self-contained release binary — no Go toolchain required."
  REL_API="https://api.github.com/repos/jgruberf5/roksbnkctl/releases/latest"
  DL_URL="$(curl -fsSL "$REL_API" | jq -r '.assets[].browser_download_url' \
            | grep -iE 'linux.*(amd64|x86_64).*\.tar\.gz$' | head -1)"
  [[ -n "$DL_URL" ]] || { echo "✗ could not resolve a linux/amd64 release asset." >&2; exit 1; }
  run curl -fsSL "$DL_URL" -o /tmp/roksbnkctl.tgz
  run tar -xzf /tmp/roksbnkctl.tgz -C /tmp
  RB="$(find /tmp -maxdepth 2 -type f -name roksbnkctl | head -1)"
  run "$RB" install
fi
hash -r
run roksbnkctl version 2>/dev/null || true

# ── 3) seed the workspace: Harbor mirror + FLP licensing ─────────────────────
stage "3/9" "Configure the workspace for Harbor and the FLP"
say "One declarative config describes the whole install: the cluster to build, the FAR repo to mirror, license_mode f5licenseproxy, and the FLP block. No secret is pasted in."
rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
SEED="$HOME/${WS}-config.yaml"
{
  echo "ibmcloud:"
  echo "  region: $REGION"
  echo "  resource_group: $RESOURCE_GROUP"
  echo "prefix: $PREFIX"
  echo "cluster:"
  echo "  create: true"
  echo "  name: $CLUSTER_NAME"
  echo "  openshift_version: \"$OCP_VERSION\""
  echo "  workers_per_zone: $WORKERS_PER_ZONE"
  # No transit gateway: it exists to give the TESTING phase's client VPC a path to
  # the cluster, and this demo never runs that phase. Skipping it drops a chunk of
  # build time and cost — and IBM Cloud caps an account at 10 transit gateways, so
  # a demo that does not need one should not burn a slot.
  echo "resources:"
  echo "  transit_gateway:"
  echo "    create: false"
  echo "tf_source:"
  echo "  type: embedded"
  echo "bnk:"
  echo "  manifest_version: $BNK_VERSION"
  echo "  far_repo_url: $FAR_REPO_URL"
  echo "  far_auth_file: $FAR_AUTH_FILE"
  echo "  subscription_jwt_file: $SUBSCRIPTION_JWT_FILE"
  echo "  license_mode: f5licenseproxy"
  echo "  flp:"
  echo "    namespace: $FLP_NAMESPACE"
  echo "    chart_version: $FLP_CHART_VERSION"
} > "$SEED"
run cat "$SEED"
run roksbnkctl -w "$WS" init --config-file "$SEED" --override-from-env

# Point the workspace at the Harbor mirror (its four fields; password on stdin).
say "Select the private Harbor as the mirror. The password is piped in on stdin, so it never lands in argv or shell history."
run roksbnkctl -w "$WS" registry target generic
run roksbnkctl -w "$WS" registry target generic_host "$REGISTRY_DOMAIN"
run roksbnkctl -w "$WS" registry target generic_repo_prefix "$PROJECT"
run roksbnkctl -w "$WS" registry target generic_username admin
type_cmd "printf '%s' \"\$REGISTRY_PASSWORD\" | roksbnkctl -w $WS registry target generic_password --password-stdin"
printf '%s' "$REGISTRY_ADMIN_PASSWORD" | roksbnkctl -w "$WS" registry target generic_password --password-stdin

# ── 4) replicate FAR into the Harbor mirror ──────────────────────────────────
stage "4/9" "Replicate FAR into the Harbor mirror"
say "roksbnkctl reads the F5 manifest, derives every chart and image the install needs — the FLP chart and its four images included — and copies each into Harbor by digest."
run roksbnkctl -w "$WS" registry replicate --target generic
say "verify re-reads the bill of materials and confirms each artifact is present in Harbor and digest-matched. From here nothing is pulled from repo.f5.com."
run roksbnkctl -w "$WS" registry verify

# ── 5) build the ROKS cluster ────────────────────────────────────────────────
stage "5/9" "Provision the ROKS cluster"
say "Build the cluster phase on its own. You could instead point roksbnkctl at an existing ROKS cluster's name."
run roksbnkctl -w "$WS" cluster up --auto

# ── 6) install the F5 License Proxy (from Harbor) ────────────────────────────
stage "6/9" "Install the F5 License Proxy"
say "The FLP phase deploys the in-cluster license proxy — proxy, vault, and postgresql — pulling every image from Harbor. It is optional: without license_mode f5licenseproxy this phase never runs."
run roksbnkctl -w "$WS" flp up --auto

# ── 7) install BIG-IP Next for Kubernetes (from Harbor, licensed by the FLP) ──
stage "7/9" "Deploy BIG-IP Next for Kubernetes from Harbor"
say "The BNK phase installs BIG-IP Next for Kubernetes from Harbor and points its licensing at the FLP. The cluster-wide controller trusts the proxy's certificate and brokers the license through it — no subscription call leaves the cluster directly."
run roksbnkctl -w "$WS" bnk up --auto

# ── 8) confirm the FLP-brokered license ──────────────────────────────────────
stage "8/9" "Confirm the FLP-brokered license"
say "The License custom resource shows mode f5licenseproxy and state Active — the proxy verified the entitlement and BIG-IP Next for Kubernetes is licensed. kubectl is internalised, so no host binary is needed."
run roksbnkctl -w "$WS" k get license -n f5-utils
run roksbnkctl -w "$WS" k get pods -n "$FLP_NAMESPACE"

# ── 9) remove every phase ────────────────────────────────────────────────────
stage "9/9" "Tear down every phase"
say "The FLP is a standalone phase, torn down first so the cluster destroy does not orphan its state. Then a single down removes BIG-IP Next for Kubernetes and the cluster itself."
run roksbnkctl -w "$WS" flp down --auto
run roksbnkctl -w "$WS" down --auto

echo
printf '\033[1;32m✓ BIG-IP Next for Kubernetes installed entirely from a private Harbor and licensed by an in-cluster F5 License Proxy — then removed with one down.\033[0m\n'
