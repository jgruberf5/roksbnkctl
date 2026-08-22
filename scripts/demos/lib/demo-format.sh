#!/usr/bin/env bash
# =============================================================================
# demo-format.sh — the shared on-camera format for every roksbnkctl demo.
#
# This is the presentation contract the disconnected-cluster-cli-demo established
# and that every other demo now follows, so the demos differ only in WHAT they
# demonstrate — never in how they look:
#
#   • banner/phase  — a CLEARED screen + a cyan "PHASE n/N — Title" card, held and
#                     stilled, so every phase opens on a clean readable title;
#   • show/run      — a bold "$ <command>" line, then (for a roksbnkctl command) a
#                     COMMAND still and, after it runs, an OUTPUT still — see "the
#                     capture contract" below;
#   • begin_long/end_long — bracket a long deployment (cluster up, replicate,
#                     bnk up, …). post_10x.py speeds ONLY those windows to 10x,
#                     leaving every banner and still at readable speed;
#   • pause         — AUTO_ADVANCE=1 (hands-off, for recording) sleeps
#                     PHASE_DELAY seconds; AUTO_ADVANCE=0 waits for ENTER.
#
# SECRET MASKING. These demos are RECORDED, so no credential may reach the screen.
# Each demo registers its secrets with `secret <value>…` in preflight; from then on
# every helper that writes to the screen (banner/say/note/ok/die/show, and the
# show_file/runmask pair) replaces each registered value — and its base64 form —
# with ***REDACTED*** before printing. Never `cat` or `run grep` a file that may
# hold a credential: use show_file, which masks. roksbnkctl redacts its own
# sensitive output already (see internal/config/applied_tfvars.go redactedVarNames
# and phase_output.go --show-sensitive), so `run` does not filter command output —
# that keeps roksbnkctl's and terraform's TTY colours intact on camera.
#
# There is NO voiceover: say/note write the spoken context straight onto the
# screen, so a recording needs no TTS, no narration files and no chapter markers.
#
# Not executable on its own — `source` it. Callers may pre-set STATE_DIR,
# TS_FILE, AUTO_ADVANCE, PHASE_DELAY and DRY_RUN; defaults are applied here.
# =============================================================================

STATE_DIR="${STATE_DIR:-$PWD/.demo-state}"; mkdir -p "$STATE_DIR"
TS_FILE="${TS_FILE:-$STATE_DIR/phase-timestamps.txt}"; : > "$TS_FILE"
AUTO_ADVANCE="${AUTO_ADVANCE:-1}"   # 1 = hands-off; 0 = wait for ENTER
PHASE_DELAY="${PHASE_DELAY:-6}"     # seconds to pause between phases when hands-off
DRY_RUN="${DRY_RUN:-0}"             # 1 = print every command, run nothing

if [[ -t 2 ]]; then B=$'\e[1m'; DIM=$'\e[2m'; G=$'\e[32m'; Y=$'\e[33m'; C=$'\e[36m'; R=$'\e[31m'; N=$'\e[0m'; else B=""; DIM=""; G=""; Y=""; C=""; R=""; N=""; fi

# ── secret masking ───────────────────────────────────────────────────────────
# secret <value>… — register a credential that must never appear on screen. Each
# value is registered both verbatim and base64-encoded (configs often carry the
# encoded form). Short/empty values are ignored: masking "1" or "" would redact
# half the screen. Call this in preflight, as soon as the values are resolved.
_SECRETS=()
secret(){
  local v b n=0
  for v in "$@"; do
    [[ -n "${v:-}" && ${#v} -ge 8 ]] || continue
    _SECRETS+=("$v"); n=$((n + 1))
    b="$(printf '%s' "$v" | base64 -w0 2>/dev/null)"
    [[ -n "$b" && "$b" != "$v" ]] && _SECRETS+=("$b")
  done
  # Confirm on camera that masking is live before anything else is printed.
  (( n )) && ok "secret masking active — ${n} credential(s) render as ***REDACTED*** on screen"
  return 0
}
# redact <text…> — the registered secrets replaced by ***REDACTED*** (no newline).
redact(){
  local s="$*" v
  (( ${#_SECRETS[@]} )) || { printf '%s' "$s"; return 0; }
  for v in "${_SECRETS[@]}"; do s="${s//"$v"/***REDACTED***}"; done
  printf '%s' "$s"
}
# mask — a stdin→stdout filter applying redact() line by line. For LOW-VOLUME
# output only (it is a bash read loop, and it strips the TTY from the producer).
mask(){ local l; while IFS= read -r l || [[ -n "$l" ]]; do redact "$l"; echo; done; }

banner(){ { echo; echo "${B}${C}==============================================================================${N}"; echo "${B}${C} $(redact "$*") ${N}"; echo "${B}${C}==============================================================================${N}"; } >&2; }
say(){ echo "${DIM}$(redact "$*")${N}" >&2; }
note(){ { echo; echo "${Y}${B}NOTE:${N} ${Y}$(redact "$*")${N}"; echo; } >&2; }
ok(){ echo "${G}✓ $(redact "$*")${N}" >&2; }
die(){ echo "${R}✗ $(redact "$*")${N}" >&2; exit 1; }

# ── the capture contract ─────────────────────────────────────────────────────
# Every still in the final cut is driven by a QUEUE row this lib writes, so the whole
# timeline is reworkable by re-running lib/post_10x.py on the SAME raw recording — no
# re-record to change a delay or a speed.
#
#   PHASE   MARK  → hold the phase banner (on a freshly cleared screen), PHASE_SECS
#   COMMAND MARK  → hold the roksbnkctl command itself, CMD_SECS
#   OUTPUT  MARK  → hold that command's settled output, OUT_SECS
#   LONG    START/END → the ONLY window that gets sped (SPEED×)
#
# Each roksbnkctl command therefore reads as:
#   [5s still: the command] → [10× streaming output] → [5s still: its output]
#
# The holds are all REQUIRED, for distinct reasons:
#   • CMD_RENDER_HOLD  — x11grab samples at 10fps and the terminal draws asynchronously,
#                        so marking the instant after echo can stamp a timestamp whose
#                        frame predates the command appearing at all;
#   • CMD_POST_HOLD    — run() executes the moment show() returns, and a command that
#                        floods stdout would have scrolled the frame out from under the
#                        mark offset;
#   • OUT_SETTLE_HOLD  — the output needs to stop moving before its still is taken;
#   • OUT_POST_HOLD    — and must STAY on screen after the mark. Without this the mark
#                        sits at the very end of the settle period, and the next phase's
#                        clear_screen lands ~0.1s later — so the "output" still captured
#                        the NEXT PHASE'S BANNER. Exactly the CMD_POST_HOLD problem, on
#                        the output side; measured and fixed, see the note below;
#   • PHASE_BANNER_HOLD— the cleared screen + banner must be drawn before its still.
# Set them all to 0 for a fast non-recorded pass (see lib/check-masking.sh).
CMD_RENDER_HOLD="${CMD_RENDER_HOLD:-1.8}"   # command on screen before the COMMAND mark
CMD_POST_HOLD="${CMD_POST_HOLD:-0.7}"       # …and a beat after, before it runs
OUT_SETTLE_HOLD="${OUT_SETTLE_HOLD:-1.2}"   # output settles before the OUTPUT mark
OUT_POST_HOLD="${OUT_POST_HOLD:-0.9}"       # …and stays after it, before the next phase clears
PHASE_BANNER_HOLD="${PHASE_BANNER_HOLD:-1.5}"

# is_rbk_cmd — true only when roksbnkctl is the COMMAND WORD, never a bare substring.
# A plain *roksbnkctl* glob also matched an argument carrying the repo path (e.g.
# `ibmcloud is instance-create … --user-data @/mnt/d/project/roksbnkctl/…`), stamping a
# 5s still on a command that is not roksbnkctl at all.
is_rbk_cmd(){
  # roksbnkctl invoked directly, or via a wrapper that names it (onvsi_run-style).
  [[ "$1" =~ (^|[[:space:]])roksbnkctl[[:space:]] ]] && return 0
  # …or the tools-runner image, whose ENTRYPOINT *is* roksbnkctl: the CI demos run every
  # step as `docker run … roksbnkctl-tools-runner:<tag> <roksbnkctl args>`, so roksbnkctl
  # is never a standalone word and the rule above would yield ZERO stills for them.
  # Still excludes the repo-path false positive, which matches neither form.
  [[ "$1" =~ roksbnkctl-tools-runner ]] && return 0
  return 1
}
clear_screen(){ printf '\033[3J\033[2J\033[H' >&2; }
show(){ { echo; echo "${B}\$ $(redact "$*")${N}"; } >&2; if is_rbk_cmd "$*"; then sleep "$CMD_RENDER_HOLD"; ts COMMAND MARK "$(redact "$*")"; sleep "$CMD_POST_HOLD"; fi; }
outmark(){ if is_rbk_cmd "$*"; then sleep "$OUT_SETTLE_HOLD"; ts OUTPUT MARK "$(redact "$*")"; sleep "$OUT_POST_HOLD"; fi; }
# run <cmd...> — run a command on camera and RETURN ITS STATUS.
#
# The status used to be outmark's, not the command's, so a failing step was
# invisible to any caller that checked. Same defect as runmask below; both are
# fixed together because a caller cannot be expected to know which of the two
# reports honestly.
run(){
  show "$@"
  [[ "$DRY_RUN" == "1" ]] && { say "  (dry-run)"; return 0; }
  "$@"
  local rc=$?
  outmark "$@"
  return "$rc"
}
# must <cmd…> — run a command on camera and DIE if it fails.
#
# `run` returns the status faithfully, but nothing checked it, and the demos are
# `set -uo pipefail` with no `-e`. So a phase whose command failed outright still
# printed its ✓ and the demo still exited 0.
#
# Observed on BNK 2.4: the swap phase's `bnk up` failed on every object with
# "namespace f5-bnk is being terminated", and the very next line was
# "✓ BNK removed and reinstalled — no re-provisioning, the cluster never moved",
# followed by DEMO COMPLETE and rc=0. A demo that reports success no matter what
# happened cannot be used to verify anything, which is the one job it has when
# it runs unattended.
#
# Use `must` for anything that CHANGES the world — cluster up, bnk up/down,
# testing up. Keep plain `run` for informational commands (version, status,
# `k get pods`), where a non-zero is often just "not there yet" and killing the
# demo over it would be its own kind of false signal.
must(){
  run "$@"
  local rc=$?
  if [[ "$rc" -ne 0 ]]; then
    printf '\n' >&2
    die "command failed (exit $rc): $*
  The demo stops here rather than printing a ✓ over a failure. Everything is left
  in place so the failure can be inspected."
  fi
  return 0
}

# runmask <cmd…> — like run, but the command's OUTPUT is masked too. Use for any
# command that may echo a credential; prefer plain run elsewhere so roksbnkctl and
# terraform keep their TTY colours.
# runmask <cmd...> — run a command on camera with secrets masked, and RETURN ITS
# STATUS.
#
# The status used to be discarded: the command was piped into `mask`, so `$?`
# was mask's, and no caller looked anyway. A failing step scrolled its error
# past and the demo carried on to its success banner — the artifactory demo
# printed "DONE — FAR IS MIRRORED INTO ARTIFACTORY" after both `registry
# replicate` and `registry verify` had errored out. On a recorded demo that is
# a green banner over a red screen.
#
# PIPESTATUS[0] is the command's own status, not the pipeline's. Callers are
# free to ignore it (none of these scripts run under `set -e`), but it is now
# there to check.
runmask(){
  show "$@"
  [[ "$DRY_RUN" == "1" ]] && { say "  (dry-run)"; return 0; }
  "$@" 2>&1 | mask
  local rc=${PIPESTATUS[0]}
  outmark "$@"
  return "$rc"
}
# show_file <path> [drop-regex] — put a file on camera with every registered secret
# masked, optionally dropping lines matching drop-regex (for long, noisy values).
# ALWAYS use this instead of `run cat` / `run grep` on a file that holds inputs.
show_file(){
  local f="$1" drop="${2:-}"
  if [[ -n "$drop" ]]; then show "grep -vE '$drop' $f"; else show "cat $f"; fi
  [[ "$DRY_RUN" == "1" ]] && { say "  (dry-run)"; return 0; }
  if [[ -n "$drop" ]]; then grep -vE "$drop" "$f" | mask; else mask < "$f"; fi
}

# queue writer: <label> <ev> <epoch> [text]. The trailing text names what the row is
# for, so the queue file reads as a shot list. Labels: PHASE/COMMAND/OUTPUT (stills), LONG.
ts(){ local a="$1" b="$2"; shift 2; echo "$a $b $(date +%s.%N) $*" >> "$TS_FILE"; }
# phase — start every phase on a CLEARED screen, hold the banner, then mark it. Phase
# banners are never sped, so each phase opens on a clean, readable title card.
phase(){ clear_screen; banner "$2"; sleep "$PHASE_BANNER_HOLD"; ts PHASE MARK "$2"; sleep 0.4; }
endphase(){ :; }
# begin_long/end_long bracket a LONG DEPLOYMENT operation (cluster up, replicate,
# flp up, bnk up). post_10x.py speeds ONLY these windows to SPEED×, leaving the
# on-screen context and every still at normal, readable speed.
begin_long(){ say "  … (long-running; sped up 10x in the recording) …"; ts LONG START; }
end_long(){ ts LONG END; }
pause(){
  if [[ "$AUTO_ADVANCE" == "1" ]]; then sleep "$PHASE_DELAY"; return 0; fi
  read -r -p "${B}${G}» ENTER for next phase (q to quit): ${N}" a </dev/tty 2>/dev/null || return 0
  [[ "$a" == "q" ]] && { echo Quit. >&2; exit 0; }
}
trap 'echo >&2; echo "${R}Interrupted.${N}" >&2; exit 130' INT

# ── binary preflight (#143) ─────────────────────────────────────────────────
# Lives in its own file so disconnected-cluster-cli-demo.sh can source it too:
# that demo inlines its own copies of the helpers below rather than sourcing
# this file, so a function defined here is invisible to it. It was: the
# preflight was a "command not found" there — in the one demo that ships the
# local binary to a VSI, where a stale binary has the widest blast radius.
source "$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)/preflight-binary.sh"

# ── forge mode (#164) ───────────────────────────────────────────────────────
# BNK Forge gates exactly one phase of the lifecycle demos; forge_mode decides
# whether that phase runs rather than letting its absence kill the whole demo.
source "$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)/forge-mode.sh"

# ── bnk line (#169) ─────────────────────────────────────────────────────────
# Per-release gating for the demo tooling, e.g. the cwc Multi-Attach guard that
# 2.4 makes unnecessary.
source "$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)/bnk-line.sh"
