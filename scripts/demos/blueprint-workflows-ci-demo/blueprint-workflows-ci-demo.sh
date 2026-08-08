#!/usr/bin/env bash
# =============================================================================
# blueprint-workflows-ci-demo.sh  (roksbnkctl v1.42.0)
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
RUNNER_TAG="${RUNNER_TAG:-v1.42.0}"
RUNNER_IMAGE="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# The four values that must never reach the ConfigMap, the Argo UI or a log.
SECRET_KEYS=(IBMCLOUD_API_KEY ROKSBNKCTL_GENERIC_PASSWORD ROKSBNKCTL_BIGIP_PASSWORD BNK_FORGE_PASSWORD)

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

# ============================ teardown =======================================
# Removes what the WORKFLOWS created, newest-dependency first, then the substrate.
# Adopted clusters are never destroyed — existing-* registered them, so roksbnkctl
# does not own them.
teardown(){
  local -a TEARDOWN_ONLY=("$@")
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  secret "$IBMCLOUD_API_KEY" "${ROKSBNKCTL_GENERIC_PASSWORD:-}" "${BNK_FORGE_PASSWORD:-}" "${ROKSBNKCTL_BIGIP_PASSWORD:-}"
  banner "TEARDOWN — blueprint workflows"
  say "Each down destroys from the workspace state on the PVC — which is why the pod MUST"
  say "mount it, and why the PVC is not an emptyDir. Without the mount there is no"
  say "terraform state to destroy from and no mirror record for bnk down to read."
  say "init is re-run first so the config is rebuilt from the SAME environment the apply"
  say "used. That matters most for the transit gateway: without it the restored config"
  say "would come back with create:true and terraform would try to DELETE a gateway the"
  say "mirror, the FLP and other VPCs are still attached to."
  # A pod that mirrors the workflow container exactly: same image, same PVC, same env
  # carriers. Anything less cannot tear down what the workflows built.
  local overrides
  overrides=$(cat <<JSON
{"spec":{"serviceAccountName":"bnk-runner",
 "containers":[{"name":"teardown","image":"$RUNNER_IMAGE","workingDir":"/work",
  "command":["sh","-ec"],"args":["CMDS"],
  "envFrom":[{"configMapRef":{"name":"bnk-env"}},{"secretRef":{"name":"bnk-secrets"}}],
  "env":[{"name":"ROKSBNKCTL_HOME","value":"/work/.roksbnkctl"},{"name":"HOME","value":"/home/runner"}WSENV],
  "volumeMounts":[{"name":"work","mountPath":"/work"}]}],
 "volumes":[{"name":"work","persistentVolumeClaim":{"claimName":"bnk-work"}}]}}
JSON
)
  # One workspace per cluster (see the README), so teardown walks each in turn.
  # `tgw disconnect` comes BETWEEN bnk down and cluster down: cluster down refuses
  # while a connection exists, because the connection pins the VPC's CRN and the VPC
  # delete would fail. Disconnecting only removes THIS cluster's connection — the
  # shared gateway and everyone else's connections stay, which is what the
  # disconnected pair needs since it adopted a gateway it does not own.
  for pair in "bnkdisco:bnk down --auto" "bnkdisco:tgw disconnect --auto" "bnkdisco:cluster down --auto" \
              "bnkconn:bnk down --auto"  "bnkconn:tgw disconnect --auto"  "bnkconn:cluster down --auto" \
              "flp:flp down --auto"; do
    ws="${pair%%:*}"; verb="${pair##*:}"
    # Optional workspace filter: `teardown bnkdisco`. Running all six workflows means
    # TWO clusters and therefore two prefixes, and teardown rebuilds each workspace's
    # config from the CURRENT bnk-env — so each phase must be torn down with the
    # environment it was built with, one at a time.
    if (( ${#TEARDOWN_ONLY[@]} )); then
      local want=0 w
      for w in "${TEARDOWN_ONLY[@]}"; do [[ "$w" == "$ws" ]] && want=1; done
      (( want )) || continue
    fi
    # Carry the SAME env the workflow pinned in its own container. bnk-env alone is
    # NOT what the apply ran with: each workflow overrides some settings in its `env:`
    # block, and those never reach the ConfigMap. Re-running `init --override-from-env`
    # with only bnk-env therefore REWRITES the workspace config into something the apply
    # never used. That is not theoretical — flp down failed with "no cluster-outputs.json
    # was found for workspace flp" because ROKSBNKCTL_FLP_MODE=vsi lives only in
    # wf-flp-vsi.yaml, so the rebuilt config lost the standalone-VSI path entirely.
    # NOTE: `kubectl run --env` is useless here — --overrides replaces the whole
    # container spec, so the pins must be injected INTO the JSON below.
    local wsenv=""
    case "$ws" in
      flp)
        wsenv=',{"name":"ROKSBNKCTL_FLP_MODE","value":"vsi"},{"name":"ROKSBNKCTL_CLUSTER_CREATE","value":"false"},{"name":"ROKSBNKCTL_CLUSTER_NAME","value":"none"}' ;;
      bnkconn)
        # the connected pair deliberately has NO registry; see the workspace split in the README
        wsenv=',{"name":"ROKSBNKCTL_GENERIC_HOST","value":""},{"name":"ROKSBNKCTL_GENERIC_CA_B64","value":""},{"name":"ROKSBNKCTL_GENERIC_USERNAME","value":""},{"name":"ROKSBNKCTL_GENERIC_PASSWORD","value":""}' ;;
    esac
    local overrides_ws="${overrides//WSENV/$wsenv}"
    say "── roksbnkctl -w $ws $verb"
    show "roksbnkctl -w $ws init --non-interactive --override-from-env && roksbnkctl -w $ws $verb"
    [[ "$DRY_RUN" == "1" ]] && continue
    kubectl -n "$ARGO_NAMESPACE" run "teardown-$ws-$RANDOM" --rm -i --restart=Never \
      --image="$RUNNER_IMAGE" \
      --overrides="${overrides_ws//CMDS/roksbnkctl init -w $ws --non-interactive --override-from-env; roksbnkctl -w $ws $verb}" \
      2>&1 | tail -20
  done
  say "The substrate (namespace, PVC, env carriers) is left in place — delete it with:"
  say "  kubectl delete ns $ARGO_NAMESPACE"
  ok "teardown complete — any ADOPTED cluster was left running"
}
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
  bash "$HERE/../lib/bootstrap-services.sh"
  set -a; . "${BOOTSTRAP_STATE:-$HERE/../.bootstrap-state}/services.env"; set +a
  bash "$HERE/../lib/bootstrap-argo.sh"
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
  [flp-vsi]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_FLP_VSI_VPC ROKSBNKCTL_FLP_VSI_ZONE ROKSBNKCTL_FLP_VSI_SSH_KEY"
  [new-cluster]="ROKSBNKCTL_COS_BUCKET"
  # The gateway is REQUIRED here: a disconnected cluster must share one with the
  # mirror it pulls from, or it is isolated from the only thing it can install from.
  [new-cluster-disconnected]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_TRANSIT_GATEWAY_NAME ROKSBNKCTL_GENERIC_HOST ROKSBNKCTL_GENERIC_PASSWORD"
  [existing-cluster]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_CLUSTER_NAME"
  [existing-disconnected]="ROKSBNKCTL_COS_BUCKET ROKSBNKCTL_CLUSTER_NAME ROKSBNKCTL_GENERIC_HOST ROKSBNKCTL_GENERIC_PASSWORD"
)
check_required(){
  local wf missing=() v
  for wf in "$@"; do
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
    ibmcloud tg connections "$tgw" --output json 2>/dev/null \
      | jq -r '.[]? | select(.network_type=="vpc") | "    • \(.name)  [\(.status)]"' >&2 \
      || say "    (could not list — check manually)"
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
  run argo submit -n "$ARGO_NAMESPACE" --wait --log "${wf_params[@]}" "$rendered"
  end_long
  ok "$wf finished"
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
