# demo-lib.sh — shared on-camera presentation helpers for the roksbnkctl demos.
# Sourced by cli-demo.sh, ci-demo.sh and far-replication-demo.sh. Presentation style:
#   • per-stage "cards": clear the screen + a cyan title bar (STAGE n/N · Title),
#     so each phase reads as a distinct stage instead of one stream of text;
#   • framed commands: a bright-green ❯ prompt + bold-white command + a dim rule,
#     so the viewer can tell the typed command from its console output below it.
# The "STAGE n/N · Title" line doubles as the chapter marker postprocess.sh greps
# to align the EN/FR voiceover, so keep the format in sync with that regex.
#
# Pacing + palette are overridable via env (PACE/STAGE_HOLD/TYPE_MIN/TYPE_MAX/
# READ_DELAY). Not executable on its own — `source` it.

PACE="${PACE:-2}"
STAGE_HOLD="${STAGE_HOLD:-2.4}"    # pause on a fresh stage card (let it register)
TYPE_MIN="${TYPE_MIN:-2}"          # per-keystroke jitter: 0.0<min>..0.0<max>s
TYPE_MAX="${TYPE_MAX:-5}"          # (human-typing effect; keep min<=max, 1..9)
READ_DELAY="${READ_DELAY:-1.2}"    # pause after a command is typed, before it runs

# SGR palette.
_BAR=$'\033[1;36m'; _TITLE=$'\033[1;97m'; _PROMPT=$'\033[1;92m'
_CMD=$'\033[1;97m'; _DIM=$'\033[2m'; _OFF=$'\033[0m'
_BARLINE='════════════════════════════════════════════════════════════════════════'
_RULELINE='────────────────────────────────────────────────────────────'

# stage <n/N> <title> — clear the screen and print a stage card. The middle line
# ("  STAGE n/N · <title>") is what postprocess.sh greps as the chapter marker,
# so <title> must contain the narration key for that stage.
stage() {
  printf '\033[2J\033[3J\033[H'
  printf '%s%s%s\n' "$_BAR" "$_BARLINE" "$_OFF"
  printf '  %sSTAGE %s%s %s·%s %s%s%s\n' "$_TITLE" "$1" "$_OFF" "$_BAR" "$_OFF" "$_TITLE" "$2" "$_OFF"
  printf '%s%s%s\n\n' "$_BAR" "$_BARLINE" "$_OFF"
  sleep "$STAGE_HOLD"
}

# say <text> — a dim intent note (on-screen context; distinct from the voiceover).
say() { printf '  %s# %s%s\n' "$_DIM" "$*" "$_OFF"; sleep 0.6; }

# type_cmd <cmd...> — animate a framed command line one character at a time
# (human-typing effect), then a dim rule + read pause. Does NOT execute.
type_cmd() {
  local s="$*" i
  printf '  %s❯%s %s' "$_PROMPT" "$_OFF" "$_CMD"
  for ((i=0; i<${#s}; i++)); do
    printf '%s' "${s:i:1}"
    sleep "0.0$(( RANDOM % (TYPE_MAX - TYPE_MIN + 1) + TYPE_MIN ))"
  done
  printf '%s\n' "$_OFF"
  printf '  %s%s%s\n' "$_DIM" "$_RULELINE" "$_OFF"
  sleep "$READ_DELAY"
}

# run <cmd...> — type the command, then execute it (argv form preserves quoting).
run() { type_cmd "$*"; "$@"; }
