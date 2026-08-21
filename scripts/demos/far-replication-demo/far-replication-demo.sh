#!/usr/bin/env bash
# =============================================================================
# far-replication-demo.sh  (roksbnkctl v1.51.0)
#
# Replicating the F5 Artifact Repository (FAR) into a private OCI registry with
# roksbnkctl. An air-gapped cluster cannot reach repo.f5.com, so every chart and
# image a BNK install needs must first be copied into a registry you control:
#
#   1. seed a mirror-only workspace -> init   (create: false — no cluster at all)
#   2. the FAR credential sitting in COS      (roksbnkctl reads it itself)
#   3. registry bom      — every chart and image a BNK install pulls
#   4. registry target   — four fields select the mirror; the password on stdin
#   5. registry diff     — what replication would copy
#   6. registry replicate — copy each artifact by digest
#   7. registry verify + list — confirm, then browse the mirror
#
# NO ROKS CLUSTER IS BUILT. `registry replicate` is a host-side registry-to-registry
# copy: it reads the workspace, pulls from FAR and pushes to the target, and never
# talks to a cluster. That keeps this demo to a few minutes.
#
# The private registry (a standard open-source Harbor — any OCI-compliant registry
# works) is stood up BEFORE recording, off-camera; how it is built is not part of
# this story. Provide its address and admin password as REGISTRY_DOMAIN +
# REGISTRY_ADMIN_PASSWORD (../lib/deploy-far-registry.sh prints both).
#
# The demo does NOT tear itself down: it ends with a report naming the Harbor UI so the
# operator can browse the mirror. `./far-replication-demo.sh teardown` empties the
# mirror and clears the workspace, leaving the registry host itself alone.
#
# Hands-off: AUTO_ADVANCE=1 (default) auto-advances between phases. Emits phase
# timestamps to $TS_FILE so record.sh can 10x the long phase (replicate) and hold
# each roksbnkctl command on screen for 5s in post.
#
# Linux / WSL. Requires: roksbnkctl v1.32.0, helm, jq.
# =============================================================================
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

# ============================ CONFIG (edit me) ===============================
REGION="${REGION:-ca-tor}"
RESOURCE_GROUP="${RESOURCE_GROUP:-default}"
BNK_VERSION="${BNK_VERSION:-2.3.0-3.2598.3-0.0.170}"   # the FAR manifest to mirror
FAR_REPO_URL="${FAR_REPO_URL:-repo.f5.com}"            # the SOURCE registry

# The FAR pull credential is NOT an input. roksbnkctl downloads bnk.far_auth_file
# from the orchestration COS bucket and extracts the service account itself
# (internal/cli/registry.go: resolveFARServiceAccount, called from buildBOM
# whenever registry.source_service_account_b64 is empty). These mirror the
# constants there; override only if your COS layout differs.
COS_INSTANCE="${COS_INSTANCE:-bnk-orchestration}"
COS_BUCKET="${COS_BUCKET:-bnk-schematics-resources}"
FAR_AUTH_FILE="${FAR_AUTH_FILE:-f5-far-auth-key.tgz}"

REGISTRY_DOMAIN="${REGISTRY_DOMAIN:-}"                 # the TARGET registry host
REGISTRY_ADMIN_PASSWORD="${REGISTRY_ADMIN_PASSWORD:-}" # its admin password
REGISTRY_PROJECT="${REGISTRY_PROJECT:-bnk-mirror}"     # project/repo prefix in it

WS="${FAR_WORKSPACE:-bnk-mirror}"                      # roksbnkctl workspace
ROKSBNKCTL_BIN="${ROKSBNKCTL_BIN:-roksbnkctl}"

# ============================ helpers ========================================
source "$HERE/../lib/demo-format.sh"

# ============================ teardown =======================================
# This demo provisions NO cloud infrastructure — no cluster, no VSI, no VPC. The only
# things it creates are the mirrored artifacts in the registry and a local workspace.
# Teardown empties the mirror it filled and clears that workspace; the REGISTRY ITSELF
# is left untouched, because it was built off-camera and this demo does not own it.
# The demo does NOT auto-tear-down, so you can browse the mirror first.
# ---------------------------------------------------------------------------
teardown(){
  # Runs standalone, so resolve the credentials itself.
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || { [[ -f "$HERE/.env" ]] && { set -a; . "$HERE/.env"; set +a; }; }
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY (or provide .env) to tear down"
  export IBMCLOUD_API_KEY
  secret "$IBMCLOUD_API_KEY" "${REGISTRY_ADMIN_PASSWORD:-}"
  banner "TEARDOWN — FAR-replication demo"
  local WSDIR="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
  if [[ -d "$WSDIR" ]]; then
    say "Delete the artifacts this demo pushed into ${REGISTRY_DOMAIN:-the registry}/${REGISTRY_PROJECT}."
    run "$ROKSBNKCTL_BIN" -w "$WS" registry delete --force
    say "Clearing the local workspace (it is only local state — nothing is provisioned)."
    rm -rf "$WSDIR"
    ok "mirror emptied and workspace '$WS' cleared"
  else
    note "no workspace '$WS' — nothing to tear down"
  fi
  ok "teardown complete — the registry host itself was left untouched (this demo never built it)"
}
[[ "${1:-}" == "teardown" ]] && { teardown; exit 0; }

# ============================ Phase 0: preflight =============================
banner "roksbnkctl — MIRROR THE F5 ARTIFACT REPOSITORY INTO A PRIVATE REGISTRY"
cat >&2 <<EOF
The story, in seven phases — and ${B}no cluster anywhere${N}:
  1. A mirror-only workspace: ${B}cluster.create: false${N}. Nothing is provisioned.
  2. The FAR credential is an ${B}auth tarball in COS${N} — roksbnkctl reads it itself.
  3. ${B}registry bom${N} — every chart and image a BNK install pulls.
  4. ${B}registry target${N} — four fields select ${C}${REGISTRY_DOMAIN:-<registry>}${N}; password on stdin.
  5. ${B}registry diff${N} — what replication would copy.
  6. ${B}registry replicate${N} — each artifact copied by digest, straight across.
  7. ${B}registry verify${N} + ${B}list${N} — digest-matched, then browse the mirror.
EOF
[[ -z "${IBMCLOUD_API_KEY:-}" && -f "$HERE/.env" ]] && { set -a; source "$HERE/.env"; set +a; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || die "set IBMCLOUD_API_KEY"; export IBMCLOUD_API_KEY
[[ -n "$REGISTRY_DOMAIN" ]]         || die "set REGISTRY_DOMAIN — the private registry host (../lib/deploy-far-registry.sh builds one)"
[[ -n "$REGISTRY_ADMIN_PASSWORD" ]] || die "set REGISTRY_ADMIN_PASSWORD — deploy-far-registry.sh prints it"
# These demos are RECORDED: register every credential so banner/say/ok/show and
# show_file mask it (and its base64 form) as ***REDACTED*** before it hits the screen.
secret "$IBMCLOUD_API_KEY" "$REGISTRY_ADMIN_PASSWORD"
# roksbnkctl shells out to helm to pull the classic-Helm charts in the BOM; the
# image copies are crane, in-process.
for c in "$ROKSBNKCTL_BIN" helm jq; do command -v "$c" >/dev/null || die "$c not found"; done
# #143: print the binary + version this demo will actually run, and warn on drift.
preflight_binary "$ROKSBNKCTL_BIN"
run "$ROKSBNKCTL_BIN" version
ok "preflight: roksbnkctl + helm present, target registry $REGISTRY_DOMAIN"

# ============================ Phase 1: mirror-only workspace =================
pause; phase P1 "PHASE 1/7  —  A mirror-only workspace (no cluster)"
say "The mirror needs three things: the BNK version, the FAR repo, and the NAME of the auth"
say "tarball in COS. cluster.create is false — this workspace never provisions anything."
# Start clean: init refuses to clobber an existing workspace. (Not shown on camera —
# the path contains "roksbnkctl", which would trip the command-freeze marker.)
[[ "$DRY_RUN" == "1" ]] || rm -rf "${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}/$WS"
SEED="$STATE_DIR/${WS}-config.yaml"
cat > "$SEED" <<YAML
ibmcloud: { region: ${REGION}, resource_group: ${RESOURCE_GROUP} }
prefix: ${WS}
tf_source: { type: embedded }
cluster: { create: false, name: none }
bnk:
  manifest_version: ${BNK_VERSION}
  far_repo_url: ${FAR_REPO_URL}
  far_auth_file: ${FAR_AUTH_FILE}
YAML
say "Nothing secret is in the seed: the FAR credential is NAMED, not embedded."
show_file "$SEED"
run "$ROKSBNKCTL_BIN" -w "$WS" init --config-file "$SEED" --override-from-env
ok "workspace '$WS' seeded — no cluster, no infrastructure"
endphase P1

# ============================ Phase 2: the FAR credential in COS =============
pause; phase P2 "PHASE 2/7  —  The FAR credential lives in COS"
say "The credential that unlocks repo.f5.com is an auth tarball in the orchestration COS bucket."
say "roksbnkctl reads it straight from COS — it is never pasted into a config and never lands as"
say "a file on this host. The bucket is in us-south even when the workspace is not; roksbnkctl"
say "resolves each bucket's own region."
run "$ROKSBNKCTL_BIN" -w "$WS" cos object list "$COS_BUCKET" --instance "$COS_INSTANCE"
ok "the credential ($FAR_AUTH_FILE) is in COS — that is the whole supply-chain secret"
endphase P2

# ============================ Phase 3: the bill of materials =================
pause; phase P3 "PHASE 3/7  —  The bill of materials"
say "roksbnkctl pulls that auth tarball from COS to authenticate, reads the F5 manifest for"
say "BNK ${BNK_VERSION}, and derives every chart and image a BNK install pulls."
run "$ROKSBNKCTL_BIN" -w "$WS" registry bom
ok "the BOM is the contract: mirror all of it and the install needs nothing from F5"
endphase P3

# ============================ Phase 4: point at the mirror ===================
pause; phase P4 "PHASE 4/7  —  Point the workspace at the mirror"
say "The target is a standard open-source Harbor — any OCI-compliant registry works; roksbnkctl"
say "does not care which. It is already running, built off-camera."
run "$ROKSBNKCTL_BIN" -w "$WS" registry target generic
run "$ROKSBNKCTL_BIN" -w "$WS" registry target generic_host "$REGISTRY_DOMAIN"
run "$ROKSBNKCTL_BIN" -w "$WS" registry target generic_repo_prefix "$REGISTRY_PROJECT"
run "$ROKSBNKCTL_BIN" -w "$WS" registry target generic_username admin
say "The password is piped in on stdin, so it never lands in argv or in shell history."
show "printf '%s' \"\$REGISTRY_ADMIN_PASSWORD\" | roksbnkctl -w $WS registry target generic_password --password-stdin"
[[ "$DRY_RUN" == "1" ]] || printf '%s' "$REGISTRY_ADMIN_PASSWORD" | "$ROKSBNKCTL_BIN" -w "$WS" registry target generic_password --password-stdin
run "$ROKSBNKCTL_BIN" -w "$WS" registry target
ok "target set: $REGISTRY_DOMAIN/$REGISTRY_PROJECT"
endphase P4

# ============================ Phase 5: diff ==================================
pause; phase P5 "PHASE 5/7  —  What replication would copy"
say "diff compares the bill of materials against what the mirror already holds. Starting from an"
say "empty project, everything is missing — and on a re-run, only the delta shows up here."
run "$ROKSBNKCTL_BIN" -w "$WS" registry diff
endphase P5

# ============================ Phase 6: replicate =============================
pause; phase P6 "PHASE 6/7  —  Replicate FAR into the mirror"   # LONG
say "Each artifact is copied BY DIGEST, straight from repo.f5.com into the registry — charts and"
say "images alike. Artifacts already present are skipped, so this is safely re-runnable."
begin_long
run "$ROKSBNKCTL_BIN" -w "$WS" registry replicate --target generic
end_long
ok "every BOM artifact now lives in $REGISTRY_DOMAIN"
endphase P6

# ============================ Phase 7: verify + list =========================
pause; phase P7 "PHASE 7/7  —  Verify by digest, then browse the mirror"
say "verify re-reads the bill of materials and confirms each artifact is present in the mirror"
say "AND digest-matched — not merely present under the right name."
run "$ROKSBNKCTL_BIN" -w "$WS" registry verify
say "Everything a BNK install needs now lives in a registry we control. An air-gapped cluster"
say "installs from here, and never resolves repo.f5.com at all."
run "$ROKSBNKCTL_BIN" -w "$WS" registry list
endphase P7

banner "DEMO COMPLETE"
cat >&2 <<EOF
FAR mirrored into a private OCI registry and verified by digest:
  source  ${FAR_REPO_URL} (BNK ${BNK_VERSION}), credential read from COS bucket ${COS_BUCKET}
  target  ${REGISTRY_DOMAIN}/${REGISTRY_PROJECT}
  one roksbnkctl workspace, and no cluster anywhere.

Reachable web UIs (explore before you tear down):
  Harbor registry:  https://${REGISTRY_DOMAIN}/   (login: admin / your REGISTRY_ADMIN_PASSWORD)
                    browse the '${REGISTRY_PROJECT}' project — every BNK chart and image is now in it
  Or from the CLI:  roksbnkctl -w ${WS} registry list

Nothing was torn down — the mirror is full and ready for an air-gapped install.
Tear down when finished (empties the mirror; the registry HOST is left untouched):
  ./far-replication-demo.sh teardown

Capture queue for the post-process: ${TS_FILE}
EOF
