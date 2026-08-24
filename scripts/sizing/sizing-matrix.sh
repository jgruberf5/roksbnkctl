#!/usr/bin/env bash
# =============================================================================
# sizing-matrix.sh — build and validate each sizing Appendix C recommends.
#
# Appendix C tells operators which cluster shape to buy. Nothing has ever checked
# that those shapes actually run BNK. This does, one sizing at a time.
#
# OPEN QUESTION this test exists to settle: does deploymentSize Small/Medium
# request hugepages? At deploymentSize Tiny — the only size ever run — TMM
# requests NONE (verified on a live 2.4 cluster: every f5-tmm container shows
# cpu/memory requests and no hugepages resource). The size->resources map lives
# inside the cne-controller image, not in any chart we can read, so the only way
# to learn what Small does is to run it. The verification below therefore READS
# what TMM asked for and checks the node can satisfy it, rather than assuming a
# number. Do not replace that with a hard-coded hugepages assertion.
#
#   ./sizing-matrix.sh small       # 6 x bx2.8x32,  deploymentSize Small
#   ./sizing-matrix.sh medium      # 6 x cx2.16x32, deploymentSize Medium
#   ./sizing-matrix.sh large       # 9 x cx2.48x96, deploymentSize Medium
#   ./sizing-matrix.sh small --dry # print the config it would use, build nothing
#
# COSTS REAL MONEY AND TIME. Each sizing builds a cluster (~30-45 min) and
# consumes a transit-gateway connection from a SHARED account quota. Check the
# quota before running more than one, and tear down when finished.
#
# TMM replicas are pinned to 1 while the shared-volume defect (#197) is open;
# the appendix's 3-and-9-replica figures cannot be validated until that is fixed.
# =============================================================================
set -uo pipefail
HERE="$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}" 2>/dev/null || echo "${BASH_SOURCE[0]}")")" && pwd)"

SIZING="${1:-}"; shift || true
DRY=0; [[ "${1:-}" == "--dry" ]] && DRY=1

case "$SIZING" in
  small)  FLAVOR=bx2.8x32   WORKERS_PER_ZONE=2 DEPLOYMENT_SIZE=Small  HUGEPAGES=2048 ;;
  medium) FLAVOR=cx2.16x32  WORKERS_PER_ZONE=2 DEPLOYMENT_SIZE=Medium HUGEPAGES=2048 ;;
  large)  FLAVOR=cx2.48x96  WORKERS_PER_ZONE=3 DEPLOYMENT_SIZE=Medium HUGEPAGES=2048 ;;
  *) echo "usage: $0 {small|medium|large} [--dry]" >&2; exit 2 ;;
esac

# The appendix's own figures, asserted after the install so a drifting doc fails
# here rather than misleading a customer.
case "$SIZING" in
  small)  WANT_NODES=6 ;;
  medium) WANT_NODES=6 ;;
  large)  WANT_NODES=9 ;;
esac

WS="sizing-$SIZING"
cat <<CFG
=== sizing: $SIZING ===
  flavour            $FLAVOR
  workers per zone   $WORKERS_PER_ZONE   (expect $WANT_NODES nodes over 3 AZs)
  deploymentSize     $DEPLOYMENT_SIZE
  hugepages          $HUGEPAGES x 2M per node   (Node Tuning Operator; REBOOTS WORKERS)
  tmmReplicas        1                          (pinned while #197 is open)
  workspace          $WS
CFG
[[ "$DRY" == "1" ]] && { echo "  --dry: nothing built"; exit 0; }

command -v roksbnkctl >/dev/null || { echo "roksbnkctl not on PATH" >&2; exit 2; }
[[ -n "${IBMCLOUD_API_KEY:-}" ]] || { echo "set IBMCLOUD_API_KEY" >&2; exit 2; }

export ROKSBNKCTL_WORKER_FLAVOR="$FLAVOR"
export ROKSBNKCTL_WORKERS_PER_ZONE="$WORKERS_PER_ZONE"
export ROKSBNKCTL_CNEINSTANCE_SIZE="$DEPLOYMENT_SIZE"
export ROKSBNKCTL_HUGEPAGES=true
export ROKSBNKCTL_HUGEPAGES_COUNT="$HUGEPAGES"
export ROKSBNKCTL_TMM_REPLICAS=1

set -e
# init prints the overrides it actually applied. Assert every variable we set
# appears there: a misspelled ROKSBNKCTL_* name is silently ignored, which
# would build a DEFAULT-sized cluster and report it as the requested sizing.
# (The first draft of this script set ROKSBNKCTL_HUGEPAGES_ENABLED, which does
# not exist, and would have "validated" a sizing with no hugepages at all.)
roksbnkctl -w "$WS" init --non-interactive --override-from-env 2>&1 | tee "$WS.init.log"
for v in ROKSBNKCTL_WORKER_FLAVOR ROKSBNKCTL_WORKERS_PER_ZONE \
         ROKSBNKCTL_CNEINSTANCE_SIZE ROKSBNKCTL_HUGEPAGES \
         ROKSBNKCTL_HUGEPAGES_COUNT ROKSBNKCTL_TMM_REPLICAS; do
  grep -q "$v" "$WS.init.log" || {
    echo "ABORT: $v was not applied by init — the name is wrong or unsupported." >&2
    echo "       Building would produce a default-sized cluster labelled '$SIZING'." >&2
    exit 3
  }
done
echo "  all 6 overrides confirmed applied"
roksbnkctl -w "$WS" cluster up --auto
roksbnkctl -w "$WS" bnk up --auto
set +e

echo "=== verifying $SIZING against Appendix C ==="
fail=0
nodes=$(roksbnkctl -w "$WS" k get nodes --no-headers 2>/dev/null | wc -l)
[[ "$nodes" == "$WANT_NODES" ]] || { echo "  FAIL nodes=$nodes want=$WANT_NODES"; fail=1; }

# Hugepages: compare what TMM REQUESTED against what the node OFFERS, instead of
# asserting a number we have not verified for this deploymentSize.
req=$(roksbnkctl -w "$WS" k get pods -n f5-bnk -l app=f5-tmm \
        -o jsonpath='{.items[0].spec.containers[*].resources.requests.hugepages-2Mi}' 2>/dev/null)
hp=$(roksbnkctl -w "$WS" k get nodes -o jsonpath='{.items[0].status.allocatable.hugepages-2Mi}' 2>/dev/null)
echo "  hugepages: TMM requests '${req:-none}', node offers '${hp:-0}'"
if [[ -n "$req" && "$req" != "0" ]]; then
  # TMM wants hugepages, so bnk.hugepages had to have worked. This is the first
  # time the Tuned/MachineConfig path is load-bearing; if it silently did nothing
  # the pod is Pending, not merely slow.
  [[ -n "$hp" && "$hp" != "0" ]] || {
    echo "  FAIL deploymentSize=$DEPLOYMENT_SIZE requests hugepages ($req) but node offers ${hp:-0}"
    echo "       — the Node Tuning Operator profile did not take."; fail=1; }
else
  echo "  note: deploymentSize=$DEPLOYMENT_SIZE requests no hugepages;"
  echo "        bnk.hugepages remains unexercised by this sizing."
fi

tmm=$(roksbnkctl -w "$WS" k get pods -n f5-bnk --no-headers 2>/dev/null | grep -c '^f5-tmm.*Running')
[[ "$tmm" -ge 1 ]] || { echo "  FAIL no TMM pod Running"; fail=1; }

lic=$(roksbnkctl -w "$WS" k get license -n f5-utils --no-headers 2>/dev/null | awk '{print $2}')
[[ "$lic" == "Active" ]] || { echo "  FAIL license=$lic"; fail=1; }

[[ "$fail" == "0" ]] && echo "  PASS $SIZING: $nodes nodes, hugepages-2Mi=$hp, TMM Running, license Active"
echo "SIZING-DONE sizing=$SIZING rc=$fail"
echo "Tear down when finished:  roksbnkctl -w $WS down --auto"
exit "$fail"
