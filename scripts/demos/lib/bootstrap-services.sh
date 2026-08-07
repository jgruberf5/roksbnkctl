#!/usr/bin/env bash
# bootstrap-services.sh — build the services substrate every disconnected demo needs,
# from a CLEAN SLATE.
#
# The ONLY thing this assumes already exists is the global Transit Gateway (and the
# FAR supply chain in COS). SSH key, services VPC, subnet, public gateway, security
# group rules, the gateway attachment and the Harbor registry VSI are all created
# here if absent.
#
# It is IDEMPOTENT: every step checks first and records what it made under
# $BOOTSTRAP_STATE, so a re-run after a partial failure continues rather than
# duplicating. Source the emitted env fragment to consume the results:
#
#   bash lib/bootstrap-services.sh
#   set -a; source "$BOOTSTRAP_STATE/services.env"; set +a
#
# Emits: SERVICES_VPC SERVICES_SUBNET SERVICES_CONN_ID HARBOR_PRIVATE_IP HARBOR_FIP
#        HARBOR_ADMIN_PASSWORD HARBOR_CA_B64 SSH_KEY_NAME SSH_KEY_FILE
set -euo pipefail

HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

: "${IBMCLOUD_API_KEY:?set IBMCLOUD_API_KEY}"
TGW_NAME="${TGW_NAME:-bnkci-testing}"          # the ONE pre-existing thing
SVC_REGION="${SVC_REGION:-us-east}"
SVC_ZONE="${SVC_ZONE:-${SVC_REGION}-1}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
SVC_PREFIX="${SVC_PREFIX:-bnk-svc}"
HARBOR_VSI_PROFILE="${HARBOR_VSI_PROFILE:-bx2-4x16}"
HARBOR_VERSION="${HARBOR_VERSION:-v2.11.1}"
HARBOR_PROJECT="${HARBOR_PROJECT:-bnk-mirror}"
HARBOR_STATUS_PROJECT="${HARBOR_STATUS_PROJECT:-bnk-status}"
SSH_KEY_NAME="${SSH_KEY_NAME:-${SVC_PREFIX}-key}"
BOOTSTRAP_STATE="${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}"
SSH_KEY_FILE="${SSH_KEY_FILE:-$BOOTSTRAP_STATE/${SSH_KEY_NAME}}"

mkdir -p "$BOOTSTRAP_STATE"; chmod 700 "$BOOTSTRAP_STATE"
say(){ echo "==> $*" >&2; }

ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q >/dev/null
TGW_ID="$(ibmcloud tg gateways --output json | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id')"
[[ -n "$TGW_ID" && "$TGW_ID" != null ]] || {
  echo "transit gateway '$TGW_NAME' not found. It is the one prerequisite this script does NOT create:" >&2
  echo "  ibmcloud tg gateway-create --name $TGW_NAME --location $SVC_REGION --routing global" >&2
  exit 1; }
say "transit gateway $TGW_NAME ($TGW_ID)"

# ── SSH key ──────────────────────────────────────────────────────────────────
# RSA, because IBM Cloud VPC rejects ed25519. Generated here rather than assumed,
# so a clean account needs nothing staged — and so the PRIVATE key is always one we
# actually hold. (A key registered in VPC whose private half you lost is useless:
# you cannot reach the VSIs you are about to build.)
if [[ ! -f "$SSH_KEY_FILE" ]]; then
  ssh-keygen -t rsa -b 4096 -N '' -f "$SSH_KEY_FILE" -C "$SSH_KEY_NAME" >/dev/null
  say "generated $SSH_KEY_FILE"
fi
chmod 600 "$SSH_KEY_FILE"
if ! ibmcloud is key "$SSH_KEY_NAME" >/dev/null 2>&1; then
  ibmcloud is key-create "$SSH_KEY_NAME" @"${SSH_KEY_FILE}.pub" --resource-group-name "$RESOURCE_GROUP" >/dev/null
  say "registered VPC SSH key $SSH_KEY_NAME"
fi

# Harbor's admin password is generated once and kept in state, never echoed.
if [[ ! -f "$BOOTSTRAP_STATE/harbor_pw" ]]; then
  printf 'Hbr%s!' "$(openssl rand -hex 8)" > "$BOOTSTRAP_STATE/harbor_pw"
  chmod 600 "$BOOTSTRAP_STATE/harbor_pw"
fi
HARBOR_ADMIN_PASSWORD="$(cat "$BOOTSTRAP_STATE/harbor_pw")"

# ── Boot image ───────────────────────────────────────────────────────────────
# Resolved at run time, never hardcoded: IBM retires stock image names, and a
# hardcoded one 404s `instance-create` months later with nothing to explain why.
HARBOR_IMAGE="${HARBOR_IMAGE:-$(ibmcloud is images --visibility public --output json \
  | jq -r '[.[]|select(.status=="available" and (.name|test("ubuntu-22-04.*amd64")))]|sort_by(.name)|last|.name')}"
[[ -n "$HARBOR_IMAGE" && "$HARBOR_IMAGE" != null ]] || { echo "no available Ubuntu 22.04 image found" >&2; exit 1; }
say "boot image $HARBOR_IMAGE"

# ── Services VPC + subnet + public gateway + SG ──────────────────────────────
if [[ -f "$BOOTSTRAP_STATE/svc_vpc_id" ]]; then
  SVC_VPC_ID="$(cat "$BOOTSTRAP_STATE/svc_vpc_id")"
  ibmcloud is vpc "$SVC_VPC_ID" >/dev/null 2>&1 || { rm -f "$BOOTSTRAP_STATE/svc_vpc_id"; SVC_VPC_ID=""; }
fi
if [[ -z "${SVC_VPC_ID:-}" ]]; then
  SVC_VPC_ID="$(ibmcloud is vpc-create "${SVC_PREFIX}-vpc" --resource-group-name "$RESOURCE_GROUP" --output json | jq -r .id)"
  echo "$SVC_VPC_ID" > "$BOOTSTRAP_STATE/svc_vpc_id"
  say "created services VPC $SVC_VPC_ID"
fi
SVC_VPC_CRN="$(ibmcloud is vpc "$SVC_VPC_ID" --output json | jq -r .crn)"

if [[ ! -f "$BOOTSTRAP_STATE/subnet_id" ]]; then
  SUBNET_ID="$(ibmcloud is subnet-create "${SVC_PREFIX}-subnet" "$SVC_VPC_ID" --zone "$SVC_ZONE" \
      --ipv4-address-count 256 --resource-group-name "$RESOURCE_GROUP" --output json | jq -r .id)"
  echo "$SUBNET_ID" > "$BOOTSTRAP_STATE/subnet_id"
  PGW_ID="$(ibmcloud is public-gateway-create "${SVC_PREFIX}-pgw" "$SVC_VPC_ID" "$SVC_ZONE" --output json | jq -r .id)"
  echo "$PGW_ID" > "$BOOTSTRAP_STATE/pgw_id"
  ibmcloud is subnet-update "$SUBNET_ID" --pgw "$PGW_ID" >/dev/null
  SG_ID="$(ibmcloud is vpc "$SVC_VPC_ID" --output json | jq -r .default_security_group.id)"
  for p in 22 443; do
    ibmcloud is security-group-rule-add "$SG_ID" inbound tcp --port-min $p --port-max $p >/dev/null || true
  done
  say "subnet + public gateway + 22/443"
fi
SUBNET_ID="$(cat "$BOOTSTRAP_STATE/subnet_id")"

# ── Attach to the gateway, and VERIFY ────────────────────────────────────────
# This is the only path from cluster workers to Harbor. A silent failure here does
# not surface until `bnk up` dies ~10 minutes in with `cert_manager: context
# deadline exceeded`, so never assume it worked.
if [[ ! -f "$BOOTSTRAP_STATE/svc_conn_id" ]]; then
  ibmcloud tg connection-create "$TGW_ID" --name "${SVC_PREFIX}-conn" \
      --network-type vpc --network-id "$SVC_VPC_CRN" --output json | jq -r '.id // empty' \
      > "$BOOTSTRAP_STATE/svc_conn_id"
fi
SVC_CONN_ID="$(cat "$BOOTSTRAP_STATE/svc_conn_id")"
[[ -n "$SVC_CONN_ID" ]] || { echo "services-VPC TGW connection was not created" >&2; exit 1; }
for _ in $(seq 1 40); do
  [[ "$(ibmcloud tg connection "$TGW_ID" "$SVC_CONN_ID" --output json 2>/dev/null | jq -r '.status // empty')" == attached ]] && break
  sleep 6
done
[[ "$(ibmcloud tg connection "$TGW_ID" "$SVC_CONN_ID" --output json | jq -r .status)" == attached ]] \
  || { echo "services-VPC TGW connection never reached 'attached'" >&2; exit 1; }
say "services VPC attached to $TGW_NAME (verified)"
ibmcloud is vpc-address-prefixes "$SVC_VPC_ID" --output json | jq -r '.[].cidr' > "$BOOTSTRAP_STATE/svc_prefixes"
say "services prefixes: $(tr '\n' ' ' < "$BOOTSTRAP_STATE/svc_prefixes")"

# ── Harbor VSI ───────────────────────────────────────────────────────────────
# The floating IP is reserved BEFORE cloud-init renders, because Harbor's TLS cert
# bakes it into the SAN — you cannot add it afterwards without regenerating.
if [[ ! -f "$BOOTSTRAP_STATE/harbor_fip" ]]; then
  ibmcloud is floating-ip-reserve "${SVC_PREFIX}-harbor-fip" --zone "$SVC_ZONE" >/dev/null
  ibmcloud is floating-ips --output json | jq -r --arg n "${SVC_PREFIX}-harbor-fip" \
      '.[]|select(.name==$n)|.address' | head -1 > "$BOOTSTRAP_STATE/harbor_fip"
fi
HARBOR_FIP="$(cat "$BOOTSTRAP_STATE/harbor_fip")"
HARBOR_FIP_ID="$(ibmcloud is floating-ips --output json | jq -r --arg n "${SVC_PREFIX}-harbor-fip" '.[]|select(.name==$n)|.id' | head -1)"

if [[ ! -f "$BOOTSTRAP_STATE/harbor_vsi_id" ]]; then
  CI="$BOOTSTRAP_STATE/harbor-cloud-init.yaml"
  HARBOR_FIP="$HARBOR_FIP" HARBOR_VERSION="$HARBOR_VERSION" HARBOR_ADMIN_PASSWORD="$HARBOR_ADMIN_PASSWORD" \
    envsubst '${HARBOR_FIP} ${HARBOR_VERSION} ${HARBOR_ADMIN_PASSWORD}' \
    < "$HERE/../disconnected-cluster-cli-demo/harbor-cloud-init.yaml.tmpl" > "$CI"
  chmod 600 "$CI"
  VSI_JSON="$(ibmcloud is instance-create "${SVC_PREFIX}-harbor" "$SVC_VPC_ID" "$SVC_ZONE" \
      "$HARBOR_VSI_PROFILE" "$SUBNET_ID" --image "$HARBOR_IMAGE" --keys "$SSH_KEY_NAME" \
      --resource-group-name "$RESOURCE_GROUP" --user-data "@$CI" --output json)"
  echo "$VSI_JSON" | jq -r .id > "$BOOTSTRAP_STATE/harbor_vsi_id"
  echo "$VSI_JSON" | jq -r '.primary_network_attachment.virtual_network_interface.id' > "$BOOTSTRAP_STATE/harbor_vni"
  say "Harbor VSI requested"
fi
HARBOR_VSI_ID="$(cat "$BOOTSTRAP_STATE/harbor_vsi_id")"

# The reserved primary IP is 0.0.0.0 at create time. Capturing it early bakes
# 0.0.0.0 into generic_host and every mirrored image resolves to https://0.0.0.0/.
HARBOR_PRIVATE_IP=""
for _ in $(seq 1 40); do
  HARBOR_PRIVATE_IP="$(ibmcloud is instance "$HARBOR_VSI_ID" --output json 2>/dev/null | jq -r '.primary_network_interface.primary_ip.address // empty')"
  [[ -n "$HARBOR_PRIVATE_IP" && "$HARBOR_PRIVATE_IP" != "0.0.0.0" ]] && break
  sleep 5
done
[[ -n "$HARBOR_PRIVATE_IP" && "$HARBOR_PRIVATE_IP" != "0.0.0.0" ]] || { echo "Harbor primary IP never bound" >&2; exit 1; }
echo "$HARBOR_PRIVATE_IP" > "$BOOTSTRAP_STATE/harbor_private_ip"
ibmcloud is virtual-network-interface-floating-ip-add "$(cat "$BOOTSTRAP_STATE/harbor_vni")" "$HARBOR_FIP_ID" >/dev/null 2>&1 || true
say "Harbor private=$HARBOR_PRIVATE_IP floating=$HARBOR_FIP"

say "waiting for Harbor cloud-init (docker + offline installer; 8-15 min)…"
up=0
for _ in $(seq 1 60); do
  curl -sk --max-time 8 "https://${HARBOR_FIP}/api/v2.0/systeminfo" 2>/dev/null | grep -q harbor_version && { up=1; break; }
  sleep 30
done
[[ $up == 1 ]] || { echo "Harbor did not come up at https://${HARBOR_FIP}" >&2; exit 1; }
say "Harbor up: v$(curl -sk "https://${HARBOR_FIP}/api/v2.0/systeminfo" | jq -r .harbor_version)"

# bnk-mirror is PUBLIC (anonymous pull): Harbor is isolated behind the gateway, so the
# network is the boundary, and anonymous pull avoids a pull-secret/ServiceAccount
# ordering race that leaves fresh cert-manager pods in ImagePullBackOff.
for pj in "$HARBOR_PROJECT" "$HARBOR_STATUS_PROJECT"; do
  curl -sk -u "admin:${HARBOR_ADMIN_PASSWORD}" -X POST "https://${HARBOR_FIP}/api/v2.0/projects" \
    -H 'Content-Type: application/json' -d "{\"project_name\":\"$pj\",\"public\":true}" -o /dev/null || true
done
say "Harbor projects: $HARBOR_PROJECT, $HARBOR_STATUS_PROJECT"

# ── The CA, from the file that generated it ──────────────────────────────────
# roksbnkctl refuses to adopt a self-signed CA it merely discovered over the wire,
# so pull harbor.crt off the VSI rather than out of the TLS handshake.
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=20"
for _ in $(seq 1 20); do
  ssh -i "$SSH_KEY_FILE" $SSH_OPTS ubuntu@"$HARBOR_FIP" 'sudo cat /opt/harbor/certs/harbor.crt' \
      > "$BOOTSTRAP_STATE/harbor-ca.crt" 2>/dev/null \
    && grep -q 'BEGIN CERTIFICATE' "$BOOTSTRAP_STATE/harbor-ca.crt" && break
  sleep 15
done
grep -q 'BEGIN CERTIFICATE' "$BOOTSTRAP_STATE/harbor-ca.crt" || { echo "could not fetch the Harbor CA" >&2; exit 1; }
HARBOR_CA_B64="$(base64 -w0 < "$BOOTSTRAP_STATE/harbor-ca.crt")"

# ── Emit the env fragment demos source ───────────────────────────────────────
cat > "$BOOTSTRAP_STATE/services.env" <<EOF
SERVICES_VPC=$SVC_VPC_ID
SERVICES_SUBNET=$SUBNET_ID
SERVICES_CONN_ID=$SVC_CONN_ID
SERVICES_PREFIXES="$(tr '\n' ' ' < "$BOOTSTRAP_STATE/svc_prefixes")"
HARBOR_PRIVATE_IP=$HARBOR_PRIVATE_IP
HARBOR_FIP=$HARBOR_FIP
HARBOR_ADMIN_PASSWORD=$HARBOR_ADMIN_PASSWORD
HARBOR_CA_B64=$HARBOR_CA_B64
SSH_KEY_NAME=$SSH_KEY_NAME
SSH_KEY_FILE=$SSH_KEY_FILE
TGW_NAME=$TGW_NAME
TGW_ID=$TGW_ID
EOF
chmod 600 "$BOOTSTRAP_STATE/services.env"
say "wrote $BOOTSTRAP_STATE/services.env"
say "BOOTSTRAP COMPLETE — services substrate ready"
