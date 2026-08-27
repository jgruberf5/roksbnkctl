#!/usr/bin/env bash
# 241-up-down-loop.sh — run `bnk up` then `bnk down` against a real cluster,
# repeatedly, and fail on any error in the logs of either phase.
#
# WHAT THIS IS FOR. #241 is a timing race against another operator's reconcile
# loop: FLO re-creates the validating webhook about ten seconds into the drain.
# A fake clientset cannot tell you whether the sweep interval and the retry grace
# are actually enough, because the thing being raced is not in the test. Only a
# real BNK install answers it, and only repeatedly — a race that loses one time in
# four looks like a pass on a single run.
#
# THE CLUSTER IS CREATED ONCE, OUTSIDE THE LOOP. `bnk up` / `bnk down` drive the
# BNK phase and leave the cluster in place, which is what makes repetition
# affordable -- a full `up`/`down` spends 27 minutes creating a ROKS cluster and 6
# destroying it, and none of that is what is under test. It is also the topology
# #241 was reported against (cluster.create: false on an existing cluster), and
# the only one where anything cluster-scoped can survive to be checked (#250).
#
#   roksbnkctl cluster up -w <workspace> --auto        # once
#   ./scripts/testing/241-up-down-loop.sh -w <workspace> [-n 3]
#
# Every cycle writes its own log; the script greps both phases for the error
# signatures #208/#217/#235/#241 each produced, and stops on the first hit so the
# cluster is left in the failing state for inspection.
set -uo pipefail

WS=""
CYCLES=3
BNK="${BNK:-./bin/roksbnkctl}"

while [ $# -gt 0 ]; do
    case "$1" in
        -w) WS="$2"; shift 2 ;;
        -n) CYCLES="$2"; shift 2 ;;
        *)  echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done
[ -n "$WS" ] || { echo "usage: $0 -w <workspace> [-n cycles]" >&2; exit 2; }
[ -x "$BNK" ] || { echo "$BNK not found or not executable (run: make build)" >&2; exit 2; }

LOGDIR="$(mktemp -d -t bnk-241-XXXXXX)"
echo "==> logs: $LOGDIR"

# The signatures every previous fix in this lineage produced. Any of them means
# the teardown is back to refusing deletes it should be accepting.
FAIL_PATTERNS=(
    'failed calling webhook'
    'no endpoints available for service'
    'did not finalize within'
    'every delete was refused'
    'Failed to delete all resource types'
    'context deadline exceeded'
    'Error: '
    'timed out waiting'
)

scan() {  # scan <phase> <logfile>
    local phase="$1" log="$2" hit=0
    for pat in "${FAIL_PATTERNS[@]}"; do
        if grep -qF "$pat" "$log"; then
            echo "  ✗ $phase: found \"$pat\""
            grep -nF "$pat" "$log" | head -5 | sed 's/^/      /'
            hit=1
        fi
    done
    return $hit
}

for i in $(seq 1 "$CYCLES"); do
    echo ""
    echo "=============================================================="
    echo "  cycle $i/$CYCLES — up"
    echo "=============================================================="
    up_log="$LOGDIR/cycle-$i-up.log"
    if ! "$BNK" bnk up -w "$WS" --auto 2>&1 | tee "$up_log"; then
        echo "✗ cycle $i: 'bnk up' exited non-zero — stopping, cluster left as-is"
        echo "  log: $up_log"
        exit 1
    fi
    if ! scan "up" "$up_log"; then
        echo "✗ cycle $i: 'bnk up' logged an error signature — stopping, cluster left as-is"
        exit 1
    fi
    echo "  ✓ up clean"

    echo ""
    echo "=============================================================="
    echo "  cycle $i/$CYCLES — down"
    echo "=============================================================="
    down_log="$LOGDIR/cycle-$i-down.log"
    start=$(date +%s)
    if ! "$BNK" bnk down -w "$WS" --auto 2>&1 | tee "$down_log"; then
        echo "✗ cycle $i: 'bnk down' exited non-zero — stopping, cluster left as-is"
        echo "  log: $down_log"
        exit 1
    fi
    elapsed=$(( $(date +%s) - start ))
    if ! scan "down" "$down_log"; then
        echo "✗ cycle $i: 'bnk down' logged an error signature — stopping, cluster left as-is"
        exit 1
    fi
    echo "  ✓ down clean (${elapsed}s)"

    # The #241 signature, which is INFORMATIONAL rather than a failure: it means
    # the sweep was outrun and won. Seeing it is how we know the loop is the thing
    # doing the work rather than the one-shot happening to be enough.
    if grep -q 'was re-created' "$down_log"; then
        grep 'was re-created' "$down_log" | sed 's/^/  ℹ /'
    fi

    # A down that takes materially longer than the drain budget means something
    # waited that should not have. Not an error signature, but the cost #241 is
    # about, so it is surfaced rather than buried.
    if [ "$elapsed" -gt 480 ]; then
        echo "  ⚠ down took ${elapsed}s — over 8 minutes is the pre-#235 cost profile; check $down_log"
    fi

    # #250: the cluster survives this teardown, so anything cluster-scoped that
    # should have gone is still observable. The webhook is the one that MUST be
    # gone -- it is named for the namespace, belongs to this workspace alone, and
    # a stale one pointing at a service that no longer exists blocks unrelated
    # work on the whole cluster. Retained CRDs and APIServices are reported by
    # the residue check but are a deliberate decision, not a failure.
    if kubectl get validatingwebhookconfiguration 2>/dev/null | grep -q "f5validate"; then
        echo "✗ cycle $i: a validating webhook served from the BNK namespace SURVIVED the teardown:"
        kubectl get validatingwebhookconfiguration 2>/dev/null | grep f5validate | sed 's/^/      /'
        exit 1
    fi
    if kubectl get ns 2>/dev/null | grep -E "f5-" | grep -q Terminating; then
        echo "✗ cycle $i: a BNK namespace is stuck Terminating after the teardown:"
        kubectl get ns 2>/dev/null | grep -E "f5-" | sed 's/^/      /'
        exit 1
    fi
    echo "  ✓ no webhook or Terminating namespace left behind"
done

echo ""
echo "✓ $CYCLES up/down cycles with no error signatures in either phase"
echo "  logs: $LOGDIR"
