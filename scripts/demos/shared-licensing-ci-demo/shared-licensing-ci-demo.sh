#!/usr/bin/env bash
# =============================================================================
# shared-licensing-ci-demo.sh  (roksbnkctl v1.52.0)
#
# The SHARED LICENSING CLUSTER, told the way it actually ships: as TWO CI JOBS.
# Every step is a `docker run` of the roksbnkctl-tools-runner image, the whole
# workspace comes from ENVIRONMENT VARIABLES, and no config.yaml is templated
# anywhere. Nothing is installed on the host — no roksbnkctl, no terraform, no
# helm, no kubectl, no ibmcloud.
#
#     ┌─ JOB 1 · licensing cluster ────────────────────────────────┐
#     │  mirror FAR → private registry                             │
#     │  F5 License Proxy ──NodePort 30001──┐                      │
#     │  outputs: flp_url, flp_ca ──────────┼──┐                   │
#     └─────────────────────────────────────┼──┼───────────────────┘
#     ┌─ JOB 2 · application cluster ───────┼──┼───────────────────┐
#     │  BNK ← charts + images ── private registry                 │
#     │  CWC ───────────────────────────────┘  │ licensed remotely │
#     │  ROKSBNKCTL_FLP_EXTERNAL_URL / _ROOT_CA_B64 ←──────────────┘
#     └────────────────────────────────────────────────────────────┘
#
# The handoff between the jobs is TWO ENVIRONMENT VARIABLES — exactly what a CI
# pipeline passes as job outputs. That is the point of the demo.
#
# Seven phases:
#   1. a CI runner with nothing installed     (the image IS roksbnkctl)
#   2. job 1: configured from the environment alone, then adopt the cluster
#   3. job 1: mirror FAR into the private registry
#   4. job 1: flp up --add-node-port-access, from inside the container
#   5. the handoff — two environment variables
#   6. job 2: adopt, then bnk up — from the registry, licensed from next door
#   7. prove it — License Active, and no FLP in the app cluster at all
#
# The demo does NOT tear itself down: it ends with a report of every reachable web UI so
# the operator can explore. `./shared-licensing-ci-demo.sh teardown` removes ONLY the FLP
# and BNK it installed — both adopted clusters keep running.
#
# BOTH ROKS CLUSTERS ARE ALREADY RUNNING and are `cluster register`ed, not created:
# cluster builds are ~40 minutes of nothing to watch. Teardown removes only the
# workloads the pipeline installed; it never destroys clusters it did not create.
#
# Hands-off: AUTO_ADVANCE=1 (default) auto-advances between phases. Emits phase
# timestamps to $TS_FILE so record.sh can 10x the long phases and hold each
# roksbnkctl command on screen for 5s in post.
#
# Linux / WSL. Requires: docker. Nothing else — that is the point.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

# ============================ CONFIG (edit me) ===============================
REGION="${REGION:-ca-tor}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"

SERVICES_CLUSTER="${SERVICES_CLUSTER:-}"   # RUNNING cluster that will host the FLP
APP_CLUSTER="${APP_CLUSTER:-}"             # RUNNING cluster that will get BNK
# The app cluster's zone prefixes, opened to the FLP's NodePort. COMMA-SEPARATED,
# EVERY zone — a pod scheduled in an omitted zone is dropped at the security group.
#   ibmcloud is vpc-address-prefixes <vpc> --output json | jq -r '[.[].cidr]|join(",")'
APP_CLUSTER_CIDR="${APP_CLUSTER_CIDR:-}"

REGISTRY_DOMAIN="${REGISTRY_DOMAIN:-}"                 # the private registry host
REGISTRY_ADMIN_PASSWORD="${REGISTRY_ADMIN_PASSWORD:-}" # its admin password
REGISTRY_PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"

RUNNER_TAG="${RUNNER_TAG:-v1.52.0}"
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
SVC_WS="${SVC_WS:-ci-svc}"                 # job 1's workspace — owns the proxy
APP_WS="${APP_WS:-ci-app}"                 # job 2's workspace — owns BNK
WORK="${CI_WORK:-$HOME/bnk-ci-flp-state}"  # the state volume, mounted at /work

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# ── the two jobs' environments ───────────────────────────────────────────────
# This IS the CI config. Job 1 says where the cluster and the registry are; job 2
# says the same, plus how to license — and the two FLP vars it gets from job 1.
SVC_ENV="$STATE_DIR/.ci-svc.env"
APP_ENV="$STATE_DIR/.ci-app.env"
SVC=(docker run --rm -v "$WORK:/work" --env-file "$SVC_ENV" "$RUNNER" -w "$SVC_WS")
APP=(docker run --rm -v "$WORK:/work" --env-file "$APP_ENV" "$RUNNER" -w "$APP_WS")

# write_svc_env — job 1's environment IS the CI config. Defined HERE, above teardown(),
# because a standalone `teardown` run needs to rebuild it after a run deleted it.
write_svc_env(){
cat > "$SVC_ENV" <<EOF
IBMCLOUD_API_KEY=$IBMCLOUD_API_KEY
ROKSBNKCTL_REGION=$REGION
ROKSBNKCTL_RESOURCE_GROUP=$RESOURCE_GROUP
ROKSBNKCTL_PREFIX=$SVC_WS
ROKSBNKCTL_CLUSTER_NAME=$SERVICES_CLUSTER
ROKSBNKCTL_CLUSTER_CREATE=false
ROKSBNKCTL_REGISTRY_TARGET=generic
ROKSBNKCTL_GENERIC_HOST=$REGISTRY_DOMAIN
ROKSBNKCTL_GENERIC_REPO_PREFIX=$REGISTRY_PROJECT
ROKSBNKCTL_GENERIC_USERNAME=admin
ROKSBNKCTL_GENERIC_PASSWORD=$REGISTRY_ADMIN_PASSWORD
EOF
chmod 600 "$SVC_ENV"
}

# ============================ teardown =======================================
# Removes ONLY what this pipeline installed: BNK from the app cluster, the FLP from the
# services cluster, the two --env-file files and the two workspaces on the /work volume.
# BOTH ROKS CLUSTERS ARE LEFT RUNNING — they were `cluster register`ed, never created.
# The private registry is left alone too (built off-camera); the command to empty its
# mirror is printed at the end.
# The demo does NOT auto-tear-down, so you can explore the UIs first.
# ---------------------------------------------------------------------------
teardown(){
  # Runs standalone: the env-files may be gone, so rebuild whatever is missing from .env.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  secret "$IBMCLOUD_API_KEY" "${REGISTRY_ADMIN_PASSWORD:-}"
  banner "TEARDOWN — shared-licensing CI demo"
  [[ -f "$SVC_ENV" ]] || write_svc_env
  if [[ -f "$APP_ENV" ]]; then
    say "Remove BNK from the app cluster ${APP_CLUSTER}…"
    run "${APP[@]}" bnk down --auto
  else
    note "no job-2 env file — skipping BNK removal (nothing recorded for ${APP_WS})"
  fi
  say "…then the License Proxy from the services cluster ${SERVICES_CLUSTER}."
  run "${SVC[@]}" flp down --auto
  say "Clear the pipeline's env-files and both workspaces on the /work volume."
  rm -f "$SVC_ENV" "$APP_ENV"
  rm -rf "$WORK/.roksbnkctl/$SVC_WS" "$WORK/.roksbnkctl/$APP_WS"
  ok "FLP + BNK removed; both clusters (${SERVICES_CLUSTER}, ${APP_CLUSTER}) are STILL RUNNING"
  say "The private registry keeps its mirrored artifacts — it was built off-camera and is shared."
  say "To empty it too:  docker run --rm -v ${WORK}:/work --env-file <svc env> ${RUNNER} -w ${SVC_WS} registry delete --force"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit 0; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — SHARED LICENSING AS A CI PIPELINE"
cat >&2 <<EOF
Two RUNNING clusters, two CI jobs, and ${B}nothing installed on this host${N}:
  1. The runner image ${B}IS${N} roksbnkctl. Every step is one docker run.
  2. Job 1 configured from the ${B}environment alone${N}; adopt ${C}${SERVICES_CLUSTER}${N}.
  3. Job 1 mirrors F5's artifact registry -> the private registry.
  4. Job 1 runs ${B}flp up --add-node-port-access${N} — from inside the container.
  5. The handoff: ${B}two environment variables${N} — the proxy's URL and its CA.
  6. Job 2 adopts ${C}${APP_CLUSTER}${N} and runs ${B}bnk up${N} — licensed from next door.
  7. ${B}Prove it${N} — License Active, and no FLP in the app cluster at all.
Then the pipeline STOPS, leaving the FLP + BNK up so you can explore. \`teardown\` removes
them and leaves BOTH adopted clusters running.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"
[[ -n "$SERVICES_CLUSTER" ]]        || die "set SERVICES_CLUSTER — the RUNNING cluster that will host the FLP"
[[ -n "$APP_CLUSTER" ]]             || die "set APP_CLUSTER — the RUNNING cluster that will get BNK"
[[ -n "$APP_CLUSTER_CIDR" ]]        || die "set APP_CLUSTER_CIDR — the app cluster zone prefixes (comma-separated, ALL zones)"
[[ -n "$REGISTRY_DOMAIN" ]]         || die "set REGISTRY_DOMAIN — the private registry host (../lib/deploy-far-registry.sh builds one)"
[[ -n "$REGISTRY_ADMIN_PASSWORD" ]] || die "set REGISTRY_ADMIN_PASSWORD — deploy-far-registry.sh prints it"
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$REGISTRY_ADMIN_PASSWORD"
command -v docker >/dev/null || die "docker not found — a CI runner already provides it (curl -fsSL https://get.docker.com | sh)"
[[ "$DRY_RUN" == "1" ]] || docker pull -q "$RUNNER" >/dev/null 2>&1 || note "could not pre-pull $RUNNER — the first step will pull it"
# The image runs as uid 1000 — the bind-mounted state dir must be writable, and
# both workspaces must start clean (init refuses to clobber an existing one).
[[ "$DRY_RUN" == "1" ]] || { mkdir -p "$WORK"; chmod 777 "$WORK"; rm -rf "$WORK/.roksbnkctl/$SVC_WS" "$WORK/.roksbnkctl/$APP_WS"; }
umask 077
write_svc_env
ok "preflight: docker present, state volume $WORK ready, job 1's environment written"

# ============================ Phase 1: the runner container ==================
pause; phase P1 "PHASE 1/7  —  A CI runner with nothing installed"
say "This host has no roksbnkctl, no terraform, no helm, no kubectl. Every step is one docker run"
say "of the tools-runner image — which is exactly what a CI job does."
run docker run --rm "$RUNNER" version
say "It even runs the Kubernetes verbs in-process, so there is no kubectl to install and nothing"
say "to keep in step with the cluster."
endphase P1

# ============================ Phase 2: job 1 from the environment ============
pause; phase P2 "PHASE 2/7  —  Job one: configure from the environment alone"
say "No config file is templated anywhere. The workspace IS the environment: where the cluster is,"
say "where the registry is, and the credential to reach it. The password comes from the CI secret"
say "store and is never written into the pipeline."
show_file "$SVC_ENV"
run "${SVC[@]}" init --non-interactive --override-from-env
say "Both clusters are already running, so the pipeline adopts them rather than spending forty"
say "minutes building one."
run "${SVC[@]}" cluster register "$SERVICES_CLUSTER"
ok "job 1's workspace exists on the /work volume — built from env vars alone"
endphase P2

# ============================ Phase 3: job 1 mirrors FAR =====================
pause; phase P3 "PHASE 3/7  —  Job one: mirror F5's registry into the private one"   # LONG
say "roksbnkctl reads F5's manifest and derives every chart and image the install needs — the"
say "License Proxy among them — then copies each into the private registry by digest."
begin_long
run "${SVC[@]}" registry replicate --target generic
end_long
run "${SVC[@]}" registry verify
ok "the mirror is complete and digest-matched"
endphase P3

# ============================ Phase 4: job 1 deploys the FLP =================
pause; phase P4 "PHASE 4/7  —  Job one: deploy the License Proxy from the container"   # LONG
say "The proxy is the only component that needs to reach F5. Deploy it once, here, and expose it"
say "so the other cluster can license through it."
say "Pass every zone's prefix — a multi-zone VPC has one per zone, and a pod scheduled in a zone"
say "you left out is dropped at the security group."
begin_long
run "${SVC[@]}" flp up --auto --add-node-port-access --node-port-source-cidr "$APP_CLUSTER_CIDR"
end_long
say "The chart needs fixing up before it will install on OpenShift, and roksbnkctl post-renders it"
say "itself — so the container needs no python, and no interpreter at all."
run "${SVC[@]}" k get svc f5-license-proxy -n f5-license-proxy
endphase P4

# ============================ Phase 5: the handoff ===========================
pause; phase P5 "PHASE 5/7  —  Hand the proxy to the next job: two environment variables"
say "This is the whole handoff. Two values a CI job prints as outputs: the proxy's address, and"
say "its root certificate."
if [[ "$DRY_RUN" == "1" ]]; then
  FLP_URL='https://<node-ip>:30001'; FLP_CA='<base64-root-ca>'
  show "roksbnkctl -w $SVC_WS flp output flp_external_endpoint"
  show "roksbnkctl -w $SVC_WS flp output flp_root_ca"
else
  FLP_URL="$("${SVC[@]}" flp output flp_external_endpoint | tr -d '\r')"
  FLP_CA="$("${SVC[@]}"  flp output flp_root_ca          | tr -d '\r')"
  [[ -n "$FLP_URL" && -n "$FLP_CA" ]] || die "flp output returned no endpoint/CA — is the FLP up?"
  show "roksbnkctl -w $SVC_WS flp output flp_external_endpoint"
  printf '%s\n' "$FLP_URL"
  show "roksbnkctl -w $SVC_WS flp output flp_root_ca"
  printf '%s…  (%s bytes, base64)\n' "${FLP_CA:0:48}" "${#FLP_CA}"
fi
# Job 2's environment: the same registry, plus how to license. The CA is passed
# VERBATIM — it is already base64, and re-encoding hands the CWC a corrupt CA.
cat > "$APP_ENV" <<EOF
IBMCLOUD_API_KEY=$IBMCLOUD_API_KEY
ROKSBNKCTL_REGION=$REGION
ROKSBNKCTL_RESOURCE_GROUP=$RESOURCE_GROUP
ROKSBNKCTL_PREFIX=$APP_WS
ROKSBNKCTL_CLUSTER_NAME=$APP_CLUSTER
ROKSBNKCTL_CLUSTER_CREATE=false
ROKSBNKCTL_REGISTRY_TARGET=generic
ROKSBNKCTL_GENERIC_HOST=$REGISTRY_DOMAIN
ROKSBNKCTL_GENERIC_REPO_PREFIX=$REGISTRY_PROJECT
ROKSBNKCTL_GENERIC_USERNAME=admin
ROKSBNKCTL_GENERIC_PASSWORD=$REGISTRY_ADMIN_PASSWORD
ROKSBNKCTL_LICENSE_MODE=f5licenseproxy
ROKSBNKCTL_FLP_EXTERNAL_URL=$FLP_URL
ROKSBNKCTL_FLP_ROOT_CA_B64=$FLP_CA
EOF
chmod 600 "$APP_ENV"
say "Job two's environment: the same registry, plus license-mode f5licenseproxy and the proxy it"
say "just received. Still no config file, anywhere."
show_file "$APP_ENV" "ROOT_CA"
endphase P5

# ============================ Phase 6: job 2 installs BNK ====================
pause; phase P6 "PHASE 6/7  —  Job two: install BIG-IP Next, licensed from next door"   # LONG
run "${APP[@]}" init --non-interactive --override-from-env
run "${APP[@]}" cluster register "$APP_CLUSTER"
say "The same private registry. Everything is already in it, so this records the mirror rather"
say "than copying anything — and that record is what bnk up resolves every artifact against."
run "${APP[@]}" registry replicate --target generic
say "Now install. Every chart and every image comes from the private registry, and the cluster-wide"
say "controller licenses through the proxy in the other cluster."
begin_long
run "${APP[@]}" bnk up --auto
end_long
ok "BNK installed — job 2's cluster reached the registry and the proxy, and nothing else"
endphase P6

# ============================ Phase 7: prove it, then clean up ===============
pause; phase P7 "PHASE 7/7  —  Confirm the pipeline licensed the cluster"
say "The License resource: mode f5licenseproxy, state Active — issued through a proxy that is not"
say "in this cluster. A CI job would gate on exactly this."
run "${APP[@]}" k get license -n f5-utils
say "And the data plane is up, pulled entirely from the private registry."
run "${APP[@]}" k get pods -n f5-bnk
say "There is no License Proxy here at all. The next command is SUPPOSED to fail — the namespace"
say "does not exist in this cluster. The error text IS the evidence."
run "${APP[@]}" k get pods -n f5-license-proxy
ok "the app cluster is licensed by a proxy that is not in it, and pulled nothing from F5"
endphase P7

banner "DEMO COMPLETE"
cat >&2 <<EOF
A CI pipeline installed BIG-IP Next from a private registry and licensed it through an F5
License Proxy in a different cluster — with nothing installed on the runner, and no config
file anywhere.
  runner image      ${RUNNER}
  job 1 (FLP)       ${SERVICES_CLUSTER}   job 2 (BNK)  ${APP_CLUSTER}
  private registry  ${REGISTRY_DOMAIN}/${REGISTRY_PROJECT}
  the cross-job handoff was two env vars: ROKSBNKCTL_FLP_EXTERNAL_URL + _ROOT_CA_B64

Reachable web UIs (explore before you tear down):
  Harbor registry:    https://${REGISTRY_DOMAIN}/   (login: admin / your REGISTRY_ADMIN_PASSWORD)
  OpenShift consoles: https://cloud.ibm.com/kubernetes/clusters — open ${SERVICES_CLUSTER}
                      or ${APP_CLUSTER}, then "OpenShift web console"
                      (login: your IBM Cloud identity)
  FLP licensing endpoint (an API job 2 dials, not a browser UI):
                      ${FLP_URL}
  The proof, any time: docker run --rm -v ${WORK}:/work --env-file <job-2 env> \\
                          ${RUNNER} -w ${APP_WS} k get license -n f5-utils

Nothing was torn down — the FLP and BNK are both still running, and the two --env-file
files are still in ${STATE_DIR} (mode 0600) so you can re-run any step by hand.
Tear down when finished (removes ONLY the FLP + BNK; BOTH clusters keep running):
  ./shared-licensing-ci-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
