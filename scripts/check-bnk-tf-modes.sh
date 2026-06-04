#!/usr/bin/env bash
# scripts/check-bnk-tf-modes.sh — Sprint 27 validator Issue 1 static
# assertions over the terraform-native BNK modules. HERMETIC: no cluster,
# no terraform init/plan; pure source-of-truth checks on the HCL that
# staff landed on branch sprint27-bnk-native-k8s.
#
# It proves, for the four restructured modules
# (cert_manager / flo / cne_instance / license), that:
#
#   C1  Every `time_sleep` resource is gated `count = local.use_legacy ? …`
#       — i.e. ZERO time_sleep survives in the kubectl path (the ~210s of
#       fixed sleeps die in kubectl mode; speed goal).
#   C2  Every `null_resource` carrying a `local-exec` curl/kubectl that
#       MUTATES THE KUBERNETES API (server-side-apply of a CR/Secret/RBAC
#       object against the cluster's `${var.kube_host}/api...`) is gated on
#       `local.use_legacy` — no curl CR-apply touches the cluster in the
#       kubectl path. EXEMPT (run in BOTH modes, by design): the FAR
#       auth-archive COS download, the tgz extractor, and the FLO/CIS
#       version-discovery helm-pull shell — the architect kept version
#       discovery terraform-side for both modes (it feeds helm_release
#       .version in kubectl mode). The discriminator is "curl/kubectl
#       against the kube API host", not "any local-exec".
#   C3  Every new `helm_release` / `kubernetes_*` / `kubectl_manifest`
#       resource is gated on `local.use_kubectl` (count or for_each), so
#       the legacy path never instantiates them.
#   C4  The CNEInstance and License `kubectl_manifest`s carry the spike's
#       `wait_for` block (CNEInstance: condition Available=True; License:
#       field status.state = "Verification Complete") AND a `depends_on`
#       on the FLO helm_release (directly or via the flo/cneinstance
#       dependency var) — CRD-before-CR ordering preserved.
#   C5  Legacy baseline intact: each `local.use_legacy`-gated time_sleep
#       and curl null_resource still exists (the benchmark baseline staff
#       promised is present, not deleted).
#
# Usage:   ./scripts/check-bnk-tf-modes.sh
# Exit:    0 = all assertions pass. Non-zero = first failed assertion named.
#
# This is a structural grep/awk checker, NOT a terraform run — it is the
# hermetic companion to `terraform fmt -check` + `terraform validate`
# (which the validator runs separately) and to the Go render test
# internal/tf/vars_crmode_test.go.

set -e
set -u
set -o pipefail

ROOT=${ROOT:-$(git -C "$(dirname "$0")/.." rev-parse --show-toplevel 2>/dev/null || echo "$(dirname "$0")/..")}
cd "$ROOT"

red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green() { printf '\033[32m%s\033[0m\n' "$*" >&2; }
bold()  { printf '\033[1m%s\033[0m\n'  "$*" >&2; }

FAILED=0
fail() { red "  ✗ $*"; FAILED=1; }
ok()   { green "  ✓ $*"; }

# Module inner main.tf files (the ones staff restructured).
CERT="terraform/modules/cert_manager/modules/cert-manager/main.tf"
FLO="terraform/modules/flo/modules/flo/main.tf"
CNE="terraform/modules/cne_instance/modules/cneinstance/main.tf"
LIC="terraform/modules/license/modules/license/main.tf"
MODULES="$CERT $FLO $CNE $LIC"

for f in $MODULES; do
  [[ -f "$f" ]] || { red "missing module file: $f"; exit 3; }
done

# block_gate <file> <resource-type> <resource-name>
# Prints the first `count =` or `for_each =` RHS inside the named resource
# block (the gating expression). Empty if the block or gate isn't found.
block_gate() {
  local file="$1" rtype="$2" rname="$3"
  awk -v rt="$rtype" -v rn="$rname" '
    $0 ~ "^resource \"" rt "\" \"" rn "\"" { inblk=1; depth=0 }
    inblk {
      n=gsub(/{/,"{"); depth+=n
      m=gsub(/}/,"}"); depth-=m
      if (gate=="" && ($0 ~ /^[[:space:]]*count[[:space:]]*=/ || $0 ~ /^[[:space:]]*for_each[[:space:]]*=/)) {
        line=$0; sub(/^[[:space:]]*(count|for_each)[[:space:]]*=[[:space:]]*/, "", line); gate=line
      }
      if (depth<=0 && NR>1 && seen) { print gate; exit }
      seen=1
    }
  ' "$file"
}

# all_resources_of_type <file> <resource-type> → "name<TAB>gate" per block.
all_resources_of_type() {
  local file="$1" rtype="$2"
  awk -v rt="$rtype" '
    $0 ~ "^resource \"" rt "\" \"" {
      name=$3; gsub(/"/,"",name)
      inblk=1; depth=0; gate=""; seen=0
    }
    inblk {
      n=gsub(/{/,"{"); depth+=n
      m=gsub(/}/,"}"); depth-=m
      if (gate=="" && ($0 ~ /^[[:space:]]*count[[:space:]]*=/ || $0 ~ /^[[:space:]]*for_each[[:space:]]*=/)) {
        line=$0; sub(/^[[:space:]]*(count|for_each)[[:space:]]*=[[:space:]]*/, "", line); gate=line
      }
      if (depth<=0 && seen) { print name "\t" gate; inblk=0 }
      seen=1
    }
  ' "$file"
}

bold "Sprint 27 validator — hermetic static assertions on the BNK modules"
echo "" >&2

# ── C1: every time_sleep is use_legacy-gated (zero in kubectl path) ──────────
bold "C1  every time_sleep gated local.use_legacy (zero in kubectl path)"
SLEEP_COUNT=0
for f in $MODULES; do
  while IFS=$'\t' read -r name gate; do
    [[ -z "$name" ]] && continue
    SLEEP_COUNT=$((SLEEP_COUNT+1))
    if [[ "$gate" != *"use_legacy"* ]]; then
      fail "$f: time_sleep.$name not gated on use_legacy (gate: ${gate:-<none>}) — would sleep in kubectl mode"
    fi
  done < <(all_resources_of_type "$f" "time_sleep")
done
[[ "$SLEEP_COUNT" -gt 0 ]] || fail "no time_sleep resources found at all — checker mis-parsed (expected the legacy baseline sleeps)"
[[ "$FAILED" == 0 ]] && ok "all $SLEEP_COUNT time_sleep resources gated use_legacy"

# ── C2: every kube-API-mutating curl null_resource is use_legacy-gated ───────
# EXEMPT: FAR COS download / tgz extractor / helm version-discovery shell —
# they curl COS or run helm-pull, NOT the kube API, and run in both modes.
# The discriminator is a curl/kubectl against `${var.kube_host}/api...`.
bold "C2  every kube-API-mutating curl null_resource gated local.use_legacy"
CURL_NR=0
for f in $MODULES; do
  while IFS=$'\t' read -r name gate; do
    [[ -z "$name" ]] && continue
    # Does THIS null_resource block curl/kubectl AGAINST THE KUBE API HOST?
    has_kube_curl=$(awk -v rn="$name" '
      $0 ~ "^resource \"null_resource\" \"" rn "\"" { inblk=1; depth=0; seen=0 }
      inblk {
        if ($0 ~ /var\.kube_host/ || $0 ~ /kubectl (apply|create|patch|label)/) hit=1
        n=gsub(/{/,"{"); depth+=n; m=gsub(/}/,"}"); depth-=m
        if (depth<=0 && seen) { print (hit?"yes":"no"); exit }
        seen=1
      }' "$f")
    [[ "$has_kube_curl" != "yes" ]] && continue
    CURL_NR=$((CURL_NR+1))
    if [[ "$gate" != *"use_legacy"* ]]; then
      fail "$f: null_resource.$name curls the kube API but is NOT use_legacy-gated (gate: ${gate:-<none>})"
    fi
  done < <(all_resources_of_type "$f" "null_resource")
done
[[ "$CURL_NR" -gt 0 ]] || fail "no kube-API curl null_resources found — checker mis-parsed (expected the legacy baseline)"
[[ "$FAILED" == 0 ]] && ok "all $CURL_NR kube-API-mutating curl null_resources gated use_legacy (FAR/helm version-discovery shell exempt)"

# ── C3: every helm_release / kubernetes_* / kubectl_manifest is use_kubectl ──
bold "C3  every helm_release / kubernetes_* / kubectl_manifest gated local.use_kubectl"
KCOUNT=0
for f in $MODULES; do
  for rt in helm_release kubernetes_namespace_v1 kubernetes_secret_v1 kubectl_manifest; do
    while IFS=$'\t' read -r name gate; do
      [[ -z "$name" ]] && continue
      KCOUNT=$((KCOUNT+1))
      if [[ "$gate" != *"use_kubectl"* ]]; then
        fail "$f: $rt.$name not gated on use_kubectl (gate: ${gate:-<none>}) — would instantiate in legacy mode"
      fi
    done < <(all_resources_of_type "$f" "$rt")
  done
done
[[ "$KCOUNT" -gt 0 ]] || fail "no kubectl-mode resources found — checker mis-parsed"
[[ "$FAILED" == 0 ]] && ok "all $KCOUNT helm_release/kubernetes_*/kubectl_manifest resources gated use_kubectl"

# ── C4: CNEInstance + License wait_for + depends_on on the FLO chart ─────────
bold "C4  CNEInstance + License kubectl_manifest carry wait_for + FLO depends_on"

# CNEInstance: condition Available=True, depends_on flo_deployment_dependency.
cne_block=$(awk '
  /^resource "kubectl_manifest" "cneinstance"/ {inblk=1; depth=0; seen=0}
  inblk { print; n=gsub(/{/,"{"); depth+=n; m=gsub(/}/,"}"); depth-=m; if (depth<=0 && seen) exit; seen=1 }
' "$CNE")
echo "$cne_block" | grep -q 'wait_for' || fail "cne_instance: CNEInstance kubectl_manifest missing wait_for"
echo "$cne_block" | grep -q 'type *= *"Available"' || fail "cne_instance: CNEInstance wait_for missing condition type=Available"
echo "$cne_block" | grep -q 'status *= *"True"' || fail "cne_instance: CNEInstance wait_for missing status=True"
echo "$cne_block" | grep -q 'depends_on.*flo_deployment_dependency' || fail "cne_instance: CNEInstance missing depends_on on the FLO helm_release (flo_deployment_dependency)"
[[ "$FAILED" == 0 ]] && ok "CNEInstance: wait_for condition Available=True + depends_on flo_deployment_dependency"

# License: field status.state = "Verification Complete", depends_on cneinstance/flo.
lic_block=$(awk '
  /^resource "kubectl_manifest" "bnk_license"/ {inblk=1; depth=0; seen=0}
  inblk { print; n=gsub(/{/,"{"); depth+=n; m=gsub(/}/,"}"); depth-=m; if (depth<=0 && seen) exit; seen=1 }
' "$LIC")
echo "$lic_block" | grep -q 'wait_for' || fail "license: License kubectl_manifest missing wait_for"
echo "$lic_block" | grep -q 'key *= *"status.state"' || fail "license: License wait_for missing field key=status.state"
echo "$lic_block" | grep -q 'value *= *"Verification Complete"' || fail "license: License wait_for missing value=\"Verification Complete\" (the spike literal — validator live-confirms)"
echo "$lic_block" | grep -q 'depends_on.*cneinstance_dependency' || fail "license: License missing depends_on on the cneinstance dependency (which itself depends on the FLO chart)"
[[ "$FAILED" == 0 ]] && ok "License: wait_for field status.state=\"Verification Complete\" + depends_on cneinstance_dependency"

# ── C5: legacy baseline intact (the benchmark resources still present) ───────
bold "C5  legacy baseline intact (curl null_resources + sleeps still present)"
grep -q 'resource "null_resource" "cneinstance"' "$CNE" || fail "legacy CNEInstance null_resource removed (baseline must stay)"
grep -q 'resource "null_resource" "bnk_license"' "$LIC" || fail "legacy License null_resource removed (baseline must stay)"
grep -q 'resource "time_sleep" "wait_for_cneinstance_crd"' "$CNE" || fail "legacy time_sleep wait_for_cneinstance_crd removed (baseline must stay)"
grep -q 'resource "time_sleep" "wait_for_license_crd"' "$LIC" || fail "legacy time_sleep wait_for_license_crd removed (baseline must stay)"
[[ "$FAILED" == 0 ]] && ok "legacy curl null_resources + time_sleep baseline present in all modules"

echo "" >&2
if [[ "$FAILED" == 0 ]]; then
  green "════════════════════════════════════════════════════════════"
  green "GREEN — all hermetic static assertions pass (C1–C5)"
  green "kubectl path: zero time_sleep, zero curl/local-exec; new"
  green "resources use_kubectl-gated; CNEInstance/License wait_for +"
  green "FLO depends_on present; legacy baseline intact."
  green "════════════════════════════════════════════════════════════"
  exit 0
else
  red "════════════════════════════════════════════════════════════"
  red "FAILED — one or more static assertions did not hold (see above)"
  red "════════════════════════════════════════════════════════════"
  exit 1
fi
