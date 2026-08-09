#!/usr/bin/env bash
# unbootstrap.sh — destroy what bootstrap-services.sh + bootstrap-argo.sh created.
#
# WHY THIS IS NOT A WORKFLOW. Everything else in this demo tears down as an Argo
# workflow. This cannot: the Argo VSI is the node those workflows are scheduled on,
# and Harbor plus the services VPC are how they reach anything. A pod cannot outlive
# the host it runs on, so the last step has to come from outside the cluster.
#
# WHY IT EXISTS AT ALL. `teardown` removes what the WORKFLOWS built and says the
# substrate is "left in place — delete it with kubectl delete ns". That covers the
# Kubernetes objects and nothing else: the two VSIs, the services VPC, its subnet and
# public gateway, two floating IPs, the registered SSH key and the gateway attachment
# all survive. Someone following the guide to the end sees "✓ teardown complete" and
# keeps paying for two VSIs. There was no inverse of bootstrap; this is it.
#
#   IBMCLOUD_API_KEY=… bash lib/unbootstrap.sh [--yes]
#
# The shared transit gateway is NEVER deleted — the demo does not create it, other
# projects attach to it, and deleting it would break things this demo never made.
# Only THIS demo's connection to it is removed.
set -uo pipefail

HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
BOOTSTRAP_STATE="${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}"
: "${IBMCLOUD_API_KEY:?set IBMCLOUD_API_KEY}"
SVC_REGION="${SVC_REGION:-us-east}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
SVC_PREFIX="${SVC_PREFIX:-bnk-svc}"
ARGO_VSI_NAME="${ARGO_VSI_NAME:-bnk-argo}"
SSH_KEY_NAME="${SSH_KEY_NAME:-${SVC_PREFIX}-key}"
TGW_NAME="${TGW_NAME:-bnkci-testing}"
say(){ echo "==> $*" >&2; }

[[ "${1:-}" == "--yes" || "${AUTO_YES:-0}" == 1 ]] || {
  echo "This deletes the Argo VSI, Harbor, the services VPC and the demo's SSH key." >&2
  echo "The shared gateway '$TGW_NAME' is NOT deleted — only this demo's connection to it." >&2
  printf "Proceed? [y/N]: " >&2; read -r a; [[ "$a" =~ ^[yY] ]] || exit 1
}

ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q >/dev/null || exit 1

# 1. VSIs first — a VPC cannot be deleted while instances live in it, and an
#    instance holds its subnet.
for n in "$ARGO_VSI_NAME" "${SVC_PREFIX}-harbor"; do
  id="$(ibmcloud is instances --output json 2>/dev/null | jq -r --arg n "$n" '.[]|select(.name==$n)|.id')"
  [[ -n "$id" ]] || continue
  ibmcloud is instance-delete "$id" -f >/dev/null 2>&1 && say "deleting VSI $n"
done
for _ in $(seq 1 60); do
  left="$(ibmcloud is instances --output json 2>/dev/null | jq -r --arg p "$SVC_PREFIX" --arg a "$ARGO_VSI_NAME" '[.[]|select(.name==$a or (.name|startswith($p)))]|length')"
  [[ "$left" == 0 ]] && break
  sleep 10
done
say "VSIs gone"

# 2. Floating IPs. Released AFTER the instances, or the release races the detach.
for id in $(ibmcloud is floating-ips --output json 2>/dev/null | jq -r --arg p "$SVC_PREFIX" '.[]|select(.name|startswith($p))|.id'); do
  ibmcloud is floating-ip-release "$id" -f >/dev/null 2>&1 && say "released floating ip $id"
done

VPC_ID="$(ibmcloud is vpcs --output json 2>/dev/null | jq -r --arg n "${SVC_PREFIX}-vpc" '.[]|select(.name==$n)|.id')"

# 3. The gateway CONNECTION — before the VPC delete, which fails while the connection
#    pins the VPC's CRN. Only ours; every other attachment stays.
TGW_ID="$(ibmcloud tg gateways --output json 2>/dev/null | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id')"
if [[ -n "$TGW_ID" && -n "$VPC_ID" ]]; then
  # `ibmcloud tg connections` takes an ID, never a name — handed a name it fails with
  # "The gateway was not found", and the failure is easy to swallow.
  cid="$(ibmcloud tg connections "$TGW_ID" --output json 2>/dev/null | jq -r --arg v "$VPC_ID" '.[]|select(.network_id|contains($v))|.id')"
  [[ -n "$cid" ]] && ibmcloud tg connection-delete "$TGW_ID" "$cid" -f >/dev/null 2>&1 \
    && say "detached the services VPC from $TGW_NAME (the gateway itself is untouched)"
fi

# 4. Subnets (detaching their public gateway first), then the gateway, then the VPC.
if [[ -n "$VPC_ID" ]]; then
  for s in $(ibmcloud is subnets --output json 2>/dev/null | jq -r --arg v "$VPC_ID" '.[]|select(.vpc.id==$v)|.id'); do
    pg="$(ibmcloud is subnet "$s" --output json 2>/dev/null | jq -r '.public_gateway.id // empty')"
    [[ -n "$pg" ]] && ibmcloud is subnet-public-gateway-detach "$s" -f >/dev/null 2>&1
    ibmcloud is subnet-delete "$s" -f >/dev/null 2>&1 && say "deleted subnet $s"
  done
  sleep 20
  for g in $(ibmcloud is public-gateways --output json 2>/dev/null | jq -r --arg v "$VPC_ID" '.[]|select(.vpc.id==$v)|.id'); do
    ibmcloud is public-gateway-delete "$g" -f >/dev/null 2>&1 && say "deleted public gateway $g"
  done
  sleep 10
  ibmcloud is vpc-delete "$VPC_ID" -f >/dev/null 2>&1 && say "deleted VPC ${SVC_PREFIX}-vpc"
fi

# 5. The generated SSH key.
kid="$(ibmcloud is keys --output json 2>/dev/null | jq -r --arg n "$SSH_KEY_NAME" '.[]|select(.name==$n)|.id')"
[[ -n "$kid" ]] && ibmcloud is key-delete "$kid" -f >/dev/null 2>&1 && say "deleted ssh key $SSH_KEY_NAME"

rm -f "$BOOTSTRAP_STATE"/services.env "$BOOTSTRAP_STATE"/argo.env 2>/dev/null
say "UNBOOTSTRAP COMPLETE — the shared gateway '$TGW_NAME' was left alone"
