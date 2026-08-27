#!/usr/bin/env bash
# cluster-residue-check.sh — report everything a BNK install leaves on a cluster.
#
# WHY THIS EXISTS. `bnk down` destroys terraform-managed resources and deletes the
# BNK namespaces. Neither of those removes CLUSTER-SCOPED objects, and a BNK 2.4
# install creates a lot of them — measured on a live 2.4.0-EA install:
#
#     101  CustomResourceDefinition
#      12  APIService
#      23  ClusterRole            (rbac.authorization.k8s.io + the OpenShift view)
#      20  ClusterRoleBinding
#
# cert-manager's own objects are excluded -- see NOT_OURS below.
#       1  ValidatingAdmissionPolicy + binding
#       1  ValidatingWebhookConfiguration   <- the #208/#235/#241 lineage
#
# A leftover CRD is not cosmetic: the next `bnk up` installs a chart that expects
# to create it, and a stale one at a different version is how a reinstall fails in
# a way nobody connects to the previous teardown. A leftover
# ValidatingWebhookConfiguration pointing at a service that no longer exists
# blocks unrelated work on the whole cluster.
#
#   ./scripts/testing/cluster-residue-check.sh                 # report residue now
#   ./scripts/testing/cluster-residue-check.sh --save before   # snapshot
#   ./scripts/testing/cluster-residue-check.sh --diff before   # what a teardown left
#
# Exits non-zero when residue is found, so it can gate a test.
set -uo pipefail

MODE=report
LABEL=""
while [ $# -gt 0 ]; do
    case "$1" in
        --save) MODE=save; LABEL="${2:-snapshot}"; shift 2 ;;
        --diff) MODE=diff; LABEL="${2:-snapshot}"; shift 2 ;;
        -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

command -v kubectl >/dev/null 2>&1 || { echo "kubectl not found on PATH" >&2; exit 2; }

CTX="$(kubectl config current-context 2>/dev/null)" || { echo "no kubectl context" >&2; exit 2; }
echo "==> cluster: $CTX"
# The shared forge kubeconfig gets repointed by other sessions. Saying which
# cluster this ran against turns a confusing empty result into an obvious one.

SNAPDIR="${TMPDIR:-/tmp}/bnk-residue"
mkdir -p "$SNAPDIR"

# What counts as ours. Matched on the API GROUP wherever possible rather than the
# object name: a name filter catches unrelated objects that happen to embed the
# cluster's name, which is how a first version of this reported 103 Calico
# ipamhandles as F5 residue.
OURS='(^|/)(f5|f5-|flo-|cne-)|\.f5\.com|\.f5net\.com|\.k8s\.f5'

# CERT-MANAGER IS DELIBERATELY NOT RESIDUE, even when roksbnkctl installed it.
#
# It is shared cluster infrastructure. Once it is there, anything on the cluster
# can start issuing Certificates against it, and those consumers do not go away
# because a BNK install did. Removing its CRDs on `bnk down` would delete every
# Certificate and Issuer on the cluster, including ones this tool never created
# and cannot see -- a far worse outcome than leaving a controller running.
#
# So its objects are excluded from the report rather than tolerated in it: a
# finding nobody is ever going to act on trains people to skim the list.
NOT_OURS='cert-manager|jetstack|\.acme\.'

collect() {
    local kinds
    kinds="$(kubectl api-resources --namespaced=false -o name 2>/dev/null | sort)"
    for k in $kinds; do
        # Skip the noisy per-node and per-image kinds no install owns.
        case "$k" in
            image.image.openshift.io|imagestreamtag*|node|nodemetrics*|*.projectcalico.org|\
            certificatesigningrequest*|oauthaccesstoken*|csinode*|*.metrics.k8s.io) continue ;;
        esac
        kubectl get "$k" -o name 2>/dev/null
    done | grep -iE "$OURS" | grep -viE "$NOT_OURS" | sort -u
}

case "$MODE" in
    save)
        collect > "$SNAPDIR/$LABEL.txt"
        echo "==> saved $(wc -l < "$SNAPDIR/$LABEL.txt") cluster-scoped object(s) to $SNAPDIR/$LABEL.txt"
        ;;
    diff)
        [ -f "$SNAPDIR/$LABEL.txt" ] || { echo "no snapshot named $LABEL in $SNAPDIR" >&2; exit 2; }
        collect > "$SNAPDIR/$LABEL.after.txt"
        echo "==> before: $(wc -l < "$SNAPDIR/$LABEL.txt")   after: $(wc -l < "$SNAPDIR/$LABEL.after.txt")"
        left="$(comm -12 "$SNAPDIR/$LABEL.txt" "$SNAPDIR/$LABEL.after.txt")"
        if [ -z "$left" ]; then
            echo "✓ nothing from the snapshot survived the teardown"
            exit 0
        fi
        echo "✗ $(printf '%s\n' "$left" | wc -l) object(s) from before the teardown are STILL PRESENT:"
        printf '%s\n' "$left" | sed 's|/.*||' | sort | uniq -c | sort -rn | sed 's/^/    /'
        echo ""
        printf '%s\n' "$left" | head -40 | sed 's/^/    /'
        exit 1
        ;;
    report)
        found="$(collect)"
        # Namespaces are reported separately: one left Terminating is the specific
        # failure #208/#217/#235/#241 all produced, and it reads differently from
        # a leftover cluster-scoped object.
        echo "==> BNK namespaces"
        kubectl get ns 2>/dev/null | grep -iE "f5-|NAME" | sed 's/^/    /'
        echo "==> cluster-scoped objects"
        if [ -z "$found" ]; then
            echo "    none"
            exit 0
        fi
        printf '%s\n' "$found" | sed 's|/.*||' | sort | uniq -c | sort -rn | sed 's/^/    /'
        echo ""
        echo "==> the ones a namespace delete can never remove:"
        printf '%s\n' "$found" | grep -iE "webhookconfiguration|apiservice|validatingadmissionpolicy|customresourcedefinition" \
            | sed 's|/.*||' | sort | uniq -c | sed 's/^/    /'
        exit 1
        ;;
esac
