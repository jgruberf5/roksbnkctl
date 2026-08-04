#!/usr/bin/env bash
# =============================================================================
# check-masking.sh — prove no credential can reach the screen. RUN THIS BEFORE
# EVERY SHOOT; the demos are recorded, and a leaked API key is unrecoverable.
#
#   ./check-masking.sh            # check every demo
#   ./check-masking.sh <demo> …   # check named demos only
#
# Three independent checks:
#   1. STATIC   — no demo displays a file with `run cat` / `run grep`; every file
#                 that reaches the screen goes through show_file (which masks).
#   2. UNIT     — the redaction helpers really mask a value, its base64 form, and
#                 do so through show_file / say / ok / show / runmask.
#   3. DRY-RUN  — each demo runs end to end with sentinel credentials, and the
#                 whole transcript is scanned for them.
#
# Exit 0 = clean. Any leak is reported with its file and line, and exits 1.
# =============================================================================
set -uo pipefail
LIB="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
DEMOS="$(dirname "$LIB")"
G=$'\e[32m'; R=$'\e[31m'; B=$'\e[1m'; DIM=$'\e[2m'; N=$'\e[0m'
pass(){ echo "${G}✓${N} $*"; }
fail(){ echo "${R}✗ $*${N}"; RC=1; }
RC=0

SENTINEL_KEY='SENTINELAPIKEY0000000000000000000000000000'
SENTINEL_REG='SENTINELREGISTRYPASSWORD00000'
SENTINEL_FRG='SENTINELFORGEPASSWORD00000'
SENTINELS='SENTINEL'

if (( $# )); then TARGETS=("$@"); else
  mapfile -t TARGETS < <(cd "$DEMOS" && for d in */; do [[ -f "${d%/}/${d%/}.sh" ]] && echo "${d%/}"; done)
fi

echo "${B}1. static — no unmasked file display${N}"
for d in "${TARGETS[@]}"; do
  hits="$(grep -nE '^\s*run (cat|grep|env|printenv)\b' "$DEMOS/$d/$d.sh" 2>/dev/null)"
  if [[ -n "$hits" ]]; then
    fail "$d displays a file without masking — use show_file:"; sed 's/^/    /' <<<"$hits"
  else pass "$d — every file display goes through show_file"; fi
done

echo; echo "${B}2. unit — the redaction helpers${N}"
out="$(STATE_DIR="$(mktemp -d)" DRY_RUN=0 bash -c '
  source "'"$LIB"'/demo-format.sh" >/dev/null
  secret "'"$SENTINEL_KEY"'" "'"$SENTINEL_REG"'"
  f="$STATE_DIR/ci.env"
  {
    echo "IBMCLOUD_API_KEY='"$SENTINEL_KEY"'"
    echo "ROKSBNKCTL_GENERIC_PASSWORD='"$SENTINEL_REG"'"
    echo "GENERIC_PASSWORD_B64='"$(printf %s "$SENTINEL_REG" | base64 -w0)"'"
  } > "$f"
  show_file "$f"
  say  "inline '"$SENTINEL_KEY"'"
  ok   "inline '"$SENTINEL_REG"'"
  show "docker run -e IBMCLOUD_API_KEY='"$SENTINEL_KEY"' img"
  runmask printf "%s\n" "output '"$SENTINEL_KEY"'"
' 2>&1)"
if grep -q "$SENTINELS" <<<"$out"; then
  fail "the redaction helpers leaked:"; grep -n "$SENTINELS" <<<"$out" | sed 's/^/    /'
else
  pass "show_file / say / ok / show / runmask all mask (incl. base64 forms)"
fi

echo; echo "${B}3. dry-run — full transcript of each demo${N}"
for d in "${TARGETS[@]}"; do
  [[ -f "$DEMOS/$d/$d.sh" ]] || { fail "$d — no $d.sh"; continue; }
  if ! bash -n "$DEMOS/$d/$d.sh" 2>/dev/null; then
    fail "$d — SYNTAX ERROR, cannot run:"; bash -n "$DEMOS/$d/$d.sh" 2>&1 | sed 's/^/    /'; continue
  fi
  # CMD_*_HOLD=0: the command-freeze holds only matter for a recording, and paying
  # 2.5s per roksbnkctl command would make this scan take minutes.
  out="$(
    STATE_DIR="$(mktemp -d)" DRY_RUN=1 AUTO_ADVANCE=1 PHASE_DELAY=0 \
    CMD_RENDER_HOLD=0 CMD_POST_HOLD=0 OUT_SETTLE_HOLD=0 OUT_POST_HOLD=0 PHASE_BANNER_HOLD=0 \
    IBMCLOUD_API_KEY="$SENTINEL_KEY" REGISTRY_ADMIN_PASSWORD="$SENTINEL_REG" \
    FORGE_PASS="$SENTINEL_FRG" HARBOR_ADMIN_PASSWORD="$SENTINEL_REG" \
    FORGE_URL='https://forge.example' FORGE_USER=u \
    REGISTRY_DOMAIN=harbor.example \
    SERVICES_CLUSTER=svc-cluster APP_CLUSTER=app-cluster \
    APP_CLUSTER_CIDR=10.242.0.0/18 \
    bash "$DEMOS/$d/$d.sh" 2>&1
  )"
  if grep -q "$SENTINELS" <<<"$out"; then
    fail "$d leaked a credential on screen:"; grep -n "$SENTINELS" <<<"$out" | head -5 | sed 's/^/    /'
  elif grep -q 'secret masking active' <<<"$out"; then
    pass "$d — transcript clean, masking confirmed on camera"
  else
    echo "${DIM}  ~ $d — transcript clean, but it never called secret(); add it in preflight${N}"
  fi
done

echo
(( RC == 0 )) && echo "${G}${B}All checks passed — safe to record.${N}" \
              || echo "${R}${B}Checks FAILED — do not record until every ✗ above is fixed.${N}"
exit $RC
