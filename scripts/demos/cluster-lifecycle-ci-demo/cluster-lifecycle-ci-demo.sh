#!/usr/bin/env bash
# =============================================================================
# cluster-lifecycle-ci-demo.sh  (roksbnkctl v1.59.0)
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
REGION="${REGION:-ca-tor}"                      # IBM Cloud region everything is created in. Default: ca-tor
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"     # IBM Cloud resource group. Default: default
OCP_VERSION="${OCP_VERSION:-4.20}"              # OpenShift version for the cluster this demo CREATES.
                                                # Default: 4.20. Pinned rather than left to IBM's current
                                                # default, which moves — a demo has to be reproducible.
WORKERS_PER_ZONE="${WORKERS_PER_ZONE:-1}"       # ROKS spans 3 AZs -> 1/zone = 3 workers

BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"        # installed in phase 5
BNK_VERSION_REBUILD="${BNK_VERSION_REBUILD:-$BNK_VERSION}"  # reinstalled in phase 7
FAR_REPO_URL="${FAR_REPO_URL:-repo.f5.com}"     # F5 Artifact Repository charts and images come from.
                                                # Default: repo.f5.com (needs Internet + a FAR service
                                                # account). A disconnected cluster mirrors it instead.

# BNK Forge is OPTIONAL. Set ALL THREE to run the registration phase, or NONE
# to skip it — a partial configuration dies rather than silently skipping,
# because someone who set two of three meant to use it (#164). roksbnkctl's own
# BNK_FORGE_URL / BNK_FORGE_USER / BNK_FORGE_PASSWORD are accepted as fallbacks.
FORGE_URL="${FORGE_URL:-}"                      # Forge base URL. Default: empty = phase skipped
FORGE_USER="${FORGE_USER:-}"                    # Forge username. Default: empty
FORGE_PASS="${FORGE_PASS:-}"                    # Forge password. Default: empty
FORGE_INSECURE="${FORGE_INSECURE:-true}"        # Forge usually has a self-signed cert

TEST_HOSTS="${TEST_HOSTS:-https://www.example.com}"   # probe targets, space-separated
RUNNER_TAG="${RUNNER_TAG:-v1.59.0}"             # runner image tag every step runs. Default: the current
                                                # release. A test bumps this on release, because a stale
                                                # pin silently exercises an old binary and passes.
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
WS="${CI_WORKSPACE:-roksbnkctl-ci}"             # the pipeline's workspace
PREFIX="${PREFIX:-$WS}"                         # name base every IBM Cloud resource derives from.
                                                # Default: $WS. Two pipelines on one account need
                                                # different prefixes or their resource names collide.
WORK="${CI_WORK:-$HOME/bnk-ci-state}"           # the state volume — mounted at /work

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# RUN is the runner invocation each CI step calls. The image ENTRYPOINT is
# roksbnkctl, so args after the image are roksbnkctl args. -e passes secrets by
# NAME (the value never appears in argv); -v mounts the state volume at /work.
# Every ROKSBNKCTL_* override in the environment is forwarded BY NAME, alongside
# the credentials. Without this the container sees none of them: the pipeline
# could not be configured at all from CI, which is the one place a container
# runner exists to serve. Found by an override being silently dropped and the
# workspace falling back to COS it had no access to.
#
# By name, not by value, for the same reason the credentials are: the value never
# appears in argv, so it cannot leak into a process list or a recording.
# NOT every ROKSBNKCTL_* is a workspace override. Some configure the CLI's own
# runtime and are HOST-specific — ROKSBNKCTL_HOME above all, which names a
# directory on this machine. Forwarding it makes the container try to write to a
# host path it cannot see:
#
#   saving workspace: creating /mnt/d/…/home/bnk24d: mkdir /mnt/d: permission denied
#
# The container sets its own ROKSBNKCTL_HOME to /work/.roksbnkctl, which is the
# mounted volume and the whole point of the state volume. So the runtime
# variables are skipped and the configuration overrides are forwarded.
# NOTE the ${RUN_ENV[@]+"${RUN_ENV[@]}"} form where this is expanded below. macOS
# ships bash 3.2, where expanding an EMPTY array under `set -u` is an unbound
# variable error; bash 4.4+ allows it. A demo run with no ROKSBNKCTL_* set — which
# is exactly what the behavioural tests do — leaves this array empty, so the plain
# "${RUN_ENV[@]}" form aborts the script on macOS and nowhere else.
RUN_ENV=()
while IFS='=' read -r _n _; do
  case "$_n" in
    ROKSBNKCTL_HOME) ;;                       # host path; the container sets its own
    ROKSBNKCTL_*)    RUN_ENV+=(-e "$_n") ;;
  esac
done < <(env)

RUN=(docker run --rm -v "$WORK:/work"
     -e IBMCLOUD_API_KEY -e BNK_FORGE_URL -e BNK_FORGE_USER -e BNK_FORGE_PASSWORD
     ${RUN_ENV[@]+"${RUN_ENV[@]}"}
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
  # `must`, not `run`. A teardown that fails must not report success: the next
  # run then races a half-deleted VPC and dies on "Provided Name … is not
  # unique", which names the symptom and not the cause. Observed exactly that —
  # a destroy failed with "context deadline exceeded", teardown returned 0, and
  # the following run failed six minutes later on a name collision.
  must "${RUN[@]}" down --auto
  ok "teardown complete — cluster ${WS} and every phase of the workspace are gone"
}
# Propagate the teardown's status. `exit 0` here discarded it, so a caller
# driving repeated runs could not tell a clean teardown from a wedged one.
[[ "${1:-}" == "teardown" ]] && { teardown; exit $?; }

# Source .env when EITHER the API key or the Forge credentials are missing.
# Keying only on IBMCLOUD_API_KEY meant that exporting the key in your shell made
# the whole file invisible, so FORGE_* sitting in .env were never read and the
# registration phase was silently skipped (#164 review). `set -a` sourcing does
# not clobber an already-exported value, so an override on the command line wins.
if [[ -f "$HERE/.env" ]] && [[ -z "${IBMCLOUD_API_KEY:-}" || -z "${FORGE_URL:-}${BNK_FORGE_URL:-}" ]]; then
  set -a; source "$HERE/.env"; set +a
fi
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"
export IBMCLOUD_API_KEY
# #164: Forge gates phase 4 ONLY. All three set = run it; none set = skip it and
# run the other six; a partial set is a mistake and still dies. Exports BNK_FORGE_*
# when enabled — the runner reads them by name, never from argv.
forge_mode

# Resolved BEFORE the opening banner so the story below states what will actually
# happen. The banner is the first frame of the recording; announcing a phase that
# will be skipped is the same defect as claiming one ran (#164 review).
if [[ "$FORGE_ENABLED" == "true" ]]; then
  FORGE_INTRO_NOTE=""
else
  FORGE_INTRO_NOTE=" (SKIPPED — no FORGE_URL / FORGE_USER / FORGE_PASS set; nothing else needs them.)"
fi

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — THE CLUSTER LIFECYCLE AS A CI PIPELINE"
cat >&2 <<EOF
The story, in seven phases — every one of them a ${B}docker run${N}:
  1. The runner image ${B}IS${N} roksbnkctl. Nothing is installed on this host.
  2. One declarative ${B}config.yaml${N} on /work -> ${B}init${N}. No prompts, ever.
  3. ${B}cluster up${N} — a real ROKS cluster (${C}${WS}${N}, OCP ${OCP_VERSION}).
  4. ${B}bnkforge register${N} — hand the durable cluster to BNK Forge.${FORGE_INTRO_NOTE}
  5. ${B}bnk up${N} — BIG-IP Next for Kubernetes + its licence.
  6. ${B}testing up${N} + ${B}test${N} — jump hosts, then the probes.
  7. ${B}bnk down${N} then ${B}bnk up${N} — swap BNK, the cluster never moves.
Then the pipeline STOPS, leaving everything up so you can explore. \`teardown\` removes it.
EOF
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$FORGE_PASS"
command -v docker >/dev/null || die "docker not found — a CI runner already provides it (curl -fsSL https://get.docker.com | sh)"
[[ "$DRY_RUN" == "1" ]] || docker pull -q "$RUNNER" >/dev/null 2>&1 || note "could not pre-pull $RUNNER — the first step will pull it"
# The runner image runs as uid 1000, so the bind-mounted state dir must be
# writable; and init refuses to clobber an existing workspace, so start clean.
[[ "$DRY_RUN" == "1" ]] || { mkdir -p "$WORK"; chmod 777 "$WORK"; rm -rf "$WORK/.roksbnkctl/$WS"; }
if [[ "$FORGE_ENABLED" == "true" ]]; then
  ok "preflight: docker present, state volume $WORK ready, BNK Forge at $FORGE_URL"
else
  ok "preflight: docker present, state volume $WORK ready — no BNK Forge configured, phase 4 will be skipped"
fi

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
  # (matching the \`init\` interview). Phase 5/6 runs \`testing up\` + \`test\`, and
  # \`test\` runs its probes FROM a jumphost — so the demo must ask for one
  # explicitly or the testing phase provisions nothing.
  tgw_jumphost: { create: true }
  client_vpc:   { create: true }   # the jumphost lives in it
bnk:
  manifest_version: ${BNK_VERSION}
  far_repo_url: ${FAR_REPO_URL}
YAML
# Only when Forge is configured. Emitting it unconditionally put an empty stanza
# on camera and persisted `bnkforge: {insecure: true}` into the workspace — a
# config declaring TLS verification disabled against a server that does not
# exist (#164 review).
if [[ "$FORGE_ENABLED" == "true" ]]; then
  cat >> "$WORK/config.yaml" <<YAML
bnkforge:
  url: ${FORGE_URL}
  username: ${FORGE_USER}
  insecure: ${FORGE_INSECURE}
YAML
fi
show_file "$WORK/config.yaml"
must "${RUN[@]}" init --config-file /work/config.yaml --override-from-env
ok "workspace '$WS' seeded on the /work volume — it outlives every container"
endphase P2

# ============================ Phase 3: cluster up ============================
pause; phase P3 "PHASE 3/7  —  cluster up: build the ROKS cluster"   # LONG
say "A full hands-off build would just be 'roksbnkctl up'. Here the pipeline drives each phase as"
say "its own job — starting with the cluster: VPC, subnets, gateways and the ROKS workers."
begin_long
must "${RUN[@]}" cluster up --auto
end_long
run "${RUN[@]}" cluster config
ok "ROKS cluster '$WS' is up"
endphase P3

# ============================ Phase 4: BNK Forge =============================
pause; phase P4 "PHASE 4/7  —  bnkforge register: hand the cluster to BNK Forge"
if [[ "$FORGE_ENABLED" == "true" ]]; then
  say "roksbnkctl registers over BNK Forge's REST API. The URL, user and password come from the CI"
  say "environment — BNK_FORGE_* passed into the container by name, never on the command line."
  REG=(bnkforge register)
  [[ "$FORGE_INSECURE" == "true" ]] && REG+=(--insecure)
  run "${RUN[@]}" "${REG[@]}"
  ok "registered with BNK Forge"
else
  forge_skip_note "PHASE 4/7 (bnkforge register)"
fi
endphase P4

# ============================ Phase 5: bnk up ================================
pause; phase P5 "PHASE 5/7  —  bnk up: install BIG-IP Next for Kubernetes"   # LONG
say "The BNK phase installs BIG-IP Next for Kubernetes ${BNK_VERSION} and its licence onto the"
say "cluster from phase 3 — and it runs the Kubernetes verbs in-process, so the image needs no kubectl."
begin_long
must "${RUN[@]}" bnk up --auto
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
must "${RUN[@]}" testing up --auto
end_long
for h in $TEST_HOSTS; do
  must "${RUN[@]}" test hosts add "$h"
done
must "${RUN[@]}" test
ok "probes ran against: $TEST_HOSTS"
endphase P6

# ============================ Phase 7: bnk down, then bnk up =================
pause; phase P7 "PHASE 7/7  —  bnk down then bnk up: swap BNK, keep the cluster"   # LONG
say "Down JUST the BNK phase. The cluster and the testing framework keep running — a pipeline can"
say "redeploy BNK on every commit without ever re-provisioning a cluster."
begin_long
must "${RUN[@]}" bnk down --auto
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
must "${RUN[@]}" bnk up --auto
end_long
run "${RUN[@]}" k get pods -n f5-bnk
ok "BNK removed and reinstalled — no re-provisioning, the cluster never moved"
endphase P7

# The recorded cluster identity feeds the closing report — read from the /work volume.
MASTER_URL="$(jq -r '.master_url // empty' "$WORK/.roksbnkctl/$WS/cluster-outputs.json" 2>/dev/null || true)"

banner "DEMO COMPLETE"
# See the CLI demo's note: the closing frame must not claim a skipped phase ran.
if [[ "$FORGE_ENABLED" == "true" ]]; then
  PHASE_CHAIN="cluster up -> bnkforge register -> bnk up -> testing up + test -> bnk down -> bnk up"
  FORGE_LINE="  BNK Forge:          ${FORGE_URL}   (login: ${FORGE_USER} / your FORGE_PASS)"
else
  PHASE_CHAIN="cluster up -> bnk up -> testing up + test -> bnk down -> bnk up   (bnkforge register skipped)"
  FORGE_LINE="  BNK Forge:          not registered — set FORGE_URL / FORGE_USER / FORGE_PASS to include phase 4"
fi
cat >&2 <<EOF
Nothing was ever installed on this host. Every step was one docker run of
  ${RUNNER}
  ${PHASE_CHAIN}
  Cluster ${WS} (${REGION}); workspace state lives on the /work volume at ${WORK}.

Reachable web UIs (explore before you tear down):
${FORGE_LINE}
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
