#!/usr/bin/env bash
# Record the disconnected demo in a headless X terminal to an MP4, then 10x the
# long deployment windows (marked by begin_long/end_long -> $TS_FILE) via ffmpeg.
#
# Usage: set the demo env (IBMCLOUD_API_KEY, EXISTING_CLUSTER, SSH_KEY_FILE,
# FAR_AUTH_LOCAL_FILE, SUBSCRIPTION_JWT_LOCAL_FILE, ROKSBNKCTL_BIN,
# FLP_STATUS_IMAGE_BUILD) and run: ./record-demo.sh
set -uo pipefail
HERE="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
OUT="${OUT:-$HERE/demo-video}"; mkdir -p "$OUT"
RAW="$OUT/demo-raw.mkv"; FINAL="${FINAL:-$OUT/demo-final.mp4}"
TS_FILE="$OUT/phase-timestamps.txt"; : > "$TS_FILE"
W=${W:-1600}; H=${H:-900}; DISP=${DISP:-:99}
export DISPLAY="$DISP"

command -v Xvfb >/dev/null || { echo "Xvfb missing"; exit 1; }
command -v xterm >/dev/null || { echo "xterm missing"; exit 1; }
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

echo "== run the demo in xterm (this is the long part) =="
# Pass the demo env explicitly into the xterm's shell.
xterm -geometry 195x48 -fa 'DejaVu Sans Mono' -fs 10 -bg '#0b1021' -fg '#e6e6e6' \
  -e bash -lc "
    cd '$HERE'
    export IBMCLOUD_API_KEY='${IBMCLOUD_API_KEY:-}'
    export EXISTING_CLUSTER='${EXISTING_CLUSTER:-bnkdemo-roks}'
    export SSH_KEY_FILE='${SSH_KEY_FILE:-$HOME/.ssh/bnk-airgap-key}'
    export SSH_KEY_NAME='${SSH_KEY_NAME:-bnk-airgap-key}'
    export FAR_AUTH_LOCAL_FILE='${FAR_AUTH_LOCAL_FILE:-$HOME/f5/f5-far-auth-key.tgz}'
    export SUBSCRIPTION_JWT_LOCAL_FILE='${SUBSCRIPTION_JWT_LOCAL_FILE:-$HOME/f5/subscription.jwt}'
    export ROKSBNKCTL_BIN='${ROKSBNKCTL_BIN:-roksbnkctl}'
    export FLP_STATUS_IMAGE_BUILD='${FLP_STATUS_IMAGE_BUILD:-$HERE/flp-status-build}'
    export TS_FILE='$TS_FILE' AUTO_ADVANCE=1 PHASE_DELAY='${PHASE_DELAY:-6}'
    export STATE_DIR='$OUT/.demo-state'
    bash '${DEMO_SCRIPT:-$HERE/disconnected-cluster-cli-demo.sh}'
    echo; echo '════════ DEMO FINISHED ════════'; sleep 12
  "

echo "== stop ffmpeg =="
kill -INT "$FF" 2>/dev/null; wait "$FF" 2>/dev/null; FF=""
kill "$XVFB" 2>/dev/null; XVFB=""
echo "raw recording: $(ls -la "$RAW" | awk '{print $5}') bytes"

echo "== post-process: 10x the long windows =="
python3 "$HERE/post_10x.py" "$RAW" "$TS_FILE" "$REC_START" "$FINAL"
echo "== DONE: $FINAL =="
