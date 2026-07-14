#!/usr/bin/env bash
#
# postprocess.sh — turn a raw asciinema .cast (the master) into a watchable,
# time-compressed silent mp4, and emit the chapter timestamps the voiceover
# step aligns to.
#
#   ./postprocess.sh out/roksbnkctl-demo-<stamp>.cast
#   ./postprocess.sh                     # defaults to out/latest.cast
#
# Produces, next to the input:
#   <base>.compressed.cast   idle gaps capped at MAX_IDLE seconds
#   <base>.chapters.tsv      <seconds>\t<index>\t<label>  (compressed timeline)
#   <base>.gif               rendered terminal animation (via agg)
#   <base>.silent.mp4        the gif as mp4 (via ffmpeg) — pre-voiceover master
#
# The raw .cast is never modified — re-run with a different MAX_IDLE any time.
# Tunables (env): MAX_IDLE (default 2.5), FONT_SIZE (14), THEME (asciinema), FPS (30).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/out}"

MAX_IDLE="${MAX_IDLE:-2.5}"
# Timeline shaping (see the Python below): SPEED time-compresses console OUTPUT
# between phases (<1 = faster); CMD_SLOW stretches the human-typing of commands
# (>1 = slower/more readable). Stage cards and the read-the-command pause stay at
# normal speed. Tune per taste; the raw .cast is never modified.
SPEED="${SPEED:-0.25}"
CMD_SLOW="${CMD_SLOW:-1.8}"
# DWELL: minimum seconds a command's finished output lingers before the next
# stage card clears the screen — otherwise the tail (e.g. `doctor` output) gets
# clipped, especially once output is sped up.
DWELL="${DWELL:-2.2}"
FONT_SIZE="${FONT_SIZE:-20}"     # bigger source → sharper after fitting to 1080p
THEME="${THEME:-asciinema}"
FPS="${FPS:-30}"
RES="${RES:-1920x1080}"          # final 16:9 canvas; gif is fit + letterboxed into it
PAD_COLOR="${PAD_COLOR:-0x1e1e1e}"

IN="${1:-$OUT_DIR/latest.cast}"
[[ -f "$IN" ]] || { echo "✗ cast not found: $IN" >&2; exit 1; }
IN="$(cd "$(dirname "$IN")" && pwd)/$(basename "$IN")"   # absolutise (follow symlink dir)
BASE="${IN%.cast}"

COMPRESSED="$BASE.compressed.cast"
CHAPTERS="$BASE.chapters.tsv"
GIF="$BASE.gif"
MP4="$BASE.silent.mp4"

log() { printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()  { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }

# ── 1) idle-compress + collapse progress spam + chapter extraction ────────────
log "Compressing idle gaps (cap ${MAX_IDLE}s), collapsing progress spam, extracting chapters…"
MAX_IDLE="$MAX_IDLE" SPEED="$SPEED" CMD_SLOW="$CMD_SLOW" DWELL="$DWELL" COMPRESSED="$COMPRESSED" CHAPTERS="$CHAPTERS" COLLAPSE="${COLLAPSE_PROGRESS:-1}" python3 - "$IN" <<'PY'
import json, os, re, sys

src = sys.argv[1]
cap = float(os.environ["MAX_IDLE"])
speed = float(os.environ.get("SPEED", "1.0"))       # console output: <1 = faster
cmd_slow = float(os.environ.get("CMD_SLOW", "1.0"))  # command typing: >1 = slower
dwell = float(os.environ.get("DWELL", "0.0"))        # linger before a screen clear
out_cast = os.environ["COMPRESSED"]
out_chap = os.environ["CHAPTERS"]
collapse = os.environ.get("COLLAPSE", "1") == "1"

ansi = re.compile(r"\x1b\[[0-9;]*[A-Za-z]")
# Stage-card marker printed by demo-lib.sh: "  STAGE n/N · <title>".
banner = re.compile(r"STAGE\s+(\S+)\s*·\s*(.+)")
# The closing line each screenplay prints AFTER the final `down` completes — its
# own chapter so the "back where we started" narration lands at the very end,
# not over the down output.
ending = re.compile(r"Back where we started")
# terraform/helm progress spam printed every ~10s: keep the first + last of each
# run so the wait is visible, drop the middle (and their elapsed time).
progress = re.compile(r"\[\d+m\d+s elapsed\]|Still (creating|destroying|modifying|reading)\.\.\.")
PROMPT = "❯"        # ❯ — a typed command begins (demo-lib.sh type_cmd)
RULE = "─" * 4      # ──── — the dim rule that ends a typed command

hdr_f = open(src, encoding="utf-8")
header = hdr_f.readline()
if not header.strip():
    sys.exit("empty cast")
hdr = json.loads(header)

new_t = 0.0
prev_orig = 0.0
chapters = []
idx = 0
collapsed = 0
in_run = False
pending = None    # buffered last progress event of the current run
in_cmd = False    # between ❯ and the dim rule — the human-typed command
read_pause = False  # the single event after a command: the read-it pause

w = open(out_cast, "w", encoding="utf-8")
w.write(json.dumps(hdr) + "\n")

ending_done = False

def emit(code, data):
    global idx, ending_done
    w.write(json.dumps([round(new_t, 6), code, data]) + "\n")
    if code == "o":
        s = ansi.sub("", data)
        m = banner.search(s)
        if m:
            idx += 1
            chapters.append((round(new_t, 3), idx, m.group(2).strip()))
        elif not ending_done and ending.search(s):
            ending_done = True
            idx += 1
            chapters.append((round(new_t, 3), idx, "Back where we started"))

for line in hdr_f:
    line = line.strip()
    if not line:
        continue
    ev = json.loads(line)
    t, code, data = ev[0], ev[1], ev[2]
    raw = t - prev_orig
    prev_orig = t
    stripped = ansi.sub("", data) if code == "o" else ""

    is_stage = code == "o" and bool(banner.search(stripped))
    starts_cmd = code == "o" and PROMPT in stripped
    ends_cmd = in_cmd and code == "o" and RULE in stripped
    is_clear = code == "o" and "2J" in data   # \x1b[2J screen clear (stage card)

    # Per-event compressed duration by mode: input (commands) stays normal or
    # slower and readable; console output between phases is sped up.
    if starts_cmd:
        in_cmd = True
        d = min(raw, cap)              # gap before the command — normal
    elif in_cmd:
        d = raw * cmd_slow             # typing keystrokes — stretch for readability
    elif read_pause:
        d = min(raw, cap)              # pause to read the command — normal
    elif is_stage:
        d = min(raw, cap)              # stage-card hold — normal
    else:
        d = min(raw, cap) * speed      # console output — fast

    if is_clear:
        d = max(d, dwell)              # let finished output linger before the wipe

    is_prog = collapse and code == "o" and not in_cmd and bool(progress.search(stripped))
    if is_prog:
        if not in_run:                 # first line of a run — show it
            new_t += d
            emit(code, data)
            in_run = True
            pending = None
        else:                          # middle — buffer as last, drop its time
            pending = (code, data)
            collapsed += 1
        read_pause = False
        continue
    if in_run:                         # run ended — flush its final line
        if pending is not None:
            new_t += min(raw, 1.0) * speed
            emit(pending[0], pending[1])
        in_run = False
        pending = None

    new_t += d
    emit(code, data)

    read_pause = ends_cmd              # next event is the read-the-command pause
    if ends_cmd:
        in_cmd = False

if in_run and pending is not None:
    new_t += 1.0 * speed
    emit(pending[0], pending[1])
w.close()
hdr_f.close()

with open(out_chap, "w", encoding="utf-8") as c:
    for secs, i, label in chapters:
        c.write(f"{secs}\t{i}\t{label}\n")

print(f"  compressed length: {new_t:.1f}s  ({new_t/60:.1f} min), {len(chapters)} chapters, collapsed {collapsed} progress lines")
PY
ok "Wrote $COMPRESSED"
ok "Wrote $CHAPTERS"
[[ -s "$CHAPTERS" ]] && { echo "── chapters ──"; cat "$CHAPTERS"; } || echo "⚠ no chapter banners found — check the screenplay's stage-card format."

# ── 2) render gif (agg) ──────────────────────────────────────────────────────
if command -v agg >/dev/null 2>&1; then
  log "Rendering GIF with agg…"
  agg --font-size "$FONT_SIZE" --theme "$THEME" --fps-cap "$FPS" "$COMPRESSED" "$GIF"
  ok "Wrote $GIF"
else
  echo "⚠ 'agg' not found — skipping render. Install: https://github.com/asciinema/agg (cargo install --git … or a release binary)."
  echo "  Compressed cast + chapters are ready for voiceover alignment regardless."
  exit 0
fi

# ── 3) gif → mp4 (ffmpeg) ────────────────────────────────────────────────────
if command -v ffmpeg >/dev/null 2>&1; then
  log "Encoding ${RES} 16:9 mp4 with ffmpeg…"
  local_w="${RES%x*}"; local_h="${RES#*x}"
  ffmpeg -y -loglevel error -i "$GIF" \
    -movflags +faststart -pix_fmt yuv420p \
    -vf "scale=${local_w}:${local_h}:force_original_aspect_ratio=decrease:flags=lanczos,pad=${local_w}:${local_h}:(ow-iw)/2:(oh-ih)/2:color=${PAD_COLOR},setsar=1" \
    "$MP4"
  ok "Wrote $MP4 (silent, ${RES} — preview; narrated videos come from voiceover.sh)"
else
  echo "⚠ 'ffmpeg' not found — GIF is ready ($GIF) but no mp4 produced. Install ffmpeg to encode."
fi

echo
ok "Post-processing done."
echo "Next (narrated, serialized audio): ./voiceover.sh $COMPRESSED $CHAPTERS"
