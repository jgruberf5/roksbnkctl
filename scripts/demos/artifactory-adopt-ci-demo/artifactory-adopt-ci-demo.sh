#!/usr/bin/env bash
# =============================================================================
# artifactory-adopt-ci-demo.sh
#
# The shape most customers actually arrive in, driven the way it actually ships.
#
# They already have a transit gateway, a VPC and a ROKS cluster — built by their
# own platform team, to their own standards. The cluster is DISCONNECTED, so it
# pulls from a registry they already run, which is far more often JFrog
# Artifactory than anything this tool would build. roksbnkctl is not asked to
# create any of that. It is asked to do BNK, on what is already there.
#
#   1. the runner container IS roksbnkctl   (version + doctor; nothing installed here)
#   2. init from the ENVIRONMENT ALONE      — no config.yaml exists anywhere
#   3. cluster register + kubeconfig        — adopt the cluster they built
#   4. registry target + replicate          — mirror the FAR into their Artifactory
#   5. bnk up                               — BIG-IP Next for Kubernetes + its licence
#   6. verify                               — pods, licence, and the 2.4 CR itself
#
# WHAT MAKES THIS DIFFERENT from cluster-lifecycle-ci-demo:
#
#   * Nothing is created. cluster.create=false, transit_gateway adopted by name,
#     and the cluster is REGISTERED rather than built.
#   * There is no config.yaml. Every setting arrives as `docker run -e`, which is
#     the only shape available to an argv-only runner: no shell, no prompts,
#     nowhere to stage a file. A demo that writes a YAML file first is not
#     testing that path.
#   * cert-manager is ADOPTED. An OpenShift estate usually installs it as a day-1
#     add-on, and `bnk up` then stops with `namespaces "cert-manager" already
#     exists`. ROKSBNKCTL_CERT_MANAGER_CREATE=false is what gets past it, and
#     because it is adopted, `bnk down` cannot delete it or the certs it issued.
#   * The registry is EXTERNAL and AUTHENTICATED. Artifactory needs a real
#     credential; without one the chart pull authenticates as the literal
#     "unused" and is refused 401.
#
# Non-interactive throughout: secrets arrive as `docker run -e` NAMES, so no value
# ever appears in argv, a process list, or a recording. State persists on the
# mounted /work volume.
#
# The demo does NOT tear itself down — it ends with what to look at.
# `./artifactory-adopt-ci-demo.sh teardown` removes ONLY what it installed: BNK.
# The cluster, the VPC, the transit gateway and Artifactory are the customer's
# and are never touched.
#
# Linux / WSL. Requires: docker. Nothing else — that is the point.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

# ============================ CONFIG (edit me) ===============================
REGION="${REGION:-us-east}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"

# What the customer already has. None of these are created by this pipeline.
EXISTING_CLUSTER="${EXISTING_CLUSTER:-}"          # required — the ROKS cluster to adopt
EXISTING_TGW="${EXISTING_TGW:-}"                  # required — transit gateway, by name or id
PREFIX="${PREFIX:-bnkadopt}"

# Their Artifactory.
ART_DOMAIN="${ART_DOMAIN:-}"                      # required — e.g. artifactory.example.com
ART_REPO="${ART_REPO:-docker-local}"              # the repository path artifacts nest under
ART_USER="${ART_USER:-}"                          # required
ART_TOKEN="${ART_TOKEN:-}"                        # required — access token or password
ART_CA_FILE="${ART_CA_FILE:-}"                    # optional — PEM, if it is self-signed

BNK_VERSION="${BNK_VERSION:-2.4.0-EA}"
FAR_AUTH_FILE="${FAR_AUTH_FILE:-}"                # required — the F5 service-account .tgz
SUBSCRIPTION_JWT_FILE="${SUBSCRIPTION_JWT_FILE:-}"   # required — the subscription JWT

RUNNER_TAG="${RUNNER_TAG:-v1.54.0}"
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
WS="${CI_WORKSPACE:-bnk-adopt}"
WORK="${CI_WORK:-$HOME/bnk-adopt-state}"          # the state volume — mounted at /work

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

secret "$ART_TOKEN"
secret "${IBMCLOUD_API_KEY:-}"

# Every ROKSBNKCTL_* in the environment is forwarded to the container BY NAME, so
# the value never appears in argv, a process list or a recording. A hand-written
# list would drift the moment a new override is added — the same disease the
# production code had removed from it — so range the environment instead.
#
# ROKSBNKCTL_HOME is the one that must NOT be forwarded: it names a directory on
# THIS host, while the container sets its own to /work/.roksbnkctl, which is the
# mounted volume and the entire point of it. Forwarding it makes the container
# try to write to a path it cannot see:
#
#   saving workspace: creating /mnt/d/…/home: mkdir /mnt/d: permission denied
#
build_env(){
  RUN_ENV=()
  while IFS='=' read -r _n _; do
    case "$_n" in
      ROKSBNKCTL_HOME) ;;                    # host path; the container sets its own
      ROKSBNKCTL_*)    RUN_ENV+=(-e "$_n") ;;
    esac
  done < <(env)
}

export ROKSBNKCTL_REGION="$REGION"
export ROKSBNKCTL_RESOURCE_GROUP="$RESOURCE_GROUP"
export ROKSBNKCTL_PREFIX="$PREFIX"
export ROKSBNKCTL_CLUSTER_NAME="$EXISTING_CLUSTER"
export ROKSBNKCTL_CLUSTER_CREATE=false
export ROKSBNKCTL_TRANSIT_GATEWAY_NAME="$EXISTING_TGW"
# Adopt the cert-manager the cluster already runs. See the header.
export ROKSBNKCTL_CERT_MANAGER_CREATE=false
export ROKSBNKCTL_REGISTRY_TARGET=generic
export ROKSBNKCTL_GENERIC_HOST="$ART_DOMAIN"
export ROKSBNKCTL_GENERIC_REPO_PREFIX="$ART_REPO"
export ROKSBNKCTL_GENERIC_USERNAME="$ART_USER"
export ROKSBNKCTL_GENERIC_PASSWORD="$ART_TOKEN"
export ROKSBNKCTL_MANIFEST_VERSION="$BNK_VERSION"
export ROKSBNKCTL_FAR_AUTH_LOCAL_FILE=/work/far-auth.tgz
export ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE=/work/subscription.jwt

# AFTER the exports above — build_env ranges the environment, so calling it any
# earlier would forward nothing.
build_env
RUN=(docker run --rm -v "$WORK:/work" -e IBMCLOUD_API_KEY ${RUN_ENV[@]+"${RUN_ENV[@]}"} "$RUNNER" -w "$WS")

# ============================ teardown =======================================
# Removes ONLY what this pipeline installed: BNK. The cluster, its VPC, the
# transit gateway and Artifactory belong to the customer and were adopted, never
# created — so there is nothing of theirs to remove, and `bnk down` is the whole
# teardown. cert-manager was adopted too, so it survives this.
# ---------------------------------------------------------------------------
teardown(){
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  banner "TEARDOWN — remove BNK, leave everything the customer owns"
  say "The cluster, VPC, transit gateway and Artifactory were ADOPTED. Only BNK is removed."
  must "${RUN[@]}" bnk down --auto
  ok "teardown complete — BNK removed; cluster '$EXISTING_CLUSTER' and cert-manager untouched"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit $?; }

# ============================ preflight ======================================
command -v docker >/dev/null || die "docker is required — this demo installs nothing else"
[[ -n "$EXISTING_CLUSTER" ]] || die "set EXISTING_CLUSTER — the ROKS cluster to adopt"
[[ -n "$EXISTING_TGW" ]]     || die "set EXISTING_TGW — the transit gateway to attach to"
[[ -n "$ART_DOMAIN" ]]       || die "set ART_DOMAIN — the Artifactory host"
[[ -n "$ART_USER" ]]         || die "set ART_USER"
[[ -n "$ART_TOKEN" ]]        || die "set ART_TOKEN — without it the chart pull is refused 401"
[[ -n "$FAR_AUTH_FILE" && -f "$FAR_AUTH_FILE" ]] || die "set FAR_AUTH_FILE to the F5 service-account .tgz"
[[ -n "$SUBSCRIPTION_JWT_FILE" && -f "$SUBSCRIPTION_JWT_FILE" ]] || die "set SUBSCRIPTION_JWT_FILE"
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env)"
export IBMCLOUD_API_KEY

mkdir -p "$WORK"
cp "$FAR_AUTH_FILE" "$WORK/far-auth.tgz"
cp "$SUBSCRIPTION_JWT_FILE" "$WORK/subscription.jwt"
[[ -n "$ART_CA_FILE" && -f "$ART_CA_FILE" ]] && cp "$ART_CA_FILE" "$WORK/artifactory-ca.pem"
ok "preflight: docker present, state volume $WORK ready, credentials staged"

# ============================ Phase 1: the runner ============================
phase P1 "PHASE 1/6  —  the runner container IS roksbnkctl"
say "Nothing is installed on this host. Every step below is one 'docker run' of the"
say "all-in-one runner image — exactly what a CI job calls."
run "${RUN[@]}" version
run "${RUN[@]}" doctor
ok "the runner is the tool"
endphase P1

# ============================ Phase 2: init from env =========================
pause; phase P2 "PHASE 2/6  —  init from the ENVIRONMENT ALONE"
say "There is no config.yaml anywhere in this demo. An argv-only runner has no shell,"
say "no prompts and nowhere to stage a file, so the whole workspace is built from -e"
say "variables. Note what is being ADOPTED rather than created:"
say "  cluster.create=false          the cluster already exists"
say "  transit_gateway               attached by name, never created"
say "  cert_manager.create=false     the cluster already runs one"
must "${RUN[@]}" init --non-interactive --override-from-env
ok "workspace '$WS' seeded on the /work volume — it outlives every container"
endphase P2

# ============================ Phase 3: adopt the cluster =====================
pause; phase P3 "PHASE 3/6  —  adopt the cluster the customer built"
say "'cluster register' takes over an existing ROKS cluster: it records it in the"
say "workspace and attaches its VPC to the transit gateway. It builds nothing."
must "${RUN[@]}" cluster register "$EXISTING_CLUSTER"
must "${RUN[@]}" kubeconfig --download
run  "${RUN[@]}" k get nodes
ok "cluster '$EXISTING_CLUSTER' adopted"
endphase P3

# ============================ Phase 4: mirror into Artifactory ===============
pause; phase P4 "PHASE 4/6  —  mirror the F5 Artifact Repository into Artifactory"
say "A disconnected cluster cannot reach repo.f5.com, so every chart and image BNK"
say "needs is copied BY DIGEST into the registry the customer already runs."
must "${RUN[@]}" registry target --target generic
run  "${RUN[@]}" registry bom
begin_long
must "${RUN[@]}" registry replicate --target generic
end_long
must "${RUN[@]}" registry verify
ok "the mirror is populated and verified — nothing below reaches the Internet for images"
endphase P4

# ============================ Phase 5: bnk up ================================
pause; phase P5 "PHASE 5/6  —  bnk up: BIG-IP Next for Kubernetes $BNK_VERSION"
say "Everything pulls from Artifactory. cert-manager is adopted, not installed."
begin_long
must "${RUN[@]}" bnk up --auto
end_long
ok "BNK is installed"
endphase P5

# ============================ Phase 6: verify ================================
pause; phase P6 "PHASE 6/6  —  verify what is actually running"
run "${RUN[@]}" k get pods -n f5-bnk
run "${RUN[@]}" k get pods -n f5-utils
run "${RUN[@]}" k get license -n f5-utils
say "And the CNEInstance itself — the 2.4 settings that place the TMM pods:"
run "${RUN[@]}" k get cneinstance -A -o yaml
say "cert-manager is still the customer's. roksbnkctl never managed it:"
run "${RUN[@]}" k get deploy -n cert-manager
ok "BNK $BNK_VERSION is running on an adopted, disconnected cluster, from Artifactory"
endphase P6

banner "DONE — BNK installed on infrastructure roksbnkctl did not create"
say "Nothing here was built by this pipeline except BNK itself."
say "Tear it down with:  $0 teardown"
