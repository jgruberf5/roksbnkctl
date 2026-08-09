#!/usr/bin/env bash
# bootstrap-argo.sh — build the Argo Workflows controller the CI demos submit to,
# from a CLEAN SLATE. Requires bootstrap-services.sh to have run first (it needs the
# services VPC, its subnet, and the SSH key).
#
# A single VSI running k3s + Argo Workflows, inside the services VPC — it has to be
# there, not hosted: the runner pods pull from Harbor's PRIVATE IP and talk to the
# target cluster over the transit gateway, neither of which a SaaS controller can
# reach.
#
# The k3s API is NOT exposed publicly. Only 22 and 443 are open on the services VPC,
# and this script reaches the API through an SSH tunnel on 22, writing a kubeconfig
# that points at 127.0.0.1. That is deliberate: opening 6443 to the Internet to run a
# demo is not a trade worth making.
#
#   bash lib/bootstrap-services.sh
#   set -a; source "$BOOTSTRAP_STATE/services.env"; set +a
#   bash lib/bootstrap-argo.sh
#   set -a; source "$BOOTSTRAP_STATE/argo.env"; set +a   # exports KUBECONFIG
set -euo pipefail

HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
BOOTSTRAP_STATE="${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}"

: "${IBMCLOUD_API_KEY:?set IBMCLOUD_API_KEY}"
: "${SERVICES_VPC:?source services.env from bootstrap-services.sh first}"
: "${SERVICES_SUBNET:?source services.env from bootstrap-services.sh first}"
: "${SSH_KEY_NAME:?source services.env from bootstrap-services.sh first}"
: "${SSH_KEY_FILE:?source services.env from bootstrap-services.sh first}"

# ssh into the VSI through a key ssh will accept (DrvFs cannot hold 0600).
source "$HERE/ssh-key.sh"

SVC_REGION="${SVC_REGION:-us-east}"
SVC_ZONE="${SVC_ZONE:-${SVC_REGION}-1}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
ARGO_VSI_NAME="${ARGO_VSI_NAME:-bnk-argo}"
ARGO_VSI_PROFILE="${ARGO_VSI_PROFILE:-bx2-4x16}"
ARGO_WF_VERSION="${ARGO_WF_VERSION:-v4.0.8}"
K3S_CHANNEL="${K3S_CHANNEL:-stable}"
ARGO_LOCAL_PORT="${ARGO_LOCAL_PORT:-6443}"

say(){ echo "==> $*" >&2; }
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=20 -o ServerAliveInterval=15"

ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q >/dev/null

# Resolved at run time — IBM retires stock image names, and a hardcoded one 404s
# `instance-create` months later with nothing to explain why.
ARGO_VSI_IMAGE="${ARGO_VSI_IMAGE:-$(ibmcloud is images --visibility public --output json \
  | jq -r '[.[]|select(.status=="available" and (.name|test("ubuntu-24-04.*amd64")))]|sort_by(.name)|last|.name')}"
[[ -n "$ARGO_VSI_IMAGE" && "$ARGO_VSI_IMAGE" != null ]] || { echo "no available Ubuntu 24.04 image found" >&2; exit 1; }
say "boot image $ARGO_VSI_IMAGE"

if ! ibmcloud is instance "$ARGO_VSI_NAME" >/dev/null 2>&1; then
  RENDERED="$BOOTSTRAP_STATE/argo-cloud-init.yaml"
  # The template also wires k3s's registries.yaml to trust Harbor, so the runner
  # image can be served from the mirror instead of a public registry.
  ARGO_WF_VERSION="$ARGO_WF_VERSION" K3S_CHANNEL="$K3S_CHANNEL" \
  HARBOR_IP="${HARBOR_PRIVATE_IP:-}" HARBOR_PASS="${HARBOR_ADMIN_PASSWORD:-}" HARBOR_CA_B64="${HARBOR_CA_B64:-}" \
    envsubst '${ARGO_WF_VERSION} ${K3S_CHANNEL} ${HARBOR_IP} ${HARBOR_PASS} ${HARBOR_CA_B64}' \
    < "$HERE/../disconnected-cluster-ci-demo/argo-vsi-cloud-init.yaml.tmpl" > "$RENDERED"
  chmod 600 "$RENDERED"
  ibmcloud is instance-create "$ARGO_VSI_NAME" "$SERVICES_VPC" "$SVC_ZONE" "$ARGO_VSI_PROFILE" \
    "$SERVICES_SUBNET" --image "$ARGO_VSI_IMAGE" --keys "$SSH_KEY_NAME" \
    --resource-group-name "$RESOURCE_GROUP" --user-data @"$RENDERED" >/dev/null
  say "VSI $ARGO_VSI_NAME requested"
else
  say "VSI $ARGO_VSI_NAME already exists — reusing"
fi

ibmcloud is floating-ip-reserve "${ARGO_VSI_NAME}-fip" --zone "$SVC_ZONE" >/dev/null 2>&1 || true
ARGO_FIP="$(ibmcloud is floating-ip "${ARGO_VSI_NAME}-fip" --output json 2>/dev/null | jq -r '.address // empty')"
[[ -n "$ARGO_FIP" ]] || { echo "could not resolve ${ARGO_VSI_NAME}-fip" >&2; exit 1; }
echo "$ARGO_FIP" > "$BOOTSTRAP_STATE/argo_fip"

VNI=""
for _ in $(seq 1 40); do
  VNI="$(ibmcloud is instance "$ARGO_VSI_NAME" --output json 2>/dev/null | jq -r '.primary_network_attachment.virtual_network_interface.id // empty')"
  [[ -n "$VNI" ]] && break; sleep 10
done
[[ -n "$VNI" ]] || { echo "$ARGO_VSI_NAME never exposed a primary VNI" >&2; exit 1; }
ibmcloud is virtual-network-interface-floating-ip-add "$VNI" "${ARGO_VSI_NAME}-fip" >/dev/null 2>&1 || true
say "Argo VSI floating IP $ARGO_FIP"

V(){ ssh -i "$(ssh_key)" $SSH_OPTS ubuntu@"$ARGO_FIP" "sudo bash -lc '$*'"; }

say "waiting for cloud-init (k3s + Argo Workflows $ARGO_WF_VERSION)…"
ready=0
for _ in $(seq 1 120); do
  V 'test -f /var/lib/cloud/argo-vsi-ready' >/dev/null 2>&1 && { ready=1; break; }
  sleep 15
done
[[ $ready == 1 ]] || { echo "Argo VSI never became ready over ssh" >&2; exit 1; }
V 'export KUBECONFIG=/etc/rancher/k3s/k3s.yaml; kubectl -n argo get pods --no-headers' >&2 || true

# ── kubeconfig via an SSH tunnel, not an exposed API ─────────────────────────
V 'cat /etc/rancher/k3s/k3s.yaml' | sed "s#https://127.0.0.1:6443#https://127.0.0.1:${ARGO_LOCAL_PORT}#" \
  > "$BOOTSTRAP_STATE/argo-kubeconfig"
chmod 600 "$BOOTSTRAP_STATE/argo-kubeconfig"

# argo CLI, matched to the server so `argo submit` cannot version-skew
if ! command -v argo >/dev/null 2>&1; then
  mkdir -p "$HOME/bin"
  curl -sSL -o /tmp/argo.gz "https://github.com/argoproj/argo-workflows/releases/download/${ARGO_WF_VERSION}/argo-linux-amd64.gz"
  gunzip -f /tmp/argo.gz && install -m0755 /tmp/argo "$HOME/bin/argo"
  say "installed argo CLI $ARGO_WF_VERSION into $HOME/bin"
fi

cat > "$BOOTSTRAP_STATE/argo.env" <<EOF
ARGO_VSI_NAME=$ARGO_VSI_NAME
ARGO_FIP=$ARGO_FIP
ARGO_WF_VERSION=$ARGO_WF_VERSION
ARGO_LOCAL_PORT=$ARGO_LOCAL_PORT
KUBECONFIG=$BOOTSTRAP_STATE/argo-kubeconfig
EOF
chmod 600 "$BOOTSTRAP_STATE/argo.env"

cat >&2 <<EOF

==> BOOTSTRAP COMPLETE — Argo Workflows $ARGO_WF_VERSION at $ARGO_FIP

    The k3s API is NOT published. Open the tunnel before using kubectl/argo:

      ssh -i "$SSH_KEY_FILE" -N -L ${ARGO_LOCAL_PORT}:127.0.0.1:6443 ubuntu@$ARGO_FIP &
      set -a; source $BOOTSTRAP_STATE/argo.env; set +a
      argo list -n bnk-ci
EOF
# The hint deliberately names SSH_KEY_FILE, not \$(ssh_key): the staged copy is
# per-run and is removed when this script exits, so printing it would hand the
# operator a path that is already gone. If ssh rejects the durable key for mode
# (a DrvFs mount cannot hold 0600), say how to fix it rather than leaving them
# with "Permission denied (publickey)".
if [[ "$(stat -c %a "$SSH_KEY_FILE" 2>/dev/null)" != 600 ]]; then
  cat >&2 <<EOF

    NOTE: $SSH_KEY_FILE is on a filesystem that cannot hold mode 0600, so ssh will
    refuse it with "Permission denied (publickey)". Copy it somewhere POSIX first:

      install -m600 "$SSH_KEY_FILE" /tmp/bnk-svc-key
      ssh -i /tmp/bnk-svc-key -N -L ${ARGO_LOCAL_PORT}:127.0.0.1:6443 ubuntu@$ARGO_FIP &
EOF
fi
