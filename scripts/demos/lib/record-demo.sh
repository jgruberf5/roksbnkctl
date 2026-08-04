#!/usr/bin/env bash
# =============================================================================
# record-demo.sh — the shared recorder for every roksbnkctl demo.
#
# Runs a demo hands-off in a headless X terminal, captures it to an MP4, then
# post-processes it with post_10x.py:
#   • the LONG deployment windows (begin_long/end_long -> $TS_FILE) become 10x;
#   • every roksbnkctl command frame (FREEZE markers) is HELD for 5 seconds.
#
# There is no voiceover stage and no VSI: the demo runs on THIS host, in an Xvfb
# display, and the camera points at that display.
#
# Usage — each demo ships a one-line record.sh that points here:
#     DEMO_SCRIPT=/path/to/<demo>.sh scripts/demos/lib/record-demo.sh
#
# The demo's own .env (next to DEMO_SCRIPT, or $ENV_FILE) is sourced INSIDE the
# recorded shell, so every input the demo needs travels with it.
#
# Env: DEMO_SCRIPT (required), ENV_FILE, OUT, FINAL, W, H, DISP, PHASE_DELAY,
#      CMD_RENDER_HOLD / CMD_POST_HOLD / OUT_SETTLE_HOLD / OUT_POST_HOLD /
#      PHASE_BANNER_HOLD (how long each command, output and banner is held
#      on screen around its queue mark — see lib/demo-format.sh).
# Requires: Xvfb, xterm, ffmpeg, python3.
# =============================================================================
set -uo pipefail
LIB="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"

DEMO_SCRIPT="${DEMO_SCRIPT:-}"
[[ -n "$DEMO_SCRIPT" && -f "$DEMO_SCRIPT" ]] || { echo "set DEMO_SCRIPT=/path/to/<demo>.sh"; exit 1; }
DEMO_SCRIPT="$(readlink -f "$DEMO_SCRIPT")"
DEMO_DIR="$(dirname "$DEMO_SCRIPT")"
DEMO_NAME="$(basename "$DEMO_SCRIPT" .sh)"
ENV_FILE="${ENV_FILE:-$DEMO_DIR/.env}"

OUT="${OUT:-$DEMO_DIR/demo-video}"; mkdir -p "$OUT"
RAW="$OUT/demo-raw.mkv"; FINAL="${FINAL:-$OUT/${DEMO_NAME}.mp4}"
TS_FILE="$OUT/phase-timestamps.txt"; : > "$TS_FILE"
W=${W:-1600}; H=${H:-900}; DISP=${DISP:-:99}
export DISPLAY="$DISP"

command -v Xvfb   >/dev/null || { echo "Xvfb missing";   exit 1; }
command -v xterm  >/dev/null || { echo "xterm missing";  exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg missing"; exit 1; }

cleanup(){ kill "${FF:-}" "${XVFB:-}" 2>/dev/null; }
trap cleanup EXIT

echo "== start Xvfb $DISP (${W}x${H}) =="
Xvfb "$DISP" -screen 0 "${W}x${H}x24" -nolisten tcp >/dev/null 2>&1 & XVFB=$!
sleep 2

echo "== start ffmpeg x11grab -> $RAW =="
REC_START="$(date +%s.%N)"; echo "$REC_START" > "$OUT/rec_start"
ffmpeg -y -f x11grab -video_size "${W}x${H}" -framerate 10 -i "$DISP" \
  -c:v libx264 -preset ultrafast -crf 26 -pix_fmt yuv420p "$RAW" >/dev/null 2>&1 & FF=$!
sleep 1

echo "== run $DEMO_NAME in xterm (this is the long part) =="
# The demo's .env is sourced inside the recorded shell, so all of its inputs —
# API key, cluster names, registry credentials — travel with the demo itself.
xterm -geometry 195x48 -fa 'DejaVu Sans Mono' -fs 10 -bg '#0b1021' -fg '#e6e6e6' \
  -e bash -lc "
    cd '$DEMO_DIR'
    [ -f '$ENV_FILE' ] && { set -a; . '$ENV_FILE'; set +a; }
    export TS_FILE='$TS_FILE' AUTO_ADVANCE=1 PHASE_DELAY='${PHASE_DELAY:-6}'
    export CMD_RENDER_HOLD='${CMD_RENDER_HOLD:-1.8}' CMD_POST_HOLD='${CMD_POST_HOLD:-0.7}'
    export OUT_SETTLE_HOLD='${OUT_SETTLE_HOLD:-1.2}' OUT_POST_HOLD='${OUT_POST_HOLD:-0.9}'
    export PHASE_BANNER_HOLD='${PHASE_BANNER_HOLD:-1.5}'
    export STATE_DIR='$OUT/.demo-state'
    bash '$DEMO_SCRIPT'
    echo; echo '════════ DEMO FINISHED ════════'; sleep 12
  "

echo "== stop ffmpeg =="
kill -INT "$FF" 2>/dev/null; wait "$FF" 2>/dev/null; FF=""
kill "$XVFB" 2>/dev/null; XVFB=""
echo "raw recording: $(ls -la "$RAW" | awk '{print $5}') bytes"

echo "== post-process: 10x the long windows, hold each roksbnkctl command 5s =="
python3 "$LIB/post_10x.py" "$RAW" "$TS_FILE" "$REC_START" "$FINAL"
echo "== DONE: $FINAL =="
