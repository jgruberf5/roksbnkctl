# shellcheck shell=bash
# ── forge_mode (#164) ────────────────────────────────────────────────────────
#
# The lifecycle demos hard-required FORGE_URL/USER/PASS in their PREFLIGHT and
# died without them — but BNK Forge is used by exactly one phase, the
# `bnkforge register` step. Everything else (init, cluster up, bnk up, testing
# up + test, the bnk down/up swap) never touches it: internal/tf/vars.go carries
# no Forge fields, and two sibling demos — shared-licensing-cli-demo and
# disconnected-cluster-cli-demo — already run `bnk up` with no Forge at all.
#
# So the gate was locking five of six phases, including the whole terraform
# half, behind a credential they do not use. Validating a terraform release
# meant either obtaining Forge access or driving the commands by hand.
#
# Three states, not two. "No Forge configured" is a legitimate way to run these
# demos; "half-configured Forge" is a mistake and must still fail loudly, because
# silently skipping the register phase when someone clearly meant to use it is
# the worse outcome — they would watch the demo and believe registration
# happened.
#
# Sets FORGE_ENABLED to "true" or "false". Dies on a partial configuration.
#
# Usage, in preflight, AFTER .env sourcing and BEFORE any phase:
#   forge_mode
forge_mode(){
  local set_count=0 missing=() present=()

  [[ -n "${FORGE_URL:-}"  ]] && { set_count=$((set_count+1)); present+=(FORGE_URL);  } || missing+=(FORGE_URL)
  [[ -n "${FORGE_USER:-}" ]] && { set_count=$((set_count+1)); present+=(FORGE_USER); } || missing+=(FORGE_USER)
  [[ -n "${FORGE_PASS:-}" ]] && { set_count=$((set_count+1)); present+=(FORGE_PASS); } || missing+=(FORGE_PASS)

  if (( set_count == 3 )); then
    FORGE_ENABLED=true
    export BNK_FORGE_URL="$FORGE_URL" BNK_FORGE_USER="$FORGE_USER" BNK_FORGE_PASSWORD="$FORGE_PASS"
  elif (( set_count == 0 )); then
    FORGE_ENABLED=false
  else
    # Partial: they meant to use Forge and got it wrong. Name what is missing.
    die "BNK Forge is half-configured: ${present[*]} set, ${missing[*]} missing.
      Set all three to run the registration phase, or none of them to skip it."
  fi
  export FORGE_ENABLED
}

# forge_skip_note prints the on-screen explanation for a skipped registration
# phase. Takes the phase label so each demo can name its own numbering, which
# differs (the CLI demo has six phases, the CI demo seven).
#
# The phase is NOT renumbered when skipped: the numbering matches the chapter
# text and the recorded narration, and shifting it would make the demo and the
# book disagree about which phase is which.
forge_skip_note(){
  note "SKIPPING ${1:-the BNK Forge phase} — no FORGE_URL/FORGE_USER/FORGE_PASS in the environment.
      Registration hands a durable cluster to BNK Forge for management. Nothing else in
      this demo needs it: the cluster build, the BNK install, the probes and the BNK
      swap all run exactly as they would with it.
      Set all three to include this phase."
}
