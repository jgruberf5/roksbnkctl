#!/usr/bin/env bash
#
# prompt-inputs.sh — collect the inputs the roksbnkctl demo shoot needs.
#
# Prompts (each with an intelligent default) for:
#   1. IBM Cloud API key   (auto-detected from the environment if present)
#   2. SSH private key file (auto-detected from ~/.ssh)
#   3. BNK Forge URL
#   4. BNK Forge username
#   5. BNK Forge password  (hidden input)
#   6. Cluster shape        (region / resource group / name / OCP ver / workers)
#   7. BNK version
#   8. FAR repo URL
#   9. FAR service account (base64) — OPTIONAL override; demo #3 otherwise
#      fetches it from the orchestration COS bucket automatically
#  10. Mirror project name  — demo #3 only
#
# Defaults come from an existing workspace config.yaml when one exists
# (read via `yq` if installed), otherwise from roksbnkctl's built-in
# defaults as of v1.17.x. Press Enter at any prompt to accept the default.
#
# Output: writes the answers to demo.env (mode 0600) next to this script so
# provision-vsi.sh / the screenplays can `source` it. demo.env holds secrets — it is
# git-ignored below and should never be committed.
#
set -euo pipefail

# ── roksbnkctl built-in defaults (source of truth: internal/config/workspace.go,
#    internal/cli/init.go, terraform/variables.tf @ v1.17.x) ──────────────────
DEF_REGION="ca-tor"
DEF_RG="default"
DEF_CLUSTER_NAME="bnk-demo"
DEF_OCP="4.18"
DEF_WORKERS_PER_ZONE="1"     # ROKS spans 3 AZs → 1/zone = 3 workers total
DEF_BNK_VERSION="2.3.0-3.2598.3-0.0.170"
DEF_FAR_REPO_URL="repo.f5.com"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_ENV="${OUT_ENV:-$SCRIPT_DIR/demo.env}"

WS="${ROKSBNKCTL_WORKSPACE:-$DEF_CLUSTER_NAME}"
CONFIG_YAML="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS/config.yaml"

# ── helpers ──────────────────────────────────────────────────────────────────

# cfg <yq-path> <fallback> — read a value from the existing workspace
# config.yaml (if it exists and yq is available), else return the fallback.
cfg() {
  local path="$1" fallback="$2" val=""
  if [[ -f "$CONFIG_YAML" ]] && command -v yq >/dev/null 2>&1; then
    val="$(yq -r "${path} // \"\"" "$CONFIG_YAML" 2>/dev/null || true)"
  fi
  if [[ -n "$val" && "$val" != "null" ]]; then printf '%s' "$val"; else printf '%s' "$fallback"; fi
}

# ask <var> <prompt> <default> — read a visible answer, defaulting on empty.
ask() {
  local __var="$1" __prompt="$2" __default="${3:-}" __in=""
  if [[ -n "$__default" ]]; then
    read -r -p "$__prompt [$__default]: " __in </dev/tty
  else
    read -r -p "$__prompt: " __in </dev/tty
  fi
  printf -v "$__var" '%s' "${__in:-$__default}"
}

# ask_secret <var> <prompt> <default> — read a hidden answer; blank keeps default.
ask_secret() {
  local __var="$1" __prompt="$2" __default="${3:-}" __in=""
  local hint=""; [[ -n "$__default" ]] && hint=" [Enter to keep existing]"
  read -r -s -p "${__prompt}${hint}: " __in </dev/tty; echo >/dev/tty
  printf -v "$__var" '%s' "${__in:-$__default}"
}

echo "── roksbnkctl demo — input collection ─────────────────────────────────────"
if [[ -f "$CONFIG_YAML" ]]; then
  echo "Reading defaults from existing workspace: $CONFIG_YAML"
  command -v yq >/dev/null 2>&1 || echo "  (install 'yq' to auto-pull those values; using built-in defaults for now)"
else
  echo "No workspace config.yaml found — using roksbnkctl built-in defaults."
fi
echo

# 1) IBM Cloud API key — auto-detect from the environment, else prompt hidden.
API_KEY="${IBMCLOUD_API_KEY:-${IC_API_KEY:-${TF_VAR_ibmcloud_api_key:-}}}"
if [[ -n "$API_KEY" ]]; then
  echo "✓ IBM Cloud API key detected in environment (will reuse it)."
else
  ask_secret API_KEY "1) IBM Cloud API key (paste)" ""
  [[ -n "$API_KEY" ]] || { echo "  ✗ API key is required." >&2; exit 1; }
fi

# 2) SSH private key file — auto-detect a sensible default.
DEF_SSH=""
for k in "$HOME/.ssh/id_ed25519" "$HOME/.ssh/id_rsa"; do
  [[ -f "$k" ]] && { DEF_SSH="$k"; break; }
done
ask SSH_KEY_FILE "2) SSH private key file" "$DEF_SSH"
[[ -f "$SSH_KEY_FILE" ]] || echo "  ⚠ '$SSH_KEY_FILE' not found — provision-vsi.sh can generate one if you leave it."

# 3–5) BNK Forge connection.
ask        FORGE_URL  "3) BNK Forge URL"      "$(cfg '.bnkforge.url' '')"
ask        FORGE_USER "4) BNK Forge username" "$(cfg '.bnkforge.username' "${USER:-}")"
ask_secret FORGE_PASS "5) BNK Forge password" ""

# 6) Cluster shape.
echo "6) Cluster shape:"
ask REGION           "   • Region"                 "$(cfg '.ibmcloud.region' "$DEF_REGION")"
ask RESOURCE_GROUP   "   • Resource group"         "$(cfg '.ibmcloud.resource_group' "$DEF_RG")"
ask CLUSTER_NAME     "   • Cluster name"           "$(cfg '.cluster.name' "$DEF_CLUSTER_NAME")"
ask OCP_VERSION      "   • OpenShift version"       "$(cfg '.cluster.openshift_version' "$DEF_OCP")"
ask WORKERS_PER_ZONE "   • Workers per zone (×3 AZs)" "$(cfg '.cluster.workers_per_zone' "$DEF_WORKERS_PER_ZONE")"

# 7) BNK version.
ask BNK_VERSION "7) BNK version" "$(cfg '.bnk.manifest_version' "$DEF_BNK_VERSION")"

# 8) FAR repo URL.
ask FAR_REPO_URL "8) FAR repo URL" "$(cfg '.bnk.far_repo_url' "$DEF_FAR_REPO_URL")"

# 9) FAR pull credential — OPTIONAL. `registry` resolves it from the orchestration
#    COS bucket (bnk.far_auth_file) when registry.source_service_account_b64 is
#    empty, so leave this blank unless you need to override that. Blank is normal.
FAR_SA_EXISTING="$(cfg '.registry.source_service_account_b64' '')"
ask_secret FAR_SERVICE_ACCOUNT_B64 "9) FAR service-account b64 override (blank = fetch from COS, the normal case)" "$FAR_SA_EXISTING"

# 10) Harbor project the mirror lands in (far-replication-demo.sh).
ask REGISTRY_PROJECT "10) Mirror project name" "$(cfg '.registry.generic_repo_prefix' 'bnk-mirror')"

# ── write demo.env (secrets → mode 0600) ────────────────────────────────────
umask 077
{
  echo "# Generated by prompt-inputs.sh — contains secrets, do NOT commit."
  echo "export IBMCLOUD_API_KEY=$(printf '%q' "$API_KEY")"
  echo "export SSH_KEY_FILE=$(printf '%q' "$SSH_KEY_FILE")"
  echo "export FORGE_URL=$(printf '%q' "$FORGE_URL")"
  echo "export FORGE_USER=$(printf '%q' "$FORGE_USER")"
  echo "export FORGE_PASS=$(printf '%q' "$FORGE_PASS")"
  echo "export REGION=$(printf '%q' "$REGION")"
  echo "export RESOURCE_GROUP=$(printf '%q' "$RESOURCE_GROUP")"
  echo "export CLUSTER_NAME=$(printf '%q' "$CLUSTER_NAME")"
  echo "export OCP_VERSION=$(printf '%q' "$OCP_VERSION")"
  echo "export WORKERS_PER_ZONE=$(printf '%q' "$WORKERS_PER_ZONE")"
  echo "export BNK_VERSION=$(printf '%q' "$BNK_VERSION")"
  echo "export FAR_REPO_URL=$(printf '%q' "$FAR_REPO_URL")"
  echo "export FAR_SERVICE_ACCOUNT_B64=$(printf '%q' "$FAR_SERVICE_ACCOUNT_B64")"
  echo "export REGISTRY_PROJECT=$(printf '%q' "$REGISTRY_PROJECT")"
} > "$OUT_ENV"
chmod 600 "$OUT_ENV"

echo
echo "── Summary (secrets masked) ───────────────────────────────────────────────"
printf '  %-22s %s\n' "IBM Cloud API key:" "${API_KEY:+********}"
printf '  %-22s %s\n' "SSH key file:"      "$SSH_KEY_FILE"
printf '  %-22s %s\n' "Forge URL:"         "$FORGE_URL"
printf '  %-22s %s\n' "Forge username:"    "$FORGE_USER"
printf '  %-22s %s\n' "Forge password:"    "${FORGE_PASS:+********}"
printf '  %-22s %s\n' "Region:"            "$REGION"
printf '  %-22s %s\n' "Resource group:"    "$RESOURCE_GROUP"
printf '  %-22s %s\n' "Cluster name:"      "$CLUSTER_NAME"
printf '  %-22s %s\n' "OpenShift version:" "$OCP_VERSION"
printf '  %-22s %s\n' "Workers per zone:"  "$WORKERS_PER_ZONE"
printf '  %-22s %s\n' "BNK version:"       "$BNK_VERSION"
printf '  %-22s %s\n' "FAR repo URL:"      "$FAR_REPO_URL"
printf '  %-22s %s\n' "FAR service acct:"  "${FAR_SERVICE_ACCOUNT_B64:+********}${FAR_SERVICE_ACCOUNT_B64:-(from COS)}"
printf '  %-22s %s\n' "Mirror project:"    "$REGISTRY_PROJECT"
echo
echo "✓ Wrote $OUT_ENV (mode 0600). Source it with:  source $OUT_ENV"
