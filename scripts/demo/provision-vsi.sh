#!/usr/bin/env bash
#
# provision-vsi.sh — stand up (or tear down) a fresh Ubuntu 24.04 IBM Cloud VSI
# to act as the operator workstation for the roksbnkctl demo shoot.
#
# It creates a self-contained VPC so nothing else is touched, and records every
# resource id in vsi-state.env for a clean `down`.
#
#   ./provision-vsi.sh up     # create VPC + subnet + PGW + SG rule + key + VSI + FIP
#   ./provision-vsi.sh down   # destroy everything it created (reverse order)
#   ./provision-vsi.sh ssh    # open an SSH session to the VSI
#
# Inputs come from demo.env (written by prompt-inputs.sh): IBMCLOUD_API_KEY,
# REGION, RESOURCE_GROUP, CLUSTER_NAME. Optional overrides:
#   VSI_PROFILE (default bx2-2x8), VSI_ZONE (default ${REGION}-1), VSI_IMAGE.
#
# Requires on the host running this: ibmcloud CLI with the `is` (VPC) plugin,
# and jq. (Install: `ibmcloud plugin install vpc-infrastructure`.)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/demo.env}"
STATE_FILE="${STATE_FILE:-$SCRIPT_DIR/vsi-state.env}"

# shellcheck disable=SC1090
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE" || { echo "✗ $ENV_FILE not found — run ./prompt-inputs.sh first." >&2; exit 1; }

: "${IBMCLOUD_API_KEY:?set via demo.env}"
: "${REGION:?set via demo.env}"
: "${RESOURCE_GROUP:?set via demo.env}"
: "${CLUSTER_NAME:?set via demo.env}"

VSI_PROFILE="${VSI_PROFILE:-bx2-2x8}"
VSI_ZONE="${VSI_ZONE:-${REGION}-1}"
# IBM Cloud VPC stock images log in as root (not ubuntu, unlike AWS/Classic).
REMOTE_USER="${REMOTE_USER:-root}"
# A dedicated RSA keypair the script owns — RSA injects on every IBM Cloud VPC
# stock image (ed25519 only on some), so this "just works". Decoupled from any
# personal key in demo.env; the path is recorded in state for record.sh/ssh.
VSI_SSH_KEY="${VSI_SSH_KEY:-$SCRIPT_DIR/vsi_key}"

# Resource names — all prefixed so they're identifiable and teardown-safe.
P="${CLUSTER_NAME}-vsi"
VPC_NAME="${P}-vpc"; SUBNET_NAME="${P}-subnet"; PGW_NAME="${P}-pgw"
KEY_NAME="${P}-key"; FIP_NAME="${P}-fip"; VSI_NAME="${P}"

# ── helpers ──────────────────────────────────────────────────────────────────
need() { command -v "$1" >/dev/null 2>&1 || { echo "✗ '$1' is required on PATH." >&2; exit 1; }; }
log()  { printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }

# is <args...> — run an `ibmcloud is` command with JSON output.
is() { ibmcloud is "$@" --output JSON; }

# stage_key <path> — echo a path to a 0600 copy of the private key. WSL DrvFs
# mounts (/mnt/*) report 0777 and SSH refuses such keys, so mirror it onto the
# Linux filesystem where perms stick. Returns the original if already 0600.
stage_key() {
  local src="$1" dst="$HOME/.roksbnkctl-demo/vsi_key"
  [[ "$(stat -c '%a' "$src" 2>/dev/null)" == "600" ]] && { printf '%s' "$src"; return; }
  install -d -m700 "$(dirname "$dst")"
  install -m600 "$src" "$dst"
  printf '%s' "$dst"
}

# state_put KEY VALUE — append/update a var in the state file.
state_put() {
  touch "$STATE_FILE"; chmod 600 "$STATE_FILE"
  grep -v "^$1=" "$STATE_FILE" > "${STATE_FILE}.tmp" 2>/dev/null || true
  echo "$1=$2" >> "${STATE_FILE}.tmp"
  mv "${STATE_FILE}.tmp" "$STATE_FILE"
}

login() {
  log "Logging in to IBM Cloud (region $REGION, rg $RESOURCE_GROUP)…"
  ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$REGION" -g "$RESOURCE_GROUP" -q >/dev/null
  ok "Logged in."
}

ensure_ssh_key() {
  if [[ ! -f "$VSI_SSH_KEY" ]]; then
    log "Generating a dedicated RSA keypair for the demo VSI → $VSI_SSH_KEY"
    mkdir -p "$(dirname "$VSI_SSH_KEY")"
    ssh-keygen -t rsa -b 4096 -N '' -C "$VSI_NAME" -f "$VSI_SSH_KEY" >/dev/null
  fi
  [[ -f "${VSI_SSH_KEY}.pub" ]] || { echo "✗ public key ${VSI_SSH_KEY}.pub missing." >&2; exit 1; }
}

# ── up ───────────────────────────────────────────────────────────────────────
do_up() {
  need ibmcloud; need jq; ensure_ssh_key; login
  : > "$STATE_FILE"; chmod 600 "$STATE_FILE"

  log "Selecting Ubuntu 24.04 stock image…"
  local image_id="${VSI_IMAGE:-}"
  if [[ -z "$image_id" ]]; then
    image_id="$(is images --visibility public \
      | jq -r '[.[] | select(.status=="available")
                    | select(.operating_system.architecture=="amd64")
                    | select(.name|startswith("ibm-ubuntu-24-04"))]
               | sort_by(.name) | last | .id')"
  fi
  [[ -n "$image_id" && "$image_id" != "null" ]] || { echo "✗ no Ubuntu 24.04 image found." >&2; exit 1; }
  ok "Image: $image_id"

  log "Registering SSH public key ($KEY_NAME)…"
  local key_id="" fp existing_id existing_fp kc key_type
  fp="$(ssh-keygen -lf "${VSI_SSH_KEY}.pub" -E sha256 2>/dev/null | awk '{print $2}' | sed 's/^SHA256://')"
  # IBM Cloud VPC key-create defaults to --key-type rsa and rejects other types
  # unless told; derive the type from the public key's algorithm field.
  case "$(awk '{print $1}' "${VSI_SSH_KEY}.pub")" in
    ssh-ed25519) key_type=ed25519 ;;
    ssh-rsa)     key_type=rsa ;;
    *)           key_type=rsa ;;
  esac
  # If a key with this name already exists, reuse it only when its fingerprint
  # matches our public key; otherwise delete the stale one and re-register.
  existing_id="$(is keys | jq -r --arg n "$KEY_NAME" '.[]|select(.name==$n)|.id' | head -1)"
  if [[ -n "$existing_id" ]]; then
    existing_fp="$(is key "$existing_id" | jq -r '.fingerprint // empty')"
    if [[ -n "$fp" && "$existing_fp" == *"$fp"* ]]; then
      key_id="$existing_id"
    else
      log "Existing key $KEY_NAME has a different fingerprint — replacing it."
      ibmcloud is key-delete "$existing_id" -f >/dev/null 2>&1 || true
    fi
  fi
  if [[ -z "$key_id" ]]; then
    if ! kc="$(is key-create "$KEY_NAME" @"${VSI_SSH_KEY}.pub" --key-type "$key_type" --resource-group-name "$RESOURCE_GROUP" 2>&1)"; then
      echo "✗ key-create failed:" >&2; echo "$kc" >&2; exit 1
    fi
    key_id="$(jq -r '.id // .[0].id // empty' <<<"$kc" 2>/dev/null || true)"
    # fall back to a name lookup if the create output wasn't clean JSON
    [[ -z "$key_id" ]] && key_id="$(is keys | jq -r --arg n "$KEY_NAME" '.[]|select(.name==$n)|.id' | head -1)"
  fi
  if [[ -z "$key_id" ]]; then
    echo "✗ could not obtain an SSH key id for $KEY_NAME — refusing to create a keyless VSI." >&2
    exit 1
  fi
  state_put KEY_ID "$key_id"; state_put VSI_SSH_KEY "$VSI_SSH_KEY"
  ok "Key: $key_id (type $key_type, fp ${fp:-unknown})"

  log "Creating VPC ($VPC_NAME)…"
  local vpc_id sg_id
  local vpc_json; vpc_json="$(is vpc-create "$VPC_NAME" --resource-group-name "$RESOURCE_GROUP")"
  vpc_id="$(jq -r '.id' <<<"$vpc_json")"
  sg_id="$(jq -r '.default_security_group.id' <<<"$vpc_json")"
  state_put VPC_ID "$vpc_id"; ok "VPC: $vpc_id (default SG $sg_id)"

  log "Creating subnet ($SUBNET_NAME) in $VSI_ZONE…"
  local subnet_id
  subnet_id="$(is subnet-create "$SUBNET_NAME" "$vpc_id" --zone "$VSI_ZONE" \
      --ipv4-address-count 256 --resource-group-name "$RESOURCE_GROUP" | jq -r '.id')"
  state_put SUBNET_ID "$subnet_id"; ok "Subnet: $subnet_id"

  log "Creating public gateway ($PGW_NAME)…"
  local pgw_json pgw_id st
  if ! pgw_json="$(is public-gateway-create "$PGW_NAME" "$vpc_id" "$VSI_ZONE" \
        --resource-group-name "$RESOURCE_GROUP" 2>&1)"; then
    echo "✗ public-gateway-create failed:" >&2; echo "$pgw_json" >&2; exit 1
  fi
  pgw_id="$(jq -r '.id // .[0].id // empty' <<<"$pgw_json" 2>/dev/null || true)"
  if [[ -z "$pgw_id" ]]; then
    echo "✗ could not parse a public-gateway id from the create output. Raw output:" >&2
    echo "$pgw_json" >&2; exit 1
  fi
  state_put PGW_ID "$pgw_id"

  log "Waiting for public gateway $pgw_id to become available…"
  for i in $(seq 1 40); do
    st="$(is public-gateway "$pgw_id" | jq -r '.status // empty')"
    [[ "$st" == "available" ]] && break
    sleep 3
  done

  log "Attaching public gateway to subnet…"
  if ! ibmcloud is subnet-public-gateway-attach "$subnet_id" "$pgw_id" >/dev/null 2>&1; then
    # some CLI versions use subnet-update instead of the attach verb
    ibmcloud is subnet-update "$subnet_id" --pgw "$pgw_id" >/dev/null
  fi
  ok "PGW: $pgw_id"

  log "Allowing inbound SSH (tcp/22) on the VPC default security group…"
  ibmcloud is security-group-rule-add "$sg_id" inbound tcp \
    --port-min 22 --port-max 22 --remote 0.0.0.0/0 >/dev/null
  ok "SSH ingress rule added."

  log "Creating VSI ($VSI_NAME, $VSI_PROFILE)…"
  # IBM Cloud VPC *minimal* images inject SSH keys via cloud-init reading the
  # metadata service, which is DISABLED by default — the key gets attached but
  # never lands in root's authorized_keys. Enable it (--metadata-service true),
  # and also pass a cloud-config user-data that writes the key to root, which
  # only becomes readable once the metadata service is on. Belt and suspenders.
  local vsi_json vsi_id user_data
  user_data="$(printf '#cloud-config\nssh_authorized_keys:\n  - %s\n' "$(cat "${VSI_SSH_KEY}.pub")")"
  vsi_json="$(is instance-create "$VSI_NAME" "$vpc_id" "$VSI_ZONE" "$VSI_PROFILE" "$subnet_id" \
      --image "$image_id" --keys "$key_id" \
      --metadata-service true \
      --user-data "$user_data" \
      --resource-group-name "$RESOURCE_GROUP")"
  vsi_id="$(jq -r '.id' <<<"$vsi_json")"
  state_put VSI_ID "$vsi_id"; ok "VSI: $vsi_id"

  log "Waiting for the VSI to reach 'running'…"
  local status="" i
  for i in $(seq 1 60); do
    status="$(is instance "$vsi_id" | jq -r '.status')"
    [[ "$status" == "running" ]] && break
    sleep 5
  done
  [[ "$status" == "running" ]] || { echo "✗ VSI not running after timeout (status=$status)." >&2; exit 1; }
  ok "VSI is running."

  log "Reserving + associating a floating IP…"
  local fip_json fip_id fip_addr inst_json vni_id nic_id
  # Modern VSIs bind floating IPs to the primary network attachment's virtual
  # network interface (VNI); older ones use a legacy network interface (NIC).
  inst_json="$(is instance "$vsi_id")"
  vni_id="$(jq -r '.primary_network_attachment.virtual_network_interface.id // empty' <<<"$inst_json")"
  nic_id="$(jq -r '.primary_network_interface.id // empty' <<<"$inst_json")"
  if [[ -n "$vni_id" ]]; then
    fip_json="$(is floating-ip-reserve "$FIP_NAME" --vni "$vni_id" --resource-group-name "$RESOURCE_GROUP")"
  elif [[ -n "$nic_id" ]]; then
    fip_json="$(is floating-ip-reserve "$FIP_NAME" --nic "$nic_id" --in "$vsi_id" --resource-group-name "$RESOURCE_GROUP")"
  else
    echo "✗ no VNI or NIC found on the instance to bind a floating IP:" >&2
    jq '{primary_network_attachment, primary_network_interface}' <<<"$inst_json" >&2
    exit 1
  fi
  fip_id="$(jq -r '.id' <<<"$fip_json")"
  fip_addr="$(jq -r '.address' <<<"$fip_json")"
  state_put FIP_ID "$fip_id"; state_put FIP_ADDR "$fip_addr"
  ok "Floating IP: $fip_addr"

  local sshkey; sshkey="$(stage_key "$VSI_SSH_KEY")"
  log "Waiting for SSH on $fip_addr…"
  for i in $(seq 1 60); do
    if ssh -o BatchMode=yes -o StrictHostKeyChecking=no -o ConnectTimeout=5 \
         -i "$sshkey" "$REMOTE_USER@$fip_addr" true 2>/dev/null; then break; fi
    sleep 5
  done

  echo
  ok "VSI ready."
  printf '  Public IP : %s\n' "$fip_addr"
  printf '  SSH       : ssh -i %s %s@%s\n' "$sshkey" "$REMOTE_USER" "$fip_addr"
  printf '  State     : %s\n' "$STATE_FILE"
}

# ── down ─────────────────────────────────────────────────────────────────────
do_down() {
  need ibmcloud; need jq
  # shellcheck disable=SC1090
  [[ -f "$STATE_FILE" ]] && source "$STATE_FILE" || { echo "Nothing to tear down ($STATE_FILE missing)."; exit 0; }
  login

  if [[ -n "${VSI_ID:-}" ]]; then
    log "Deleting VSI…"; ibmcloud is instance-delete "$VSI_ID" -f >/dev/null 2>&1 || true
    for i in $(seq 1 60); do ibmcloud is instance "$VSI_ID" >/dev/null 2>&1 || break; sleep 5; done
  fi
  [[ -n "${FIP_ID:-}"    ]] && { log "Releasing floating IP…"; ibmcloud is floating-ip-release "$FIP_ID" -f >/dev/null 2>&1 || true; }
  [[ -n "${SUBNET_ID:-}" ]] && ibmcloud is subnet-public-gateway-detach "$SUBNET_ID" -f >/dev/null 2>&1 || true
  [[ -n "${PGW_ID:-}"    ]] && { log "Deleting public gateway…"; ibmcloud is public-gateway-delete "$PGW_ID" -f >/dev/null 2>&1 || true; }
  [[ -n "${SUBNET_ID:-}" ]] && { log "Deleting subnet…"; ibmcloud is subnet-delete "$SUBNET_ID" -f >/dev/null 2>&1 || true; }
  [[ -n "${VPC_ID:-}"    ]] && { log "Deleting VPC…"; ibmcloud is vpc-delete "$VPC_ID" -f >/dev/null 2>&1 || true; }
  [[ -n "${KEY_ID:-}"    ]] && ibmcloud is key-delete "$KEY_ID" -f >/dev/null 2>&1 || true

  rm -f "$STATE_FILE"
  ok "Teardown complete."
}

# ── ssh ──────────────────────────────────────────────────────────────────────
do_ssh() {
  # shellcheck disable=SC1090
  [[ -f "$STATE_FILE" ]] && source "$STATE_FILE" || { echo "✗ no VSI (run 'up' first)." >&2; exit 1; }
  : "${FIP_ADDR:?no floating IP in state}"
  local k; k="$(stage_key "$VSI_SSH_KEY")"
  exec ssh -o StrictHostKeyChecking=no -i "$k" "$REMOTE_USER@$FIP_ADDR"
}

case "${1:-up}" in
  up)   do_up ;;
  down) do_down ;;
  ssh)  do_ssh ;;
  *)    echo "usage: $0 {up|down|ssh}" >&2; exit 2 ;;
esac
