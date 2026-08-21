#!/usr/bin/env bash
# =============================================================================
# cluster-lifecycle-cli-demo.sh  (roksbnkctl v1.51.0)
#
# The roksbnkctl LIFECYCLE, driven one phase at a time from the CLI, on a cluster
# roksbnkctl builds and then destroys on camera:
#
#   1. one declarative config.yaml -> init      (no interactive interview)
#   2. cluster up        — a real ROKS cluster
#   3. bnkforge register — hand the durable cluster to BNK Forge
#   4. bnk up            — BIG-IP Next for Kubernetes + its licence
#   5. testing up + test — jump hosts, then the connectivity/DNS/throughput probes
#   6. bnk down, then bnk up again — swap BNK, the cluster never moves
#
# The point is that each PHASE is independently drivable. A hands-off build would
# just be `roksbnkctl up`; here every phase is invoked on its own so the audience
# sees the seams.
#
# The demo does NOT tear itself down: it ends with a report of every reachable web UI
# so the operator can explore. `./cluster-lifecycle-cli-demo.sh teardown` removes the
# lot (one `roksbnkctl down` — this demo CREATED its cluster, so that goes too).
#
# Hands-off: AUTO_ADVANCE=1 (default) auto-advances between phases. Emits phase
# timestamps to $TS_FILE so record.sh can 10x the long phases (cluster up, bnk up,
# testing up, down) and hold each roksbnkctl command on screen for 5s in post.
#
# Linux / WSL. Requires: roksbnkctl v1.32.0, terraform, helm, jq.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

# ============================ CONFIG (edit me) ===============================
REGION="${REGION:-ca-tor}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
CLUSTER_NAME="${CLUSTER_NAME:-bnk-demo}"        # the cluster this demo BUILDS and DESTROYS
OCP_VERSION="${OCP_VERSION:-4.20}"
WORKERS_PER_ZONE="${WORKERS_PER_ZONE:-1}"       # ROKS spans 3 AZs -> 1/zone = 3 workers

BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"        # installed in phase 4
BNK_VERSION_REBUILD="${BNK_VERSION_REBUILD:-$BNK_VERSION}"  # reinstalled in phase 6
FAR_REPO_URL="${FAR_REPO_URL:-repo.f5.com}"

FORGE_URL="${FORGE_URL:-}"                      # BNK Forge — required
FORGE_USER="${FORGE_USER:-}"
FORGE_PASS="${FORGE_PASS:-}"
FORGE_PROJECT="${FORGE_PROJECT:-}"              # optional Forge project id
FORGE_INSECURE="${FORGE_INSECURE:-true}"        # Forge usually has a self-signed cert

TEST_HOSTS="${TEST_HOSTS:-https://www.example.com}"   # probe targets, space-separated
WS="${WS:-$CLUSTER_NAME}"                       # roksbnkctl workspace
PREFIX="${PREFIX:-$CLUSTER_NAME}"
ROKSBNKCTL_BIN="${ROKSBNKCTL_BIN:-roksbnkctl}"

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# ============================ teardown =======================================
# Removes everything THIS demo created: every phase of workspace '$WS' — the testing
# jumphosts, BNK, and the ROKS cluster + VPC/transit-gateway/registry-COS that
# `cluster up` provisioned. It adopts nothing, so there is nothing to leave behind.
# The demo does NOT auto-tear-down, so you can explore the UIs first.
# ---------------------------------------------------------------------------
teardown(){
  # Runs standalone, so resolve the credential itself.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  secret "$IBMCLOUD_API_KEY" "${FORGE_PASS:-}"
  banner "TEARDOWN — cluster-lifecycle CLI demo"
  say "One 'down' removes every phase of workspace '${WS}' — testing, BNK, and the cluster itself."
  run "$ROKSBNKCTL_BIN" -w "$WS" down --auto
  ok "teardown complete — cluster ${CLUSTER_NAME} and every phase of workspace '${WS}' are gone"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit 0; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — THE CLUSTER LIFECYCLE, ONE PHASE AT A TIME"
cat >&2 <<EOF
The story, in six phases:
  1. One declarative ${B}config.yaml${N} -> ${B}init${N}. No interview.
  2. ${B}cluster up${N} — a real ROKS cluster (${C}${CLUSTER_NAME}${N}, OCP ${OCP_VERSION}).
  3. ${B}bnkforge register${N} — hand the durable cluster to BNK Forge.
  4. ${B}bnk up${N} — BIG-IP Next for Kubernetes + its licence.
  5. ${B}testing up${N} + ${B}test${N} — jump hosts, then the probes.
  6. ${B}bnk down${N} then ${B}bnk up${N} — swap BNK, the cluster never moves.
Then the demo STOPS, leaving everything up so you can explore. `teardown` removes it.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"; export IBMCLOUD_API_KEY
[[ -n "$FORGE_URL"  ]] || die "set FORGE_URL — phase 3 registers with a live BNK Forge"
[[ -n "$FORGE_USER" ]] || die "set FORGE_USER"
[[ -n "$FORGE_PASS" ]] || die "set FORGE_PASS"
export BNK_FORGE_PASSWORD="$FORGE_PASS"
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$FORGE_PASS"
# roksbnkctl shells out to terraform for every apply and to helm for chart/BOM
# resolution; everything else (kubectl, oc, ibmcloud, dig, iperf3) is internal.
for c in "$ROKSBNKCTL_BIN" terraform helm jq; do command -v "$c" >/dev/null || die "$c not found"; done
# #143: print the binary + version this demo will actually run, and warn on drift.
preflight_binary "$ROKSBNKCTL_BIN"
run "$ROKSBNKCTL_BIN" version
say "doctor is roksbnkctl's own preflight — it checks the host tooling and the IBM Cloud access it needs."
run "$ROKSBNKCTL_BIN" doctor
ok "preflight: roksbnkctl + terraform + helm present, BNK Forge at $FORGE_URL"

# ============================ Phase 1: config + init =========================
pause; phase P1 "PHASE 1/6  —  One declarative config.yaml, then init"
say "The whole input is one file. init seeds the workspace from it — there is no interactive"
say "interview to sit through, and the same file re-runs this demo identically tomorrow."
# Start clean: init refuses to clobber an existing workspace. (Not shown on camera —
# the path contains "roksbnkctl", which would trip the command-freeze marker.)
[[ "$DRY_RUN" == "1" ]] || rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
SEED="$STATE_DIR/${WS}-config.yaml"
cat > "$SEED" <<YAML
ibmcloud: { region: ${REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${PREFIX}
tf_source: { type: embedded }
cluster:
  create: true
  name: ${CLUSTER_NAME}
  openshift_version: "${OCP_VERSION}"
  workers_per_zone: ${WORKERS_PER_ZONE}
resources:
  # The testing phase provisions ONLY these toggles, and they now default OFF
  # (matching the `init` interview). Phase 5/6 runs `testing up` + `test`, and
  # `test` runs its probes FROM a jumphost — so the demo must ask for one
  # explicitly or the testing phase provisions nothing.
  tgw_jumphost: { create: true }
  client_vpc:   { create: true }   # the jumphost lives in it
bnk:
  manifest_version: ${BNK_VERSION}
  far_repo_url: ${FAR_REPO_URL}
YAML
show_file "$SEED"
run "$ROKSBNKCTL_BIN" -w "$WS" init --config-file "$SEED" --override-from-env
ok "workspace '$WS' seeded — nothing provisioned yet"
endphase P1

# ============================ Phase 2: cluster up ============================
pause; phase P2 "PHASE 2/6  —  cluster up: build the ROKS cluster"   # LONG
say "A full hands-off build would just be 'roksbnkctl up'. Here we drive each phase on its own,"
say "starting with the cluster: VPC, subnets, gateways, and ${WORKERS_PER_ZONE} worker(s) per zone across 3 AZs."
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" cluster up --auto
end_long
run "$ROKSBNKCTL_BIN" -w "$WS" cluster config
ok "ROKS cluster '$CLUSTER_NAME' is up"
endphase P2

# ============================ Phase 3: BNK Forge =============================
pause; phase P3 "PHASE 3/6  —  bnkforge register: hand the cluster to BNK Forge"
say "The cluster is durable, so register it with BNK Forge over its REST API. The password comes"
say "from BNK_FORGE_PASSWORD in the environment — never from the command line, never from argv."
REG_ARGS=(-w "$WS" bnkforge register --url "$FORGE_URL" --username "$FORGE_USER")
[[ "$FORGE_INSECURE" == "true" ]] && REG_ARGS+=(--insecure)
[[ -n "$FORGE_PROJECT" ]] && REG_ARGS+=(--project "$FORGE_PROJECT")
run "$ROKSBNKCTL_BIN" "${REG_ARGS[@]}"
ok "registered with BNK Forge"
endphase P3

# ============================ Phase 4: bnk up ================================
pause; phase P4 "PHASE 4/6  —  bnk up: install BIG-IP Next for Kubernetes"   # LONG
say "The BNK phase installs BIG-IP Next for Kubernetes ${BNK_VERSION} and its licence onto the"
say "cluster from phase 2 — cert-manager, the operators, the CNEInstance and the dataplane."
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" bnk up --auto
end_long
run "$ROKSBNKCTL_BIN" -w "$WS" k get pods -n f5-bnk
run "$ROKSBNKCTL_BIN" -w "$WS" k get licenses.k8s.f5net.com -A
ok "BNK installed and licensed"
endphase P4

# ============================ Phase 5: testing + tests =======================
pause; phase P5 "PHASE 5/6  —  testing up + test: the probe framework"   # LONG
say "The testing phase stands up the jump host(s) the connectivity / DNS / throughput probes run"
say "from. It is its own phase, so it builds and tears down independently of BNK."
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" testing up --auto
end_long
for h in $TEST_HOSTS; do
  run "$ROKSBNKCTL_BIN" -w "$WS" test hosts add "$h"
done
run "$ROKSBNKCTL_BIN" -w "$WS" test
ok "probes ran against: $TEST_HOSTS"
endphase P5

# ============================ Phase 6: bnk down, then bnk up =================
pause; phase P6 "PHASE 6/6  —  bnk down then bnk up: swap BNK, keep the cluster"   # LONG
say "Down JUST the BNK phase. The cluster and the testing framework keep running — this phase"
say "independence is the whole point of the demo."
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" bnk down --auto
end_long
run "$ROKSBNKCTL_BIN" -w "$WS" k get pods -n f5-bnk
WS_CONFIG="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS/config.yaml"
if [[ "$BNK_VERSION_REBUILD" != "$BNK_VERSION" ]]; then
  say "A version swap is one line of the workspace config: bump bnk.manifest_version, then bnk up."
  show "sed -i 's/manifest_version: .*/manifest_version: ${BNK_VERSION_REBUILD}/' <workspace>/config.yaml"
  [[ "$DRY_RUN" == "1" ]] || sed -i "s|manifest_version: .*|manifest_version: ${BNK_VERSION_REBUILD}|" "$WS_CONFIG"
  [[ "$DRY_RUN" == "1" ]] || grep -n 'manifest_version' "$WS_CONFIG" >&2
else
  say "Reinstall the BNK phase on the same running cluster. For a version swap you would bump"
  say "bnk.manifest_version in the workspace config first — set BNK_VERSION_REBUILD to see that here."
fi
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" bnk up --auto
end_long
run "$ROKSBNKCTL_BIN" -w "$WS" k get pods -n f5-bnk
ok "BNK removed and reinstalled — no re-provisioning, the cluster never moved"
endphase P6

# The recorded cluster identity feeds the closing report (jq is a preflight dep).
MASTER_URL="$(jq -r '.master_url // empty' "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS/cluster-outputs.json" 2>/dev/null || true)"

banner "DEMO COMPLETE"
cat >&2 <<EOF
Every phase was driven on its own, and the cluster never moved between them:
  cluster up -> bnkforge register -> bnk up -> testing up + test -> bnk down -> bnk up
  Cluster ${CLUSTER_NAME} (${REGION}), OCP ${OCP_VERSION}, ${WORKERS_PER_ZONE} worker(s)/zone.

Reachable web UIs (explore before you tear down):
  BNK Forge:          ${FORGE_URL}   (login: ${FORGE_USER} / your FORGE_PASS)
  OpenShift console:  https://cloud.ibm.com/kubernetes/clusters — open ${CLUSTER_NAME},
                      then "OpenShift web console"   (login: your IBM Cloud identity)
  Cluster API:        ${MASTER_URL:-<see: roksbnkctl -w $WS cluster config>}
  kubectl / oc access:  roksbnkctl -w ${WS} kubeconfig --download

Nothing was torn down — BNK and the testing framework are still running.
Tear it all down when finished (this cluster was CREATED by the demo, so it goes too):
  ./cluster-lifecycle-cli-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
