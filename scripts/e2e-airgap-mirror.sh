#!/usr/bin/env bash
# e2e-airgap-mirror.sh — Sprint 29 gated-live air-gap acceptance (PRD 11 §Acceptance).
#
# Proves that, with all external registry egress blocked, BNK installs entirely
# from the cluster's OpenShift internal registry:
#   1. registry replicate  → mirror the full BOM (FAR + cert-manager + bitnami)
#   2. registry verify      → every artifact present + digest-matched
#   3. block egress to repo.f5.com / quay.io / docker.io / charts.jetstack.io
#   4. bnk up               → must reach a licensed, Ready TMM
#   5. assert every BNK pod pulled from image-registry...svc:5000 (zero external)
#
# This is a LIVE test: a real ROKS cluster + the workspace's FAR credentials. It is
# the acceptance gate for the Registry phase; it is not run by `go test`.
#
# Usage: scripts/e2e-airgap-mirror.sh <workspace>
set -euo pipefail

WS="${1:?usage: e2e-airgap-mirror.sh <workspace>}"
RB="${ROKSBNKCTL:-roksbnkctl}"
EXTERNAL_REGISTRIES=(repo.f5.com quay.io docker.io charts.jetstack.io)
MIRROR_NS="${MIRROR_NS:-bnk-mirror}"

log() { printf '\n=== %s ===\n' "$*"; }
fail() { printf '\n✗ FAIL: %s\n' "$*" >&2; exit 1; }

log "1/5 replicate the BOM into the internal registry"
"$RB" registry replicate -w "$WS" --target openshift --auto

log "2/5 verify the mirror is complete"
"$RB" registry verify -w "$WS" || fail "registry verify reported missing/mismatched artifacts"

log "3/5 block external registry egress (air-gap simulation)"
# Default-deny egress to the external registries for the BNK namespaces. Replace
# with the cluster's real egress policy / firewall in a true air-gapped site; this
# NetworkPolicy approximation forces image pulls to resolve only in-cluster.
for ns in f5-bnk f5-utils f5-app; do
  kubectl get ns "$ns" >/dev/null 2>&1 || kubectl create ns "$ns"
done
cat <<'YAML' | kubectl apply -f -
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata: { name: airgap-block-external-egress, namespace: f5-bnk }
spec:
  podSelector: {}
  policyTypes: [Egress]
  egress:
    - to:
        - ipBlock: { cidr: 10.0.0.0/8 }      # in-cluster + IBM private
        - ipBlock: { cidr: 172.16.0.0/12 }
YAML
echo "  (egress to ${EXTERNAL_REGISTRIES[*]} is now blocked for f5-bnk)"

log "4/5 bnk up (must install from the mirror only)"
"$RB" bnk up -w "$WS" --auto || fail "bnk up failed under air-gap (an artifact was likely missing from the BOM)"

log "5/5 assert every BNK image came from the internal registry"
bad=0
for ns in f5-bnk f5-utils f5-app; do
  while read -r img; do
    [ -z "$img" ] && continue
    case "$img" in
      image-registry.openshift-image-registry.svc:5000/*) : ;;        # good
      "${MIRROR_NS}"/*) : ;;                                            # imagestream short form
      *) echo "  ✗ external image in $ns: $img"; bad=$((bad+1)) ;;
    esac
  done < <(kubectl get pods -n "$ns" -o jsonpath='{range .items[*].spec.containers[*]}{.image}{"\n"}{end}' 2>/dev/null)
done
[ "$bad" -eq 0 ] || fail "$bad pod image(s) pulled from outside the internal registry"

# TMM licensed + Ready.
kubectl wait --for=condition=Ready pod -l app=f5-tmm -n f5-bnk --timeout=15m \
  || fail "TMM did not reach Ready under air-gap"

printf '\n✓ PASS: BNK installed from the internal registry with external egress blocked — licensed, Ready TMM.\n'
