#!/usr/bin/env bash
# =============================================================================
# sizing-matrix.sh — build and verify a reference deployment for each sizing.
#
# Appendix C tells operators which cluster shape to buy. This builds one, installs
# BNK onto it, and asserts that EVERY component actually came up — not merely that
# the apply exited zero. An install that "succeeds" while silently omitting a
# component is the failure mode this exists to catch; see the CSRC note below.
#
#   ./sizing-matrix.sh baseline    # 3 x bx2.16x64, 1 TMM
#   ./sizing-matrix.sh small       # 6 x bx2.8x32,  3 TMM
#   ./sizing-matrix.sh medium      # 6 x cx2.16x32, 3 TMM
#   ./sizing-matrix.sh large       # 9 x cx2.48x96, 9 TMM
#
# deploymentSize is Tiny in EVERY one. The column headings are CLUSTER sizes;
# what changes between them is the node flavour and tmmReplicas. Anything above
# Tiny requests hugepages ROKS cannot allocate (#203), so a run that sets Small
# or Medium here would be building a configuration that cannot come up.
#   ./sizing-matrix.sh small --dry # print the config it would use, build nothing
#   ./sizing-matrix.sh small --verify-only   # re-run the checks against a build
#
# COSTS REAL MONEY AND TIME. Each sizing builds a cluster (~45 min) plus ~25 min
# for BNK, and consumes a VPC from a per-region quota. Build them SEQUENTIALLY and
# tear down between: three at once can wall the account's VPC limit.
#
# WHAT THIS SCRIPT LEARNED THE HARD WAY — do not "simplify" these away:
#
#  1. bnk.manifest_version MUST be set. Unset installs the 2.3 line, and the first
#     symptom is a CNEInstance validation error minutes into the apply saying
#     deploymentSize "Tiny" is unsupported — because Tiny does not exist on 2.3.
#     The message names the size, not the version.
#
#  2. 2.4.0-EA is on repo.f5.com and needs the NON-GA production key
#     (non-ga-prod-pull-key.tgz). Of the four keys F5 issues, only that one
#     resolves it; the others authenticate and then report "not found".
#
#  3. far_auth_local_file is ignored unless subscription_jwt_local_file is set too
#     — the no-COS path is gated on BOTH being non-empty. With only one set it
#     silently falls back to COS and the GA key, failing much later with a 403 that
#     names a GCP project rather than a config field.
#
#  4. bnk down returns 0 while leaving the namespace and every *.k8s.f5.com CRD
#     installed. Reinstalling a DIFFERENT manifest line over those stale CRDs
#     validates against the old schema. clean_slate() below deletes both.
#
#  5. `roksbnkctl k` rejects --no-headers and returns empty for -o jsonpath, so the
#     verification uses real kubectl. The resolved kubeconfig is SHARED between
#     workspaces, so the context is pinned explicitly rather than assumed.
# =============================================================================
set -uo pipefail
HERE="$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || echo "${BASH_SOURCE[0]}")")" && pwd)"

# The manifest the BNK 2.4 IBM install guide documents. Pin it: see note 1.
# 2.4 is Early Access -- the ORDINARY production FAR token pulls 2.3.0 and no 2.4
# version at all, so the non-GA production grant is required until 2.4 GAs. The
# wrong key authenticates and then reports "not found", which reads like a wrong
# version rather than a wrong credential.
: "${BNK_MANIFEST_VERSION:=2.4.0-EA}"
: "${BNK_FAR_REPO_URL:=repo.f5.com}"
: "${BNK_FAR_AUTH_LOCAL_FILE:=/mnt/d/roksbnkresources/non-ga-prod-pull-key.tgz}"

SIZING="${1:-}"; shift || true
DRY=0; VERIFY_ONLY=0
for a in "$@"; do
  [[ "$a" == "--dry" ]] && DRY=1
  [[ "$a" == "--verify-only" ]] && VERIFY_ONLY=1
done

# Appendix C names three CLUSTER sizes but only two deploymentSize values: the
# Large cluster runs the Medium profile and gets its capacity from nine TMM pods
# on nine larger nodes. Tiny is not in the guide — it is what the engineering
# reference cluster runs, kept here as the smallest known-working configuration.
case "$SIZING" in
  baseline) FLAVOR=bx2.16x64  WORKERS_PER_ZONE=1 DEPLOYMENT_SIZE=Tiny TMM=1 CIDR=10.246.0.0/16 ;;
  small)    FLAVOR=bx2.8x32   WORKERS_PER_ZONE=2 DEPLOYMENT_SIZE=Tiny TMM=3 CIDR=10.252.0.0/16 ;;
  medium)   FLAVOR=cx2.16x32  WORKERS_PER_ZONE=2 DEPLOYMENT_SIZE=Tiny TMM=3 CIDR=10.253.0.0/16 ;;
  large)    FLAVOR=cx2.48x96  WORKERS_PER_ZONE=3 DEPLOYMENT_SIZE=Tiny TMM=9 CIDR=10.254.0.0/16 ;;
  *) echo "usage: $0 {baseline|small|medium|large} [--dry|--verify-only]" >&2; exit 2 ;;
esac
WANT_NODES=$((WORKERS_PER_ZONE * 3))
WS="sz-$SIZING"
BIN="${ROKSBNKCTL:-roksbnkctl}"

cat <<CFG
=== sizing: $SIZING ===
  flavour            $FLAVOR
  workers per zone   $WORKERS_PER_ZONE   (expect $WANT_NODES nodes over 3 AZs)
  deploymentSize     $DEPLOYMENT_SIZE
  TMM replicas       $TMM
  manifest           $BNK_MANIFEST_VERSION
  registry           $BNK_FAR_REPO_URL
  namespace          f5-bnk ONLY (flo_namespace == flo_utils_namespace)
  vpc cidr           $CIDR
  workspace          $WS
CFG
[[ "$DRY" == "1" ]] && { echo "  --dry: nothing built"; exit 0; }

command -v "$BIN" >/dev/null || { echo "$BIN not on PATH (set ROKSBNKCTL)" >&2; exit 2; }

# --- verification ------------------------------------------------------------
verify() {
  local ctx kc fail=0
  kc="$($BIN -w "$WS" kubeconfig 2>/dev/null)"; export KUBECONFIG="$kc"
  ctx="$(kubectl config get-contexts -o name 2>/dev/null | grep -m1 "$WS\|bnk24-$SIZING")"
  [[ -z "$ctx" ]] && ctx="$(kubectl config current-context 2>/dev/null)"
  K(){ kubectl --context "$ctx" "$@" 2>/dev/null; }
  chk(){ if [[ "$2" == "$3" ]]; then printf "  PASS  %-38s %s\n" "$1" "$2"
         else printf "  FAIL  %-38s got=%-18s want=%s\n" "$1" "${2:-<empty>}" "$3"; fail=1; fi; }
  ge(){ if [[ "${2:-0}" -ge "$3" ]] 2>/dev/null; then printf "  PASS  %-38s %s (>=%s)\n" "$1" "$2" "$3"
        else printf "  FAIL  %-38s got=%-18s want>=%s\n" "$1" "${2:-0}" "$3"; fail=1; fi; }

  echo "=== [$SIZING] verify (context: $ctx) ==="
  chk "worker nodes"        "$(K get nodes --no-headers | wc -l)" "$WANT_NODES"
  chk "f5-* namespaces"     "$(K get ns -o name | grep -c 'namespace/f5-')" "1"
  chk "f5-utils absent"     "$(K get ns f5-utils -o name | wc -l)" "0"
  chk "CNEManifest version" "$(K get cnemanifests -A -o jsonpath='{.items[0].spec.version}')" "$BNK_MANIFEST_VERSION"
  chk "containerPlatform"   "$(K get f5tmm -A -o jsonpath='{.items[0].spec.containerPlatform}')" "IBM"
  chk "deploymentSize"      "$(K get cneinstance -A -o jsonpath='{.items[0].spec.deploymentSize}')" "$DEPLOYMENT_SIZE"

  for kind in cneinstance f5tmm csrc cwc dssm coremond observer rabbitmq; do
    chk "$kind Available" \
      "$(K get "$kind" -A -o jsonpath="{.items[0].status.conditions[?(@.type=='Available')].status}")" "True"
  done

  # CSRC is the component FLO silently omits under the wrong containerPlatform,
  # and macvlan-internal is the NAD it creates at runtime — so the NAD's absence
  # is the visible symptom of an install that otherwise looks healthy.
  ge  "csrc pods Running"   "$(K get pods -A --no-headers | grep -c 'f5-spk-csrc.*Running')" 1
  chk "macvlan-internal NAD" "$(K get net-attach-def -A --no-headers | grep -c macvlan-internal)" "1"
  ge  "tmm pods Running"    "$(K get pods -A --no-headers | grep -c 'f5-tmm.*Running')" "$TMM"

  # Hugepages are DERIVED, never assumed: the deploymentSize->resources map lives
  # inside the cne-controller image, not in any chart we can read. At Tiny, TMM
  # requests none. Read what it asked for and check the node can satisfy it.
  local req off
  req="$(K get pods -n f5-bnk -l app=f5-tmm -o jsonpath='{.items[0].spec.containers[*].resources.requests.hugepages-2Mi}')"
  off="$(K get nodes -o jsonpath='{.items[0].status.allocatable.hugepages-2Mi}')"
  echo "     hugepages: TMM requests '${req:-none}' / node offers '${off:-0}'"
  if [[ -n "$req" && "$req" != "0" ]]; then
    [[ -n "$off" && "$off" != "0" ]] || { echo "  FAIL  TMM requests hugepages, node offers none"; fail=1; }
  else
    echo "     note: this sizing leaves bnk.hugepages unexercised"
  fi

  chk "licence" "$(K get license -A --no-headers | awk '{print $3}' | head -1)" "Active"
  local bad; bad="$(K get pods -A --no-headers | awk '$4!="Running" && $4!="Completed"' | wc -l)"
  chk "pods not Running/Completed" "$bad" "0"
  [[ "$bad" != "0" ]] && K get pods -A --no-headers | awk '$4!="Running" && $4!="Completed"' | sed 's/^/     /' | head -10
  echo "VERIFY-$SIZING-RC=$fail"
  return "$fail"
}

if [[ "$VERIFY_ONLY" == "1" ]]; then verify; exit $?; fi

# --- clean slate -------------------------------------------------------------
# See note 4: bnk down leaves the namespace and CRDs behind.
clean_slate() {
  echo "=== [$SIZING] clean slate ==="
  $BIN -w "$WS" bnk down --auto >/dev/null 2>&1; echo "  bnk down rc=$?"
  local kc ctx; kc="$($BIN -w "$WS" kubeconfig 2>/dev/null)"; export KUBECONFIG="$kc"
  ctx="$(kubectl config current-context 2>/dev/null)" || return 0
  for ns in f5-bnk f5-utils; do
    kubectl --context "$ctx" get ns "$ns" >/dev/null 2>&1 || continue
    kubectl --context "$ctx" delete ns "$ns" >/dev/null 2>&1
    for _ in $(seq 1 60); do
      kubectl --context "$ctx" get ns "$ns" >/dev/null 2>&1 || break
      sleep 5
    done
    echo "  removed ns $ns"
  done
  local n=0
  for c in $(kubectl --context "$ctx" get crd -o name 2>/dev/null | grep 'k8s\.f5\.com' | sed 's|.*/||'); do
    kubectl --context "$ctx" delete crd "$c" >/dev/null 2>&1 && n=$((n+1))
  done
  echo "  removed $n f5 CRDs"
}

echo "=== [$SIZING] cluster up ==="
$BIN -w "$WS" cluster up --auto || { echo "cluster up failed" >&2; exit 1; }
clean_slate
echo "=== [$SIZING] bnk up ==="
$BIN -w "$WS" bnk up --auto || { echo "bnk up failed" >&2; exit 1; }
verify
rc=$?
echo
echo "Tear down when finished:  $BIN -w $WS down --auto"
exit "$rc"
