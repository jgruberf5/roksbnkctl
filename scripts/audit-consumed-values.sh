#!/usr/bin/env bash
# audit-consumed-values.sh — find settings handed to a consumer that ignores them.
#
# THE HOP NOTHING ELSE CHECKS.
#
# A setting can die at four places. Three are already guarded:
#
#   config field -> tfvar        partially, by the render tests
#   tfvar        -> declared     terraform warns; audited clean
#   declared     -> read         internal/tf/module_variable_read_test.go (#204)
#   read         -> CONSUMED     nothing -- this script
#
# The last hop is where #227 lived: cneinstance_gtm_* was rendered, declared and
# genuinely read by terraform. It just landed on environment variables the
# controller does not look up. From terraform's point of view the variable IS
# used, so the hop-C guard cannot see it.
#
# WHAT IT DOES. Extracts every container env name and helm values key roksbnkctl
# emits, pulls the consumer that is supposed to read it (the controller image,
# the chart), and reports any name that does not appear in it.
#
# WHY IT IS NOT IN CI. It needs FAR credentials and pulls multi-GB images, the
# same reason `make release` builds the book and CI does not. Run it at release
# time, or when adding a value to a CR or a chart.
#
#   FAR_KEY=/path/to/cne_pull_64.json ./scripts/audit-consumed-values.sh
#
# EVIDENCE, NOT PROOF. A zero means the literal string is absent from the
# consumer, which is strong: a Go binary that reads an env var contains its name.
# It is not proof -- a name could be assembled at runtime from pieces. Treat a
# zero as "investigate", and note that the controls below are there so a broken
# extraction shows up as controls failing rather than as a clean report.
set -uo pipefail

HERE="$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
WORK="${AUDIT_WORK:-$(mktemp -d -t roksbnkctl-audit.XXXXXX)}"
FAR_HOST="${FAR_HOST:-repo.f5.com}"
FAR_KEY="${FAR_KEY:-}"
RC=0

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }
die()  { warn "$*"; exit 2; }

[ -n "$FAR_KEY" ] && [ -r "$FAR_KEY" ] || die "set FAR_KEY to a readable FAR service-account json
  e.g. FAR_KEY=/path/to/cne_pull_64.json $0"

command -v docker >/dev/null || die "docker not on PATH"
command -v helm   >/dev/null || die "helm not on PATH"

# ISOLATED CREDENTIALS, AND CLEANED UP.
#
# BOTH clients, not one. An earlier version isolated docker and then called
# `helm registry login` with no equivalent, which writes to the operator's REAL
# ~/.config/helm/registry/config.json -- so running an audit silently added
# repo.f5.com to their persistent credentials, from a script whose header claims
# it keeps the credential out of their config. Half-isolation is worse than none,
# because the claim is what someone relies on.
#
# The docker side additionally works around the Windows credential helper, which
# fails under WSL with "The stub received bad data".
DCFG="$WORK/dockercfg"; mkdir -p "$DCFG"; chmod 700 "$DCFG"
printf '{}' > "$DCFG/config.json"; chmod 600 "$DCFG/config.json"
HELM_CFG="$WORK/helm-registry.json"
printf '{}' > "$HELM_CFG"; chmod 600 "$HELM_CFG"
export HELM_REGISTRY_CONFIG="$HELM_CFG"

# The credential lands in those files, so they go when the script does -- on
# success, on failure, and on Ctrl-C. Only the pulled corpora are worth keeping,
# and only when the caller asked for a cache via AUDIT_WORK.
# shellcheck disable=SC2329  # invoked by the trap below
cleanup() {
    docker --config "$DCFG" logout "$FAR_HOST" >/dev/null 2>&1 || true
    rm -f "$DCFG/config.json" "$HELM_CFG"
    rmdir "$DCFG" 2>/dev/null || true
    if [ -z "${AUDIT_WORK:-}" ]; then
        rm -rf "$WORK"
    else
        say "    (corpora kept in $WORK; credentials removed)"
    fi
}
trap cleanup EXIT INT TERM

say "==> Authenticating to $FAR_HOST"
< "$FAR_KEY" docker --config "$DCFG" login -u _json_key_base64 --password-stdin "$FAR_HOST" >/dev/null 2>&1 \
    || die "docker login to $FAR_HOST failed"
< "$FAR_KEY" helm registry login -u _json_key_base64 --password-stdin "$FAR_HOST" >/dev/null 2>&1 \
    || die "helm login to $FAR_HOST failed"

# ── extraction ───────────────────────────────────────────────────────────────
#
# Names are taken from the SOURCE, not a list kept beside it. A list would be the
# very thing this script exists to catch.

# env_names <component> — the env names terraform injects for one CNEInstance
# component (cneController, tmm, coremon).
env_names() {
    python3 - "$1" <<'PY'
import re, sys, pathlib
comp = sys.argv[1]
s = pathlib.Path("terraform/modules/cne_instance/modules/cneinstance/main.tf").read_text()
i = s.index("adv_env_defaults = {")
blob = s[i:i+16000]
m = re.search(r'\n    ' + re.escape(comp) + r' = ', blob)
if not m:
    sys.exit(0)
seg = blob[m.end():]
nxt = re.search(r'\n    [a-zA-Z]+ = ', seg)
if nxt:
    seg = seg[:nxt.start()]
for n in sorted(set(re.findall(r'name  *= *"([A-Z][A-Z0-9_]*)"', seg))):
    print(n)
PY
}

# helm_keys <file> <local name> — the TOP-LEVEL keys set in one helm values block.
#
# TOP-LEVEL ONLY, and that restriction is load-bearing. A values map can nest
# another chart's values under its name -- flo_helm_values carries
# "f5-spk-crds-common" and "f5-ipam-operator" sub-maps -- and those keys belong to
# THOSE charts. An earlier version scraped every key at any depth and checked them
# all against the parent chart, which reported fullnameOverride as dead when it is
# real in f5-ipam-controller. Checking a name against the wrong consumer is how an
# audit deletes working configuration.
helm_keys() {
    python3 - "$1" "$2" <<'PYEOF'
import re, sys, pathlib
f, name = sys.argv[1], sys.argv[2]
s = pathlib.Path(f).read_text()
m = re.search(re.escape(name) + r' = \{(.*?)\n  \}\n', s, re.S)
if not m:
    sys.exit(0)
for k in sorted(set(re.findall(r'^\s{4}([a-zA-Z_][a-zA-Z0-9_]*)\s*=', m.group(1), re.M))):
    print(k)
PYEOF
}

# helm_subcharts <file> <local name> — the quoted sub-map names in a values block:
# the charts whose values are nested inside this one. Each needs checking against
# ITS OWN chart, which this script does not do yet. They are listed so the gap is
# visible rather than silently unaudited.
helm_subcharts() {
    python3 - "$1" "$2" <<'PYEOF'
import re, sys, pathlib
f, name = sys.argv[1], sys.argv[2]
s = pathlib.Path(f).read_text()
m = re.search(re.escape(name) + r' = \{(.*?)\n  \}\n', s, re.S)
if not m:
    sys.exit(0)
for k in sorted(set(re.findall(r'^\s{4}"([^"]+)"\s*=', m.group(1), re.M))):
    print(k)
PYEOF
}

# ── consumers ────────────────────────────────────────────────────────────────

# strings_of_image <ref> <cache> — the image's whole string table, once.
strings_of_image() {
    local ref="$1" out="$2"
    [ -s "$out" ] && return 0
    say "    pulling $ref"
    docker --config "$DCFG" pull -q "$ref" >/dev/null 2>&1 || { warn "    could not pull $ref"; return 1; }
    local cid; cid="$(docker --config "$DCFG" create "$ref" 2>/dev/null)" || return 1
    docker --config "$DCFG" export "$cid" 2>/dev/null | tar xO 2>/dev/null | strings -n 5 > "$out"
    docker --config "$DCFG" rm -f "$cid" >/dev/null 2>&1
    [ -s "$out" ]
}

# text_of_chart <oci ref> <version> <cache> — the chart's whole text, templates included.
text_of_chart() {
    local chart="$1" ver="$2" out="$3"
    [ -s "$out" ] && return 0
    say "    pulling $chart:$ver"
    local d="$WORK/chart.$$"; rm -rf "$d"; mkdir -p "$d"
    helm pull "$chart" --version "$ver" -d "$d" >/dev/null 2>&1 || { warn "    could not pull $chart:$ver"; return 1; }
    tar xOzf "$d"/*.tgz > "$out" 2>/dev/null
    [ -s "$out" ]
}

# report <label> <corpus> <controls csv> <names...>
#
# The controls are the point. If a name we KNOW is consumed reads as absent, the
# extraction or the corpus is broken and every other zero is meaningless — so a
# failed control is a hard error, not a finding.
report() {
    local label="$1" corpus="$2" controls="$3"; shift 3
    local bad=0
    IFS=',' read -ra ctl <<< "$controls"
    for c in "${ctl[@]}"; do
        [ -z "$c" ] && continue
        if [ "$(grep -o -- "$c" "$corpus" | wc -l)" -eq 0 ]; then
            warn "  !! CONTROL '$c' absent from $label — the corpus or the extraction is wrong,"
            warn "     so the rest of this section proves nothing. Not reporting it."
            RC=2
            return
        fi
    done
    say "  $label"
    for n in "$@"; do
        local k; k="$(grep -o -- "$n" "$corpus" | wc -l)"
        if [ "$k" -eq 0 ]; then
            say "    DEAD  $n"
            bad=$((bad + 1))
            RC=1
        fi
    done
    [ "$bad" -eq 0 ] && say "    all consumed"
}

cd "$ROOT" || die "cannot enter $ROOT"

MANIFEST_VERSION="${MANIFEST_VERSION:-2.4.0-EA}"
say "==> Resolving component versions from manifest $MANIFEST_VERSION"
MD="$WORK/manifest"; mkdir -p "$MD"
helm pull "oci://$FAR_HOST/release/f5-bigip-k8s-manifest" --version "$MANIFEST_VERSION" -d "$MD" >/dev/null 2>&1 \
    || die "could not pull the manifest $MANIFEST_VERSION"
tar xzf "$MD"/*.tgz -C "$MD" 2>/dev/null
MF="$(find "$MD" -name 'bigip-k8s-manifest-*.yaml' | head -1)"
[ -n "$MF" ] || die "no manifest yaml in the pulled chart"

ver_of() { python3 -c "
import re,sys
s=open('$MF').read()
m=re.search(r'- name: '+re.escape(sys.argv[1])+r'\s*\n\s*version: (\S+)', s)
print(m.group(1) if m else '')
" "$1"; }

INGRESS_V="$(ver_of images/f5ingress)"
TMM_V="$(ver_of images/tmm-img)"
COREMOND_V="$(ver_of images/f5-coremond)"
FLO_V="$(ver_of charts/f5-lifecycle-operator)"
CIS_V="$(ver_of charts/f5-bnk-cis)"
say "    f5ingress=$INGRESS_V tmm=$TMM_V coremond=$COREMOND_V flo=$FLO_V cis=$CIS_V"

say "==> Container env"
if [ -n "$INGRESS_V" ] && strings_of_image "$FAR_HOST/images/f5ingress:$INGRESS_V" "$WORK/f5ingress.txt"; then
    # shellcheck disable=SC2046
    report "cneController env vs f5ingress:$INGRESS_V" "$WORK/f5ingress.txt" \
        "CLOUD_PROVIDER,GSLB_DATACENTER_NAME" $(env_names cneController)
fi
if [ -n "$TMM_V" ] && strings_of_image "$FAR_HOST/images/tmm-img:$TMM_V" "$WORK/tmm.txt"; then
    # shellcheck disable=SC2046
    report "tmm env vs tmm-img:$TMM_V" "$WORK/tmm.txt" \
        "TMM_DEFAULT_MTU" $(env_names tmm)
fi
if [ -n "$COREMOND_V" ] && strings_of_image "$FAR_HOST/images/f5-coremond:$COREMOND_V" "$WORK/coremond.txt"; then
    # shellcheck disable=SC2046
    report "coremon env vs f5-coremond:$COREMOND_V" "$WORK/coremond.txt" \
        "COREMOND_OVERRIDE_CORE_PATTERN" $(env_names coremon)
fi

say "==> Helm values"
FLO_TF="terraform/modules/flo/modules/flo/main.tf"
if [ -n "$FLO_V" ] && text_of_chart "oci://$FAR_HOST/charts/f5-lifecycle-operator" "$FLO_V" "$WORK/flo-chart.txt"; then
    # shellcheck disable=SC2046
    report "flo_helm_values vs f5-lifecycle-operator:$FLO_V" "$WORK/flo-chart.txt" \
        "containerPlatform,namespace" $(helm_keys "$FLO_TF" flo_helm_values)
fi
if [ -n "$CIS_V" ] && text_of_chart "oci://$FAR_HOST/charts/f5-bnk-cis" "$CIS_V" "$WORK/cis-chart.txt"; then
    # shellcheck disable=SC2046
    report "cis_helm_values vs f5-bnk-cis:$CIS_V" "$WORK/cis-chart.txt" \
        "image,namespace" $(helm_keys "$FLO_TF" cis_helm_values)
fi

for blk in flo_helm_values cis_helm_values; do
    subs="$(helm_subcharts "$FLO_TF" "$blk")"
    if [ -n "$subs" ]; then
        say "  $blk nests values for other charts — NOT audited here; check each against its own chart:"
        for sc in $subs; do say "    - $sc"; done
    fi
done

say ""
case "$RC" in
    0) say "==> Every emitted name appears in its consumer." ;;
    1) say "==> DEAD names above are emitted and not consumed. Either the consumer"
       say "    changed, or the name was never right. #227 was the latter." ;;
    2) say "==> A control failed. Fix the extraction before trusting any result." ;;
esac
exit "$RC"
