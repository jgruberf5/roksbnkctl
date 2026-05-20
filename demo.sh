#!/bin/bash
#
# demo.sh — a brief tour of roksbnkctl.
#
# Assumptions (do these once, before running the demo):
#   - roksbnkctl is on PATH
#   - IBMCLOUD_API_KEY is exported (or persisted via `roksbnkctl init`)
#   - A workspace named `canada-roks` has already been initialized
#     and points at a live ROKS cluster with BNK deployed.
#       roksbnkctl init -w canada-roks
#       roksbnkctl up   -w canada-roks --var-file ./terraform.tfvars
#   - `./terraform.tfvars` exists in CWD (run `roksbnkctl tfvars` first)
#
# Long-running (`up`) and destructive (`down`, `bnk down`) commands are
# shown via `--help` only; everything else is real, read-only, and fast.

. /usr/local/bin/demo-magic.sh

# default pause before each `pe`; bump per-step where output needs reading time
PROMPT_TIMEOUT=3

clear

# --- intro -----------------------------------------------------------------

pe "# roksbnkctl: deploy F5 BIG-IP Next for Kubernetes (BNK) onto IBM Cloud"
pe "# ROKS, manage the COS supply chain, and validate the deployment."

pe "roksbnkctl version"

PROMPT_TIMEOUT=5
pe "roksbnkctl --help"
PROMPT_TIMEOUT=3
wait
clear
# --- preflight -------------------------------------------------------------

pe "# Preflight. Terraform on PATH is the only required host install —"
pe "# kubectl, oc, ibmcloud, iperf3, dig are internalised."

PROMPT_TIMEOUT=5
pe "roksbnkctl doctor"
PROMPT_TIMEOUT=3
wait
clear
# --- workspaces ------------------------------------------------------------

pe "# Per-environment config + state bundles under ~/.roksbnkctl/<name>/."

pe "roksbnkctl workspaces list"
pe "roksbnkctl workspaces use canada-roks"
pe "roksbnkctl status"
wait
clear
# --- COS supply chain (value prop #2) --------------------------------------

pe "# COS supply chain: FAR pull keys, JWT licenses — what BNK consumes."

pe "roksbnkctl cos --help"
pe "roksbnkctl cos object list bnk-schematics-resources  --instance bnk-orchestration"
wait
clear
# --- lifecycle: tfvars → plan → up → test → down ---------------------------

pe "# 4-command lifecycle: init → up → test → down."
pe "# tfvars emits the upstream TF's example as a starting point."

PROMPT_TIMEOUT=5
pe "roksbnkctl tfvars -o - | head -40"
wait
clear
pe "# plan is read-only; shows what 'up' would change. Fast."
PROMPT_TIMEOUT=8
pe "roksbnkctl plan --var-file=./terraform.tfvars"
wait
clear
PROMPT_TIMEOUT=3
pe "# 'up' provisions ROKS + deploys BNK; ~30 minutes against real cloud."
pe "roksbnkctl up --var-file=./terraform.tfvars"

# --- internalised passthroughs --------------------------------------------
wait
clear
pe "# Internal kubectl (client-go) — no host kubectl required."
pe "roksbnkctl k get nodes"
pe "roksbnkctl k get pods -n f5-utils"
wait
clear
pe "# Internalised ibmcloud — workspace API key + region pre-loaded."
pe "roksbnkctl ibmcloud ks clusters"

# --- built-in validation (value prop #3) -----------------------------------
wait
clear
pe "# Built-in connectivity, DNS, and throughput suites."
pe "roksbnkctl test --help"
wait
clear
PROMPT_TIMEOUT=6
pe "roksbnkctl test dns"
pe "roksbnkctl test connectivity"
PROMPT_TIMEOUT=3

# --- teardown (shown as help only) -----------------------------------------
wait
clear
pe "# Selective teardown for repeated installs on the same cluster"
pe "roksbnkctl bnk down --help"

pe "roksbnkctl status"
