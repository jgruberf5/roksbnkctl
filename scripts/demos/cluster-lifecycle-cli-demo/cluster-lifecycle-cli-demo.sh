#!/usr/bin/env bash
#
# cli-demo.sh — Demo #1: the roksbnkctl CLI lifecycle. Runs ON the fresh Ubuntu
# 24.04 VSI (copied there with demo.env + demo-lib.sh by record.sh) and is
# captured by asciinema. Deterministic and re-runnable: `init` is seeded
# declaratively via --config-file (no interactive prompts).
#
#   ./cli-demo.sh            # run the full demonstration
#   ./cli-demo.sh teardown   # emergency teardown (roksbnkctl down) if a shoot aborts
#
# Story — drive each phase of the roksbnkctl lifecycle independently, to show the
# phases can be controlled on their own:
#   build cluster → register with BNK Forge → install BNK (+license) →
#   testing framework + tests → remove BNK → rebuild BNK (new version) →
#   `down` removes everything. (A full hands-off build would just be `roksbnkctl up`.)
# The closing `down` destroys the cluster ON CAMERA, so the demo self-cleans.
#
# Inputs come from demo.env (written by prompt-inputs.sh): IBMCLOUD_API_KEY,
# REGION, RESOURCE_GROUP, CLUSTER_NAME, OCP_VERSION, WORKERS_PER_ZONE,
# BNK_VERSION, FAR_REPO_URL, FORGE_URL, FORGE_USER, FORGE_PASS.
# Optional: FORGE_PROJECT, PACE, PREFIX (default = CLUSTER_NAME).
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
: "${FORGE_URL:?set via demo.env — the demo registers to a live BNK Forge}"
: "${FORGE_USER:?}"; : "${FORGE_PASS:?}"

WS="$CLUSTER_NAME"
PREFIX="${PREFIX:-$CLUSTER_NAME}"
export PATH="$HOME/.local/bin:$PATH"
SUDO=""; [[ "$(id -u)" -eq 0 ]] || SUDO="sudo"
FORGE_INSECURE="${FORGE_INSECURE:-true}"
export BNK_FORGE_PASSWORD="$FORGE_PASS"

# ── emergency teardown (a normal run ends with `down` on camera) ──────────────
if [[ "${1:-}" == "teardown" ]]; then
  stage "T" "Destroying the workspace"; roksbnkctl -w "$WS" down --auto || true
  echo; say "Teardown complete."
  exit 0
fi

# ── 1) system preparation ────────────────────────────────────────────────────
stage "1/10" "Preparing a fresh Ubuntu 24.04 host"
say "roksbnkctl only requires terraform and helm installed locally; it internalises kubectl/oc/ibmcloud/dig/iperf3."
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
stage "2/10" "Installing roksbnkctl"
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

# ── 3) seed the workspace config + init ──────────────────────────────────────
stage "3/10" "Configuring the workspace"
say "One declarative config.yaml is the whole input — seed it, no interactive interview."
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
  echo "tf_source:"
  echo "  type: embedded"
  echo "bnk:"
  echo "  manifest_version: $BNK_VERSION"
  [[ -n "${FAR_REPO_URL:-}" ]] && echo "  far_repo_url: $FAR_REPO_URL"
} > "$SEED"
run cat "$SEED"
run roksbnkctl -w "$WS" init --config-file "$SEED" --override-from-env

# ── 4) build the ROKS cluster ────────────────────────────────────────────────
stage "4/10" "Build the ROKS cluster"
say "A full hands-off build would just be 'roksbnkctl up'. Here we drive each phase on its own — starting with the cluster."
run roksbnkctl -w "$WS" cluster up --auto

# ── 5) register the cluster with BNK Forge (roksbnkctl bnkforge register) ─────
stage "5/10" "Register with BNK Forge"
say "Register the durable cluster with BNK Forge v3 over its REST API; password from the env, never the command line."
REG_ARGS=(-w "$WS" bnkforge register --url "$FORGE_URL" --username "$FORGE_USER")
[[ "$FORGE_INSECURE" == "true" ]] && REG_ARGS+=(--insecure)
[[ -n "${FORGE_PROJECT:-}" ]] && REG_ARGS+=(--project "$FORGE_PROJECT")
run roksbnkctl "${REG_ARGS[@]}"

# ── 6) install BIG-IP Next for Kubernetes (+ license) ────────────────────────
stage "6/10" "Install BIG-IP Next for Kubernetes"
say "The BNK phase deploys BIG-IP Next for Kubernetes and its trial license onto the cluster."
run roksbnkctl -w "$WS" bnk up --auto

# ── 7) build the testing framework + run tests ───────────────────────────────
stage "7/10" "Build the testing framework and run tests"
say "The testing phase stands up the jump host(s) for the connectivity / DNS / throughput probes; then we run them."
run roksbnkctl -w "$WS" testing up --auto
for h in ${TEST_HOSTS:-https://www.example.com}; do
  run roksbnkctl -w "$WS" test hosts add "$h"
done
run roksbnkctl -w "$WS" test

# ── 8) remove BIG-IP Next for Kubernetes (phase down; cluster stays up) ───────
stage "8/10" "Remove BIG-IP Next for Kubernetes"
say "Down just the BNK phase — the cluster and testing framework keep running."
run roksbnkctl -w "$WS" bnk down --auto

# ── 9) rebuild BIG-IP Next for Kubernetes (e.g. a new version) ────────────────
stage "9/10" "Rebuild BIG-IP Next for Kubernetes — new version"
say "Reinstall the BNK phase — for example a new version — on the same running cluster, no re-provisioning."
run roksbnkctl -w "$WS" bnk up --auto

# ── 10) remove everything with a single down ─────────────────────────────────
stage "10/10" "Remove all phases with one down"
say "A single 'down' removes any or all phases — including the cluster itself."
run roksbnkctl -w "$WS" down --auto

echo
printf '\033[1;32m✓ Back where we started — roksbnkctl drove every phase independently, then removed them all with one down.\033[0m\n'
