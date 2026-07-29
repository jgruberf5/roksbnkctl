#!/usr/bin/env bash
#
# voiceover.sh — produce narrated, 1080p 16:9 demo videos (one per language) with
# SERIALIZED, SYNCED audio.
#
#   ./voiceover.sh <compressed.cast> <chapters.tsv> [lang...]
#   ./voiceover.sh                    # defaults to out/latest.compressed.cast, "en fr"
#
# For each language it: (1) synthesizes one narration clip per chapter with Piper
# and measures each clip's length; (2) inserts a HOLD into the cast timeline at
# each chapter so the screen pauses just long enough for that clip to finish
# before the next chapter starts — so only one voice is ever heard at a time and
# it stays aligned to what's on screen; (3) renders the held cast to 1080p; and
# (4) muxes each clip at its (shifted) chapter start. Output: <base>.<lang>.mp4.
#
# Requires: piper (+ voice models), agg, ffmpeg. Models via PIPER_MODEL_EN /
# PIPER_MODEL_FR. Tunables: FONT_SIZE, THEME, FPS, RES (default 1920x1080),
# PAD_COLOR, NARR_PAD (silence after each clip, default 0.6s).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/out}"

CAST="${1:-$OUT_DIR/latest.compressed.cast}"
CHAPTERS_ARG="${2:-}"
shift $(( $# > 2 ? 2 : $# )) || true
LANGS=("$@"); [[ ${#LANGS[@]} -gt 0 ]] || LANGS=(en fr)

FONT_SIZE="${FONT_SIZE:-20}"; THEME="${THEME:-asciinema}"; FPS="${FPS:-30}"
RES="${RES:-1920x1080}"; PAD_COLOR="${PAD_COLOR:-0x1e1e1e}"
NARR_PAD="${NARR_PAD:-0.6}"
# Seconds of silence Piper inserts after each sentence within a narration clip,
# so multi-sentence narration doesn't run together (e.g. "…IBM Cloud." → a clear
# pause → "roksbnkctl only requires…"). The video hold grows with the clip.
SENTENCE_SILENCE="${SENTENCE_SILENCE:-2.0}"
CW="${RES%x*}"; CH="${RES#*x}"

[[ -f "$CAST" ]] || { echo "✗ compressed cast not found: $CAST (run postprocess.sh first)." >&2; exit 1; }
CHAPTERS="${CHAPTERS_ARG:-${CAST%.compressed.cast}.chapters.tsv}"
[[ -f "$CHAPTERS" ]] || { echo "✗ chapters file not found: $CHAPTERS" >&2; exit 1; }
for t in piper agg ffmpeg ffprobe; do command -v "$t" >/dev/null 2>&1 || { echo "✗ '$t' not on PATH." >&2; exit 1; }; done

log() { printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()  { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }

# narr_text <chapter-label> <file> — first line whose key is a substring of the
# label (keying on the label survives chapter renumbering, e.g. forge skipped).
narr_text() { awk -F'|' -v lbl="$1" '!/^[[:space:]]*#/ && NF>=2 && index(lbl,$1)>0 {sub(/^[^|]*\|/,""); print; exit}' "$2"; }
model_for() {
  case "$1" in
    en) echo "${PIPER_MODEL_EN:-$SCRIPT_DIR/voices/en_US-ryan-high.onnx}" ;;
    fr) echo "${PIPER_MODEL_FR:-$SCRIPT_DIR/voices/fr_FR-tom-medium.onnx}" ;;
    *)  echo "${SCRIPT_DIR}/voices/$1.onnx" ;;
  esac
}

mux_lang() {
  local lang="$1" model narr out tmp
  model="$(model_for "$lang")"
  narr="$SCRIPT_DIR/narration.$lang.txt"
  [[ -f "$narr" ]]  || { echo "✗ no narration file: $narr" >&2; return 1; }
  [[ -f "$model" ]] || { echo "✗ Piper model missing: $model" >&2; return 1; }
  out="${CAST%.compressed.cast}.$lang.mp4"
  tmp="$(mktemp -d)"

  # 1) synthesize one clip per chapter, measure durations, build the dur table.
  log "[$lang] synthesizing narration…"
  local durfile="$tmp/durs.tsv"; : > "$durfile"
  local -a wavs=() secs_list=()
  while IFS=$'\t' read -r secs idx label; do
    [[ -z "${idx:-}" ]] && continue
    local text wav="$tmp/seg_$idx.wav" d=0
    text="$(narr_text "$label" "$narr")"
    if [[ -n "$text" ]]; then
      printf '%s\n' "$text" | python3 "$SCRIPT_DIR/narrate.py" "$model" "$wav" "$SENTENCE_SILENCE" >/dev/null 2>&1
      d="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$wav" 2>/dev/null || echo 0)"
    else
      wav=""; printf '\033[2m  chapter %s (%s): no %s narration\033[0m\n' "$idx" "$label" "$lang"
    fi
    printf '%s\t%s\n' "$secs" "$d" >> "$durfile"
    wavs+=("$wav"); secs_list+=("$secs")
  done < "$CHAPTERS"

  # 2) insert holds so each chapter stays on screen >= its clip length + pad.
  log "[$lang] inserting holds (serialize + sync)…"
  local held="$tmp/held.cast" newchap="$tmp/newchap.tsv"
  CAST="$CAST" DURFILE="$durfile" PAD="$NARR_PAD" HELD="$held" NEWCHAP="$newchap" python3 - <<'PY'
import json, os
cast=os.environ["CAST"]; pad=float(os.environ["PAD"])
held=os.environ["HELD"]; newchap=os.environ["NEWCHAP"]
chaps=[]
for ln in open(os.environ["DURFILE"]):
    ln=ln.strip()
    if not ln: continue
    s,d=ln.split("\t"); chaps.append([float(s),float(d)])
chaps.sort(key=lambda x:x[0])
ct=[c for c,_ in chaps]; du=[d for _,d in chaps]
# cumulative shift applied at/after each chapter start
seg_delta=[]; delta=0.0
for i in range(len(chaps)):
    if i>0:
        gap=ct[i]-ct[i-1]; need=du[i-1]+pad
        if need>gap: delta+=need-gap
    seg_delta.append(delta)
def dfor(t):
    d=0.0
    for i in range(len(ct)):
        if ct[i]<=t+1e-9: d=seg_delta[i]
        else: break
    return d
f=open(cast, encoding="utf-8"); hdr=f.readline()
w=open(held,"w",encoding="utf-8"); w.write(hdr if hdr.endswith("\n") else hdr+"\n")
last=0.0
for line in f:
    line=line.strip()
    if not line: continue
    ev=json.loads(line); nt=ev[0]+dfor(ev[0])
    w.write(json.dumps([round(nt,6),ev[1],ev[2]])+"\n"); last=max(last,nt)
# extend the timeline so the final clip fully plays
new_ct=[ct[i]+seg_delta[i] for i in range(len(ct))]
end_need=new_ct[-1]+du[-1]+pad if new_ct else last
if end_need>last: w.write(json.dumps([round(end_need,6),"o",""])+"\n")
w.close(); f.close()
with open(newchap,"w",encoding="utf-8") as c:
    for i in range(len(new_ct)): c.write(f"{new_ct[i]:.3f}\n")
PY

  # 3) render the held cast, then mux the clips at their shifted chapter starts.
  #
  # --idle-time-limit is CRITICAL here: agg defaults to 5s, which would compress
  # the per-chapter holds we just inserted (step 2) back down to 5s in the render.
  # The audio is muxed at the FULL hold offsets, so a compressed video races ahead
  # and the narration falls progressively behind. Raise the limit above the
  # longest hold (max clip duration + pad) so every hold renders at full length
  # and audio stays aligned. The compressed cast's own gaps are already <= MAX_IDLE
  # (postprocess), so nothing else is affected.
  local idle_limit
  idle_limit=$(awk -F'\t' -v pad="$NARR_PAD" 'BEGIN{m=0} {if($2+0>m)m=$2+0} END{v=int(m+pad+3); print (v<10?10:v)}' "$durfile")
  log "[$lang] rendering ${RES} + muxing… (idle-time-limit ${idle_limit}s)"
  local gif="$tmp/$lang.gif"
  agg --font-size "$FONT_SIZE" --theme "$THEME" --fps-cap "$FPS" --idle-time-limit "$idle_limit" "$held" "$gif" >/dev/null 2>&1

  local -a newsecs=(); mapfile -t newsecs < "$newchap"
  local -a inputs=(); local fc="[0:v]scale=${CW}:${CH}:force_original_aspect_ratio=decrease:flags=lanczos,pad=${CW}:${CH}:(ow-iw)/2:(oh-ih)/2:color=${PAD_COLOR},setsar=1[v];"
  local labels="" n=0 i
  for i in "${!wavs[@]}"; do
    [[ -z "${wavs[$i]}" ]] && continue
    n=$((n+1)); inputs+=(-i "${wavs[$i]}")
    local ms; ms="$(awk -v s="${newsecs[$i]}" 'BEGIN{printf "%d", s*1000}')"
    fc+="[$n]adelay=${ms}|${ms}[a$n];"; labels+="[a$n]"
  done
  [[ $n -gt 0 ]] || { echo "✗ [$lang] no narration clips." >&2; rm -rf "$tmp"; return 1; }
  fc+="${labels}amix=inputs=$n:normalize=0[a]"

  ffmpeg -y -loglevel error -i "$gif" "${inputs[@]}" \
    -filter_complex "$fc" -map "[v]" -map "[a]" \
    -pix_fmt yuv420p -movflags +faststart -c:a aac "$out"
  rm -rf "$tmp"
  ok "[$lang] wrote $out"
}

for lang in "${LANGS[@]}"; do mux_lang "$lang"; done
echo; ok "Narrated videos complete in $OUT_DIR."
