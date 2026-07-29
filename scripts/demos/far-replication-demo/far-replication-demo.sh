#!/usr/bin/env bash
#
# far-replication-demo.sh — Demo #3: replicating the F5 Artifact Repository (FAR)
# into a private OCI registry with roksbnkctl.
#
# Runs ON the demo VSI (copied there with demo.env + demo-lib.sh by record.sh)
# and is captured by asciinema, exactly like cli-demo.sh and ci-demo.sh.
#
# Story — an air-gapped cluster cannot reach repo.f5.com, so every chart and
# image a BNK install needs must first be copied into a registry you control:
#   seed a workspace → pull the FAR credential from COS → show the bill of
#   materials → point the workspace at the registry → diff → replicate → verify →
#   list.
#
# The private registry (a standard open-source Harbor — any OCI-compliant
# registry works) is stood up BEFORE recording, off-camera; how it is built is
# not part of this story. The demo just targets it. Provide its address and admin
# password in demo.env as REGISTRY_DOMAIN + REGISTRY_ADMIN_PASSWORD (scripts/
# deploy-far-registry.sh prints both when it provisions one).
#
# NOTE: no ROKS cluster is built. `registry replicate` reads the workspace, pulls
# from FAR and pushes to the target; it never talks to a cluster (the "needs a
# live cluster" line in `registry --help` is stale, from the removed openshift
# target). That keeps this shoot to a few minutes.
#
#   ./far-replication-demo.sh            # run the demonstration
#   ./far-replication-demo.sh teardown   # no-op: the registry is managed off-camera
#
# Inputs come from demo.env: IBMCLOUD_API_KEY, REGION, RESOURCE_GROUP,
# CLUSTER_NAME, BNK_VERSION, FAR_REPO_URL, REGISTRY_DOMAIN, REGISTRY_ADMIN_PASSWORD.
# The FAR pull credential is fetched from COS by roksbnkctl itself — not an input.
# Optional: COS_INSTANCE / COS_BUCKET / FAR_AUTH_FILE (COS coordinates),
# FAR_WORKSPACE (default <CLUSTER_NAME>-mirror), REGISTRY_PROJECT
# (default bnk-mirror), PACE / TYPE_MIN / TYPE_MAX / READ_DELAY / STAGE_HOLD.
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
: "${REGION:?}"; : "${RESOURCE_GROUP:?}"; : "${CLUSTER_NAME:?}"; : "${BNK_VERSION:?}"
: "${FAR_REPO_URL:?set via demo.env — the source registry to mirror FROM}"

# The FAR pull credential is NOT an input. roksbnkctl downloads bnk.far_auth_file
# from the orchestration COS bucket and extracts the service account itself
# (internal/cli/registry.go: resolveFARServiceAccount, called from buildBOM
# whenever registry.source_service_account_b64 is empty). These mirror the
# constants there; override in demo.env only if your COS layout differs.
COS_INSTANCE="${COS_INSTANCE:-bnk-orchestration}"
COS_BUCKET="${COS_BUCKET:-bnk-schematics-resources}"
FAR_AUTH_FILE="${FAR_AUTH_FILE:-f5-far-auth-key.tgz}"

WS="${FAR_WORKSPACE:-${CLUSTER_NAME}-mirror}"
PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"
export PATH="$HOME/.local/bin:$PATH"
SUDO=""; [[ "$(id -u)" -eq 0 ]] || SUDO="sudo"

# ── emergency teardown ───────────────────────────────────────────────────────
# The registry lives outside this demo (built and destroyed off-camera), so there
# is nothing here to tear down — the workspace is just local state.
if [[ "${1:-}" == "teardown" ]]; then
  rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
  echo "cleared local workspace '$WS'. The registry is managed outside this demo."
  exit 0
fi

: "${REGISTRY_DOMAIN:?set via demo.env — the private registry host (e.g. 1.2.3.4.sslip.io). Build one with scripts/demos/lib/deploy-far-registry.sh.}"
: "${REGISTRY_ADMIN_PASSWORD:?set via demo.env — the registry admin password (deploy-far-registry.sh prints it)}"

# ── 1) host preparation ──────────────────────────────────────────────────────
stage "1/10" "Preparing the demo host"
say "The only local dependency is helm, which roksbnkctl uses to pull the classic-Helm charts in the bill of materials."
export DEBIAN_FRONTEND=noninteractive
run $SUDO apt-get update -qq
run $SUDO apt-get install -y -qq curl jq tar
if ! command -v helm >/dev/null 2>&1; then
  helm_ver="$(curl -fsSL https://api.github.com/repos/helm/helm/releases/latest | jq -r .tag_name)"
  curl -fsSL "https://get.helm.sh/helm-${helm_ver}-linux-amd64.tar.gz" | tar -xz -C /tmp
  $SUDO install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm
fi
run helm version --short

# ── 2) install roksbnkctl ────────────────────────────────────────────────────
stage "2/10" "Install the roksbnkctl binary"
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

# ── 3) seed a workspace for the mirror ───────────────────────────────────────
stage "3/10" "Seed a workspace for the mirror"
say "The mirror needs the BNK version, the FAR repo, and the name of the auth tarball in COS. No credential is pasted in, and no cluster is involved."
rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
SEED="$HOME/${WS}-config.yaml"
{
  echo "ibmcloud:"
  echo "  region: $REGION"
  echo "  resource_group: $RESOURCE_GROUP"
  echo "prefix: $WS"
  echo "cluster:"
  echo "  create: true"
  echo "  name: $WS"
  echo "tf_source:"
  echo "  type: embedded"
  echo "bnk:"
  echo "  manifest_version: $BNK_VERSION"
  echo "  far_repo_url: $FAR_REPO_URL"
  echo "  far_auth_file: $FAR_AUTH_FILE"
} > "$SEED"
# Nothing secret in the seed: the FAR credential is named, not embedded.
run cat "$SEED"
run roksbnkctl -w "$WS" init --config-file "$SEED" --override-from-env

# ── 4) the FAR credential in COS ─────────────────────────────────────────────
stage "4/10" "Pull the FAR credential from COS"
say "The credential that unlocks repo.f5.com is an auth tarball in the orchestration COS bucket. roksbnkctl reads it straight from COS — it is never pasted into a config, and never leaves as a file."
say "The bucket is in us-south while the workspace is not; roksbnkctl resolves each bucket's own region."
run roksbnkctl -w "$WS" cos object list "$COS_BUCKET" --instance "$COS_INSTANCE"

# ── 5) the bill of materials ─────────────────────────────────────────────────
stage "5/10" "The bill of materials"
say "roksbnkctl pulls that auth tarball from COS to authenticate, reads the F5 manifest, and derives every chart and image a BNK install pulls."
run roksbnkctl -w "$WS" registry bom

# ── 6) point the workspace at the mirror ─────────────────────────────────────
stage "6/10" "Point the workspace at the mirror"
say "The target is a standard open-source Harbor — any OCI-compliant registry works; roksbnkctl does not care which. It is already running."
say "Four fields select it. The password is piped in on stdin so it never lands in argv or shell history."
run roksbnkctl -w "$WS" registry target generic
run roksbnkctl -w "$WS" registry target generic_host "$REGISTRY_DOMAIN"
run roksbnkctl -w "$WS" registry target generic_repo_prefix "$PROJECT"
run roksbnkctl -w "$WS" registry target generic_username admin
# type_cmd (not run) — the pipeline is executed below so the secret stays off screen.
type_cmd "printf '%s' \"\$REGISTRY_PASSWORD\" | roksbnkctl -w $WS registry target generic_password --password-stdin"
printf '%s' "$REGISTRY_ADMIN_PASSWORD" | roksbnkctl -w "$WS" registry target generic_password --password-stdin
run roksbnkctl -w "$WS" registry target

# ── 7) diff ──────────────────────────────────────────────────────────────────
stage "7/10" "What replication would copy"
say "diff compares the bill of materials against what the mirror already holds. The registry is empty, so everything is missing."
run roksbnkctl -w "$WS" registry diff

# ── 8) replicate ─────────────────────────────────────────────────────────────
stage "8/10" "Replicate FAR into the mirror"
say "Each artifact is copied by digest, straight from repo.f5.com into the registry. Artifacts already present are skipped."
run roksbnkctl -w "$WS" registry replicate --target generic

# ── 9) verify ────────────────────────────────────────────────────────────────
stage "9/10" "Verify every mirrored artifact"
say "verify re-reads the bill of materials and confirms each artifact is present in the mirror and digest-matched."
run roksbnkctl -w "$WS" registry verify

# ── 10) list ─────────────────────────────────────────────────────────────────
stage "10/10" "Browse the mirror"
say "Everything a BNK install needs now lives in a registry we control — charts and images alike. An air-gapped cluster installs from here."
run roksbnkctl -w "$WS" registry list

echo
printf '\033[1;32m✓ FAR mirrored into a private OCI registry and verified by digest — with one roksbnkctl workspace and no cluster.\033[0m\n'
