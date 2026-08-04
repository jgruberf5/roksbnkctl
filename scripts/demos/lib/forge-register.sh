#!/usr/bin/env bash
#
# forge-register.sh — register this workspace's ROKS cluster with BNK Forge v3
# via its REST API. BNK Forge v3 has NO CLI (it's REST/UI-first), so this is the
# faithful integration path — roksbnkctl's `bnkforge register` targets the older
# v1 CLI and does not work against v3.
#
#   forge-register.sh [workspace]
#
# Idempotent (find-or-create): authenticates, ensures a default IBM credential
# template (holding the IBM Cloud API key so Forge can derive the cert-based
# kubeconfig on demand), ensures a project, then registers the cluster and
# reads it back.
#
# Env: FORGE_URL, FORGE_USER, FORGE_PASS, IBMCLOUD_API_KEY, REGION,
#      RESOURCE_GROUP, CLUSTER_NAME. Optional: FORGE_PROJECT (default roks-demo),
#      FORGE_CRED_NAME (default roksbnkctl-ibm), FORGE_INSECURE (default true).
#
set -euo pipefail

: "${FORGE_URL:?set via demo.env}"; : "${FORGE_USER:?}"; : "${FORGE_PASS:?}"
: "${IBMCLOUD_API_KEY:?}"
WS="${1:-${CLUSTER_NAME:?}}"
FORGE_PROJECT="${FORGE_PROJECT:-roks-demo}"
CRED_NAME="${FORGE_CRED_NAME:-roksbnkctl-ibm}"
RG="${RESOURCE_GROUP:-default}"
INSECURE="${FORGE_INSECURE:-true}"; KFLAG=""; [ "$INSECURE" = true ] && KFLAG="-k"
export PATH="$HOME/.local/bin:$PATH"
RH="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"

log(){ printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok(){  printf '\033[1;32m✓ %s\033[0m\n' "$*"; }
die(){ echo "✗ $*" >&2; exit 1; }
command -v jq >/dev/null || die "jq required"

# ── resolve the cluster id + region roksbnkctl recorded ──────────────────────
CID=""; CREGION="${REGION:-}"
CFG="$(roksbnkctl -w "$WS" cluster config -o json 2>/dev/null || true)"
if [ -n "$CFG" ]; then
  CID="$(printf '%s' "$CFG"     | jq -r '.cluster_id // .ClusterID // .clusterId // .id // empty' 2>/dev/null || true)"
  CREGION="$(printf '%s' "$CFG" | jq -r '.region // .Region // empty' 2>/dev/null || echo "$CREGION")"
fi
if [ -z "$CID" ] && [ -f "$RH/$WS/cluster-outputs.json" ]; then
  CID="$(jq -r '.cluster_id // .ClusterID // .clusterId // .id // empty' "$RH/$WS/cluster-outputs.json")"
  [ -z "$CREGION" ] && CREGION="$(jq -r '.region // .Region // empty' "$RH/$WS/cluster-outputs.json")"
fi
[ -n "$CID" ]     || die "no cluster id for workspace '$WS' — run 'roksbnkctl cluster up' first"
[ -n "$CREGION" ] || CREGION="${REGION:?region unknown}"

# ── auth ─────────────────────────────────────────────────────────────────────
TOK=""
api(){ # api METHOD PATH [json-body]
  local m="$1" p="$2" d="${3:-}"
  if [ -n "$d" ]; then
    curl $KFLAG -s -X "$m" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "$d" "$FORGE_URL$p"
  else
    curl $KFLAG -s -X "$m" -H "Authorization: Bearer $TOK" "$FORGE_URL$p"
  fi
}

log "Authenticating to BNK Forge ($FORGE_URL)…"
TOK="$(curl $KFLAG -s -X POST -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg u "$FORGE_USER" --arg p "$FORGE_PASS" '{username:$u,password:$p}')" \
        "$FORGE_URL/api/auth/login" | jq -r '.token // empty')"
[ -n "$TOK" ] || die "forge login failed (check FORGE_USER/FORGE_PASS)"
ok "authenticated as $FORGE_USER"

# ── ensure a default IBM credential template ─────────────────────────────────
log "Ensuring IBM credential template '$CRED_NAME' (default)…"
TID="$(api GET /api/credential-templates | jq -r --arg n "$CRED_NAME" '.[]? | select(.name==$n) | .id' | head -1)"
CRED_BODY="$(jq -nc --arg k "$IBMCLOUD_API_KEY" --arg rg "$RG" \
  '{ibmcloud_api_key:$k, ibmcloud_resource_group:$rg, is_default:true}')"
if [ -z "$TID" ]; then
  TID="$(api POST /api/credential-templates \
    "$(jq -nc --arg n "$CRED_NAME" --argjson b "$CRED_BODY" '{name:$n, provider:"IBM"} + $b')" \
    | jq -r '.id // empty')"
  [ -n "$TID" ] || die "could not create credential template"
else
  api PUT "/api/credential-templates/$TID" "$CRED_BODY" >/dev/null
fi
ok "credential template id=$TID"

# ── ensure a project ─────────────────────────────────────────────────────────
log "Ensuring project '$FORGE_PROJECT'…"
PID="$(api GET /api/projects | jq -r --arg n "$FORGE_PROJECT" '(.projects // .)[]? | select(.name==$n) | .id' | head -1)"
if [ -z "$PID" ]; then
  PID="$(api POST /api/projects "$(jq -nc --arg n "$FORGE_PROJECT" '{name:$n, description:"roksbnkctl demo"}')" \
    | jq -r '.id // .project.id // empty')"
  [ -n "$PID" ] || die "could not create project"
fi
ok "project id=$PID"

# ── register the cluster ─────────────────────────────────────────────────────
log "Registering cluster '$WS' (cluster_id=$CID, region=$CREGION)…"
REG="$(api POST "/api/projects/$PID/k8s/clusters" \
  "$(jq -nc --arg n "$WS" --arg c "$CID" --arg r "$CREGION" --argjson t "$TID" \
     '{name:$n, provider:"IBM", cluster_id:$c, region:$r, template_id:$t}')")"
NEWID="$(printf '%s' "$REG" | jq -r '.id // .cluster.id // empty' 2>/dev/null || true)"
if [ -z "$NEWID" ]; then
  echo "── forge response ──" >&2; printf '%s\n' "$REG" | jq . 2>/dev/null >&2 || printf '%s\n' "$REG" >&2
  die "registration did not return a cluster id — adjust the payload fields above"
fi
ok "registered '$WS' with BNK Forge (forge cluster id=$NEWID, project=$FORGE_PROJECT)"

# ── verify: read it back ─────────────────────────────────────────────────────
log "Verifying registration…"
api GET "/api/k8s/clusters/$NEWID" | jq -r '"  name=\(.name // "?")  provider=\(.provider // "?")  status=\(.status // .health // .connection_status // "n/a")"' 2>/dev/null || true
ok "cluster is registered in BNK Forge."
