#!/usr/bin/env bash
# =============================================================================
# blueprint-workflows-ci-demo.sh  (roksbnkctl v1.54.0)
#
# The six BNK Forge blueprints, as Argo Workflows, driven ENTIRELY by environment
# variables. Every setting is rendered into a ConfigMap (non-secret) or a Secret
# (credentials) and attached to every step with envFrom — there is no config.yaml
# anywhere, because a Forge container module and an argv-only CI runner have no
# shell, no prompts and nowhere to stage a file.
#
#   WORKFLOW                            BLUEPRINT                        USE CASE
#   wf-far-mirror                       far-mirror / harbor-registry     1 (mirror half)
#   wf-flp-vsi                          flp-vsi                          2
#   wf-new-cluster                      roks-new-cluster                 3
#   wf-new-cluster-disconnected         roks-new-cluster-disconnected    4
#   wf-existing-cluster                 roks-existing-cluster            5
#   wf-existing-disconnected            roks-disconnected                6
#
# SELECTING WHAT RUNS. Each workflow costs real quota and 45–90 minutes for the
# cluster ones, so nothing runs unless you ask for it:
#
#   ./blueprint-workflows-ci-demo.sh                    # setup + far-mirror only
#   ./blueprint-workflows-ci-demo.sh far-mirror flp-vsi # a chosen subset
#   ./blueprint-workflows-ci-demo.sh all                # every workflow, in order
#   ./blueprint-workflows-ci-demo.sh setup              # render env carriers, submit nothing
#   DRY_RUN=1 ./blueprint-workflows-ci-demo.sh all      # print every command, run none
#
# Requires: kubectl + argo CLI pointed at a cluster running Argo Workflows, and a
# registry/FLP as noted per workflow in the README.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

ARGO_NAMESPACE="${ARGO_NAMESPACE:-bnk-ci}"
RUNNER_TAG="${RUNNER_TAG:-v1.54.0}"
RUNNER_IMAGE="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# The four values that must never reach the ConfigMap, the Argo UI or a log.
# Anything credential-grade goes to the Secret, never the ConfigMap. The
# ConfigMap is deliberately rendered into the Argo UI and printed by this script
# — that visibility is its purpose — so a credential landing there is exposed by
# design, not by accident.
SECRET_KEYS=(IBMCLOUD_API_KEY ROKSBNKCTL_GENERIC_PASSWORD ROKSBNKCTL_BIGIP_PASSWORD BNK_FORGE_PASSWORD ROKSBNKCTL_GTM_PASSWORD)

# Every workflow, in dependency order: the mirror and the proxy must exist before
# anything disconnected can use them.
#
# `all` is NOT unattended. The two disconnected variants must share one Transit
# Gateway to reach the mirror, and only one roksbnkctl-created cluster VPC may be
# attached to a gateway at a time (they all get the same address prefixes — see
# check_tgw_exclusivity). So the reuse variants must adopt the cluster the build
# variants just made (ROKSBNKCTL_CLUSTER_NAME = ROKSBNKCTL_PREFIX), with BNK removed
# between them by `roksbnkctl bnk down` — never by destroying the module, which
# cascades into the cluster. The guard refuses the combination that gets this wrong.
ALL_WORKFLOWS=(far-mirror flp-vsi new-cluster new-cluster-disconnected existing-cluster existing-disconnected)

# ============================ submit_wf ======================================
# submit_wf <workflow-basename> [argo params…] — render, submit, and VERIFY.
#
# The single place a workflow is run. Teardown used to bypass this entirely with
# `kubectl run`, which meant the half of the lifecycle that costs money was the half
# no pipeline could execute and no one could watch in the Argo UI.
#
# Defined HERE, above teardown(), because the `teardown` dispatch line executes as
# the script is read — a helper defined further down does not exist yet at that
# point, and bash reports it as `submit_wf: command not found` after the banner has
# already printed, which reads like a broken workflow rather than a missing function.
#
# Returns non-zero unless the workflow reaches Succeeded. `argo submit --wait` is not
# trusted here: its watch is a long-lived connection, and through an SSH tunnel a
# blip makes it return early.
submit_wf(){
  local wf="$1"; shift
  local f="$HERE/workflows/wf-${wf}.yaml"
  [[ -f "$f" ]] || { echo "no such workflow: $wf" >&2; return 1; }
  mkdir -p "$STATE_DIR"
  local rendered="$STATE_DIR/wf-${wf}.rendered.yaml"
  sed "s|PLACEHOLDER_RUNNER_IMAGE|$RUNNER_IMAGE|g" "$f" > "$rendered"
  show "argo submit -n $ARGO_NAMESPACE workflows/wf-${wf}.yaml $*"
  [[ "$DRY_RUN" == "1" ]] && return 0
  local name
  name="$(argo submit -n "$ARGO_NAMESPACE" -o name "$@" "$rendered" 2>/dev/null | tail -1)"
  name="${name#workflow.argoproj.io/}"
  [[ -n "$name" ]] || { echo "${R}${B}submit failed for $wf${N}" >&2; return 1; }
  say "submitted $name — following its logs"
  argo logs -n "$ARGO_NAMESPACE" --follow "$name" 2>/dev/null || true
  local ph=""
  for _ in $(seq 1 720); do
    ph="$(kubectl get wf -n "$ARGO_NAMESPACE" "$name" -o jsonpath='{.status.phase}' 2>/dev/null)"
    case "$ph" in Succeeded|Failed|Error) break ;; esac
    sleep 10
  done
  if [[ "$ph" != Succeeded ]]; then
    echo "${R}${B}$wf ended in phase '${ph:-unknown}'.${N}  argo logs -n $ARGO_NAMESPACE $name" >&2
    return 1
  fi
  ok "$wf finished ($name)"
}

# ============================ teardown =======================================
# Removes what the WORKFLOWS created, newest-dependency first, then the substrate.
# Adopted clusters are never destroyed — existing-* registered them, so roksbnkctl
# does not own them.
teardown(){
  local -a ONLY=("$@")
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  secret "$IBMCLOUD_API_KEY" "${ROKSBNKCTL_GENERIC_PASSWORD:-}" "${BNK_FORGE_PASSWORD:-}" "${ROKSBNKCTL_BIGIP_PASSWORD:-}"
  banner "TEARDOWN — blueprint workflows"
  say "Teardown runs as WORKFLOWS, the same way everything else here does — so it is"
  say "visible in the Argo UI, re-runnable, and executable by a pipeline rather than"
  say "only from an operator's laptop."
  say ""
  say "Each down destroys from the workspace state on the PVC, which is why the pod"
  say "MUST mount it and why the PVC is not an emptyDir: without it there is no"
  say "terraform state to destroy from and no mirror record for bnk down to read."

  # Disconnected first: it is the one attached to the SHARED gateway, and leaving it
  # attached blocks nothing else but costs the most to forget.
  local -a plan=(bnkdisco:down-disconnected bnkconn:down-connected flp:down-flp-vsi)
  local rc=0 pair ws wf
  for pair in "${plan[@]}"; do
    ws="${pair%%:*}"; wf="${pair##*:}"
    if (( ${#ONLY[@]} )); then
      local want=0 w
      for w in "${ONLY[@]}"; do [[ "$w" == "$ws" ]] && want=1; done
      (( want )) || continue
    fi
    say "── $ws  ($wf)"
    # phase=all: bnk down → tgw disconnect → cluster down. The FLP workflow takes no
    # phase — there is only one thing to destroy.
    if [[ "$wf" == down-flp-vsi ]]; then
      submit_wf "$wf" || rc=1
    else
      submit_wf "$wf" -p phase=all || rc=1
    fi
  done

  echo >&2
  say 'What teardown does NOT remove — the substrate `bootstrap` created:'
  say "  the Argo VSI (the host these very workflows run on), Harbor, the services"
  say "  VPC and its gateway attachment. A workflow cannot destroy the node it is"
  say "  scheduled on, so that step has to come from OUTSIDE the cluster:"
  say "      ./blueprint-workflows-ci-demo.sh unbootstrap"
  (( rc == 0 )) && ok "teardown complete — any ADOPTED cluster was left running" \
                || echo "${R}${B}teardown had failures — see the workflows above.${N}" >&2
  return $rc
}

[[ "${1:-}" == "unbootstrap" ]] && {
  shift
  # Deliberately NOT a workflow: it deletes the Argo VSI those workflows run on.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  TGW_NAME="${ROKSBNKCTL_TRANSIT_GATEWAY_NAME:-${TGW_NAME:-bnkci-testing}}" \
    bash "$HERE/../lib/unbootstrap.sh" "$@"; exit $?; }
[[ "${1:-}" == "teardown" ]] && { shift; teardown "$@"; exit 0; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — THE SIX BNK FORGE BLUEPRINTS AS ARGO WORKFLOWS"
cat >&2 <<EOF
Every setting is an ${B}environment variable${N}. No config.yaml, anywhere.
  1. ${B}wf-far-mirror${N}                mirror F5's artifact registry into a private one
  2. ${B}wf-flp-vsi${N}                   a standalone F5 License Proxy on a VSI
  3. ${B}wf-new-cluster${N}               new VPC + CONNECTED cluster + TGW, install BNK
  4. ${B}wf-new-cluster-disconnected${N}  the same, DISCONNECTED: mirror + proxy
  5. ${B}wf-existing-cluster${N}          adopt a running CONNECTED cluster, install BNK
  6. ${B}wf-existing-disconnected${N}     adopt a running DISCONNECTED cluster, install BNK
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"
# These demos are RECORDED: register every credential so nothing reaches the screen.
secret "$IBMCLOUD_API_KEY" "${ROKSBNKCTL_GENERIC_PASSWORD:-}" "${BNK_FORGE_PASSWORD:-}" "${ROKSBNKCTL_BIGIP_PASSWORD:-}"
# ── bootstrap: build what is missing rather than demanding it ────────────────
# `bootstrap` provisions the whole substrate from a CLEAN SLATE — SSH key, services
# VPC, its gateway attachment, Harbor, and the k3s + Argo Workflows controller. The
# ONLY thing it will not create is the global transit gateway itself.
#
# It is idempotent, so re-running is safe, and it is opt-in: an operator who already
# has Harbor and an Argo controller sets the values in .env and never calls it.
if [[ "${1:-}" == "bootstrap" ]]; then
  say "bootstrapping the services substrate + Argo controller (clean slate)…"
  # This script runs without `set -e` on purpose — a failed demo step should not
  # kill the narration. The bootstrap is the exception: each stage FEEDS the next
  # (services.env carries the VPC, subnet and key that the Argo stage requires), so
  # a swallowed failure here does not degrade, it cascades. It did exactly that
  # once: the Harbor CA fetch failed, services.env was never written, the Argo
  # stage died on an unset SERVICES_VPC, and the script still printed
  # "BOOTSTRAP COMPLETE" and exited 0 — three failures reported as success.
  _bs="${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}"
  bash "$HERE/../lib/bootstrap-services.sh" \
    || { echo "${R}${B}bootstrap-services.sh failed — stopping.${N} Nothing below it can work." >&2; exit 1; }
  [[ -r "$_bs/services.env" ]] \
    || { echo "${R}${B}bootstrap-services.sh did not write services.env — stopping.${N}" >&2
         echo "  It carries SERVICES_VPC / SERVICES_SUBNET / SSH_KEY_FILE, which the Argo stage requires." >&2
         exit 1; }
  set -a; . "$_bs/services.env"; set +a
  bash "$HERE/../lib/bootstrap-argo.sh" \
    || { echo "${R}${B}bootstrap-argo.sh failed — stopping.${N} Harbor is up; re-run bootstrap to continue." >&2; exit 1; }
  banner "BOOTSTRAP COMPLETE"
  cat >&2 <<EOF
Fold the generated values into .env (they are written to
${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}/services.env and argo.env):

  ROKSBNKCTL_GENERIC_HOST      = the Harbor PRIVATE ip
  ROKSBNKCTL_GENERIC_PASSWORD  = the generated Harbor admin password
  ROKSBNKCTL_GENERIC_CA_B64    = Harbor's CA, from the file that generated it
  ROKSBNKCTL_FLP_VSI_VPC       = the services VPC
  KUBECONFIG                   = the Argo controller, reached over an ssh tunnel

Then open the tunnel and run: ./blueprint-workflows-ci-demo.sh far-mirror flp-vsi
EOF
  exit 0
fi

for c in kubectl argo; do command -v "$c" >/dev/null || die "$c not found — run './blueprint-workflows-ci-demo.sh bootstrap' first, or install them"; done
[[ "$DRY_RUN" == "1" ]] || kubectl cluster-info >/dev/null 2>&1 || die "kubectl cannot reach a cluster (set KUBECONFIG)"
ok "preflight: kubectl + argo present, runner $RUNNER_IMAGE"

# ── required-input preflight ─────────────────────────────────────────────────
# Required means "the install cannot succeed without it". These are checked BEFORE
# anything is submitted, because the alternative is what the blueprints just hit: a
# single blank ROKSBNKCTL_COS_BUCKET failed a run FORTY MINUTES IN — `bnk up` cannot
# reach the entitlement without it and nothing surfaced the omission until then.
#
# cos_instance / far_auth_file / subscription_jwt_file are deliberately NOT here:
# roksbnkctl supplies working defaults for those. cos_bucket has none, because the
# bucket is account-suffixed (bnk-artifacts-<account>).
declare -A WF_REQUIRES=(
  [far-mirror]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_GENERIC_HOST ROKSBNKCTL_GENERIC_PASSWORD"
  # FLP_VSI_VPC is deliberately NOT here: the proxy can adopt a VPC or build its
  # own (#60), and requiring the adopt variable would forbid the create path
  # outright. The either/or is checked in check_flp_vsi_network below, which can
  # say which of the two is missing — a flat "required" list cannot.
  [flp-vsi]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_FLP_VSI_ZONE ROKSBNKCTL_FLP_VSI_SSH_KEY"
  [new-cluster]="ROKSBNKCTL_COS_BUCKET"
  # The gateway is REQUIRED here: a disconnected cluster must share one with the
  # mirror it pulls from, or it is isolated from the only thing it can install from.
  [new-cluster-disconnected]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_TRANSIT_GATEWAY_NAME ROKSBNKCTL_GENERIC_HOST ROKSBNKCTL_GENERIC_PASSWORD"
  [existing-cluster]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_CLUSTER_NAME"
  [existing-disconnected]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_CLUSTER_NAME ROKSBNKCTL_GENERIC_HOST ROKSBNKCTL_GENERIC_PASSWORD"
)
# The FLP VSI needs a network, one of two ways, and they are mutually exclusive:
#
#   adopt   ROKSBNKCTL_FLP_VSI_VPC=<vpc id>
#   create  ROKSBNKCTL_FLP_VSI_CREATE_VPC=true  (+ optional name / subnet cidr)
#
# Checked BEFORE submission for the same reason everything else here is: the
# alternative is discovering it deep into a run that has already built things.
# roksbnkctl refuses both-at-once itself, but its refusal arrives inside a
# container in Argo, where the operator has to go looking for it.
check_flp_vsi_network(){
  local adopt="${ROKSBNKCTL_FLP_VSI_VPC:-}" create="${ROKSBNKCTL_FLP_VSI_CREATE_VPC:-}"
  case "$create" in true|TRUE|1|yes) create=1 ;; *) create=0 ;; esac
  if [[ -n "$adopt" && "$create" == "1" ]]; then
    { echo; echo "${R}${B}Refusing: ROKSBNKCTL_FLP_VSI_VPC and ROKSBNKCTL_FLP_VSI_CREATE_VPC are mutually exclusive.${N}"
      echo "  Adopt an existing VPC, or build one — not both."; echo; } >&2
    exit 2
  fi
  if [[ -z "$adopt" && "$create" != "1" ]]; then
    { echo; echo "${R}${B}Refusing: the FLP VSI has no network.${N}"
      echo "  Either adopt one:   ROKSBNKCTL_FLP_VSI_VPC=<vpc-id>"
      echo "  or build its own:   ROKSBNKCTL_FLP_VSI_CREATE_VPC=true"
      echo "                      ROKSBNKCTL_FLP_VSI_SUBNET_CIDR=10.250.0.0/24   # optional"
      echo "                      ROKSBNKCTL_FLP_VSI_VPC_NAME=flp-vsi-vpc        # optional"
      echo; } >&2
    exit 2
  fi
}

check_required(){
  local wf missing=() v
  for wf in "$@"; do
    [[ "$wf" == "flp-vsi" ]] && check_flp_vsi_network
    for v in ${WF_REQUIRES[$wf]:-}; do
      [[ -n "${!v:-}" ]] || missing+=("$v (needed by $wf)")
    done
  done
  # A self-signed mirror needs its CA supplied or pinned — replicate REFUSES to adopt
  # one it merely discovered over the wire, and that refusal is fatal by design.
  for wf in "$@"; do
    case "$wf" in
      far-mirror|*disconnected)
        [[ -n "${ROKSBNKCTL_GENERIC_CA_B64:-}${ROKSBNKCTL_GENERIC_CA_SHA256:-}" ]] || \
          missing+=("ROKSBNKCTL_GENERIC_CA_B64 or _CA_SHA256 (needed by $wf — a self-signed mirror CA must be supplied or pinned)")
        ;;
    esac
  done
  ((${#missing[@]})) || return 0
  { echo; echo "${R}${B}Missing required settings — nothing was submitted:${N}"
    printf '  • %s\n' "${missing[@]}"
    echo; echo "Set them in .env. Fail-fast is deliberate: a blank COS bucket is the field that"
    echo "failed a blueprint run forty minutes in, with nothing surfacing the omission."; } >&2
  exit 2
}

# ── one cluster VPC per Transit Gateway ──────────────────────────────────────
# roksbnkctl gives EVERY cluster VPC it creates the SAME address prefixes —
# 10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18. Two such VPCs on one gateway
# overlap, and the gateway cannot route to both: traffic is ambiguous and silently
# blackholed.
#
# It does not present as a routing error. It presents as INTERMITTENT IMAGE PULLS:
#
#   Failed to pull image "10.243.0.4/bnk-mirror/…":
#     dial tcp 10.243.0.4:443: connect: connection timed out
#
# Some pulls succeed, some time out, and every security group and network ACL in the
# path allows the traffic — which sends you looking at firewalls. This cost an hour
# on the blueprint side before anyone checked CIDRs. Hence a guard here.
#
# Detaching is enough; the cluster need not be destroyed. `roksbnkctl -w bnk tgw
# disconnect --auto` frees the gateway.
# tgw_id — resolve a Transit Gateway NAME to its id.
#
# `ibmcloud tg connections` accepts an ID only; handed a name it fails with
# "The gateway was not found." Everything below used the name, and the failure was
# swallowed by `2>/dev/null` plus an `|| say "(could not list)"` fallback — so the
# attachment listing silently printed nothing for as long as it has existed.
tgw_id(){
  local name="$1"
  ibmcloud tg gateways --output json 2>/dev/null \
    | jq -r --arg n "$name" '.[]? | select(.name==$n) | .id' | head -1
}

# check_tgw_prefix_overlap — does OUR cluster VPC block collide with a VPC that is
# ALREADY on the gateway?
#
# Listing the attachments by name is not enough, and this is the case that proved it:
# a long-lived `app-eu-gb-1` sat on the shared gateway holding 10.242.0.0/16, and the
# demo's own ROKSBNKCTL_CLUSTER_VPC_CIDR was 10.242.0.0/16. Nothing in the name says
# so. The attachment is not a "roksbnkctl-created cluster VPC", so the old check
# printed it and moved on — while the gateway would have blackholed one of the two.
#
# The symptom is never a routing error. It is intermittent image pulls from the
# mirror, with every security group and ACL in the path allowing the traffic.
#
# Attached VPCs may live in ANY region (the gateway is global-routing), so the region
# is parsed out of each connection's network CRN rather than assumed to be ours.
check_tgw_prefix_overlap(){
  local tgw="$1" ours="${ROKSBNKCTL_CLUSTER_VPC_CIDR:-}"
  [[ -n "$ours" ]] || return 0
  command -v python3 >/dev/null 2>&1 || return 0

  local gwid; gwid="$(tgw_id "$tgw")"
  [[ -n "$gwid" ]] || { note "Could not resolve gateway ${tgw} to an id — prefix overlap NOT checked."; return 0; }
  local conns; conns="$(ibmcloud tg connections "$gwid" --output json 2>/dev/null)" || return 0
  local overlaps=""
  while IFS=$'\t' read -r cname crn; do
    [[ -n "$crn" ]] || continue
    local region vpcid prefixes
    region="$(cut -d: -f6 <<<"$crn")"
    # CRN tail after "::" is "vpc:<id>", so the id is field 2, not 3.
    vpcid="$(awk -F:: '{print $2}' <<<"$crn" | cut -d: -f2)"
    [[ -n "$region" && -n "$vpcid" ]] || continue
    # `ibmcloud is` has NO --region flag; the region is global CLI state, so it has
    # to be switched per VPC and restored afterwards. Attached VPCs are frequently
    # in another region — the gateway is global-routing, which is the whole point.
    ibmcloud target -r "$region" -q >/dev/null 2>&1 || continue
    prefixes="$(ibmcloud is vpc-address-prefixes "$vpcid" --output json 2>/dev/null \
                | jq -r '.[]?.cidr' | tr '\n' ' ')" || true
    [[ -n "$prefixes" ]] || continue
    if ! python3 - "$ours" $prefixes <<'PYEOF'
import ipaddress, sys
mine = ipaddress.ip_network(sys.argv[1])
for other in sys.argv[2:]:
    if mine.overlaps(ipaddress.ip_network(other)):
        sys.exit(1)
sys.exit(0)
PYEOF
    then
      overlaps+="    • ${cname} (${region}): ${prefixes}"$'\n'
    fi
  done < <(jq -r '.[]? | select(.network_type=="vpc") | "\(.name)\t\(.network_id)"' <<<"$conns")
  # Put the CLI back where we found it, or every later command silently runs in
  # whichever region the last attachment happened to live in.
  [[ -n "${ROKSBNKCTL_REGION:-}" ]] && ibmcloud target -r "$ROKSBNKCTL_REGION" -q >/dev/null 2>&1

  [[ -n "$overlaps" ]] || return 0
  { echo; echo "${R}${B}Refusing: ROKSBNKCTL_CLUSTER_VPC_CIDR (${ours}) overlaps a VPC already on ${tgw}.${N}"
    echo "$overlaps"
    echo "  A transit gateway cannot route to two VPCs with overlapping prefixes — it"
    echo "  silently blackholes one. It does NOT surface as a routing error; it surfaces as"
    echo "  intermittent image-pull timeouts from the mirror, with every security group and"
    echo "  ACL in the path allowing the traffic."
    echo
    echo "  Pick a block none of the above uses — the list above is authoritative, so"
    echo "  read it rather than reusing an example: ROKSBNKCTL_CLUSTER_VPC_CIDR=10.245.0.0/16"
    echo "  (split three ways per zone, so /18 is the smallest usable size)."; } >&2
  exit 2
}

check_tgw_exclusivity(){
  local wf shared=0
  for wf in "$@"; do case "$wf" in *disconnected) shared=1 ;; esac; done
  (( shared )) || return 0
  local tgw="${ROKSBNKCTL_TRANSIT_GATEWAY_NAME:-}"
  [[ -n "$tgw" ]] || return 0

  # The deterministic footgun: creating a cluster VPC on the shared gateway while
  # ALSO adopting a DIFFERENT cluster that is already on it. Two cluster VPCs, same
  # prefixes, one gateway. Adopting the cluster the other variant just built is fine
  # — that is one VPC, and it is the sequence the blueprints' CONSTRAINTS.md wants.
  if [[ " $* " == *" new-cluster-disconnected "* && " $* " == *" existing-disconnected "* ]] \
     && [[ "${ROKSBNKCTL_CLUSTER_NAME:-}" != "${ROKSBNKCTL_PREFIX:-}" ]]; then
    { echo; echo "${R}${B}Refusing: two cluster VPCs would share ${tgw}.${N}"
      echo "  new-cluster-disconnected CREATES a cluster VPC on ${tgw};"
      echo "  existing-disconnected ADOPTS ${ROKSBNKCTL_CLUSTER_NAME:-<unset>}, a different cluster on it."
      echo "  roksbnkctl gives both the SAME address prefixes, so the gateway blackholes one —"
      echo "  surfacing as intermittent image-pull timeouts, not as a routing error."
      echo
      echo "  Run them separately, or point ROKSBNKCTL_CLUSTER_NAME at the cluster the"
      echo "  disconnected build creates (=\$ROKSBNKCTL_PREFIX) so both act on ONE VPC."
      echo "  Between runs, free the gateway with: roksbnkctl -w bnk tgw disconnect --auto"; } >&2
    exit 2
  fi

  # Anything else needs eyes on what is already attached. The runner image has no
  # tg-cli plugin, so this is a best-effort check from THIS host.
  if command -v ibmcloud >/dev/null 2>&1 && ibmcloud tg --help >/dev/null 2>&1; then
    say "VPCs currently attached to ${tgw} — only ONE may be a roksbnkctl-created cluster VPC:"
    local _gwid; _gwid="$(tgw_id "$tgw")"
    if [[ -n "$_gwid" ]]; then
      ibmcloud tg connections "$_gwid" --output json 2>/dev/null \
        | jq -r '.[]? | select(.network_type=="vpc") | "    • \(.name)  [\(.status)]"' >&2 \
        || say "    (could not list — check manually)"
    else
      say "    (gateway ${tgw} not found in this account — check manually)"
    fi
    # ONLY the variant that CREATES a cluster VPC on the shared gateway can collide.
    # `existing-disconnected` adopts a cluster that is already attached, so its VPC
    # is on the gateway by definition — checking ROKSBNKCTL_CLUSTER_VPC_CIDR against
    # it reports the cluster overlapping ITSELF and refuses a perfectly good run.
    # (`new-cluster` builds its own gateway, so it never shares this one.)
    local creates_vpc=0 w2
    for w2 in "$@"; do [[ "$w2" == new-cluster-disconnected ]] && creates_vpc=1; done
    if (( creates_vpc )); then
      check_tgw_prefix_overlap "$tgw"
    else
      say "adopt-only run — the cluster VPC already exists on ${tgw}, so no prefix check applies"
    fi
  else
    note "No ibmcloud tg plugin on this host, so the gateway's attachments were not checked.
  Confirm only ONE roksbnkctl-created cluster VPC is attached to ${tgw} before continuing —
  a second one overlaps its address prefixes and the gateway silently blackholes traffic."
  fi
}

# Which workflows were asked for?
REQUESTED=("$@")
[[ ${#REQUESTED[@]} -eq 0 ]] && REQUESTED=(far-mirror)
[[ "${REQUESTED[0]:-}" == "all" ]] && REQUESTED=("${ALL_WORKFLOWS[@]}")
SETUP_ONLY=0; [[ "${REQUESTED[0]:-}" == "setup" ]] && { SETUP_ONLY=1; REQUESTED=(); }
((${#REQUESTED[@]})) && { check_required "${REQUESTED[@]}"; check_tgw_exclusivity "${REQUESTED[@]}"; ok "required settings present for: ${REQUESTED[*]}"; }

# ============================ Phase 1: the substrate =========================
pause; phase P1 "PHASE 1/3  —  The substrate: namespace, workspace PVC, RBAC"
say "The PVC is the part that matters. roksbnkctl keeps its config, terraform state and"
say "the mirror record under /work/.roksbnkctl, and every 'down' reads that state — an"
say "emptyDir would orphan the IAM trusted profile and leave terraform nothing to"
say "destroy from."
run kubectl apply -f "$HERE/workflows/00-prereqs.yaml"
say "The FLP handoff needs to write one Secret; that RBAC is a separate file so a"
say "blanket secret-write grant is never applied by accident."
run kubectl apply -f "$HERE/workflows/01-flp-handoff-rbac.yaml"
endphase P1

# ============================ Phase 2: the environment =======================
pause; phase P2 "PHASE 2/3  —  The whole workspace, as environment variables"
say "Non-secret settings become a ConfigMap so the workspace shape is visible in the"
say "Argo UI and in kubectl — that visibility is the point. The four credentials go to"
say "a Secret and are never rendered, logged or printed."
CM="$STATE_DIR/bnk-env.env"; : > "$CM"
SEC="$STATE_DIR/bnk-secrets.env"; : > "$SEC"
is_secret(){ local k="$1"; for s in "${SECRET_KEYS[@]}"; do [[ "$k" == "$s" ]] && return 0; done; return 1; }
# Split .env: every ROKSBNKCTL_*/BNKFORGE_*/CWC_*/REGISTRY_* key, secrets aside.
while IFS='=' read -r k v; do
  [[ "$k" =~ ^(ROKSBNKCTL_|BNKFORGE_|BNK_FORGE_|CWC_|REGISTRY_COS_NAME|IBMCLOUD_API_KEY) ]] || continue
  [[ -n "${!k:-}" ]] || continue
  if is_secret "$k"; then printf '%s=%s\n' "$k" "${!k}" >> "$SEC"; else printf '%s=%s\n' "$k" "${!k}" >> "$CM"; fi
done < <(grep -oE '^[A-Z_][A-Z0-9_]*=' "$HERE/.env.example" | tr -d '=' | sed 's/$/=/')
chmod 600 "$SEC"
say "$(wc -l < "$CM") settings → bnk-env (ConfigMap); $(wc -l < "$SEC") credentials → bnk-secrets (Secret)."
show_file "$CM"
run kubectl create configmap bnk-env -n "$ARGO_NAMESPACE" --from-env-file="$CM" --dry-run=client -o yaml
[[ "$DRY_RUN" == "1" ]] || kubectl create configmap bnk-env -n "$ARGO_NAMESPACE" --from-env-file="$CM" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
show "kubectl create secret generic bnk-secrets -n $ARGO_NAMESPACE --from-env-file=<credentials>   # values never printed"
[[ "$DRY_RUN" == "1" ]] || kubectl create secret generic bnk-secrets -n "$ARGO_NAMESPACE" --from-env-file="$SEC" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
ok "bnk-env + bnk-secrets applied — a step's environment IS the workspace definition"
endphase P2

[[ "$SETUP_ONLY" == "1" ]] && { banner "SETUP COMPLETE"; say "Submit a workflow with: argo submit -n $ARGO_NAMESPACE --wait workflows/wf-<name>.yaml"; exit 0; }

# ============================ Phase 3: the workflows =========================
pause; phase P3 "PHASE 3/3  —  Submit: ${REQUESTED[*]}"
for wf in "${REQUESTED[@]}"; do
  f="$HERE/workflows/wf-${wf}.yaml"
  [[ -f "$f" ]] || { note "no such workflow: $wf (have: ${ALL_WORKFLOWS[*]})"; continue; }
  say "── $wf"
  rendered="$STATE_DIR/wf-${wf}.rendered.yaml"
  sed "s|PLACEHOLDER_RUNNER_IMAGE|$RUNNER_IMAGE|g" "$f" > "$rendered"
  # Per-workflow parameters. flp-status only exists when a status image was built
  # and pushed, so the check is opt-in: asserting on an optional component that was
  # never configured would fail a run whose real output (the flp-handoff Secret)
  # is perfectly good.
  wf_params=()
  if [[ "$wf" == "flp-vsi" && -n "${ROKSBNKCTL_FLP_VSI_STATUS_IMAGE:-}" ]]; then
    wf_params+=(-p status-check=true)
    say "  flp-status image configured — enabling the status check"
  fi
  # The cluster workflows default bnkforge=true because the blueprints always
  # register. A bare CI run often has no Forge, and registering against an empty
  # BNK_FORGE_URL fails the run AFTER the cluster build — an hour in, for a
  # bookkeeping step. Decide it from whether a Forge is actually configured.
  if grep -q 'parameters.bnkforge' "$f" 2>/dev/null; then
    if [[ -n "${BNK_FORGE_URL:-}" ]]; then
      wf_params+=(-p bnkforge=true)
      say "  BNK Forge configured — registration enabled"
    else
      wf_params+=(-p bnkforge=false)
      say "  no BNK_FORGE_URL — skipping Forge registration"
    fi
  fi
  begin_long
  # `--wait` is NOT a completion signal on its own. Its watch is a long-lived
  # connection to the API server, and this demo reaches that server through an SSH
  # tunnel; when the tunnel blips, argo logs "Failed to re-establish workflow watch"
  # and RETURNS — so the script cheerfully printed "far-mirror finished" while the
  # workflow was still Running, and every later step ran against a half-built mirror.
  #
  # So submit without --wait, capture the name, and poll the resource itself. The
  # phase in the API is the only thing that actually answers "is it done".
  wf_name="$(argo submit -n "$ARGO_NAMESPACE" -o name "${wf_params[@]}" "$rendered" 2>/dev/null | tail -1)"
  wf_name="${wf_name#workflow.argoproj.io/}"
  if [[ -z "$wf_name" ]]; then
    end_long; echo "${R}${B}submit failed for $wf — nothing to wait on.${N}" >&2; exit 1
  fi
  say "submitted $wf_name — following its logs"
  argo logs -n "$ARGO_NAMESPACE" --follow "$wf_name" 2>/dev/null || true
  # Logs can end early for the same reason; the phase is authoritative.
  wf_phase=""
  for _ in $(seq 1 720); do
    wf_phase="$(kubectl get wf -n "$ARGO_NAMESPACE" "$wf_name" -o jsonpath='{.status.phase}' 2>/dev/null)"
    case "$wf_phase" in Succeeded|Failed|Error) break ;; esac
    sleep 10
  done
  end_long
  if [[ "$wf_phase" != Succeeded ]]; then
    echo "${R}${B}$wf ended in phase '${wf_phase:-unknown}' — stopping.${N}" >&2
    kubectl get wf -n "$ARGO_NAMESPACE" "$wf_name" -o jsonpath='{.status.message}{"\n"}' >&2 2>/dev/null || true
    say "inspect with: argo logs -n $ARGO_NAMESPACE $wf_name"
    exit 1
  fi
  ok "$wf finished ($wf_name, phase=$wf_phase)"
done
endphase P3

banner "DEMO COMPLETE"
cat >&2 <<EOF
Submitted: ${REQUESTED[*]}
  Every step ran with its workspace built from bnk-env + bnk-secrets — no config.yaml.
  Workspace state persists on the bnk-work PVC, so a later 'down' can destroy from it.

Watch or re-inspect:
  argo list -n ${ARGO_NAMESPACE}
  argo logs -n ${ARGO_NAMESPACE} @latest
  kubectl get cm bnk-env -n ${ARGO_NAMESPACE} -o yaml     # the whole workspace, readable

Nothing was torn down. When finished:
  ./blueprint-workflows-ci-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
