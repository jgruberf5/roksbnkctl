#!/usr/bin/env bash
# cwc-guard.sh — silent workaround for the f5-spk-cwc Multi-Attach deadlock.
#
# F5 DEFECT: the cluster-wide-controller (f5-spk-cwc) Deployment mounts a ReadWriteOnce
# PVC but ships with a RollingUpdate strategy. When the F5 Lifecycle Operator rolls it
# during `bnk up`, the new pod is scheduled before the old is terminated and — landing on
# a different node — cannot attach the RWO volume, so it sticks in ContainerCreating
# forever ("Multi-Attach error"). The rollout never completes and BNK licensing never
# activates (License CR stays out of Active), so `bnk up`'s license gate times out.
#
# This guard runs in the BACKGROUND on the operator VSI during `bnk up`: as soon as the
# cwc Deployment appears it forces strategy=Recreate (old pod terminated before new →
# volume freed) and clears any rollover deadlock by cycling replicas 0→1. Licensing then
# activates within bnk up's window. It is silent and self-terminating.
#
# THIS IS NOW A BNK 2.3-ONLY WORKAROUND (#169). The removal condition above is MET in 2.4:
# the 2.4.0-EA Deployment ships `strategy: Recreate` itself, observed at revision 1 with no
# patch annotations, so it is the product's own. Callers gate on the manifest line
# (`bnk_line_of` in ../lib/forge-mode.sh) rather than deleting this — 2.3 still ships
# RollingUpdate and is still the default manifest version.
#
# The PVC is still ReadWriteOnce in 2.4; F5 took the Recreate route rather than RWX.
# Evidence is one 2.4.0-EA cluster, so worth re-confirming at GA — though a strategy field
# is unlikely to regress.
set -uo pipefail
export PATH=$PATH:/usr/local/bin
KB=/home/ubuntu/kubectl
[ -x "$KB" ] || { curl -sLo "$KB" "https://dl.k8s.io/release/v1.31.4/bin/linux/amd64/kubectl" >/dev/null 2>&1 && chmod +x "$KB"; }

# Find a kubeconfig that can reach the cluster (wait up to ~10 min for adopt/kubeconfig).
KC=""
for _ in $(seq 1 60); do
  for c in $(find /home/ubuntu/.roksbnkctl -type f -name kubeconfig 2>/dev/null) /home/ubuntu/.kube/config; do
    [ -f "$c" ] && KUBECONFIG="$c" "$KB" get ns >/dev/null 2>&1 && { KC="$c"; break 2; }
  done
  sleep 10
done
[ -z "$KC" ] && exit 0
export KUBECONFIG="$KC"

# CWC lives in the shared-components namespace — f5-utils by default, but the
# SAME namespace as BNK on a one-namespace install (#66). Honour the override
# or this guard watches a namespace that does not exist, never patches, and the
# Multi-Attach deadlock it exists to break simply hangs bnk up.
NS="${ROKSBNKCTL_FLO_UTILS_NAMESPACE:-f5-utils}"

patched=0
for _ in $(seq 1 120); do              # up to ~20 min, covering the whole bnk up
  if "$KB" -n "$NS" get deploy f5-spk-cwc >/dev/null 2>&1; then
    if [ "$patched" = 0 ]; then
      "$KB" -n "$NS" patch deploy f5-spk-cwc --type=merge \
        -p '{"spec":{"strategy":{"type":"Recreate","rollingUpdate":null}}}' >/dev/null 2>&1 && patched=1
    fi
    # A rollover deadlock shows as >1 cwc pod with one stuck in ContainerCreating: release
    # the RWO volume by cycling to 0 then 1 so a single pod attaches cleanly.
    pods="$("$KB" -n "$NS" get pods 2>/dev/null | grep f5-spk-cwc || true)"
    if [ "$(echo "$pods" | grep -c f5-spk-cwc)" -gt 1 ] && echo "$pods" | grep -q ContainerCreating; then
      "$KB" -n "$NS" scale deploy f5-spk-cwc --replicas=0 >/dev/null 2>&1
      for _ in $(seq 1 20); do [ "$("$KB" -n "$NS" get pods 2>/dev/null | grep -c f5-spk-cwc)" = 0 ] && break; sleep 6; done
      "$KB" -n "$NS" scale deploy f5-spk-cwc --replicas=1 >/dev/null 2>&1
    fi
    [ "$("$KB" -n "$NS" get licenses.k8s.f5net.com bnk-license -o jsonpath='{.status.state}' 2>/dev/null)" = "Active" ] && exit 0
  fi
  sleep 10
done
