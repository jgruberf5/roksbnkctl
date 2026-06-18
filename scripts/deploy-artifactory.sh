#!/usr/bin/env bash
#
# deploy-artifactory.sh — stand up a single-VSI Artifactory in the cluster VPC.
#
# Provisions an Ubuntu Virtual Server Instance in AZ1 of the same VPC as a
# roksbnkctl workspace's ROKS cluster, attaches a Floating IP, and uses
# cloud-init to install Docker + run JFrog Artifactory OSS via docker compose.
# The web UI (port 8082) and SSH (22) are opened on the FIP. This gives a real
# OCI registry to point `roksbnkctl registry replicate --target generic` at.
#
# Usage:
#   scripts/deploy-artifactory.sh <workspace>            # deploy
#   scripts/deploy-artifactory.sh <workspace> --destroy  # tear it down
#
# Requires: ibmcloud CLI + the `vpc-infrastructure` (is) plugin, jq, roksbnkctl.
# Auth: reuses an existing `ibmcloud login` if present; otherwise logs in with
# the WORKSPACE's own IBM Cloud credential, resolved like roksbnkctl: $PWD/.env,
# then config.yaml api_key_b64, then the OS keychain. No IBMCLOUD_API_KEY needs to
# be exported (it's honored as an override if set).
# Inputs come from ~/.roksbnkctl/<workspace>/cluster-outputs.json (region,
# resource group, VPC, subnets) — so `roksbnkctl cluster up` must have run.
#
# Tunables (env): PROFILE (default cx2-4x8 = 4 vCPU / 8 GB), IMAGE (auto-select
# newest Ubuntu LTS amd64 if unset), ARTIFACTORY_IMAGE.
set -euo pipefail

usage() {
  cat <<'USAGE'
deploy-artifactory.sh — stand up a single-VSI Artifactory in the cluster VPC

Usage:
  scripts/deploy-artifactory.sh <workspace>            deploy
  scripts/deploy-artifactory.sh <workspace> --destroy  tear it down
  scripts/deploy-artifactory.sh --help

Provisions an Ubuntu VSI in AZ1 of the workspace's cluster VPC, attaches a
Floating IP, and runs JFrog Artifactory OSS (Docker, via cloud-init). Opens SSH
(22) and the Artifactory web UI (8081/8082) on the FIP — a private OCI registry
to point `roksbnkctl registry replicate --target generic` at.

Arguments:
  <workspace>   a roksbnkctl workspace with a cluster — validated against
                `roksbnkctl ws list`. Inputs (region, resource group, VPC,
                subnets) come from ~/.roksbnkctl/<workspace>/cluster-outputs.json.

Options:
  --destroy     delete the VSI, Floating IP, security group, and SSH key
  -h, --help    show this help

Environment:
  IBMCLOUD_API_KEY   override the workspace credential (optional — by default the
                     key is resolved from the workspace, or an existing login)
  PROFILE            VSI profile (default: cx2-4x8 = 4 vCPU / 8 GB)
  IMAGE              image id (default: newest available Ubuntu LTS amd64)
  ARTIFACTORY_IMAGE  Artifactory image (default: jfrog/artifactory-oss:latest)

Requires: ibmcloud CLI + the `vpc-infrastructure` (is) plugin, jq, roksbnkctl.
USAGE
}

case "${1:-}" in
  -h|--help) usage; exit 0 ;;
  "")        usage >&2; exit 2 ;;
esac

WS="$1"
ACTION="${2:-deploy}"
[[ "$ACTION" == "--destroy" ]] && ACTION="destroy"

PROFILE="${PROFILE:-cx2-4x8}"                                  # 4 vCPU, 8 GB RAM
ARTIFACTORY_IMAGE="${ARTIFACTORY_IMAGE:-releases-docker.jfrog.io/jfrog/artifactory-oss:latest}"
HOME_DIR="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
OUTPUTS="$HOME_DIR/$WS/cluster-outputs.json"

# Deterministic, workspace-scoped resource names (so --destroy can find them).
PREFIX="${WS}-artifactory"
VSI_NAME="$PREFIX"
SG_NAME="$PREFIX-sg"
KEY_NAME="$PREFIX-key"
FIP_NAME="$PREFIX-fip"

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 2; }; }
need ibmcloud; need jq; need roksbnkctl

# Validate the workspace exists per `roksbnkctl ws list` (first column = NAME,
# skipping the header row) before touching anything.
if ! roksbnkctl ws list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qxF "$WS"; then
  echo "workspace '$WS' not found. Known workspaces:" >&2
  roksbnkctl ws list >&2 || true
  exit 1
fi

[[ -f "$OUTPUTS" ]] || { echo "no cluster outputs at $OUTPUTS — run 'roksbnkctl -w $WS cluster up' first" >&2; exit 1; }
REGION=$(jq -r '.region' "$OUTPUTS")
RG=$(jq -r '.resource_group_id' "$OUTPUTS")
VPC=$(jq -r '.vpc_id' "$OUTPUTS")
ZONE="${REGION}-1"   # AZ1
[[ -n "$REGION" && -n "$VPC" && -n "$RG" ]] || { echo "cluster-outputs.json is missing region/vpc/resource_group_id" >&2; exit 1; }

# resolve_apikey resolves the workspace's IBM Cloud API key the way roksbnkctl
# does — so no IBMCLOUD_API_KEY needs to be exported. Order:
#   1. IBMCLOUD_API_KEY already in the env (explicit override)
#   2. $PWD/.env  — roksbnkctl loads it at startup (godotenv); the key is
#      commonly kept there. We read the key out without executing the file.
#   3. workspace config.yaml  ibmcloud.api_key_b64  (base64-decoded)
#   4. OS keychain  (service=roksbnkctl, account=<ws>/ibmcloud_api_key) best-effort
resolve_apikey() {
  if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then printf '%s' "$IBMCLOUD_API_KEY"; return 0; fi

  if [[ -f .env ]]; then
    local name line val
    for name in IBMCLOUD_API_KEY IC_API_KEY TF_VAR_ibmcloud_api_key TF_VAR_IBMCLOUD_API_KEY TF_VAR_IC_API_KEY; do
      line=$(grep -E "^[[:space:]]*(export[[:space:]]+)?${name}[[:space:]]*=" .env 2>/dev/null | tail -1) || true
      [[ -z "$line" ]] && continue
      val=${line#*=}; val=${val%$'\r'}                                  # after '=', strip CR
      val=$(printf '%s' "$val" | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')  # trim
      val=${val#\"}; val=${val%\"}; val=${val#\'}; val=${val%\'}        # strip quotes
      [[ -n "$val" ]] && { printf '%s' "$val"; return 0; }
    done
  fi

  local cfg="$HOME_DIR/$WS/config.yaml" b64
  if [[ -f "$cfg" ]]; then
    b64=$(grep -E '^[[:space:]]*api_key_b64:' "$cfg" | head -1 | sed -E 's/^[^:]*:[[:space:]]*"?([^"#[:space:]]+)"?.*/\1/')
    if [[ -n "$b64" ]]; then printf '%s' "$b64" | base64 -d 2>/dev/null && return 0; fi
  fi

  local acct="$WS/ibmcloud_api_key" k=""
  if command -v security >/dev/null 2>&1; then        # macOS keychain
    k=$(security find-generic-password -s roksbnkctl -a "$acct" -w 2>/dev/null) || true
  elif command -v secret-tool >/dev/null 2>&1; then   # Linux libsecret
    k=$(secret-tool lookup service roksbnkctl username "$acct" 2>/dev/null) || true
  fi
  [[ -n "$k" ]] && { printf '%s' "$k"; return 0; }
  return 1
}

# ── auth + region ───────────────────────────────────────────────────────────
# Reuse an existing `ibmcloud` session if there is one (`is regions` only works
# when logged in); otherwise log in with the workspace's own credential.
if ! ibmcloud is regions >/dev/null 2>&1; then
  if api_key=$(resolve_apikey); then
    echo "==> logging in with workspace '$WS' credential -r $REGION"
    ibmcloud login --apikey "$api_key" -r "$REGION" >/dev/null
    unset api_key
  else
    echo "not logged in, and no credential found for workspace '$WS' (config.yaml api_key_b64 / keychain)." >&2
    echo "  run 'ibmcloud login', or set IBMCLOUD_API_KEY, or re-run 'roksbnkctl -w $WS init'." >&2
    exit 1
  fi
fi
ibmcloud target -r "$REGION" >/dev/null

# Find a resource by name within a list command; echoes its id or "".
id_of() { ibmcloud is "$1" --output json 2>/dev/null | jq -r --arg n "$2" '.[]|select(.name==$n)|.id' | head -1; }

# ── teardown ────────────────────────────────────────────────────────────────
if [[ "$ACTION" == "destroy" ]]; then
  fip=$(id_of floating-ips "$FIP_NAME"); [[ -n "$fip" ]] && { echo "==> releasing FIP $FIP_NAME"; ibmcloud is floating-ip-release "$fip" -f >/dev/null; }
  vsi=$(id_of instances "$VSI_NAME"); [[ -n "$vsi" ]] && { echo "==> deleting VSI $VSI_NAME"; ibmcloud is instance-delete "$vsi" -f >/dev/null; }
  # SG + key can only go once the instance releases them — poll briefly.
  for _ in $(seq 1 30); do [[ -z "$(id_of instances "$VSI_NAME")" ]] && break; sleep 5; done
  sg=$(id_of security-groups "$SG_NAME"); [[ -n "$sg" ]] && { echo "==> deleting SG $SG_NAME"; ibmcloud is security-group-delete "$sg" -f >/dev/null 2>&1 || echo "  (SG still in use; retry shortly)"; }
  key=$(id_of keys "$KEY_NAME"); [[ -n "$key" ]] && { echo "==> deleting SSH key $KEY_NAME"; ibmcloud is key-delete "$key" -f >/dev/null; }
  echo "==> teardown complete for workspace '$WS'"
  exit 0
fi

echo "==> workspace=$WS region=$REGION zone=$ZONE vpc=$VPC profile=$PROFILE"

# ── AZ1 subnet in the cluster VPC ───────────────────────────────────────────
SUBNET=$(ibmcloud is subnets --output json | jq -r --arg vpc "$VPC" --arg z "$ZONE" \
  '.[]|select(.vpc.id==$vpc)|select(.zone.name==$z)|.id' | head -1)
[[ -n "$SUBNET" ]] || { echo "no subnet found in VPC $VPC zone $ZONE" >&2; exit 1; }
echo "==> AZ1 subnet: $SUBNET"

# ── SSH key (generate + upload, reuse if present) ───────────────────────────
KEY_DIR="$HOME_DIR/$WS/artifactory"
mkdir -p "$KEY_DIR"
KEY_FILE="$KEY_DIR/id_ed25519"
[[ -f "$KEY_FILE" ]] || ssh-keygen -t ed25519 -N "" -C "$KEY_NAME" -f "$KEY_FILE" >/dev/null
KEY_ID=$(id_of keys "$KEY_NAME")
if [[ -z "$KEY_ID" ]]; then
  echo "==> uploading SSH key $KEY_NAME"
  KEY_ID=$(ibmcloud is key-create "$KEY_NAME" "@$KEY_FILE.pub" --resource-group-id "$RG" --output json | jq -r '.id')
fi

# ── security group (SSH + Artifactory UI) ───────────────────────────────────
SG_ID=$(id_of security-groups "$SG_NAME")
if [[ -z "$SG_ID" ]]; then
  echo "==> creating security group $SG_NAME (22, 8081, 8082 in; all out)"
  SG_ID=$(ibmcloud is security-group-create "$SG_NAME" "$VPC" --resource-group-id "$RG" --output json | jq -r '.id')
  for p in 22 8081 8082; do
    ibmcloud is security-group-rule-add "$SG_ID" inbound tcp --port-min "$p" --port-max "$p" --remote 0.0.0.0/0 >/dev/null
  done
  ibmcloud is security-group-rule-add "$SG_ID" outbound all --remote 0.0.0.0/0 >/dev/null
fi

# ── Ubuntu image (newest available LTS amd64, override with IMAGE) ───────────
IMAGE_ID="${IMAGE:-}"
if [[ -z "$IMAGE_ID" ]]; then
  IMAGE_ID=$(ibmcloud is images --output json | jq -r '
    [ .[] | select(.status=="available")
          | select(.operating_system.architecture=="amd64")
          | select(.operating_system.name|ascii_downcase|test("ubuntu"))
          | select(.operating_system.version|test("24.04|22.04")) ]
    | sort_by(.operating_system.version, .created_at) | last | .id')
fi
[[ -n "$IMAGE_ID" && "$IMAGE_ID" != "null" ]] || { echo "could not resolve an Ubuntu image; set IMAGE=<id>" >&2; exit 1; }
echo "==> image: $IMAGE_ID"

# ── cloud-init: Docker + Artifactory OSS via docker compose ─────────────────
CLOUD_INIT="$(mktemp)"; trap 'rm -f "$CLOUD_INIT"' EXIT
cat >"$CLOUD_INIT" <<CLOUDINIT
#!/usr/bin/env bash
set -eux
export DEBIAN_FRONTEND=noninteractive
sysctl -w vm.max_map_count=262144 || true
echo "vm.max_map_count=262144" >> /etc/sysctl.conf
apt-get update -y
apt-get install -y ca-certificates curl gnupg
install -m0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
. /etc/os-release
echo "deb [arch=\$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu \${VERSION_CODENAME} stable" > /etc/apt/sources.list.d/docker.list
apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
systemctl enable --now docker
usermod -aG docker ubuntu || true
# Artifactory writes as uid 1030 (the 'artifactory' user inside the image).
mkdir -p /opt/artifactory/data
chown -R 1030:1030 /opt/artifactory/data
cat >/opt/artifactory/docker-compose.yml <<'YAML'
services:
  artifactory:
    image: ${ARTIFACTORY_IMAGE}
    container_name: artifactory
    ports:
      - "8081:8081"
      - "8082:8082"
    volumes:
      - /opt/artifactory/data:/var/opt/jfrog/artifactory
    ulimits:
      nofile:
        soft: 32000
        hard: 40000
    restart: unless-stopped
YAML
cd /opt/artifactory
docker compose up -d
CLOUDINIT

# ── create the VSI ──────────────────────────────────────────────────────────
VSI_ID=$(id_of instances "$VSI_NAME")
if [[ -z "$VSI_ID" ]]; then
  echo "==> creating VSI $VSI_NAME ($PROFILE)"
  VSI_ID=$(ibmcloud is instance-create "$VSI_NAME" "$VPC" "$ZONE" "$PROFILE" "$SUBNET" \
    --image "$IMAGE_ID" --keys "$KEY_ID" --resource-group-id "$RG" \
    --user-data "@$CLOUD_INIT" --output json | jq -r '.id')
else
  echo "==> VSI $VSI_NAME already exists ($VSI_ID)"
fi

# Attach our SG to the primary NIC (additive to the VPC default SG).
NIC_ID=$(ibmcloud is instance "$VSI_ID" --output json | jq -r '.primary_network_interface.id')
PRIV_IP=$(ibmcloud is instance "$VSI_ID" --output json | jq -r '.primary_network_interface.primary_ip.address // .primary_network_interface.primary_ipv4_address')
ibmcloud is security-group-network-interface-add "$SG_ID" "$NIC_ID" >/dev/null 2>&1 || true

# ── floating IP ─────────────────────────────────────────────────────────────
FIP_ID=$(id_of floating-ips "$FIP_NAME")
if [[ -z "$FIP_ID" ]]; then
  echo "==> reserving + binding floating IP $FIP_NAME"
  FIP_ADDR=$(ibmcloud is floating-ip-reserve "$FIP_NAME" --nic "$NIC_ID" --resource-group-id "$RG" --output json | jq -r '.address')
else
  FIP_ADDR=$(ibmcloud is floating-ip "$FIP_ID" --output json | jq -r '.address')
fi

cat <<DONE

==> Artifactory VSI deployed for workspace '$WS'
    VSI:        $VSI_NAME ($PROFILE, $ZONE)  private IP $PRIV_IP
    Floating IP: $FIP_ADDR
    SSH:        ssh -i $KEY_FILE ubuntu@$FIP_ADDR
    Web UI:     http://$FIP_ADDR:8082/ui/   (default login: admin / password)
    Registry:   http://$FIP_ADDR:8082  →  point  roksbnkctl registry target generic
                generic_host $FIP_ADDR:8082 ; generic_username admin ; generic_password ...

Artifactory takes ~3–5 min to finish its first-boot init after the VSI is up.
Watch it:   ssh -i $KEY_FILE ubuntu@$FIP_ADDR 'sudo docker logs -f artifactory'
Tear down:  $0 $WS --destroy
DONE
