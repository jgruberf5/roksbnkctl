#!/usr/bin/env bash
# =============================================================================
# artifactory-mirror-demo.sh
#
# Mirroring the F5 Artifact Repository into a SELF-HOSTED JFrog Artifactory
# running on a single IBM Cloud VSI.
#
#   1. the Artifactory that is already running   (built off-camera; UI prereqs done)
#   2. a mirror-only workspace                   (cluster.create: false)
#   3. registry bom        — every chart and image a BNK install pulls
#   4. registry target     — point the workspace at Artifactory, password on stdin
#   5. registry diff       — what replication would copy
#   6. registry replicate  — copy each artifact by digest
#   7. registry verify     — the only step that proves anything
#   8. the same run as a CONTAINER, driven by an Argo workflow
#
# SCOPE: this demo shows the MIRROR succeeding. It builds no cluster and installs
# no BNK — `registry replicate` is a registry-to-registry copy that never talks
# to Kubernetes, which is what makes it a standalone supply-chain story and keeps
# it to about 25 minutes.
#
# Phase 8 is the point of the whole thing for a CI audience: phases 3-7 are the
# interactive path an operator uses to TEST a mirror, and phase 8 is the same
# work as an unattended container step. They are the same binary and the same
# verbs; only the way settings arrive changes — config.yaml for the CLI,
# ROKSBNKCTL_* environment variables for the container.
#
# Requires: roksbnkctl, jq, curl. For phase 8 also: kubectl + argo against a
# cluster running Argo Workflows (../lib/bootstrap-argo.sh stands one up).
# =============================================================================
set -uo pipefail

# Steps whose failure must change the final verdict (see the banner at the end).
FAILED_STEPS=()
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
source "$HERE/../lib/demo-format.sh"

[[ -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }

BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"
WS="${ART_WORKSPACE:-art-mirror}"
RBK="${ROKSBNKCTL_BIN:-roksbnkctl}"
REGION="${REGION:-us-east}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"

ART_DOMAIN="${ART_DOMAIN:-}"
# docker-local, not a name of our choosing. JCR cannot create a repository over
# REST ("400 available only in Artifactory Pro"), so the VSI bootstraps it via
# artifactory.config.import.yml -- whose OnboardingConfiguration schema takes
# only repoTypes and creates JFrog's DEFAULT names for each type.
ART_REPO="${ART_REPO:-docker-local}"
# admin: JCR has no user management at all (users, groups and permission targets
# are Pro-only), so there is no scoped identity to create.
ART_USER="${ART_USER:-admin}"
ART_TOKEN="${ART_TOKEN:-}"

# Where the FAR pull credential lives. The BUCKET has no usable default: COS
# bucket names are globally unique, so every account's is suffixed
# (bnk-artifacts-<hex>), and the built-in "bnk-artifacts" belongs to somebody
# else. Getting it wrong fails with AccessDenied rather than NotFound, which
# reads like a credentials problem and sends you looking in the wrong place.
COS_INSTANCE="${COS_INSTANCE:-bnk-supply-chain}"
COS_BUCKET="${COS_BUCKET:-}"
COS_REGION="${COS_REGION:-us-south}"

RUNNER_IMAGE="${RUNNER_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:v1.57.0}"
ARGO_NS="${ARGO_NS:-bnk-ci}"

# Everything secret goes through secret() BEFORE it can reach the screen. These
# demos get recorded, so a credential printed once is a credential published.
[[ -n "${IBMCLOUD_API_KEY:-}" ]] && secret "$IBMCLOUD_API_KEY"
[[ -n "$ART_TOKEN" ]] && secret "$ART_TOKEN"

need(){ command -v "$1" >/dev/null || die "$1 is required"; }
need jq; need curl; need "$RBK"
# #143: print the binary + version this demo will actually run, and warn on drift.
preflight_binary "$RBK"

[[ -n "$ART_DOMAIN" ]] || die "set ART_DOMAIN (the Artifactory host) — see .env.example"
[[ -n "$ART_TOKEN"  ]] || die "set ART_TOKEN (an Artifactory access token) — see .env.example"
[[ -n "$COS_BUCKET" ]] || die "set COS_BUCKET — the COS bucket holding the FAR credential (globally unique, e.g. bnk-artifacts-<hex>); \`ibmcloud cos buckets\` lists yours"

teardown(){
  banner "TEARDOWN — empty the mirror, keep Artifactory"
  say "The Artifactory host itself is left alone: this demo did not build it."
  run "$RBK" -w "$WS" registry delete --force || true
  ok "mirror emptied"
  exit 0
}
[[ "${1:-}" == "teardown" ]] && teardown

# --------------------------------------------------------------------------- 1
phase 1 "THE ARTIFACTORY THAT IS ALREADY RUNNING"
say "Built off-camera on one IBM Cloud VSI. Two things were done in its UI first,"
say "because they are what a real deployment does by hand:"
say "  • the admin password was set on first login"
say "  • a LOCAL Docker repository '$ART_REPO' was created"
say
say "A local repository, specifically. A remote repository is a read-through"
say "cache of an upstream and cannot be pushed to at all."
run curl -sf "https://$ART_DOMAIN/artifactory/api/system/ping"
echo >&2
say "and the repository we are about to fill:"
runmask curl -sf -u "$ART_USER:$ART_TOKEN" \
  "https://$ART_DOMAIN/artifactory/api/repositories/$ART_REPO"
echo >&2
note "Artifactory OSS could not do this. Docker repository support is a licensed
feature, so an OSS instance has no Docker repository type to create. This host
runs JFrog Container Registry — the free edition built around Docker and Helm
repositories, which is exactly what a FAR mirror needs."

# --------------------------------------------------------------------------- 2
phase 2 "A MIRROR-ONLY WORKSPACE"
say "No cluster is involved anywhere in this demo, so the workspace says so."
SEED="$(mktemp)"; trap 'rm -f "$SEED"' EXIT
# prefix and tf_source.type are REQUIRED by init --non-interactive; without them
# it refuses the file rather than defaulting. cluster.name: none goes with
# create: false to say plainly that no cluster is ever involved. far_auth_file
# NAMES the FAR credential in COS rather than embedding it, which is what keeps
# this seed safe to show on screen.
cat >"$SEED" <<YAML
ibmcloud: { region: $REGION, resource_group: $RESOURCE_GROUP }
prefix: $WS
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: $BNK_VERSION
  far_auth_file: ${FAR_AUTH_FILE:-f5-far-auth-key.tgz}
cos:
  instance: ${COS_INSTANCE:-bnk-supply-chain}
  bucket: $COS_BUCKET
  region: ${COS_REGION:-us-south}
YAML
show_file "$SEED"
run "$RBK" -w "$WS" init --config-file "$SEED" --non-interactive
ok "workspace $WS — no cluster, ever"

# --------------------------------------------------------------------------- 3
phase 3 "THE BILL OF MATERIALS"
say "Every chart and image a BNK $BNK_VERSION install pulls. Built offline from"
say "the FAR manifest — this step needs no registry at all."
run "$RBK" -w "$WS" registry bom

# --------------------------------------------------------------------------- 4
phase 4 "POINT THE WORKSPACE AT ARTIFACTORY"
say "Four fields. The token arrives on STDIN, never as an argument — an argument"
say "is visible in ps output and in a terminal recording, and this is a recording."
run "$RBK" -w "$WS" registry target generic
run "$RBK" -w "$WS" registry target generic_host "$ART_DOMAIN"
run "$RBK" -w "$WS" registry target generic_repo_prefix "$ART_REPO"
run "$RBK" -w "$WS" registry target generic_username "$ART_USER"
show "echo \"\$ART_TOKEN\" | $RBK -w $WS registry target generic_password --password-stdin"
printf '%s' "$ART_TOKEN" | "$RBK" -w "$WS" registry target generic_password --password-stdin
say
say "roksbnkctl addresses artifacts as <host>/<prefix>/<image>, which is"
say "Artifactory's REPOSITORY-PATH method. On a subdomain-configured instance"
say "the whole subdomain goes in generic_host and the prefix is left empty."
runmask "$RBK" -w "$WS" registry target

# --------------------------------------------------------------------------- 5
phase 5 "WHAT REPLICATION WOULD COPY"
run "$RBK" -w "$WS" registry diff

# --------------------------------------------------------------------------- 6
phase 6 "REPLICATE FAR INTO ARTIFACTORY"
say "Registry to registry, by digest. Nothing is written to disk and no cluster"
say "is contacted; this host is only the pipe between repo.f5.com and Artifactory."
begin_long
runmask "$RBK" -w "$WS" registry replicate || FAILED_STEPS+=("registry replicate")
end_long

# --------------------------------------------------------------------------- 7
phase 7 "VERIFY — THE STEP THAT PROVES IT"
say "replicate reports what it pushed. verify re-reads the bill of materials and"
say "checks every artifact against the digest it should have, so a partial copy or"
say "a tag that moved underneath us is caught here rather than at install time."
run "$RBK" -w "$WS" registry verify || FAILED_STEPS+=("registry verify")
run "$RBK" -w "$WS" registry diff
ok "diff is empty — the mirror and the bill of materials agree"

# --------------------------------------------------------------------------- 8
phase 8 "THE SAME MIRROR, AS A CONTAINER"
say "Phases 3-7 are the interactive path: an operator with a workspace, testing"
say "a mirror by hand. Unattended, the same verbs run in a container with no"
say "config.yaml anywhere — every setting arrives as an environment variable."
say
show_file "$HERE/wf-artifactory-mirror.yaml"
if command -v argo >/dev/null && command -v kubectl >/dev/null && kubectl get ns "$ARGO_NS" >/dev/null 2>&1; then
  say "submitting to Argo — same binary, same verbs, no workspace file"
  run kubectl -n "$ARGO_NS" create secret generic artifactory-mirror \
    --from-literal=host="$ART_DOMAIN" \
    --from-literal=repo="$ART_REPO" \
    --from-literal=username="$ART_USER" \
    --from-literal=password="$ART_TOKEN" \
    --dry-run=client -o yaml >/dev/null
  # ibmcloud-api-key is NOT optional in practice. The workflow declares it
  # optional so a mirror with a pre-seeded source credential can run without
  # one, but `registry bom` resolves the FAR service account from COS and needs
  # a key to do it — omit it and the workflow's first real step exits 1 with
  # "no IBM Cloud API key for workspace".
  ART_SECRET_ARGS=(--from-literal=host="$ART_DOMAIN" --from-literal=repo="$ART_REPO"
                   --from-literal=username="$ART_USER" --from-file=password=/dev/stdin)
  if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
    ART_SECRET_ARGS+=(--from-literal=ibmcloud-api-key="$IBMCLOUD_API_KEY")
  else
    say "  ⚠ IBMCLOUD_API_KEY is not set — the Argo run will fail at registry bom,"
    say "    which resolves the FAR service account from COS."
  fi
  printf '%s' "$ART_TOKEN" | kubectl -n "$ARGO_NS" create secret generic artifactory-mirror \
    "${ART_SECRET_ARGS[@]}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  begin_long
  # The COS coordinates have to be PASSED. wf-artifactory-mirror.yaml declares
  # cos-bucket with an empty default on purpose — bucket names are globally
  # unique, so every account's is suffixed (bnk-artifacts-<hex>) and no default
  # can be right. Leaving it empty does not fail loudly: the tool falls back to
  # a bare "bnk-artifacts", which belongs to somebody else, and the run dies on
  # a COS 403 that reads like a permissions problem rather than a missing
  # setting.
  runmask argo submit -n "$ARGO_NS" "$HERE/wf-artifactory-mirror.yaml" \
    -p runner-image="$RUNNER_IMAGE" -p bnk-version="$BNK_VERSION" \
    -p cos-instance="${COS_INSTANCE:-}" -p cos-bucket="${COS_BUCKET:-}" \
    -p cos-region="${COS_REGION:-}" --wait \
    || FAILED_STEPS+=("argo submit (the container path)")
  end_long
  ok "the container reached the same verified mirror"
else
  note "No Argo cluster reachable, so the workflow is shown but not submitted.
Stand one up with ../lib/bootstrap-argo.sh, or apply the YAML above to any
cluster running Argo Workflows. The workflow needs only the artifactory-mirror
secret and the runner image."
fi

# The banner asserts a fact, so it has to be earned. This demo used to print
# "DONE — FAR IS MIRRORED" unconditionally, and did exactly that after both
# `registry replicate` and `registry verify` failed on a missing API key —
# a green banner over a red screen, on a demo that gets recorded.
if (( ${#FAILED_STEPS[@]} )); then
  banner "FAILED — THE MIRROR IS NOT COMPLETE"
  say "These steps did not succeed:"
  for f in "${FAILED_STEPS[@]}"; do say "  ✗ $f"; done
  say "Scroll up for the error. Nothing below this line was proven."
  exit 1
fi

banner "DONE — FAR IS MIRRORED INTO ARTIFACTORY"
say "Browse it:  https://$ART_DOMAIN/ui/repos/tree/General/$ART_REPO"
say "Empty it:   ./artifactory-mirror-demo.sh teardown"
