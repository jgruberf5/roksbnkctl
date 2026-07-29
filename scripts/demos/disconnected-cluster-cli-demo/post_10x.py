#!/usr/bin/env python3
# Speed the LONG windows (from the demo's begin_long/end_long timestamps) to 10x,
# leaving everything else at 1x. Args: <raw.mkv> <timestamps> <rec_start_epoch> <out.mp4>
import sys, subprocess, os

raw, ts_file, rec_start, out = sys.argv[1], sys.argv[2], float(sys.argv[3]), sys.argv[4]
SPEED = 10
TMP = "/tmp/demo10x"; os.makedirs(TMP, exist_ok=True)

# Collect [start,end] LONG windows as offsets from the recording start.
longs, cur = [], None
for line in open(ts_file):
    p = line.split()
    if len(p) != 3:
        continue
    label, ev, epoch = p[0], p[1], float(p[2])
    if label == "LONG" and ev == "START":
        cur = epoch - rec_start
    elif label == "LONG" and ev == "END" and cur is not None:
        longs.append((max(cur, 0.0), epoch - rec_start)); cur = None
longs.sort()

dur = float(subprocess.check_output(
    ["ffprobe", "-v", "error", "-show_entries", "format=duration",
     "-of", "default=nk=1:nw=1", raw]).strip())

# Build ordered segments: (start, end, speed).
segs, t = [], 0.0
for s, e in longs:
    s = min(max(s, 0.0), dur); e = min(max(e, s), dur)
    if s > t:
        segs.append((t, s, 1))
    if e > s:
        segs.append((s, e, SPEED))
    t = e
if t < dur:
    segs.append((t, dur, 1))

print(f"duration={dur:.1f}s, {len(longs)} long windows -> {len(segs)} segments")
parts = []
for i, (s, e, sp) in enumerate(segs):
    pf = f"{TMP}/seg{i:02d}.mp4"
    vf = f"setpts=PTS/{sp}" if sp != 1 else "setpts=PTS"
    print(f"  seg {i}: {s:.1f}-{e:.1f}s x{sp}")
    subprocess.run(
        ["ffmpeg", "-y", "-v", "error", "-i", raw, "-ss", f"{s:.3f}", "-to", f"{e:.3f}",
         "-filter:v", vf, "-an", "-c:v", "libx264", "-preset", "veryfast",
         "-crf", "24", "-pix_fmt", "yuv420p", "-r", "10", pf], check=True)
    parts.append(pf)

listf = f"{TMP}/concat.txt"
with open(listf, "w") as f:
    for p in parts:
        f.write(f"file '{p}'\n")
subprocess.run(["ffmpeg", "-y", "-v", "error", "-f", "concat", "-safe", "0",
                "-i", listf, "-c", "copy", out], check=True)
print("wrote", out, "(", int(os.path.getsize(out) / 1e6), "MB )")
