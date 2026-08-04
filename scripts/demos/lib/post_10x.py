#!/usr/bin/env python3
# Build the final cut from a RAW recording + its QUEUE file. The queue records every
# event with its exact wall-clock time, so the whole timeline is reworkable by just
# RE-RUNNING this script on the same raw — no re-recording to change delays/speeds.
#
# Queue labels (written by the demo's ts()):
#   PHASE  MARK <epoch>   → hold the phase banner (a clean screen) as a still, PHASE_SECS
#   COMMAND MARK <epoch>  → hold the roksbnkctl command as a still, CMD_SECS
#   OUTPUT MARK <epoch>   → hold the command's settled output as a still, OUT_SECS
#   LONG   START/END      → speed that window SPEED× (the only sped part)
#
# Stills are cut from the RAW at each mark's timestamp, so they show the exact frame
# that was on screen even when the mark sits inside a 10× window. Each roksbnkctl
# command therefore reads as:  [CMD_SECS still: command] → [10× output] → [OUT_SECS still: output].
#
# Args: <raw.mkv> <queue> <rec_start_epoch> <out.mp4>
# Env : SPEED (10), PHASE_SECS (4), CMD_SECS (5), OUT_SECS (5)
import sys, subprocess, os

raw, qfile, rec_start, out = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
SPEED      = int(os.environ.get("SPEED", "10"))
STILL_SECS = {
    "PHASE":   float(os.environ.get("PHASE_SECS", "4")),
    "COMMAND": float(os.environ.get("CMD_SECS", "5")),
    "OUTPUT":  float(os.environ.get("OUT_SECS", "5")),
}
TMP = "/tmp/demo10x"
os.makedirs(TMP, exist_ok=True)

dur = float(subprocess.check_output(
    ["ffprobe", "-v", "error", "-show_entries", "format=duration",
     "-of", "default=nk=1:nw=1", raw]).strip())

# Parse the queue into long (10×) windows and stills, as offsets from the rec start.
longs, stills, counts, cur = [], [], {"PHASE": 0, "COMMAND": 0, "OUTPUT": 0}, None
for line in open(qfile):
    p = line.split()
    if len(p) < 3:
        continue
    label, ev, epoch = p[0], p[1], float(p[2])
    off = min(max(epoch - rec_start, 0.0), dur)
    if label == "LONG" and ev == "START":
        cur = off
    elif label == "LONG" and ev == "END" and cur is not None:
        longs.append((cur, off)); cur = None
    elif label in STILL_SECS:
        stills.append((off, STILL_SECS[label])); counts[label] += 1
longs.sort()
stills.sort()
# offset → still seconds (last write wins on an exact collision; offsets are distinct)
still_at = {round(o, 3): s for o, s in stills}

def speed_at(t):
    for s, e in longs:
        if s <= t < e:
            return SPEED
    return 1

ENC = ["-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "24",
       "-pix_fmt", "yuv420p", "-r", "10"]

# Cut the raw at every long-window boundary and every still point; play each raw
# segment at its speed, and after a segment that ends on a still point, splice in the
# held still of the frame at that point.
cuts = sorted({0.0, dur} | {s for s, e in longs} | {e for s, e in longs} | {o for o, _ in stills})
parts, i = [], 0
for a, b in zip(cuts, cuts[1:]):
    if b > a:
        sp = speed_at((a + b) / 2.0)
        vf = f"setpts=PTS/{sp}" if sp != 1 else "setpts=PTS"
        pf = f"{TMP}/seg{i:03d}.mp4"
        # -ss/-to MUST precede -i (input-side trim). After -i they trim on the OUTPUT
        # timeline, which is post-setpts — so a 10x window only compressed ~2.5x and the
        # "sped" sections were still long dead screen. Input-side trim gives a true 10x.
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-ss", f"{a:.3f}", "-to", f"{b:.3f}",
                        "-i", raw, "-filter:v", vf] + ENC + [pf], check=True)
        parts.append(pf); i += 1
    key = round(b, 3)
    if key in still_at and b < dur:
        secs = still_at[key]
        png = f"{TMP}/still{i:03d}.png"; mf = f"{TMP}/still{i:03d}.mp4"
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-ss", f"{b:.3f}", "-i", raw,
                        "-frames:v", "1", png], check=True)
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-loop", "1",
                        "-t", str(secs), "-i", png] + ENC + [mf], check=True)
        parts.append(mf); i += 1

listf = f"{TMP}/concat.txt"
with open(listf, "w") as f:
    for p in parts:
        f.write(f"file '{p}'\n")
# Re-encode the concatenation (not -c copy) so the final has clean, monotonic PTS —
# a -c copy concat of independently-encoded parts yields a file that scrubs/seeks
# unreliably. Re-encoding is a little slower but gives a properly seekable MP4.
subprocess.run(["ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0",
                "-i", listf, "-c:v", "libx264", "-preset", "veryfast", "-crf", "24",
                "-pix_fmt", "yuv420p", "-r", "10", "-movflags", "+faststart", out], check=True)
print(f"wrote {out} ({int(os.path.getsize(out) / 1e6)} MB) — "
      f"{len(longs)} long×{SPEED}, "
      f"{counts['PHASE']} banners×{STILL_SECS['PHASE']}s, "
      f"{counts['COMMAND']} commands×{STILL_SECS['COMMAND']}s, "
      f"{counts['OUTPUT']} outputs×{STILL_SECS['OUTPUT']}s")
