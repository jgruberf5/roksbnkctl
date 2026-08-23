#!/usr/bin/env bash
# =============================================================================
# shared-licensing-cli-demo.sh  (roksbnkctl v1.52.0)
#
# A SHARED LICENSING CLUSTER. One cluster runs the F5 License Proxy (FLP) and holds
# the only egress to F5. A second, air-gapped cluster installs BIG-IP Next for
# Kubernetes entirely from a private registry and licenses THROUGH that proxy —
# reaching neither repo.f5.com nor F5's licensing service.
#
#     ┌──────── services cluster (the only egress to F5) ───────┐
#     │  F5 License Proxy ──NodePort 30001──┐                   │
#     └─────────────────────────────────────┼───────────────────┘
#     ┌─────────────────────────────────────┼── app cluster ────┐
#     │  BNK + CNEInstance + CWC ───────────┘                   │
#     │      └── charts + images ── private registry (Harbor)   │
#     └─────────────────────────────────────────────────────────┘
#
# Seven phases:
#   1. adopt the services cluster            (cluster register — not created)
#   2. mirror FAR into the private registry  (bom -> replicate -> verify)
#   3. flp up --add-node-port-access         (the FLP, reachable from next door)
#   4. adopt the app cluster + point it at the FLP and the same registry
#   5. bnk up                                (disconnected + remotely licensed)
#   6. prove it                              (License Active; no FLP in this cluster)
#
# The demo does NOT tear itself down: it ends with a report of every reachable web UI so
# the operator can explore. `./shared-licensing-cli-demo.sh teardown` removes ONLY the
# FLP and BNK it installed — both adopted clusters keep running.
#
# BOTH ROKS CLUSTERS ARE ALREADY RUNNING and are `cluster register`ed, not created.
# Cluster creation is ~40 minutes of nothing to watch and is not what this demo is
# about; registering is instant and is a first-class roksbnkctl flow. Because the
# demo never creates them, it never destroys them.
#
# The private registry (a standard open-source Harbor) is stood up BEFORE recording,
# off-camera — ../lib/deploy-far-registry.sh prints its address and admin password.
#
# Hands-off: AUTO_ADVANCE=1 (default) auto-advances between phases. Emits phase
# timestamps to $TS_FILE so record.sh can 10x the long phases (replicate, flp up,
# bnk up, the downs) and hold each roksbnkctl command on screen for 5s in post.
#
# Linux / WSL. Requires: roksbnkctl v1.32.0, terraform, helm, jq.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

# ============================ CONFIG (edit me) ===============================
REGION="${REGION:-ca-tor}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"
FAR_REPO_URL="${FAR_REPO_URL:-repo.f5.com}"

SERVICES_CLUSTER="${SERVICES_CLUSTER:-}"   # RUNNING cluster that will host the FLP
APP_CLUSTER="${APP_CLUSTER:-}"             # RUNNING cluster that will get BNK
# The app cluster's zone prefixes, opened to the FLP's NodePort. COMMA-SEPARATED,
# and it must list EVERY zone: a multi-zone VPC carries one address prefix per
# zone, and a consuming pod scheduled in an omitted zone is silently dropped at
# the security group.
#   ibmcloud is vpc-address-prefixes <vpc> --output json | jq -r '[.[].cidr]|join(",")'
APP_CLUSTER_CIDR="${APP_CLUSTER_CIDR:-}"

REGISTRY_DOMAIN="${REGISTRY_DOMAIN:-}"                 # the private registry host
REGISTRY_ADMIN_PASSWORD="${REGISTRY_ADMIN_PASSWORD:-}" # its admin password
REGISTRY_PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"

FLP_NAMESPACE="${FLP_NAMESPACE:-f5-license-proxy}"
SVC_WS="${SVC_WS:-services}"               # workspace for the FLP cluster
APP_WS="${APP_WS:-app}"                    # workspace for the BNK cluster
ROKSBNKCTL_BIN="${ROKSBNKCTL_BIN:-roksbnkctl}"

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# ============================ teardown =======================================
# Removes ONLY what this demo installed: BNK from the app cluster, the FLP from the
# services cluster, and the two local workspaces. BOTH ROKS CLUSTERS ARE LEFT RUNNING —
# they were `cluster register`ed, never created, so roksbnkctl does not own them and
# must not destroy them. The private registry is likewise left alone (built off-camera);
# the command to empty its mirror is printed at the end.
# The demo does NOT auto-tear-down, so you can explore the UIs first.
# ---------------------------------------------------------------------------
teardown(){
  # Runs standalone, so resolve the credentials itself.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  secret "$IBMCLOUD_API_KEY" "${REGISTRY_ADMIN_PASSWORD:-}"
  banner "TEARDOWN — shared-licensing CLI demo"
  say "Remove BNK from the app cluster ${APP_CLUSTER}…"
  run "$ROKSBNKCTL_BIN" -w "$APP_WS" bnk down --auto
  say "…then the License Proxy from the services cluster ${SERVICES_CLUSTER}."
  run "$ROKSBNKCTL_BIN" -w "$SVC_WS" flp down --auto
  say "Clear the two local workspaces (local state only — the clusters are not ours)."
  rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$APP_WS" "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$SVC_WS"
  ok "FLP + BNK removed; both clusters (${SERVICES_CLUSTER}, ${APP_CLUSTER}) are STILL RUNNING"
  say "The private registry keeps its mirrored artifacts — it was built off-camera and is shared."
  say "To empty it too:  roksbnkctl -w ${SVC_WS} registry delete --force  (before clearing the workspace)"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit $?; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — A SHARED LICENSING CLUSTER"
cat >&2 <<EOF
Two RUNNING clusters, adopted — never created. The story, in six phases:
  1. ${B}Adopt${N} the services cluster ${C}${SERVICES_CLUSTER}${N} (cluster register).
  2. ${B}Mirror${N} F5's artifact registry -> the private registry.
  3. ${B}flp up --add-node-port-access${N} — the FLP, reachable from next door.
  4. ${B}Adopt${N} the app cluster ${C}${APP_CLUSTER}${N}; point it at that FLP + the same registry.
  5. ${B}bnk up${N} — images from the registry, licensing through the OTHER cluster.
  6. ${B}Prove it${N} — License Active, and no FLP in this cluster at all.
Then the demo STOPS, leaving the FLP + BNK up so you can explore. \`teardown\` removes
them and leaves BOTH adopted clusters running.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"; export IBMCLOUD_API_KEY
[[ -n "$SERVICES_CLUSTER" ]]        || die "set SERVICES_CLUSTER — the RUNNING cluster that will host the FLP"
[[ -n "$APP_CLUSTER" ]]             || die "set APP_CLUSTER — the RUNNING cluster that will get BNK"
[[ -n "$APP_CLUSTER_CIDR" ]]        || die "set APP_CLUSTER_CIDR — the app cluster zone prefixes (comma-separated, ALL zones)"
[[ -n "$REGISTRY_DOMAIN" ]]         || die "set REGISTRY_DOMAIN — the private registry host (../lib/deploy-far-registry.sh builds one)"
[[ -n "$REGISTRY_ADMIN_PASSWORD" ]] || die "set REGISTRY_ADMIN_PASSWORD — deploy-far-registry.sh prints it"
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$REGISTRY_ADMIN_PASSWORD"
for c in "$ROKSBNKCTL_BIN" terraform helm jq; do command -v "$c" >/dev/null || die "$c not found"; done
# #143: print the binary + version this demo will actually run, and warn on drift.
preflight_binary "$ROKSBNKCTL_BIN"
run "$ROKSBNKCTL_BIN" version
ok "preflight: two clusters named, registry $REGISTRY_DOMAIN, roksbnkctl + terraform + helm present"

# ============================ Phase 1: adopt the services cluster ============
pause; phase P1 "PHASE 1/6  —  Adopt the services cluster (the only egress to F5)"
say "Both ROKS clusters are already running. roksbnkctl does not have to build them — cluster.create"
say "is false, and 'cluster register' looks the cluster up in IBM Cloud and records its identity."
say "Nothing is provisioned; the demo never owns these clusters, so it can never destroy them."
[[ "$DRY_RUN" == "1" ]] || rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$SVC_WS"
SVC_SEED="$STATE_DIR/${SVC_WS}-config.yaml"
cat > "$SVC_SEED" <<YAML
ibmcloud: { region: ${REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${SVC_WS}
tf_source: { type: embedded }
cluster: { create: false, name: ${SERVICES_CLUSTER} }
bnk:
  manifest_version: ${BNK_VERSION}
  far_repo_url: ${FAR_REPO_URL}
  flp:
    namespace: ${FLP_NAMESPACE}
YAML
show_file "$SVC_SEED"
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" init --config-file "$SVC_SEED" --override-from-env
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" cluster register "$SERVICES_CLUSTER"
ok "services cluster '$SERVICES_CLUSTER' adopted"
endphase P1

# ============================ Phase 2: mirror FAR ============================
pause; phase P2 "PHASE 2/6  —  Mirror F5's artifact registry -> the private registry"   # LONG
say "Point the workspace at the private registry. Four fields select it; the password is piped in"
say "on stdin so it never lands in argv or shell history."
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry target generic
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry target generic_host "$REGISTRY_DOMAIN"
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry target generic_repo_prefix "$REGISTRY_PROJECT"
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry target generic_username admin
show "printf '%s' \"\$REGISTRY_ADMIN_PASSWORD\" | roksbnkctl -w $SVC_WS registry target generic_password --password-stdin"
[[ "$DRY_RUN" == "1" ]] || printf '%s' "$REGISTRY_ADMIN_PASSWORD" | "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry target generic_password --password-stdin
say "roksbnkctl reads the F5 manifest and derives every chart and image the install needs — the"
say "License Proxy among them. Each is then copied into the registry by digest."
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry bom
begin_long
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry replicate --target generic
end_long
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" registry verify
ok "from here, nothing is pulled from F5 — everything comes from $REGISTRY_DOMAIN"
endphase P2

# ============================ Phase 3: the FLP ===============================
pause; phase P3 "PHASE 3/6  —  flp up: the License Proxy, reachable from next door"   # LONG
say "The proxy is a cluster-wide licensing broker, and it is the only thing that needs to reach"
say "F5. Deploy it once, here."
say "--add-node-port-access is what makes it usable from the OTHER cluster: it puts the worker"
say "node IPs in the proxy's certificate (IP SANs), makes every node answer, and opens the"
say "NodePort to the app cluster's security group."
say "Pass EVERY zone's prefix. A multi-zone VPC has one per zone, and a pod scheduled in a zone"
say "you left out is silently dropped at the security group."
begin_long
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" flp up --auto --add-node-port-access --node-port-source-cidr "$APP_CLUSTER_CIDR"
end_long
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" k get svc f5-license-proxy -n "$FLP_NAMESPACE"
run "$ROKSBNKCTL_BIN" -w "$SVC_WS" flp output
ok "the FLP answers on every worker node, and every one of those addresses is in its certificate"
endphase P3

# ============================ Phase 4: adopt the app cluster =================
pause; phase P4 "PHASE 4/6  —  Adopt the app cluster and point it at that FLP"
say "A second workspace, for the second cluster — also already running, also just registered."
say "The handoff between the two workspaces is exactly two values: the proxy's external address,"
say "and its root CA. Both are read straight out of the services workspace."
if [[ "$DRY_RUN" == "1" ]]; then
  FLP_URL='https://<node-ip>:30001'; FLP_CA='<base64-root-ca>'
else
  FLP_URL="$("$ROKSBNKCTL_BIN" -w "$SVC_WS" flp output flp_external_endpoint | tr -d '\r')"
  # The CA is ALREADY base64 — pass it through verbatim. Re-encoding hands the CWC a corrupt CA.
  FLP_CA="$("$ROKSBNKCTL_BIN" -w "$SVC_WS" flp output flp_root_ca | tr -d '\r')"
  [[ -n "$FLP_URL" && -n "$FLP_CA" ]] || die "flp output returned no endpoint/CA — is the FLP up?"
fi
[[ "$DRY_RUN" == "1" ]] || rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$APP_WS"
APP_SEED="$STATE_DIR/${APP_WS}-config.yaml"
cat > "$APP_SEED" <<YAML
ibmcloud: { region: ${REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${APP_WS}
tf_source: { type: embedded }
cluster: { create: false, name: ${APP_CLUSTER} }
bnk:
  manifest_version: ${BNK_VERSION}
  far_repo_url: ${FAR_REPO_URL}
  license_mode: f5licenseproxy
  flp:
    external:
      url: ${FLP_URL}
      root_ca_b64: ${FLP_CA}
YAML
say "license_mode is f5licenseproxy and flp.external points at the proxy in the OTHER cluster."
say "This cluster never runs an FLP of its own."
show_file "$APP_SEED"
run "$ROKSBNKCTL_BIN" -w "$APP_WS" init --config-file "$APP_SEED" --override-from-env
run "$ROKSBNKCTL_BIN" -w "$APP_WS" cluster register "$APP_CLUSTER"
say "The same private registry — already populated, so replicate records the mirror rather than"
say "copying anything. That record is what tells bnk up where every chart and image comes from."
run "$ROKSBNKCTL_BIN" -w "$APP_WS" registry target generic
run "$ROKSBNKCTL_BIN" -w "$APP_WS" registry target generic_host "$REGISTRY_DOMAIN"
run "$ROKSBNKCTL_BIN" -w "$APP_WS" registry target generic_repo_prefix "$REGISTRY_PROJECT"
run "$ROKSBNKCTL_BIN" -w "$APP_WS" registry target generic_username admin
show "printf '%s' \"\$REGISTRY_ADMIN_PASSWORD\" | roksbnkctl -w $APP_WS registry target generic_password --password-stdin"
[[ "$DRY_RUN" == "1" ]] || printf '%s' "$REGISTRY_ADMIN_PASSWORD" | "$ROKSBNKCTL_BIN" -w "$APP_WS" registry target generic_password --password-stdin
run "$ROKSBNKCTL_BIN" -w "$APP_WS" registry replicate --target generic
ok "app cluster '$APP_CLUSTER' adopted, aimed at the mirror and at the FLP next door"
endphase P4

# ============================ Phase 5: bnk up ================================
pause; phase P5 "PHASE 5/6  —  bnk up: disconnected, and licensed from next door"   # LONG
say "Every chart and image comes from the private registry, and the CNEInstance is told to pull"
say "from it too. The cluster-wide controller licenses through the proxy in the other cluster."
begin_long
run "$ROKSBNKCTL_BIN" -w "$APP_WS" bnk up --auto
end_long
ok "BNK installed — this cluster reached the registry and the proxy, and nothing else"
endphase P5

# ============================ Phase 6: the proof =============================
pause; phase P6 "PHASE 6/6  —  Confirm the remote FLP licensed the cluster"
say "The License custom resource: mode f5licenseproxy, state Active — licensed by a proxy that is"
say "not even in this cluster."
run "$ROKSBNKCTL_BIN" -w "$APP_WS" k get license -n f5-utils
say "The CNEInstance is reconciled and the dataplane is up, pulled entirely from the registry."
run "$ROKSBNKCTL_BIN" -w "$APP_WS" k get cneinstance -A
run "$ROKSBNKCTL_BIN" -w "$APP_WS" k get pods -n f5-bnk
say "And there is no License Proxy here at all. The next command is SUPPOSED to fail — the"
say "namespace does not exist in this cluster. The error text IS the evidence."
run "$ROKSBNKCTL_BIN" -w "$APP_WS" k get pods -n "$FLP_NAMESPACE"
endphase P6

banner "DEMO COMPLETE"
cat >&2 <<EOF
BIG-IP Next installed from a private registry and licensed by an F5 License Proxy in a
DIFFERENT cluster — neither cluster reached repo.f5.com, and only one of them can reach F5 at all.
  services cluster  ${SERVICES_CLUSTER}  (FLP, the only egress to F5)
  app cluster       ${APP_CLUSTER}  (BNK, air-gapped)
  private registry  ${REGISTRY_DOMAIN}/${REGISTRY_PROJECT}

Reachable web UIs (explore before you tear down):
  Harbor registry:    https://${REGISTRY_DOMAIN}/   (login: admin / your REGISTRY_ADMIN_PASSWORD)
  OpenShift consoles: https://cloud.ibm.com/kubernetes/clusters — open ${SERVICES_CLUSTER}
                      or ${APP_CLUSTER}, then "OpenShift web console"
                      (login: your IBM Cloud identity)
  FLP licensing endpoint (an API the app cluster dials, not a browser UI):
                      ${FLP_URL}
                      status:  roksbnkctl -w ${SVC_WS} flp status
  The proof, any time: roksbnkctl -w ${APP_WS} k get license -n f5-utils

Nothing was torn down — the FLP and BNK are both still running, so you can watch the
app cluster stay licensed by the proxy next door.
Tear down when finished (removes ONLY the FLP + BNK; BOTH clusters keep running):
  ./shared-licensing-cli-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
