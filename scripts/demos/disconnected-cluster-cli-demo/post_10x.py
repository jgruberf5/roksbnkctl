#!/usr/bin/env python3
# Post-process the raw demo recording:
#   - speed the LONG windows (from the demo's begin_long/end_long timestamps) to 10x
#   - HOLD each roksbnkctl command frame (FREEZE markers) on screen for 5 seconds,
#     so the audience clearly sees the roksbnkctl command being used
# Args: <raw.mkv> <timestamps> <rec_start_epoch> <out.mp4>
import sys, subprocess, os

raw, ts_file, rec_start, out = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
SPEED = 10
FREEZE_SECS = 5
TMP = "/tmp/demo10x"
os.makedirs(TMP, exist_ok=True)

# Parse LONG windows and FREEZE points, as offsets from the recording start.
longs, freezes, cur = [], [], None
for line in open(ts_file):
    p = line.split()
    if len(p) != 3:
        continue
    label, ev, epoch = p[0], p[1], float(p[2])
    off = epoch - rec_start
    if label == "LONG" and ev == "START":
        cur = off
    elif label == "LONG" and ev == "END" and cur is not None:
        longs.append((max(cur, 0.0), off)); cur = None
    elif label == "FREEZE":
        freezes.append(max(off, 0.0))
longs.sort()

dur = float(subprocess.check_output(
    ["ffprobe", "-v", "error", "-show_entries", "format=duration",
     "-of", "default=nk=1:nw=1", raw]).strip())

def speed_at(t):
    for s, e in longs:
        if s <= t < e:
            return SPEED
    return 1

ENC = ["-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "24",
       "-pix_fmt", "yuv420p", "-r", "10"]

# Cut the timeline at every LONG boundary AND every freeze point; a 5s still of the
# frame at each freeze point is inserted right after the segment that ends on it.
freezes = sorted(min(f, dur) for f in freezes)
freeze_set = {round(f, 3) for f in freezes}
cuts = sorted({0.0, dur} | {s for s, e in longs} | {e for s, e in longs} | set(freezes))

parts = []
i = 0
for a, b in zip(cuts, cuts[1:]):
    if b > a:
        sp = speed_at((a + b) / 2.0)
        vf = f"setpts=PTS/{sp}" if sp != 1 else "setpts=PTS"
        pf = f"{TMP}/seg{i:03d}.mp4"
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-i", raw, "-ss", f"{a:.3f}",
                        "-to", f"{b:.3f}", "-filter:v", vf] + ENC + [pf], check=True)
        parts.append(pf); i += 1
    if round(b, 3) in freeze_set and b < dur:
        png = f"{TMP}/frz{i:03d}.png"
        mf = f"{TMP}/frz{i:03d}.mp4"
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-ss", f"{b:.3f}", "-i", raw,
                        "-frames:v", "1", png], check=True)
        subprocess.run(["ffmpeg", "-y", "-v", "error", "-loop", "1",
                        "-t", str(FREEZE_SECS), "-i", png] + ENC + [mf], check=True)
        parts.append(mf); i += 1

listf = f"{TMP}/concat.txt"
with open(listf, "w") as f:
    for p in parts:
        f.write(f"file '{p}'\n")
subprocess.run(["ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0",
                "-i", listf, "-c", "copy", out], check=True)
print(f"wrote {out} ({int(os.path.getsize(out) / 1e6)} MB) — "
      f"{len(longs)} long windows x{SPEED}, {len(freezes)} command freezes x{FREEZE_SECS}s")
