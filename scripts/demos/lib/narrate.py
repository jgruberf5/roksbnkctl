#!/usr/bin/env python3
# narrate.py <model> <out.wav> <silence_sec>   (narration text on stdin)
#
# Synthesizes narration with a guaranteed pause between sentences. Piper only
# inserts inter-sentence silence when it DETECTS a sentence boundary, and our
# phonetic respellings ("… IBM Cloud. rocks BNK cuddle …") put a lowercase word
# after the period, which Piper doesn't treat as a new sentence — so its
# --sentence-silence is a no-op there. Instead we split on sentence punctuation
# ourselves, synth each piece, and concatenate them with real silence between.
import os, re, subprocess, sys, tempfile, wave

model, out, sil = sys.argv[1], sys.argv[2], float(sys.argv[3])
text = sys.stdin.read().strip()
piper = os.environ.get("PIPER_BIN", "piper")

# Split after . ! ? when followed by whitespace — leaves "24.04" / "v1.17.5"
# (no space after the dot) intact.
parts = [p.strip() for p in re.split(r"(?<=[.!?])\s+", text) if p.strip()]
if not parts:
    parts = [text]

tmp = tempfile.mkdtemp()
segs = []
for i, p in enumerate(parts):
    w = os.path.join(tmp, f"{i}.wav")
    subprocess.run([piper, "-m", model, "-f", w], input=p + "\n", text=True,
                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    if os.path.exists(w) and os.path.getsize(w) > 44:
        segs.append(w)

if not segs:
    sys.exit("narrate: piper produced no audio")

ow = wave.open(out, "wb")
params = None
for i, w in enumerate(segs):
    rw = wave.open(w, "rb")
    if params is None:
        params = rw.getparams()
        ow.setparams(params)
    if i > 0 and sil > 0:                       # real silence between sentences
        n = int(params.framerate * sil)
        ow.writeframes(b"\x00" * (n * params.sampwidth * params.nchannels))
    ow.writeframes(rw.readframes(rw.getnframes()))
    rw.close()
ow.close()
