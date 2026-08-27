#!/usr/bin/env bash
# =============================================================================
# disconnected-cluster-cli-demo.sh  (roksbnkctl v1.58.0)
#
# Shows the THREE things that make a disconnected F5 BIG-IP Next for Kubernetes
# (BNK) install work, onto a cluster you ALREADY HAVE:
#
#   1. a PRIVATE REGISTRY MIRROR (Harbor) seeded from F5's artifact registry,
#   2. a STANDALONE F5 LICENSE PROXY (FLP) VSI — the one controlled path to F5,
#   3. a DISCONNECTED bnk up onto an EXISTING ROKS cluster, over a Transit Gateway.
#
# Topology (co-located, air-gapped — matches Appendix A):
#   Region A — SERVICES VPC (the ONLY egress): Harbor VSI (the operator host,
#              runs roksbnkctl) + standalone FLP VSI (floating IP for licensing).
#   Region B — an EXISTING private ROKS cluster (public_gateway=false, no worker
#              egress), already on the same global Transit Gateway.
#   Harbor is addressed by its PRIVATE IP over the TGW (no split-horizon DNS).
#
# The cluster is NOT created here — that is a prerequisite. This demo is about the
# private supply chain + FLP + BNK, not ROKS provisioning.
#
# Hands-off: set AUTO_ADVANCE=1 (default here) to auto-advance between phases.
# Emits phase timestamps to $TS_FILE so a recording can be 10x-sped over the long
# phases (mirror, bnk up) in post.
#
# Linux / WSL. Requires: roksbnkctl v1.32.0, ibmcloud CLI (+ is/tg plugins), jq.
# =============================================================================
set -uo pipefail

# ============================ CONFIG (edit me) ===============================
TGW_NAME="${TGW_NAME:-bnkci-testing}"          # EXISTING global transit gateway
SVC_REGION="${SVC_REGION:-us-east}"            # region A: Harbor + FLP
SVC_ZONE="${SVC_ZONE:-us-east-1}"
BNK_REGION="${BNK_REGION:-us-south}"           # region B: the existing cluster
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"

EXISTING_CLUSTER="${EXISTING_CLUSTER:-bnkdemo-roks}"   # the PRE-EXISTING no-BNK cluster to adopt
SVC_PREFIX="${SVC_PREFIX:-bnk-svc}"            # services VPC + Harbor
FLP_WS="${FLP_WS:-flp}"                        # workspace (on the VSI) for the standalone FLP
MIRROR_WS="${MIRROR_WS:-mirror}"              # workspace (on the VSI) for the registry mirror
BNK_WS="${BNK_WS:-bnk}"                        # workspace (on the VSI) for the BNK install
SSH_KEY_NAME="${SSH_KEY_NAME:-bnk-airgap-key}" # EXISTING IBM Cloud VPC SSH key
SSH_KEY_FILE="${SSH_KEY_FILE:-$HOME/.ssh/bnk-airgap-key}"  # its PRIVATE key on THIS host
# `chmod 600` is not enough on its own. If SSH_KEY_FILE lives on a DrvFs mount
# (/mnt/c, /mnt/d — the normal case when the repo is on a Windows drive under
# WSL), chmod REPORTS SUCCESS and leaves the file 0777, and OpenSSH then refuses
# it: "Permissions 0777 … are too open" followed by "Permission denied
# (publickey)". Only the second line gets read, so it presents as a wrong key.
#
# lib/ssh-key.sh already solves this for the bootstrap — it stages a copy on a
# real Linux filesystem when the mode cannot be set. This demo was still doing a
# bare chmod, so pointing it at a key on /mnt/… failed exactly the way the helper
# exists to prevent. Use `$(ssh_key)` in place of "$SSH_KEY_FILE" for ssh/scp.
source "$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/../lib" && pwd)/ssh-key.sh"
# This demo inlines its own copies of the format helpers rather than sourcing
# lib/demo-format.sh, so preflight_binary has to be sourced explicitly — it was
# a "command not found" here, silently, because the script runs without `set -e`
# (#143). Same readlink -f form as above so a symlinked checkout still resolves.
source "$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/../lib" && pwd)/preflight-binary.sh"
# Same reason as above: this demo inlines the format helpers rather than sourcing
# demo-format.sh, so bnk_line_of has to be sourced explicitly or the cwc gate below
# is a silent `command not found` (#169, and the #164 lesson).
source "$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/../lib" && pwd)/bnk-line.sh"

HARBOR_VSI_PROFILE="${HARBOR_VSI_PROFILE:-bx2-4x16}"
# Resolved at RUN TIME, not hardcoded. IBM retires stock image names: the previous
# default (…-amd64-3) no longer exists, and instance-create then 404s months later
# with nothing in the message to explain why.
HARBOR_IMAGE="${HARBOR_IMAGE:-}"
HARBOR_ADMIN_PASSWORD="${HARBOR_ADMIN_PASSWORD:-Harbor12345!}"
HARBOR_PROJECT="${HARBOR_PROJECT:-bnk-mirror}"
HARBOR_STATUS_PROJECT="${HARBOR_STATUS_PROJECT:-bnk-status}"   # public project for the flp-status image
HARBOR_VERSION="${HARBOR_VERSION:-v2.11.1}"

MANIFEST_VERSION="${MANIFEST_VERSION:-2.3.0-3.2598.3-0.0.170}"
FAR_AUTH_LOCAL_FILE="${FAR_AUTH_LOCAL_FILE:-$HOME/f5/f5-far-auth-key.tgz}"
SUBSCRIPTION_JWT_LOCAL_FILE="${SUBSCRIPTION_JWT_LOCAL_FILE:-$HOME/f5/subscription.jwt}"
FLP_STATUS_IMAGE_BUILD="${FLP_STATUS_IMAGE_BUILD:-$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/flp-status-build}"  # dir with a prebuilt flp-status linux binary + Dockerfile

ROKSBNKCTL_BIN="${ROKSBNKCTL_BIN:-roksbnkctl}"        # LOCAL binary; shipped to the VSI. preflight_binary below
                                                      # prints which version this actually resolves to (#143).
STATE_DIR="${STATE_DIR:-$PWD/.demo-state}"; mkdir -p "$STATE_DIR"
TS_FILE="${TS_FILE:-$STATE_DIR/phase-timestamps.txt}"; : > "$TS_FILE"
AUTO_ADVANCE="${AUTO_ADVANCE:-1}"              # 1 = hands-off; 0 = wait for ENTER
PHASE_DELAY="${PHASE_DELAY:-6}"               # seconds to pause between phases when hands-off
DRY_RUN="${DRY_RUN:-0}"

# ============================ helpers ========================================
if [[ -t 2 ]]; then B=$'\e[1m'; DIM=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; C=$'\e[36m'; R=$'\e[31m'; N=$'\e[0m'; else B=""; DIM=""; G=""; Y=""; C=""; R=""; N=""; fi
# ── secret masking ───────────────────────────────────────────────────────────
# This demo is RECORDED: no credential may reach the screen. secret() registers a
# value (and its base64 form, since configs carry the encoded form); every helper
# below runs its text through redact() first. Verify with ../lib/check-masking.sh.
_SECRETS=()
secret(){
  local v b n=0
  for v in "$@"; do
    [[ -n "${v:-}" && ${#v} -ge 8 ]] || continue
    _SECRETS+=("$v"); n=$((n + 1))
    b="$(printf '%s' "$v" | base64 -w0 2>/dev/null)"
    [[ -n "$b" && "$b" != "$v" ]] && _SECRETS+=("$b")
  done
  (( n )) && ok "secret masking active — ${n} credential(s) render as ***REDACTED*** on screen"
  return 0
}
redact(){
  local s="$*" v
  (( ${#_SECRETS[@]} )) || { printf '%s' "$s"; return 0; }
  for v in "${_SECRETS[@]}"; do s="${s//"$v"/***REDACTED***}"; done
  printf '%s' "$s"
}
mask(){ local l; while IFS= read -r l || [[ -n "$l" ]]; do redact "$l"; echo; done; }
banner(){ { echo; echo "${B}${C}==============================================================================${N}"; echo "${B}${C} $(redact "$*") ${N}"; echo "${B}${C}==============================================================================${N}"; } >&2; }
say(){ echo "${DIM}$(redact "$*")${N}" >&2; }
note(){ { echo; echo "${Y}${B}NOTE:${N} ${Y}$(redact "$*")${N}"; echo; } >&2; }
ok(){ echo "${G}✓ $(redact "$*")${N}" >&2; }
die(){ echo "${R}✗ $(redact "$*")${N}" >&2; exit 1; }
# The recording is driven ENTIRELY by a queue file ($TS_FILE): every phase banner,
# every roksbnkctl command, every command's settled output, and every long window is
# logged with its exact wall-clock time. post_10x.py builds the final cut from that
# queue + the raw video — so delays/speeds can be reworked by RE-RUNNING post_10x
# alone; a re-record is only needed to change what actually happens on screen.
#
# Per roksbnkctl command the queue records TWO stills: the COMMAND (held so the viewer
# reads the exact invocation) and its OUTPUT (held so the viewer reads the result).
CMD_RENDER_HOLD="${CMD_RENDER_HOLD:-1.8}"   # command sits on screen before the COMMAND mark
CMD_POST_HOLD="${CMD_POST_HOLD:-0.7}"       # …and a beat after the mark, before it runs
OUT_SETTLE_HOLD="${OUT_SETTLE_HOLD:-1.2}"   # output settles on screen before the OUTPUT mark
# True only when roksbnkctl is the COMMAND WORD — not merely a substring. Without the
# word boundary, an ibmcloud command whose argument holds the repo path (e.g.
# `--user-data @/…/project/roksbnkctl/…/harbor-cloud-init.yaml`) would spuriously get a
# command/output still stamped on it. Match roksbnkctl only at start or after a space.
is_rbk_cmd(){ [[ "$1" =~ (^|[[:space:]])roksbnkctl[[:space:]] ]]; }
show(){ { echo; echo "${B}\$ $(redact "$*")${N}"; } >&2; if is_rbk_cmd "$*"; then sleep "$CMD_RENDER_HOLD"; ts COMMAND MARK "$(redact "$*")"; sleep "$CMD_POST_HOLD"; fi; }
outmark(){ if is_rbk_cmd "$*"; then sleep "$OUT_SETTLE_HOLD"; ts OUTPUT MARK "$(redact "$*")"; fi; }
run(){ show "$@"; [[ "$DRY_RUN" == "1" ]] && { say "  (dry-run)"; return 0; }; "$@"; outmark "$@"; }
# queue writer: <label> <ev> <epoch> [text]. Labels: PHASE / COMMAND / OUTPUT (stills),
# LONG (10x window). The trailing text records WHICH command/phase each row is, so the
# queue is human-readable and the timeline is auditable without watching the video.
ts(){ local a="$1" b="$2"; shift 2; echo "$a $b $(date +%s.%N) $*" >> "$TS_FILE"; }
# phase: CLEAR the screen so every phase starts on a blank terminal, show the banner
# ALONE, then mark it (post_10x holds this banner still and NEVER speeds it) and hold
# a readable beat before any command runs.
PHASE_BANNER_HOLD="${PHASE_BANNER_HOLD:-1.5}"
clear_screen(){ printf '\033[3J\033[2J\033[H' >&2; }
phase(){ clear_screen; banner "$2"; sleep "$PHASE_BANNER_HOLD"; ts PHASE MARK "$2"; sleep 0.4; }
endphase(){ :; }
# begin_long/end_long bracket a LONG DEPLOYMENT operation (install wait, replicate,
# flp up, bnk up). The recording post-processor speeds ONLY these windows to 10x,
# leaving the narration between them at normal, readable speed.
begin_long(){ say "  … (long-running; sped up 10x in the recording) …"; ts LONG START; }
end_long(){ ts LONG END; }
pause(){
  if [[ "$AUTO_ADVANCE" == "1" ]]; then sleep "$PHASE_DELAY"; return 0; fi
  read -r -p "${B}${G}» ENTER for next phase (q to quit): ${N}" a </dev/tty 2>/dev/null || return 0
  [[ "$a" == "q" ]] && { echo Quit. >&2; exit 0; }
}
trap 'echo >&2; echo "${R}Interrupted.${N}" >&2; exit 130' INT

# onvsi_run: show a command locally, then run it ON the Harbor VSI (the co-located
# operator host). Output streams back to this console. IBMCLOUD_API_KEY + PATH are
# exported on the VSI; roksbnkctl reaches IBM Cloud + Harbor's private IP from there.
HARBOR_SSH=""   # set once the Harbor VSI exists (Phase 1)
onvsi_run(){
  show "(on harbor VSI) $*"
  [[ "$DRY_RUN" == "1" ]] && { say "  (dry-run)"; return 0; }
  $HARBOR_SSH "export IBMCLOUD_API_KEY='$IBMCLOUD_API_KEY' PATH=\$PATH:/usr/local/bin; $*" 2>&1
  outmark "$*"
}
onvsi(){ [[ "$DRY_RUN" == "1" ]] && return 0; $HARBOR_SSH "export IBMCLOUD_API_KEY='$IBMCLOUD_API_KEY' PATH=\$PATH:/usr/local/bin; $*" 2>&1; }

# ---------------------------------------------------------------------------
# Manual teardown:  ./disconnected-cluster-cli-demo.sh teardown
# Removes everything THIS demo created — the standalone FLP VSI (roksbnkctl flp down
# on the operator VSI), the Harbor VSI, the services VPC (subnets, public gateway,
# floating IPs) and its TGW connection. The adopted cluster is LEFT UNTOUCHED (run
# `roksbnkctl -w bnk bnk down --auto` on the operator VSI first if you also want BNK
# removed from it). The demo does NOT auto-tear-down, so you can explore the UIs first.
# ---------------------------------------------------------------------------
teardown(){
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f .env ]] && { set -a; . ./.env; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  banner "TEARDOWN — disconnected-cluster CLI demo"
  local HFIP VPC SSH_OPTS TGW_ID
  HFIP="$(cat "$STATE_DIR/harbor_fip" 2>/dev/null || true)"
  VPC="$(cat "$STATE_DIR/svc_vpc_id" 2>/dev/null || true)"
  SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=15 -o ServerAliveInterval=15 -o ServerAliveCountMax=8"
  if [[ -n "$HFIP" ]]; then
    say "Destroying the standalone FLP (roksbnkctl -w ${FLP_WS} flp down) on the operator VSI…"
    ssh -i "$(ssh_key)" $SSH_OPTS ubuntu@"$HFIP" \
      "export IBMCLOUD_API_KEY='$IBMCLOUD_API_KEY' PATH=\$PATH:/usr/local/bin; roksbnkctl -w ${FLP_WS} flp down --auto" 2>&1 | tail -3 || true
  fi
  ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q >/dev/null 2>&1 || die "ibmcloud login failed"
  TGW_ID="$(ibmcloud tg gateways --output json 2>/dev/null | jq -r --arg n "$TGW_NAME" '.[]|select(.name==$n)|.id' | head -1)"
  for c in $(ibmcloud tg connections "$TGW_ID" --output json 2>/dev/null | jq -r --arg n "${SVC_PREFIX}-conn" '.[]|select(.name==$n)|.id'); do
    ibmcloud tg connection-delete "$TGW_ID" "$c" -f >/dev/null 2>&1 && ok "TGW connection ${SVC_PREFIX}-conn deleted"
  done
  [[ -n "$VPC" ]] || VPC="$(ibmcloud is vpcs --output json 2>/dev/null | jq -r --arg n "${SVC_PREFIX}-vpc" '.[]|select(.name==$n)|.id' | head -1)"
  if [[ -n "$VPC" ]]; then
    local insts; insts="$(ibmcloud is instances --vpc "$VPC" --output json 2>/dev/null | jq -r '.[].id' | tr '\n' ' ')"
    [[ -n "${insts// }" ]] && { ibmcloud is instance-delete $insts -f >/dev/null 2>&1 && ok "VSIs deleted"; }
    for _ in $(seq 1 30); do [[ "$(ibmcloud is instances --vpc "$VPC" --output json 2>/dev/null | jq 'length')" == "0" ]] && break; sleep 8; done
    for f in "${SVC_PREFIX}-harbor-fip" "${SVC_PREFIX}-pgw"; do ibmcloud is floating-ip-release "$f" -f >/dev/null 2>&1 && ok "floating IP $f released"; done
    for s in $(ibmcloud is subnets --vpc "$VPC" --output json 2>/dev/null | jq -r '.[].id'); do
      ibmcloud is subnet-public-gateway-detach "$s" -f >/dev/null 2>&1; sleep 2
      ibmcloud is subnet-delete "$s" -f >/dev/null 2>&1 && ok "subnet deleted"
    done
    for p in $(ibmcloud is public-gateways --output json 2>/dev/null | jq -r --arg v "$VPC" '.[]|select(.vpc.id==$v)|.id'); do ibmcloud is public-gateway-delete "$p" -f >/dev/null 2>&1 && ok "public gateway deleted"; done
    sleep 6
    for _ in $(seq 1 15); do
      ibmcloud is vpc-delete "$VPC" -f >/dev/null 2>&1
      [[ -z "$(ibmcloud is vpcs --output json 2>/dev/null | jq -r --arg v "$VPC" '.[]|select(.id==$v)|.id')" ]] && { ok "services VPC deleted"; break; }
      sleep 8
    done
  else
    note "services VPC not found (already gone, or run from a different STATE_DIR)"
  fi
  ok "teardown complete — the adopted cluster ${EXISTING_CLUSTER} was left untouched"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit $?; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — DISCONNECTED BNK onto an EXISTING cluster"
cat >&2 <<EOF
The story, in five phases:
  1. Services VPC + ${B}Harbor${N} registry VSI (the operator host), on the TGW.
  2. ${B}Mirror${N} F5's artifact registry -> Harbor (registry replicate).
  3. ${B}Standalone F5 License Proxy${N} VSI (with a floating IP for remote status).
  4. ${B}Adopt${N} the existing private cluster ${C}${EXISTING_CLUSTER}${N} + trust Harbor on its nodes.
  5. ${B}bnk up${N} — disconnected: images from Harbor, licensing via the FLP. One pass.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f .env ]] && { set -a; source ./.env; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"; export IBMCLOUD_API_KEY
secret "$IBMCLOUD_API_KEY" "$HARBOR_ADMIN_PASSWORD"
for c in "$ROKSBNKCTL_BIN" ibmcloud jq; do command -v "$c" >/dev/null || die "$c not found"; done
# #143: print the binary + version this demo will actually run, and warn on drift.
preflight_binary "$ROKSBNKCTL_BIN"
[[ -f "$FAR_AUTH_LOCAL_FILE" ]]        || die "FAR auth file not found: $FAR_AUTH_LOCAL_FILE"
[[ -f "$SUBSCRIPTION_JWT_LOCAL_FILE" ]] || die "subscription JWT not found: $SUBSCRIPTION_JWT_LOCAL_FILE"
[[ -f "$SSH_KEY_FILE" ]]               || die "SSH private key not found: $SSH_KEY_FILE (SSH_KEY_FILE)"
show "ibmcloud login --apikey *** -r $SVC_REGION -g $RESOURCE_GROUP -q   # key redacted from the demo output"
[[ "$DRY_RUN" == "1" ]] || ibmcloud login --apikey "$IBMCLOUD_API_KEY" -r "$SVC_REGION" -g "$RESOURCE_GROUP" -q >/dev/null
ibmcloud plugin show vpc-infrastructure >/dev/null 2>&1 || ibmcloud plugin install -f vpc-infrastructure >/dev/null
ibmcloud plugin show tg-cli            >/dev/null 2>&1 || ibmcloud plugin install -f tg-cli >/dev/null
TGW_JSON="$(ibmcloud tg gateways --output json 2>/dev/null | jq -c --arg n "$TGW_NAME" '.[]|select(.name==$n)' | head -1)"
TGW_ID="$(echo "$TGW_JSON" | jq -r '.id // empty')"; [[ -n "$TGW_ID" ]] || die "TGW '$TGW_NAME' not found"
[[ "$(echo "$TGW_JSON" | jq -r '.global')" == "true" ]] || die "TGW '$TGW_NAME' must be GLOBAL (cross-region)"
# existing cluster must be present (in BNK_REGION) and reachable
ibmcloud ks cluster get -c "$EXISTING_CLUSTER" >/dev/null 2>&1 || die "existing cluster '$EXISTING_CLUSTER' not found — provision it first (a plain no-BNK ROKS cluster on $TGW_NAME)"
ok "preflight: TGW '$TGW_NAME' ($TGW_ID), existing cluster '$EXISTING_CLUSTER', FAR/JWT/key present"
HARBOR_PW_B64="$(printf '%s' "$HARBOR_ADMIN_PASSWORD" | base64 | tr -d '\n')"

# ============================ Phase 1: services VPC + Harbor + operator ======
pause; phase P1 "PHASE 1/5  —  Services VPC + Harbor registry (the operator host), on the TGW"
say "roksbnkctl provisions neither registries nor standalone VPCs; this prerequisite is the"
say "ibmcloud CLI. Harbor installs via cloud-init; we then put roksbnkctl ON this VSI."
ibmcloud target -r "$SVC_REGION" -g "$RESOURCE_GROUP" >/dev/null

# Clean-slate safety net, both resolved here rather than assumed:
#
#   the boot image — IBM retires stock names, so pick the newest available 22.04
#   rather than a literal that will 404 `instance-create` in a few months;
#
#   the SSH key — a key registered in VPC whose PRIVATE half you no longer hold is
#   worse than no key: every VSI below accepts it and none of them let you in.
#   Generate the pair if the private file is missing, and register it if VPC has
#   never seen it. RSA because IBM Cloud VPC rejects ed25519.
if [[ -z "$HARBOR_IMAGE" ]]; then
  HARBOR_IMAGE="$(ibmcloud is images --visibility public --output json \
    | jq -r '[.[]|select(.status=="available" and (.name|test("ubuntu-22-04.*amd64")))]|sort_by(.name)|last|.name')"
  [[ -n "$HARBOR_IMAGE" && "$HARBOR_IMAGE" != null ]] || die "no available Ubuntu 22.04 image in $SVC_REGION"
  say "boot image resolved: $HARBOR_IMAGE"
fi
if [[ ! -f "$SSH_KEY_FILE" ]]; then
  mkdir -p "$(dirname "$SSH_KEY_FILE")"
  ssh-keygen -t rsa -b 4096 -N '' -f "$SSH_KEY_FILE" -C "$SSH_KEY_NAME" >/dev/null
  say "generated $SSH_KEY_FILE"
fi
chmod 600 "$SSH_KEY_FILE"
if ! ibmcloud is key "$SSH_KEY_NAME" >/dev/null 2>&1; then
  run ibmcloud is key-create "$SSH_KEY_NAME" @"${SSH_KEY_FILE}.pub" --resource-group-name "$RESOURCE_GROUP" >/dev/null
  ok "registered VPC SSH key $SSH_KEY_NAME"
fi
VPC_JSON="$(run ibmcloud is vpc-create "${SVC_PREFIX}-vpc" --resource-group-name "$RESOURCE_GROUP" --output json)"
SVC_VPC_ID="$(echo "$VPC_JSON" | jq -r .id)"; SVC_VPC_CRN="$(echo "$VPC_JSON" | jq -r .crn)"
echo "$SVC_VPC_ID" > "$STATE_DIR/svc_vpc_id"
SUBNET_ID="$(run ibmcloud is subnet-create "${SVC_PREFIX}-subnet" "$SVC_VPC_ID" --zone "$SVC_ZONE" --ipv4-address-count 256 --resource-group-name "$RESOURCE_GROUP" --output json | jq -r .id)"
PGW_ID="$(run ibmcloud is public-gateway-create "${SVC_PREFIX}-pgw" "$SVC_VPC_ID" "$SVC_ZONE" --output json | jq -r .id)"
run ibmcloud is subnet-update "$SUBNET_ID" --public-gateway-id "$PGW_ID" >/dev/null
SG_ID="$(ibmcloud is vpc "$SVC_VPC_ID" --output json | jq -r .default_security_group.id)"
for p in 22 443; do run ibmcloud is security-group-rule-add "$SG_ID" inbound tcp --port-min $p --port-max $p >/dev/null; done
# Attach the services VPC to the TGW — the ONLY path from the cluster's worker nodes to
# Harbor. If this silently fails (or never reaches "attached"), nodes can't pull from the
# mirror and bnk up dies much later with a baffling `cert_manager: context deadline
# exceeded` (only nodes with a cached image survive). So verify it actually attaches.
SVC_CONN_ID="$(run ibmcloud tg connection-create "$TGW_ID" --name "${SVC_PREFIX}-conn" --network-type vpc --network-id "$SVC_VPC_CRN" --output json | jq -r '.id // empty')"
[[ -n "$SVC_CONN_ID" ]] || die "services-VPC TGW connection was not created (name conflict with a not-yet-deleted ${SVC_PREFIX}-conn?)"
if [[ "$DRY_RUN" != "1" ]]; then
  for _ in $(seq 1 30); do
    [[ "$(ibmcloud tg connection "$TGW_ID" "$SVC_CONN_ID" --output json 2>/dev/null | jq -r '.status // empty')" == "attached" ]] && break
    sleep 6
  done
  [[ "$(ibmcloud tg connection "$TGW_ID" "$SVC_CONN_ID" --output json 2>/dev/null | jq -r '.status')" == "attached" ]] || die "services-VPC TGW connection never reached 'attached' — nodes could not reach Harbor"
fi
ok "services VPC $SVC_VPC_ID attached to $TGW_NAME (connection $SVC_CONN_ID, verified attached)"

run ibmcloud is floating-ip-reserve "${SVC_PREFIX}-harbor-fip" --zone "$SVC_ZONE" >/dev/null
HARBOR_FIP="$(ibmcloud is floating-ips --output json | jq -r --arg n "${SVC_PREFIX}-harbor-fip" '.[]|select(.name==$n)|.address' | head -1)"
HARBOR_FIP_ID="$(ibmcloud is floating-ips --output json | jq -r --arg n "${SVC_PREFIX}-harbor-fip" '.[]|select(.name==$n)|.id' | head -1)"
echo "$HARBOR_FIP" > "$STATE_DIR/harbor_fip"
CI="$STATE_DIR/harbor-cloud-init.yaml"
# Render the Harbor cloud-init from harbor-cloud-init.yaml.tmpl (co-located with
# this script). Only these three vars are substituted; the $(...) / $PRIV in the
# template run on the VSI at cloud-init time and are left intact.
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
HARBOR_FIP="$HARBOR_FIP" HARBOR_VERSION="$HARBOR_VERSION" HARBOR_ADMIN_PASSWORD="$HARBOR_ADMIN_PASSWORD" \
  envsubst '${HARBOR_FIP} ${HARBOR_VERSION} ${HARBOR_ADMIN_PASSWORD}' \
  < "$HERE/harbor-cloud-init.yaml.tmpl" > "$CI"
VSI_JSON="$(run ibmcloud is instance-create "${SVC_PREFIX}-harbor" "$SVC_VPC_ID" "$SVC_ZONE" "$HARBOR_VSI_PROFILE" "$SUBNET_ID" --image "$HARBOR_IMAGE" --keys "$SSH_KEY_NAME" --resource-group-name "$RESOURCE_GROUP" --user-data "@$CI" --output json)"
HARBOR_VSI_ID="$(echo "$VSI_JSON" | jq -r .id)"
# The reserved primary IP is 'pending' (0.0.0.0) at instance-create time and only
# binds a moment later. Capturing it too early bakes 0.0.0.0 into generic_host, so
# every mirrored image resolves to https://0.0.0.0/… and cert-manager (and thus
# bnk up) fails with RegistryUnavailable. Poll until the IP actually binds.
HARBOR_PRIVATE_IP="$(echo "$VSI_JSON" | jq -r '.primary_network_interface.primary_ip.address // empty')"
for _ in $(seq 1 30); do
  [[ -n "$HARBOR_PRIVATE_IP" && "$HARBOR_PRIVATE_IP" != "0.0.0.0" ]] && break
  sleep 4
  HARBOR_PRIVATE_IP="$(ibmcloud is instance "$HARBOR_VSI_ID" --output json 2>/dev/null | jq -r '.primary_network_interface.primary_ip.address // empty')"
done
[[ -z "$HARBOR_PRIVATE_IP" || "$HARBOR_PRIVATE_IP" == "0.0.0.0" ]] && die "Harbor VSI primary IP never bound (still 0.0.0.0) — cannot address the mirror"
echo "$HARBOR_PRIVATE_IP" > "$STATE_DIR/harbor_private_ip"
HARBOR_VNI="$(echo "$VSI_JSON" | jq -r '.primary_network_attachment.virtual_network_interface.id')"
run ibmcloud is virtual-network-interface-floating-ip-add "$HARBOR_VNI" "$HARBOR_FIP_ID" >/dev/null
ok "Harbor VSI — private ${B}${HARBOR_PRIVATE_IP}${N}, floating ${B}${HARBOR_FIP}${N}"
# LogLevel=ERROR silences the "Warning: Permanently added … to known hosts" spam that
# UserKnownHostsFile=/dev/null otherwise prints on stderr — critical because onvsi captures
# 2>&1 into shell vars (FAR service account, CAs, FLP URL), and that warning would corrupt them.
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=20 -o ServerAliveInterval=15 -o ServerAliveCountMax=8"
HARBOR_SSH="ssh -i $(ssh_key) $SSH_OPTS ubuntu@$HARBOR_FIP"

say "Waiting for Harbor to finish installing (cloud-init: Docker + offline installer; ~8–15 min)…"
begin_long
for i in $(seq 1 40); do curl -sk --max-time 8 "https://${HARBOR_FIP}/api/v2.0/systeminfo" 2>/dev/null | grep -q harbor_version && break; sleep 30; done
end_long
curl -sk --max-time 8 "https://${HARBOR_FIP}/api/v2.0/systeminfo" 2>/dev/null | grep -q harbor_version || die "Harbor did not come up at https://${HARBOR_FIP}"
ok "Harbor is up (v$(curl -sk https://${HARBOR_FIP}/api/v2.0/systeminfo | jq -r .harbor_version))"
# bnk-mirror is PUBLIC (anonymous pull). Harbor is network-isolated behind the TGW, so
# the network is the security boundary; anonymous pull avoids a per-namespace pull-secret /
# ServiceAccount-ordering race that can leave fresh cert-manager pods in ImagePullBackOff.
for pj in "$HARBOR_PROJECT:true" "$HARBOR_STATUS_PROJECT:true"; do
  code=$(curl -sk -u "admin:${HARBOR_ADMIN_PASSWORD}" -X POST "https://${HARBOR_FIP}/api/v2.0/projects" -H 'Content-Type: application/json' -d "{\"project_name\":\"${pj%%:*}\",\"public\":${pj##*:}}" -o /dev/null -w '%{http_code}')
  [[ "$code" =~ ^20 || "$code" == 409 ]] && ok "Harbor project '${pj%%:*}' ready" || die "project '${pj%%:*}' HTTP $code"
done

say "Put the operator ON the VSI: ship roksbnkctl + the FAR/JWT files + the SSH key,"
say "and install terraform >= 1.10 (the floor roksbnkctl enforces) — it shells out for every apply."
[[ "$DRY_RUN" == "1" ]] || {
  scp -i "$(ssh_key)" $SSH_OPTS "$(command -v "$ROKSBNKCTL_BIN")" ubuntu@"$HARBOR_FIP":/home/ubuntu/roksbnkctl >/dev/null
  scp -i "$(ssh_key)" $SSH_OPTS "$FAR_AUTH_LOCAL_FILE" ubuntu@"$HARBOR_FIP":/home/ubuntu/far-auth.tgz >/dev/null
  scp -i "$(ssh_key)" $SSH_OPTS "$SUBSCRIPTION_JWT_LOCAL_FILE" ubuntu@"$HARBOR_FIP":/home/ubuntu/subscription.jwt >/dev/null
  scp -i "$(ssh_key)" $SSH_OPTS "$SSH_KEY_FILE" ubuntu@"$HARBOR_FIP":/home/ubuntu/.flpkey >/dev/null
  onvsi "sudo install -m755 /home/ubuntu/roksbnkctl /usr/local/bin/roksbnkctl; chmod 600 /home/ubuntu/.flpkey; sudo cp /opt/harbor/certs/harbor.crt /usr/local/share/ca-certificates/harbor.crt; sudo update-ca-certificates >/dev/null 2>&1"
  onvsi "command -v terraform >/dev/null || { curl -fsSL https://releases.hashicorp.com/terraform/1.10.5/terraform_1.10.5_linux_amd64.zip -o /tmp/tf.zip && sudo apt-get install -y unzip >/dev/null 2>&1 && sudo unzip -o /tmp/tf.zip -d /usr/local/bin >/dev/null 2>&1; }"
  # roksbnkctl also shells out to helm (chart/BOM resolution during mirror + bnk up).
  onvsi "command -v helm >/dev/null || { curl -fsSL https://get.helm.sh/helm-v3.16.3-linux-amd64.tar.gz -o /tmp/helm.tgz && tar -xzf /tmp/helm.tgz -C /tmp && sudo install -m755 /tmp/linux-amd64/helm /usr/local/bin/helm; }; echo \"installed \$(roksbnkctl version 2>/dev/null | head -1) + terraform \$(terraform version 2>/dev/null | head -1 | awk '{print \$2}') + helm \$(helm version --short 2>/dev/null)\""
}
ok "operator ready on the Harbor VSI (roksbnkctl + terraform + helm)"

# Harbor's CA, read from the FILE cloud-init generated — the authoritative copy. Taken
# once here and handed to every workspace that needs it, so nothing later has to learn
# trust from the network. The fingerprint travels with it as the pin.
HARBOR_CA_B64="$(onvsi "sudo base64 -w0 /opt/harbor/certs/harbor.crt" | tr -d '\r')"
HARBOR_CA_SHA256="$(onvsi "sudo openssl x509 -in /opt/harbor/certs/harbor.crt -noout -fingerprint -sha256" | tr -d '\r' | sed -E 's/.*=//; s/://g' | tr 'A-F' 'a-f')"
[[ "$DRY_RUN" == "1" || -n "$HARBOR_CA_B64" ]] || die "could not read Harbor's CA from /opt/harbor/certs/harbor.crt"
ok "Harbor CA taken from the source file — SHA-256 ${HARBOR_CA_SHA256}"

# Build + push the flp-status web-UI image into Harbor's PUBLIC status project, so the
# FLP VSI (Phase 3) can pull it by private IP. podman (certs.d trust, no daemon) — docker
# would need the CA in system trust + a restart, which would bounce Harbor itself.
if [[ -n "$FLP_STATUS_IMAGE_BUILD" && -f "$FLP_STATUS_IMAGE_BUILD/flp-status" ]]; then
  say "Build + push the flp-status image to Harbor's public '${HARBOR_STATUS_PROJECT}' project (podman)."
  [[ "$DRY_RUN" == "1" ]] || {
    onvsi "mkdir -p /home/ubuntu/flpimg"
    scp -i "$(ssh_key)" $SSH_OPTS "$FLP_STATUS_IMAGE_BUILD/flp-status" "$FLP_STATUS_IMAGE_BUILD/Dockerfile" ubuntu@"$HARBOR_FIP":/home/ubuntu/flpimg/ >/dev/null
    onvsi "sudo apt-get install -y podman >/dev/null 2>&1; sudo mkdir -p /etc/containers/certs.d/${HARBOR_PRIVATE_IP}; sudo cp /opt/harbor/certs/harbor.crt /etc/containers/certs.d/${HARBOR_PRIVATE_IP}/ca.crt; sudo podman build -t ${HARBOR_PRIVATE_IP}/${HARBOR_STATUS_PROJECT}/flp-status:v1 /home/ubuntu/flpimg >/dev/null 2>&1; echo '${HARBOR_ADMIN_PASSWORD}' | sudo podman login ${HARBOR_PRIVATE_IP} -u admin --password-stdin >/dev/null 2>&1; sudo podman push ${HARBOR_PRIVATE_IP}/${HARBOR_STATUS_PROJECT}/flp-status:v1 2>&1 | tail -1"
  }
  ok "flp-status image in Harbor (${HARBOR_STATUS_PROJECT}/flp-status:v1)"
else
  note "flp-status build dir not found ($FLP_STATUS_IMAGE_BUILD) — Phase 3 assumes the image is already in Harbor's ${HARBOR_STATUS_PROJECT} project."
fi
endphase P1

# ============================ Phase 2: mirror (on VSI) =======================
pause; phase P2 "PHASE 2/5  —  Mirror F5's artifact registry -> Harbor (on the VSI, private IP)"   # LONG
say "registry replicate is a host-side registry-to-registry copy — no cluster needed. Run it ON"
say "the VSI so the mirror record lives beside the workspace bnk up will use."
say "registry.generic_ca_b64 carries Harbor's CA from the file that generated it, and"
say "generic_ca_sha256 pins it — so replicate never learns trust from the network. Without"
say "either, it REFUSES to adopt a self-signed CA discovered over the wire."
FAR_SA_B64="$(onvsi "roksbnkctl tfx far-extract --tarball /home/ubuntu/far-auth.tgz --out /home/ubuntu/far-sa.json >/dev/null 2>&1; cat /home/ubuntu/far-sa.json" | tr -d '\r')"
onvsi "cat > /home/ubuntu/${MIRROR_WS}.yaml <<YAML
ibmcloud: { region: ${SVC_REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${MIRROR_WS}
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: ${MANIFEST_VERSION}
  far_auth_local_file: /home/ubuntu/far-auth.tgz
  subscription_jwt_local_file: /home/ubuntu/subscription.jwt
registry:
  target: generic
  generic_host: ${HARBOR_PRIVATE_IP}
  generic_repo_prefix: ${HARBOR_PROJECT}
  generic_username: admin
  generic_password_b64: ${HARBOR_PW_B64}
  generic_ca_b64: ${HARBOR_CA_B64}
  generic_ca_sha256: ${HARBOR_CA_SHA256}
  source_service_account_b64: ${FAR_SA_B64}
YAML"
onvsi_run "roksbnkctl -w ${MIRROR_WS} init --config-file /home/ubuntu/${MIRROR_WS}.yaml"
onvsi_run "roksbnkctl -w ${MIRROR_WS} registry bom"
begin_long
onvsi_run "roksbnkctl -w ${MIRROR_WS} registry replicate"
end_long
onvsi_run "roksbnkctl -w ${MIRROR_WS} registry verify"
ok "Harbor holds every BNK chart + image (private IP ${HARBOR_PRIVATE_IP})"
endphase P2

# ============================ Phase 3: standalone FLP (on VSI) ===============
pause; phase P3 "PHASE 3/5  —  Standalone F5 License Proxy VSI (with operator floating IP)"
say "flp up (mode vsi) into the services VPC — no cluster. floating_ip: true gives it an operator"
say "floating IP so 'roksbnkctl flp status' + the :80 web UI work from anywhere; the cluster still"
say "reaches it PRIVATELY over the TGW. Its status image comes from Harbor's public project."
onvsi "cat > /home/ubuntu/${FLP_WS}.yaml <<YAML
ibmcloud: { region: ${SVC_REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${FLP_WS}
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: ${MANIFEST_VERSION}
  far_auth_local_file: /home/ubuntu/far-auth.tgz
  subscription_jwt_local_file: /home/ubuntu/subscription.jwt
  flp:
    mode: vsi
    vsi:
      vpc: ${SVC_VPC_ID}
      zone: ${SVC_ZONE}
      profile: bx2-4x16
      ssh_key: ${SSH_KEY_NAME}
      floating_ip: true
      status_image: ${HARBOR_PRIVATE_IP}/${HARBOR_STATUS_PROJECT}/flp-status:v1
      status_registry_host: ${HARBOR_PRIVATE_IP}
      status_registry_ca_b64: ${HARBOR_CA_B64}
YAML"
# The flp-status image must be in Harbor's public project. Build it from the mirrored
# BNK toolchain if a prebuilt binary dir was provided; else assume it is already there.
[[ -n "$FLP_STATUS_IMAGE_BUILD" ]] && say "(flp-status image expected in ${HARBOR_STATUS_PROJECT}; build/push it before this phase if absent)"
onvsi_run "roksbnkctl -w ${FLP_WS} init --config-file /home/ubuntu/${FLP_WS}.yaml"
begin_long
onvsi_run "roksbnkctl -w ${FLP_WS} flp up --auto"
end_long
# The FLP listener starts accepting connections a little AFTER `flp up` returns, so the
# very first `flp status` probes can print transient "unable to connect" lines before it
# goes green. Poll silently until the listener is serving, so the SHOWN status is clean.
say "letting the FLP listener finish coming up (so the status below reads clean)…"
[[ "$DRY_RUN" == "1" ]] || for _ in $(seq 1 30); do
  st="$(onvsi "roksbnkctl -w ${FLP_WS} flp status 2>&1")"
  if echo "$st" | grep -qi "listener" && ! echo "$st" | grep -qiE "unable to connect|connection refused|dial tcp|HTTP 000"; then break; fi
  sleep 10
done
onvsi_run "roksbnkctl -w ${FLP_WS} flp status"
FLP_URL="$(onvsi "roksbnkctl -w ${FLP_WS} flp output 2>/dev/null | awk '/flp_external_endpoint:/{print \$2}'" | tr -d '\r' | head -1)"
FLP_CA="$(onvsi "roksbnkctl -w ${FLP_WS} flp output 2>/dev/null | awk '/flp_root_ca:/{print \$2}'" | tr -d '\r' | head -1)"
FLP_FIP="$(onvsi "roksbnkctl -w ${FLP_WS} flp output 2>/dev/null | awk '/flp_floating_ip:/{print \$2}'" | tr -d '\r' | head -1)"
ok "FLP up — cluster endpoint ${B}${FLP_URL}${N} (private, over TGW); web UI ${B}http://${FLP_FIP}/${N}"
endphase P3

# ============================ Phase 4: adopt the existing cluster ============
pause; phase P4 "PHASE 4/5  —  Adopt the existing private cluster + trust Harbor on its nodes"
say "cluster: {create: false, name: ${EXISTING_CLUSTER}} adopts the running cluster. The registry"
say "points at Harbor's PRIVATE IP (over the TGW), and bnk.flp.external at the FLP from Phase 3."
onvsi "cat > /home/ubuntu/${BNK_WS}.yaml <<YAML
ibmcloud: { region: ${BNK_REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${BNK_WS}
tf_source: { type: embedded }
cluster: { create: false, name: ${EXISTING_CLUSTER} }
resources:
  transit_gateway: { create: false, existing: ${TGW_NAME} }
  registry_cos:    { create: false }
  cert_manager:    { create: true }
  bnk:             { create: true }
  tgw_jumphost:    { create: false }
  cluster_jumphosts: { create: false }
  client_vpc:      { create: false }
registry:
  target: generic
  generic_host: ${HARBOR_PRIVATE_IP}
  generic_repo_prefix: ${HARBOR_PROJECT}
  generic_username: admin
  generic_password_b64: ${HARBOR_PW_B64}
  generic_ca_b64: ${HARBOR_CA_B64}
  generic_ca_sha256: ${HARBOR_CA_SHA256}
bnk:
  manifest_version: ${MANIFEST_VERSION}
  far_auth_local_file: /home/ubuntu/far-auth.tgz
  subscription_jwt_local_file: /home/ubuntu/subscription.jwt
  license_mode: f5licenseproxy
  flp:
    external:
      url: ${FLP_URL}
      root_ca_b64: ${FLP_CA}
YAML"
onvsi_run "roksbnkctl -w ${BNK_WS} init --config-file /home/ubuntu/${BNK_WS}.yaml"
say "Adopt the running cluster: 'cluster register' discovers it + records cluster-outputs.json"
say "(which activates bnk up's reuse path), then pull its admin kubeconfig for kubectl access."
onvsi_run "roksbnkctl -w ${BNK_WS} cluster register ${EXISTING_CLUSTER}"
onvsi_run "roksbnkctl -w ${BNK_WS} kubeconfig --download"
say "Copy the mirror record into the BNK workspace. registry replicate records Harbor's host + CA"
say "in it, so bnk up knows exactly what to trust."
onvsi "cp ~/.roksbnkctl/${MIRROR_WS}/registry-mirror.json ~/.roksbnkctl/${BNK_WS}/registry-mirror.json 2>/dev/null || true"
say "bnk up trusts Harbor on every node: before pulling, it installs the recorded CA into each"
say "node's certs.d via a self-refreshing DaemonSet (node-cached image, no egress) and gates on it"
say "landing cluster-wide. ROKS blocks the native image.config path, so per-node trust is the"
say "supported air-gap approach; a cluster or Harbor rebuild auto-rolls the CA."
ok "cluster adopted; mirror record (host + CA) copied; bnk up handles node CA trust natively"
endphase P4

# ============================ Phase 5: disconnected bnk up ===================
pause; phase P5 "PHASE 5/5  —  Install BNK — disconnected, ONE pass (images from Harbor, licensing via FLP)"   # LONG
say "bnk up resolves every chart/image from Harbor's mirror record and licenses through the FLP."
say "bnk up gates the F5SPKVlan applies on a dry-run admission probe, so the CRs land in one pass."
# Silent workaround for the f5-spk-cwc Multi-Attach deadlock, BNK 2.3 ONLY.
#
# F5 defect on 2.3: the cluster-wide-controller Deployment mounts a ReadWriteOnce PVC but
# ships with a RollingUpdate strategy, so an FLO rollover deadlocks the new pod on the RWO
# volume and stalls licensing. This launches a background guard on the VSI that forces the
# cwc to Recreate and clears the deadlock, so the license activates within this bnk up.
#
# FIXED IN 2.4 (#169): the 2.4 Deployment ships `strategy: Recreate` itself — observed on
# a live 2.4.0-EA cluster, at revision 1 with no patch annotations, so it is the product's
# own and not something a guard applied. Recreate makes the deadlock structurally
# impossible: the old pod is terminated before the new one is created, so the RWO volume is
# always released before anything attaches it.
#
# Gated, not removed. 2.3 still ships RollingUpdate and is still the default manifest, and
# dropping the guard there reintroduces a hang that surfaces as bnk up's 15-minute license
# gate timing out with no useful error.
[[ "$DRY_RUN" == "1" || "$(bnk_line_of "$MANIFEST_VERSION")" != "2.3" ]] || {
  scp -i "$(ssh_key)" $SSH_OPTS "$HERE/cwc-guard.sh" ubuntu@"$HARBOR_FIP":/home/ubuntu/cwc-guard.sh >/dev/null 2>&1
  onvsi "setsid bash /home/ubuntu/cwc-guard.sh >/home/ubuntu/cwc-guard.log 2>&1 </dev/null & echo cwc-guard-launched" >/dev/null 2>&1
}
begin_long
onvsi_run "roksbnkctl -w ${BNK_WS} bnk up --auto"
end_long
ok "Disconnected BNK install complete — verifying:"
onvsi_run "roksbnkctl -w ${BNK_WS} k get pods -n f5-bnk"
onvsi_run "roksbnkctl -w ${BNK_WS} k get licenses.k8s.f5net.com -A"
endphase P5

banner "DEMO COMPLETE"
cat >&2 <<EOF
Disconnected, onto an existing cluster:
  - Harbor mirror + standalone FLP (floating IP) in ${SVC_REGION} (the only egress)
  - EXISTING private cluster ${EXISTING_CLUSTER} in ${BNK_REGION}, adopted over the TGW
  - BNK installed entirely from Harbor + licensed via the FLP — one pass

Reachable web UIs (explore before you tear down):
  Harbor registry:  https://${HARBOR_FIP}/     (login: admin / your HARBOR_ADMIN_PASSWORD)
  FLP status:       http://${FLP_FIP}/          (read-only status, no login)

Tear it all down when finished (leaves the adopted cluster ${EXISTING_CLUSTER} untouched):
  ./disconnected-cluster-cli-demo.sh teardown

Phase timestamps for the 10x post-process: ${TS_FILE}
EOF
