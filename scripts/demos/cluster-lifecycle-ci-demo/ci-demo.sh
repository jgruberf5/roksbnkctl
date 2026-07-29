#!/usr/bin/env bash
#
# ci-demo.sh — Demo #2: the roksbnkctl-tools-runner container as a CI pipeline.
#
# Same phase-by-phase story as cli-demo.sh, but ZERO host install — every step is a
# `docker run` of the all-in-one runner image, exactly what a CI job calls.
# Non-interactive, secrets via env, state persisted in a mounted /work volume.
#
#   build cluster → register with BNK Forge → install BNK (+license) →
#   testing framework + tests → remove BNK → rebuild BNK → `down` removes all.
# The closing `down` destroys the cluster ON CAMERA, so the demo self-cleans.
#
#   ./ci-demo.sh            # run the demonstration
#   ./ci-demo.sh teardown   # emergency `down` if a shoot aborts
#
# Inputs come from demo.env. Optional: RUNNER_IMAGE / RUNNER_TAG, CI_WORKSPACE
# (default roksbnkctl-ci), PACE / TYPE_MIN / TYPE_MAX / READ_DELAY / STAGE_HOLD.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
trap 'echo $? > "$SCRIPT_DIR/demo_exit"' EXIT
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/demo.env}"
# shellcheck disable=SC1090
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE" || { echo "✗ $ENV_FILE not found — run ./prompt-inputs.sh first." >&2; exit 1; }
# shellcheck disable=SC1091
source "$SCRIPT_DIR/demo-lib.sh"

: "${IBMCLOUD_API_KEY:?}"; : "${REGION:?}"; : "${RESOURCE_GROUP:?}"
: "${OCP_VERSION:?}"; : "${WORKERS_PER_ZONE:?}"; : "${BNK_VERSION:?}"
: "${FORGE_URL:?}"; : "${FORGE_USER:?}"; : "${FORGE_PASS:?}"

WS="${CI_WORKSPACE:-roksbnkctl-ci}"
PREFIX="$WS"
RUNNER_TAG="${RUNNER_TAG:-v1.17.5}"
RUNNER="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:$RUNNER_TAG}"
WORK="${CI_WORK:-$HOME/bnk-ci-state}"     # the state volume — mounted at /work
FORGE_INSECURE="${FORGE_INSECURE:-true}"

export IBMCLOUD_API_KEY
export BNK_FORGE_URL="$FORGE_URL" BNK_FORGE_USER="$FORGE_USER" BNK_FORGE_PASSWORD="$FORGE_PASS"

# RUN is the runner invocation each CI step calls. The image ENTRYPOINT is
# roksbnkctl, so args after the image are roksbnkctl args. -e passes secrets by
# name; -v mounts the state volume at /work.
RUN=(docker run --rm -v "$WORK:/work"
     -e IBMCLOUD_API_KEY -e BNK_FORGE_URL -e BNK_FORGE_USER -e BNK_FORGE_PASSWORD
     "$RUNNER" -w "$WS")

# ── emergency teardown (a normal run ends with `down` on camera) ─────────────
if [[ "${1:-}" == "teardown" ]]; then
  stage "T" "Destroying the workspace"; run "${RUN[@]}" down --auto || true
  echo; say "Teardown complete."; exit 0
fi

# ── prep: docker present + writable, clean state dir (off-narrative) ──────────
if ! command -v docker >/dev/null 2>&1; then
  echo "▶ Installing docker (a CI runner already provides it)…" >&2
  curl -fsSL https://get.docker.com | sh >/dev/null 2>&1
fi
docker pull -q "$RUNNER" >/dev/null 2>&1 || true
# runner image runs as uid 1000 — make the bind-mounted state dir writable, and
# start the workspace clean (init aborts on an existing one).
mkdir -p "$WORK"; chmod 777 "$WORK"
rm -rf "$WORK/.roksbnkctl/$WS"

# ── 1) zero host install ─────────────────────────────────────────────────────
stage "1/9" "The runner container is a complete roksbnkctl"
say "Nothing is installed on this host — the runner image bundles roksbnkctl, terraform, helm, and all the tooling."
run docker run --rm "$RUNNER" version
run docker run --rm "$RUNNER" doctor || true

# ── 2) declarative, non-interactive config ───────────────────────────────────
stage "2/9" "Configuring declaratively"
say "One config.yaml is the whole input — no prompts. Secrets stay in the CI environment."
{
  echo "ibmcloud:"; echo "  region: $REGION"; echo "  resource_group: $RESOURCE_GROUP"
  echo "prefix: $PREFIX"
  echo "cluster:"; echo "  create: true"; echo "  name: $WS"
  echo "  openshift_version: \"$OCP_VERSION\""; echo "  workers_per_zone: $WORKERS_PER_ZONE"
  echo "tf_source:"; echo "  type: embedded"
  echo "bnk:"; echo "  manifest_version: $BNK_VERSION"
  [[ -n "${FAR_REPO_URL:-}" ]] && echo "  far_repo_url: $FAR_REPO_URL"
  echo "bnkforge:"; echo "  url: $FORGE_URL"; echo "  username: $FORGE_USER"; echo "  insecure: $FORGE_INSECURE"
} > "$WORK/config.yaml"
run cat "$WORK/config.yaml"
run "${RUN[@]}" init --config-file /work/config.yaml --override-from-env

# ── 3) build the ROKS cluster ────────────────────────────────────────────────
stage "3/9" "Build the ROKS cluster"
say "A full hands-off build would just be 'roksbnkctl up'. Here the pipeline drives each phase on its own — starting with the cluster."
run "${RUN[@]}" cluster up --auto

# ── 4) register the cluster with BNK Forge ───────────────────────────────────
stage "4/9" "Register with BNK Forge"
say "roksbnkctl registers over BNK Forge's REST API; the password comes from the CI env, never the command line."
REG=(bnkforge register)
[[ "$FORGE_INSECURE" == "true" ]] && REG+=(--insecure)
run "${RUN[@]}" "${REG[@]}"

# ── 5) install BIG-IP Next for Kubernetes (+ license) ────────────────────────
stage "5/9" "Install BIG-IP Next for Kubernetes"
say "The BNK phase deploys BIG-IP Next for Kubernetes and its trial license onto the cluster."
run "${RUN[@]}" bnk up --auto

# ── 6) build the testing framework + run tests ───────────────────────────────
stage "6/9" "Build the testing framework and run tests"
say "The testing phase stands up the jump host(s) for the connectivity / DNS / throughput probes; then we run them."
run "${RUN[@]}" testing up --auto
for h in ${TEST_HOSTS:-https://www.example.com}; do run "${RUN[@]}" test hosts add "$h"; done
run "${RUN[@]}" test

# ── 7) remove BIG-IP Next for Kubernetes (phase down; cluster stays) ─────────
stage "7/9" "Remove BIG-IP Next for Kubernetes"
say "Down just the BNK phase — the cluster and testing framework keep running."
run "${RUN[@]}" bnk down --auto

# ── 8) rebuild BIG-IP Next for Kubernetes (e.g. a new version) ───────────────
stage "8/9" "Rebuild BIG-IP Next for Kubernetes — new version"
say "Reinstall the BNK phase — for example a new version — on the same running cluster, no re-provisioning."
run "${RUN[@]}" bnk up --auto

# ── 9) remove everything with a single down ──────────────────────────────────
stage "9/9" "Remove all phases with one down"
say "A single 'down' removes any or all phases — including the cluster itself."
run "${RUN[@]}" down --auto

echo; printf '\033[1;32m✓ Back where we started — the runner container drove every phase independently, then removed them all with one down.\033[0m\n'
