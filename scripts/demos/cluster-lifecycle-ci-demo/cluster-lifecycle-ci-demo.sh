#!/usr/bin/env bash
# =============================================================================
# cluster-lifecycle-ci-demo.sh  (roksbnkctl v1.32.0)
#
# The SAME lifecycle as cluster-lifecycle-cli-demo, told the way it actually ships:
# as a CI pipeline. ZERO host install — every step is a `docker run` of the
# all-in-one roksbnkctl-tools-runner image, exactly what a CI job calls:
#
#   1. the runner container IS roksbnkctl  (version + doctor, nothing installed here)
#   2. one declarative config.yaml on the mounted /work volume -> init
#   3. cluster up        — a real ROKS cluster
#   4. bnkforge register — hand the durable cluster to BNK Forge
#   5. bnk up            — BIG-IP Next for Kubernetes + its licence
#   6. testing up + test — jump hosts, then the connectivity/DNS/throughput probes
#   7. bnk down, then bnk up again — swap BNK without touching the cluster
#
# Non-interactive throughout: secrets arrive as `docker run -e` names and state is
# persisted in a mounted /work volume.
#
# The demo does NOT tear itself down: it ends with a report of every reachable web UI
# so the operator can explore. `./cluster-lifecycle-ci-demo.sh teardown` removes the
# lot (one more docker run — `roksbnkctl down`; the pipeline CREATED its cluster).
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
OCP_VERSION="${OCP_VERSION:-4.18}"
WORKERS_PER_ZONE="${WORKERS_PER_ZONE:-1}"       # ROKS spans 3 AZs -> 1/zone = 3 workers

BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"        # installed in phase 5
BNK_VERSION_REBUILD="${BNK_VERSION_REBUILD:-$BNK_VERSION}"  # reinstalled in phase 7
FAR_REPO_URL="${FAR_REPO_URL:-repo.f5.com}"

FORGE_URL="${FORGE_URL:-}"                      # BNK Forge — required
FORGE_USER="${FORGE_USER:-}"
FORGE_PASS="${FORGE_PASS:-}"
FORGE_INSECURE="${FORGE_INSECURE:-true}"        # Forge usually has a self-signed cert

TEST_HOSTS="${TEST_HOSTS:-https://www.example.com}"   # probe targets, space-separated
RUNNER_TAG="${RUNNER_TAG:-v1.32.0}"
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
WS="${CI_WORKSPACE:-roksbnkctl-ci}"             # the pipeline's workspace
PREFIX="${PREFIX:-$WS}"
WORK="${CI_WORK:-$HOME/bnk-ci-state}"           # the state volume — mounted at /work

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# RUN is the runner invocation each CI step calls. The image ENTRYPOINT is
# roksbnkctl, so args after the image are roksbnkctl args. -e passes secrets by
# NAME (the value never appears in argv); -v mounts the state volume at /work.
RUN=(docker run --rm -v "$WORK:/work"
     -e IBMCLOUD_API_KEY -e BNK_FORGE_URL -e BNK_FORGE_USER -e BNK_FORGE_PASSWORD
     "$RUNNER" -w "$WS")

# ============================ teardown =======================================
# Removes everything THIS pipeline created: every phase of workspace '$WS' — the
# testing jumphosts, BNK, and the ROKS cluster + VPC/transit-gateway/registry-COS that
# `cluster up` provisioned. It adopts nothing, so there is nothing to leave behind.
# One more docker run of the same runner image, exactly like every other step.
# The demo does NOT auto-tear-down, so you can explore the UIs first.
# ---------------------------------------------------------------------------
teardown(){
  # Runs standalone, so resolve the credentials the runner needs itself.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  export BNK_FORGE_URL="${FORGE_URL:-}" BNK_FORGE_USER="${FORGE_USER:-}" BNK_FORGE_PASSWORD="${FORGE_PASS:-}"
  secret "$IBMCLOUD_API_KEY" "${FORGE_PASS:-}"
  banner "TEARDOWN — cluster-lifecycle CI demo"
  say "One 'down' removes every phase of workspace '${WS}' — testing, BNK, and the cluster itself."
  run "${RUN[@]}" down --auto
  ok "teardown complete — cluster ${WS} and every phase of the workspace are gone"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit 0; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — THE CLUSTER LIFECYCLE AS A CI PIPELINE"
cat >&2 <<EOF
The story, in seven phases — every one of them a ${B}docker run${N}:
  1. The runner image ${B}IS${N} roksbnkctl. Nothing is installed on this host.
  2. One declarative ${B}config.yaml${N} on /work -> ${B}init${N}. No prompts, ever.
  3. ${B}cluster up${N} — a real ROKS cluster (${C}${WS}${N}, OCP ${OCP_VERSION}).
  4. ${B}bnkforge register${N} — hand the durable cluster to BNK Forge.
  5. ${B}bnk up${N} — BIG-IP Next for Kubernetes + its licence.
  6. ${B}testing up${N} + ${B}test${N} — jump hosts, then the probes.
  7. ${B}bnk down${N} then ${B}bnk up${N} — swap BNK, the cluster never moves.
Then the pipeline STOPS, leaving everything up so you can explore. \`teardown\` removes it.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"
[[ -n "$FORGE_URL"  ]] || die "set FORGE_URL — phase 4 registers with a live BNK Forge"
[[ -n "$FORGE_USER" ]] || die "set FORGE_USER"
[[ -n "$FORGE_PASS" ]] || die "set FORGE_PASS"
export IBMCLOUD_API_KEY
export BNK_FORGE_URL="$FORGE_URL" BNK_FORGE_USER="$FORGE_USER" BNK_FORGE_PASSWORD="$FORGE_PASS"
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$FORGE_PASS"
command -v docker >/dev/null || die "docker not found — a CI runner already provides it (curl -fsSL https://get.docker.com | sh)"
[[ "$DRY_RUN" == "1" ]] || docker pull -q "$RUNNER" >/dev/null 2>&1 || note "could not pre-pull $RUNNER — the first step will pull it"
# The runner image runs as uid 1000, so the bind-mounted state dir must be
# writable; and init refuses to clobber an existing workspace, so start clean.
[[ "$DRY_RUN" == "1" ]] || { mkdir -p "$WORK"; chmod 777 "$WORK"; rm -rf "$WORK/.roksbnkctl/$WS"; }
ok "preflight: docker present, state volume $WORK ready, BNK Forge at $FORGE_URL"

# ============================ Phase 1: the runner container ==================
pause; phase P1 "PHASE 1/7  —  The runner container IS roksbnkctl"
say "Nothing is installed on this host — no roksbnkctl, no terraform, no helm, no kubectl, no"
say "ibmcloud. The runner image bundles all of it, and its ENTRYPOINT is roksbnkctl itself."
run docker run --rm "$RUNNER" version
say "doctor is roksbnkctl's own preflight. Run it inside the image and it reports on the image."
run docker run --rm "$RUNNER" doctor
ok "the pipeline's entire toolchain is one image: $RUNNER"
endphase P1

# ============================ Phase 2: config + init =========================
pause; phase P2 "PHASE 2/7  —  Configure declaratively, on the mounted volume"
say "One config.yaml on the /work volume is the whole input — no prompts. Secrets never go in it:"
say "they stay in the CI environment and are passed to each container by name with -e."
cat > "$WORK/config.yaml" <<YAML
ibmcloud: { region: ${REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${PREFIX}
tf_source: { type: embedded }
cluster:
  create: true
  name: ${WS}
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
bnkforge:
  url: ${FORGE_URL}
  username: ${FORGE_USER}
  insecure: ${FORGE_INSECURE}
YAML
show_file "$WORK/config.yaml"
run "${RUN[@]}" init --config-file /work/config.yaml --override-from-env
ok "workspace '$WS' seeded on the /work volume — it outlives every container"
endphase P2

# ============================ Phase 3: cluster up ============================
pause; phase P3 "PHASE 3/7  —  cluster up: build the ROKS cluster"   # LONG
say "A full hands-off build would just be 'roksbnkctl up'. Here the pipeline drives each phase as"
say "its own job — starting with the cluster: VPC, subnets, gateways and the ROKS workers."
begin_long
run "${RUN[@]}" cluster up --auto
end_long
run "${RUN[@]}" cluster config
ok "ROKS cluster '$WS' is up"
endphase P3

# ============================ Phase 4: BNK Forge =============================
pause; phase P4 "PHASE 4/7  —  bnkforge register: hand the cluster to BNK Forge"
say "roksbnkctl registers over BNK Forge's REST API. The URL, user and password come from the CI"
say "environment — BNK_FORGE_* passed into the container by name, never on the command line."
REG=(bnkforge register)
[[ "$FORGE_INSECURE" == "true" ]] && REG+=(--insecure)
run "${RUN[@]}" "${REG[@]}"
ok "registered with BNK Forge"
endphase P4

# ============================ Phase 5: bnk up ================================
pause; phase P5 "PHASE 5/7  —  bnk up: install BIG-IP Next for Kubernetes"   # LONG
say "The BNK phase installs BIG-IP Next for Kubernetes ${BNK_VERSION} and its licence onto the"
say "cluster from phase 3 — and it runs the Kubernetes verbs in-process, so the image needs no kubectl."
begin_long
run "${RUN[@]}" bnk up --auto
end_long
run "${RUN[@]}" k get pods -n f5-bnk
run "${RUN[@]}" k get licenses.k8s.f5net.com -A
ok "BNK installed and licensed"
endphase P5

# ============================ Phase 6: testing + tests =======================
pause; phase P6 "PHASE 6/7  —  testing up + test: the probe framework"   # LONG
say "The testing phase stands up the jump host(s) the connectivity / DNS / throughput probes run"
say "from. In CI this is the gate — 'test' is what a pipeline asserts on."
begin_long
run "${RUN[@]}" testing up --auto
end_long
for h in $TEST_HOSTS; do
  run "${RUN[@]}" test hosts add "$h"
done
run "${RUN[@]}" test
ok "probes ran against: $TEST_HOSTS"
endphase P6

# ============================ Phase 7: bnk down, then bnk up =================
pause; phase P7 "PHASE 7/7  —  bnk down then bnk up: swap BNK, keep the cluster"   # LONG
say "Down JUST the BNK phase. The cluster and the testing framework keep running — a pipeline can"
say "redeploy BNK on every commit without ever re-provisioning a cluster."
begin_long
run "${RUN[@]}" bnk down --auto
end_long
run "${RUN[@]}" k get pods -n f5-bnk
if [[ "$BNK_VERSION_REBUILD" != "$BNK_VERSION" ]]; then
  say "A version swap is one line of the config on the /work volume: bump bnk.manifest_version."
  show "sed -i 's/manifest_version: .*/manifest_version: ${BNK_VERSION_REBUILD}/' /work/.roksbnkctl/${WS}/config.yaml"
  [[ "$DRY_RUN" == "1" ]] || sed -i "s|manifest_version: .*|manifest_version: ${BNK_VERSION_REBUILD}|" "$WORK/.roksbnkctl/$WS/config.yaml"
else
  say "Reinstall the BNK phase on the same running cluster. For a version swap the pipeline would"
  say "bump bnk.manifest_version first — set BNK_VERSION_REBUILD to see that here."
fi
begin_long
run "${RUN[@]}" bnk up --auto
end_long
run "${RUN[@]}" k get pods -n f5-bnk
ok "BNK removed and reinstalled — no re-provisioning, the cluster never moved"
endphase P7

# The recorded cluster identity feeds the closing report — read from the /work volume.
MASTER_URL="$(jq -r '.master_url // empty' "$WORK/.roksbnkctl/$WS/cluster-outputs.json" 2>/dev/null || true)"

banner "DEMO COMPLETE"
cat >&2 <<EOF
Nothing was ever installed on this host. Every step was one docker run of
  ${RUNNER}
  cluster up -> bnkforge register -> bnk up -> testing up + test -> bnk down -> bnk up
  Cluster ${WS} (${REGION}); workspace state lives on the /work volume at ${WORK}.

Reachable web UIs (explore before you tear down):
  BNK Forge:          ${FORGE_URL}   (login: ${FORGE_USER} / your FORGE_PASS)
  OpenShift console:  https://cloud.ibm.com/kubernetes/clusters — open ${WS},
                      then "OpenShift web console"   (login: your IBM Cloud identity)
  Cluster API:        ${MASTER_URL:-<see: docker run … \$RUNNER -w $WS cluster config>}
  kubectl / oc access:  docker run --rm -v ${WORK}:/work -e IBMCLOUD_API_KEY \\
                          ${RUNNER} -w ${WS} kubeconfig --download

Nothing was torn down — BNK and the testing framework are still running.
Tear it all down when finished (this cluster was CREATED by the pipeline, so it goes too):
  ./cluster-lifecycle-ci-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
