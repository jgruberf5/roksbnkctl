#!/usr/bin/env bash
# =============================================================================
# disconnected-cluster-ci-demo.sh — GitOps disconnected BNK install via Argo Workflows.
#
# The SAME air-gapped deployment as the disconnected-cluster CLI demo, told the way it
# ships in a pipeline: **Argo Workflows** (on a single k3s VSI) runs the
# roksbnkctl-tools-runner container through two Workflows — a mirror (FAR -> Harbor) and
# an install (adopt the air-gapped ROKS cluster over the TGW + `bnk up`). No git, no
# ArgoCD Application: `argo submit` the Workflow YAMLs directly. The two Workflows share
# a PERSISTENT PVC, so the workspace (and its recorded mirror CA) carries across, and a
# later `bnk down` teardown is clean.
#
# Reuses the CLI demo's services VPC, Harbor VSI, FLP VSI and disco-demo cluster; builds
# only the Argo Workflows controller VSI. The runner image must be >= v1.33.0 (native
# operator + node CA trust, so the mirror push + `bnk up` chart pulls work from a
# container with no OS trust for the self-signed Harbor).
#
# Presentation contract + recording markers come from ../lib/demo-format.sh:
#   phase/banner, say/note, begin_long/end_long (10x in post), and FREEZE marks
#   (each roksbnkctl / argo command frame held 5s in the final cut).
# Inputs arrive via .env (see .env.example); AUTO_ADVANCE/PHASE_DELAY/DRY_RUN pace it.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
# shellcheck source=/dev/null
. "$HERE/../lib/demo-format.sh"

# ── Inputs ───────────────────────────────────────────────────────────────────
: "${IBMCLOUD_API_KEY:?set IBMCLOUD_API_KEY}"
REGION="${REGION:-us-east}"                       # where the services VPC (Harbor/FLP/Argo VSI) lives

# Clean-slate path: if the services substrate has already been bootstrapped, adopt it
# rather than making the operator copy eight values across by hand. Everything below
# still honours an explicit value, so a hand-built Harbor is unaffected.
#
#   bash ../lib/bootstrap-services.sh     # SSH key, services VPC, TGW attach, Harbor
#
# The FLP values are NOT bootstrapped — `roksbnkctl flp up` builds it, and this demo's
# own phase 1 does that. They stay required.
BOOTSTRAP_STATE="${BOOTSTRAP_STATE:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/.bootstrap-state}"
if [[ -f "$BOOTSTRAP_STATE/services.env" ]]; then
  # shellcheck disable=SC1091
  set -a; . "$BOOTSTRAP_STATE/services.env"; set +a
  echo "→ adopted the bootstrapped services substrate from $BOOTSTRAP_STATE/services.env" >&2
fi
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
SERVICES_VPC="${SERVICES_VPC:?set SERVICES_VPC, or run ../lib/bootstrap-services.sh to build one}"
SERVICES_SUBNET="${SERVICES_SUBNET:?set SERVICES_SUBNET, or run ../lib/bootstrap-services.sh}"
HARBOR_PRIVATE_IP="${HARBOR_PRIVATE_IP:?set HARBOR_PRIVATE_IP, or run ../lib/bootstrap-services.sh}"
HARBOR_ADMIN_PASSWORD="${HARBOR_ADMIN_PASSWORD:?set HARBOR_ADMIN_PASSWORD}"
FLP_EXTERNAL_URL="${FLP_EXTERNAL_URL:?set FLP_EXTERNAL_URL (from: roksbnkctl -w flp flp output)}"
FLP_ROOT_CA_B64="${FLP_ROOT_CA_B64:?set FLP_ROOT_CA_B64 (base64 of the FLP root CA)}"
HARBOR_CA_B64="${HARBOR_CA_B64:?set HARBOR_CA_B64 - base64 of the Harbor CA, so the k3s VSI trusts Harbor to pull the runner image}"
FAR_COS_BUCKET="${FAR_COS_BUCKET:?set FAR_COS_BUCKET (orchestration COS bucket holding f5-far-auth-key.tgz + subscription.jwt)}"
CLUSTER_NAME="${CLUSTER_NAME:-disco-demo}"

SSH_KEY_FILE="${SSH_KEY_FILE:?set SSH_KEY_FILE, or run ../lib/bootstrap-services.sh (it generates one)}"
SSH_KEY_NAME="${SSH_KEY_NAME:?set SSH_KEY_NAME, or run ../lib/bootstrap-services.sh (it registers one)}"
ARGO_VSI_NAME="${ARGO_VSI_NAME:-bnk-argo}"
ARGO_VSI_PROFILE="${ARGO_VSI_PROFILE:-bx2-4x16}"
ARGO_VSI_IMAGE="${ARGO_VSI_IMAGE:-ibm-ubuntu-24-04-4-minimal-amd64-6}"   # `ibmcloud is images | grep ubuntu-24-04` if instance-create 404s
ARGO_WF_VERSION="${ARGO_WF_VERSION:-v4.0.8}"
K3S_CHANNEL="${K3S_CHANNEL:-stable}"

# The runner image is served from the private Harbor mirror (not ghcr) — a full air-gap:
# the k3s VSI pulls it over the private IP, trusting Harbor's CA via registries.yaml.
# Mirror it once:  docker pull ghcr.io/jgruberf5/roksbnkctl-tools-runner:<tag>
#                  docker tag  … <HARBOR_PRIVATE_IP>/bnk-mirror/roksbnkctl-tools-runner:<tag> && docker push …
RUNNER_IMAGE="${RUNNER_IMAGE:-${HARBOR_PRIVATE_IP}/bnk-mirror/roksbnkctl-tools-runner:${RUNNER_TAG:-v1.55.1}}"

SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=8"
export IBMCLOUD_API_KEY

# ── On-VSI command helper: present a clean command, run it over ssh with the VSI's
#    KUBECONFIG + PATH. A command mentioning roksbnkctl or argo drops a FREEZE mark
#    (post_10x.py holds that frame 5s), so the pipeline commands are taught.
ARGO_FIP=""   # resolved after provisioning
# A COMMAND still fires before, and an OUTPUT still after, any roksbnkctl or argo command —
# post_10x.py holds each frame 5s in the final cut (the same convention as the CLI demo's
# show/outmark). The hold vars + ts()/redact() come from ../lib/demo-format.sh.
is_demo_cmd(){
  case "$*" in
    roksbnkctl\ *|argo\ *|*\ roksbnkctl\ *|*\ argo\ *) return 0 ;;
  esac
  return 1
}
vshow(){
  { echo; echo "${B}\$ $*${N}"; } >&2
  if is_demo_cmd "$*"; then
    sleep "${CMD_RENDER_HOLD:-1.8}"; ts COMMAND MARK "$*"; sleep "${CMD_POST_HOLD:-0.7}"
  fi
}
V(){
  ssh -i "$SSH_KEY_FILE" $SSH_OPTS "ubuntu@${ARGO_FIP}" "export KUBECONFIG=/home/ubuntu/.kube/config PATH=\$PATH:/usr/local/bin; $*"
}
vrun(){
  vshow "$@"
  if [ "$DRY_RUN" = "1" ]; then say "  (dry-run)"; return 0; fi
  V "$@"
  if is_demo_cmd "$*"; then
    sleep "${OUT_SETTLE_HOLD:-1.2}"; ts OUTPUT MARK "$*"; sleep "${OUT_POST_HOLD:-0.7}"
  fi
}

# =============================================================================
phase 1 "The pipeline — a disconnected install as two Argo Workflows"
say "Two Workflow YAMLs run the roksbnkctl-tools-runner"
say "image through the same steps as the CLI demo, each step its own pod:"
echo >&2
say "  wf-mirror   — init -> registry replicate -> registry verify   (FAR -> Harbor)"
say "  wf-install  — cluster register -> bnk up -> bnk status         (adopt + install)"
note "The runner image is the whole toolchain; the Workflows just sequence these commands:"
{ echo "${B}    roksbnkctl -w bnk init --config-file /config/bnk.yaml --override-from-env${N}"
  echo "${B}    roksbnkctl -w bnk registry replicate --target generic${N}"
  echo "${B}    roksbnkctl -w bnk registry verify${N}"
  echo "${B}    roksbnkctl -w bnk cluster register ${CLUSTER_NAME}${N}"
  echo "${B}    roksbnkctl -w bnk bnk up --auto${N}"
  echo "${B}    roksbnkctl -w bnk bnk status${N}"; } >&2
ts COMMAND MARK "the roksbnkctl pipeline"
pause

# =============================================================================
phase 2 "Provision the Argo Workflows controller — one VSI, k3s + Argo Workflows"
say "A single ${ARGO_VSI_PROFILE} in the services VPC (the only egress). cloud-init installs"
say "k3s + Argo Workflows ${ARGO_WF_VERSION} + the argo CLI. It has egress, so it pulls the runner"
say "image freely; only the ROKS cluster it targets is air-gapped."
begin_long
if [[ "$DRY_RUN" != "1" ]]; then
  ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$REGION" -g "$RESOURCE_GROUP" -q >/dev/null 2>&1 || die "ibmcloud login failed"
  RENDERED="$(mktemp)"; export ARGO_WF_VERSION K3S_CHANNEL
  export HARBOR_IP="$HARBOR_PRIVATE_IP" HARBOR_PASS="$HARBOR_ADMIN_PASSWORD" HARBOR_CA_B64   # k3s registries.yaml → pull the runner from Harbor
  envsubst '${ARGO_WF_VERSION} ${K3S_CHANNEL} ${HARBOR_IP} ${HARBOR_PASS} ${HARBOR_CA_B64}' < "$HERE/argo-vsi-cloud-init.yaml.tmpl" > "$RENDERED"
  if ! ibmcloud is instance "$ARGO_VSI_NAME" >/dev/null 2>&1; then
    ibmcloud is instance-create "$ARGO_VSI_NAME" "$SERVICES_VPC" "$REGION-1" "$ARGO_VSI_PROFILE" \
      "$SERVICES_SUBNET" --image "$ARGO_VSI_IMAGE" --keys "$SSH_KEY_NAME" \
      --user-data @"$RENDERED" >/dev/null 2>&1 || die "instance-create failed"
    ok "VSI ${ARGO_VSI_NAME} requested"
  else say "  VSI ${ARGO_VSI_NAME} already exists — reusing"; fi
  # Floating IP for the ssh control channel — newer stock images use the VNI model, so
  # bind the FIP to the primary VNI (not `floating-ip-update --nic`), after the VNI exists.
  ibmcloud is floating-ip-reserve "${ARGO_VSI_NAME}-fip" --zone "$REGION-1" >/dev/null 2>&1 || true
  ARGO_FIP="$(ibmcloud is floating-ip "${ARGO_VSI_NAME}-fip" --output json 2>/dev/null | jq -r '.address // empty')"
  [[ -n "$ARGO_FIP" ]] || die "could not resolve the ${ARGO_VSI_NAME}-fip floating IP"
  VNI=""
  for i in $(seq 1 40); do
    VNI="$(ibmcloud is instance "$ARGO_VSI_NAME" --output json 2>/dev/null | jq -r '.primary_network_attachment.virtual_network_interface.id // empty')"
    [[ -n "$VNI" ]] && break; sleep 10
  done
  [[ -n "$VNI" ]] || die "ArgoWF VSI never exposed a primary VNI to bind the floating IP"
  ibmcloud is virtual-network-interface-floating-ip-add "$VNI" "${ARGO_VSI_NAME}-fip" >/dev/null 2>&1 || true
  say "  waiting for cloud-init (k3s + Argo Workflows) to finish (also gates ssh reachability) …"
  ready=0
  for i in $(seq 1 120); do
    V 'test -f /var/lib/cloud/argo-vsi-ready' >/dev/null 2>&1 && { ok "Argo Workflows VSI ready at ${ARGO_FIP}"; ready=1; break; }
    sleep 15
  done
  [[ "$ready" == 1 ]] || die "Argo VSI ${ARGO_FIP} unreachable over ssh, or cloud-init never finished"
else say "  (dry-run) would provision ${ARGO_VSI_NAME}"; fi
end_long
vrun 'argo version --short 2>/dev/null | head -1'
vrun 'kubectl -n argo get pods'
pause

# =============================================================================
phase 3 "Publish the pipeline substrate — PVC, config, and the one secret NOT in git"
say "Render the Harbor private IP + runner image into the ConfigMap and the two Workflow"
say "YAMLs, apply the prereqs (a PERSISTENT PVC the Workflows share + a teardown can read),"
say "and create the Secret imperatively — the API key, Harbor password and FLP handoff"
say "never touch git."
if [[ "$DRY_RUN" != "1" ]]; then
  RCM="$(sed -e "s#PLACEHOLDER_HARBOR_PRIVATE_IP#$HARBOR_PRIVATE_IP#" -e "s#PLACEHOLDER_COS_BUCKET#$FAR_COS_BUCKET#" "$HERE/gitops/disconnected-bnk/00-configmap.yaml")"
  RWM="$(sed "s#PLACEHOLDER_RUNNER_IMAGE#$RUNNER_IMAGE#" "$HERE/workflows/wf-mirror.yaml")"
  RWI="$(sed "s#PLACEHOLDER_RUNNER_IMAGE#$RUNNER_IMAGE#" "$HERE/workflows/wf-install.yaml")"
  scp -i "$SSH_KEY_FILE" $SSH_OPTS "$HERE/workflows/00-prereqs.yaml" "ubuntu@${ARGO_FIP}:/tmp/00-prereqs.yaml" >/dev/null 2>&1
  printf '%s' "$RCM" | V 'cat > /tmp/configmap.yaml'
  printf '%s' "$RWM" | V 'cat > /tmp/wf-mirror.yaml'
  printf '%s' "$RWI" | V 'cat > /tmp/wf-install.yaml'
  V 'kubectl apply -f /tmp/00-prereqs.yaml >/dev/null && kubectl apply -f /tmp/configmap.yaml >/dev/null' && ok "prereqs + config applied (PVC bnk-work created)"
fi
vshow "kubectl create secret generic bnk-secrets -n bnk-ci --from-literal=IBMCLOUD_API_KEY=*** \\"
say   "    --from-literal=ROKSBNKCTL_GENERIC_PASSWORD=*** \\"
say   "    --from-literal=ROKSBNKCTL_FLP_EXTERNAL_URL=${FLP_EXTERNAL_URL} \\"
say   "    --from-literal=ROKSBNKCTL_FLP_ROOT_CA_B64=***"
if [[ "$DRY_RUN" != "1" ]]; then
  V "kubectl create secret generic bnk-secrets -n bnk-ci \
      --from-literal=IBMCLOUD_API_KEY='$IBMCLOUD_API_KEY' \
      --from-literal=ROKSBNKCTL_GENERIC_PASSWORD='$HARBOR_ADMIN_PASSWORD' \
      --from-literal=ROKSBNKCTL_FLP_EXTERNAL_URL='$FLP_EXTERNAL_URL' \
      --from-literal=ROKSBNKCTL_FLP_ROOT_CA_B64='$FLP_ROOT_CA_B64' \
      --dry-run=client -o yaml | kubectl apply -f -" >/dev/null 2>&1 && ok "bnk-secrets created"
fi
pause

# =============================================================================
phase 4 "Workflow 1 — mirror FAR into Harbor (argo submit)"
say "One 'argo submit' runs init -> registry replicate -> registry verify on the runner"
say "image. The runner captures Harbor's self-signed CA and mirrors 89 artifacts by its"
say "private IP over the TGW; the workspace lands on the shared PVC."
vrun "argo submit -n bnk-ci --wait /tmp/wf-mirror.yaml"
vrun "argo get -n bnk-ci @latest"
pause

# =============================================================================
phase 5 "Workflow 2 — sync the disconnected install (adopt + bnk up)"
say "The second Workflow adopts disco-demo over the TGW and runs 'bnk up': images from"
say "Harbor, license via the FLP, node + operator CA trust native. A silent cwc-guard"
say "sidecar clears the F5 Multi-Attach defect so licensing activates on a reused cluster."
begin_long
vrun "argo submit -n bnk-ci --wait /tmp/wf-install.yaml"
end_long
pause

# =============================================================================
phase 6 "Verify — License Active, the Workflow Succeeded"
say "The runner's last step already ran 'bnk status'. Confirm the install Workflow is"
say "Succeeded and the license is Active on the air-gapped cluster."
vrun "argo get -n bnk-ci @latest"
vrun "argo logs -n bnk-ci @latest | grep -iE 'STATE:|Active|deployed|Succeeded' | tail -8"
ok "Disconnected BNK install complete — Argo Workflows drove the runner."
pause

banner "Disconnected-cluster CI demo complete (Argo Workflows)"
say "Argo Workflows UI:  https://${ARGO_FIP}:30746/workflows/bnk-ci   (server auth — no login)"
say "Harbor:             https://${HARBOR_PRIVATE_IP}/  (admin / your HARBOR_ADMIN_PASSWORD, over the VPC)"
say "FLP status:         ${FLP_EXTERNAL_URL%:8443}/"
echo >&2
say "Teardown (the persistent PVC makes it clean):"
say "  argo submit -n bnk-ci --wait - <<'EOF' ... bnk down --auto   # or run bnk down against the PVC"
say "  ibmcloud is instance-delete ${ARGO_VSI_NAME} --force && ibmcloud is floating-ip-release ${ARGO_VSI_NAME}-fip --force"
