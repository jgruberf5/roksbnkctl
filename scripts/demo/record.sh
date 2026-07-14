#!/usr/bin/env bash
#
# record.sh — capture the roksbnkctl demo as an asciinema .cast.
#
# Runs from the control host. It copies the screenplay + demo.env to the VSI and
# launches `asciinema rec -c ./<screenplay>` inside a tmux session (so a long shoot
# survives SSH disconnects), polls for completion, then pulls the raw .cast back
# into ./out/. The raw cast is the MASTER — postprocess.sh derives the mp4 from
# it, so re-cuts never need another cloud run.
#
#   ./record.sh            # provision (if needed) → record → fetch  [end-to-end]
#   ./record.sh start      # copy files + kick off the tmux recording, return
#   ./record.sh wait       # block until the recording finishes
#   ./record.sh fetch      # pull the .cast back to ./out/
#   ./record.sh attach     # attach to the live tmux session to watch
#   ./record.sh teardown   # run the screenplay's teardown on the VSI
#
# Reads the VSI address from vsi-state.env (provision-vsi.sh) and the SSH key
# from demo.env.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/demo.env}"
STATE_FILE="${STATE_FILE:-$SCRIPT_DIR/vsi-state.env}"
OUT_DIR="${OUT_DIR:-$SCRIPT_DIR/out}"

REMOTE_USER="${REMOTE_USER:-root}"   # IBM Cloud VPC stock images log in as root
REMOTE_DIR="${REMOTE_DIR:-/root/roksbnkctl-demo}"
REMOTE_CAST="$REMOTE_DIR/demo.cast"
TMUX_SES="roksdemo"
# Terminal geometry for the recording. ~16:9 at agg's default font, wide enough
# that roksbnkctl/terraform output doesn't wrap. Scaled/padded to 1080p in post.
REC_COLS="${REC_COLS:-160}"
REC_ROWS="${REC_ROWS:-50}"
# Which screenplay to record:
#   cli-demo.sh              demo #1 — the roksbnkctl CLI lifecycle (default)
#   ci-demo.sh               demo #2 — the tools-runner container as a CI pipeline
#   far-replication-demo.sh  demo #3 — replicating FAR into a private registry
#   flp-licensed-demo.sh     demo #4 — a shared licensing cluster: the F5 License
#                            Proxy in one cluster licenses BNK in another, which
#                            installs entirely from a private registry
#   ci-flp-demo.sh           demo #5 — demo #4 as a CI PIPELINE: every step is a
#                            docker run of the tools-runner image, the workspace
#                            comes from env vars, and the proxy handoff between the
#                            two jobs is two of them. Nothing installed on the host.
SCREENPLAY="${SCREENPLAY:-cli-demo.sh}"
POLL_SECS="${POLL_SECS:-30}"
MAX_WAIT_SECS="${MAX_WAIT_SECS:-14400}"   # 4h ceiling

# shellcheck disable=SC1090
[[ -f "$ENV_FILE" ]] && source "$ENV_FILE" || { echo "✗ $ENV_FILE not found — run ./prompt-inputs.sh first." >&2; exit 1; }

log() { printf '\033[1;36m▶ %s\033[0m\n' "$*"; }
ok()  { printf '\033[1;32m✓ %s\033[0m\n' "$*"; }

ensure_vsi() {
  if [[ ! -f "$STATE_FILE" ]]; then
    log "No VSI yet — provisioning one…"
    "$SCRIPT_DIR/provision-vsi.sh" up
  fi
  # shellcheck disable=SC1090
  source "$STATE_FILE"
  : "${FIP_ADDR:?no floating IP in $STATE_FILE — re-run provision-vsi.sh up}"
  # provision-vsi.sh records the managed key it baked into the VSI.
  local key="${VSI_SSH_KEY:-${SSH_KEY_FILE:-}}"
  : "${key:?no VSI SSH key recorded in $STATE_FILE — re-run provision-vsi.sh up}"
  # WSL DrvFs (/mnt/*) forces 0777, which SSH refuses; mirror onto the Linux fs.
  if [[ "$(stat -c '%a' "$key" 2>/dev/null)" != "600" ]]; then
    install -d -m700 "$HOME/.roksbnkctl-demo"
    install -m600 "$key" "$HOME/.roksbnkctl-demo/vsi_key"
    key="$HOME/.roksbnkctl-demo/vsi_key"
  fi
  SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
            -o ServerAliveInterval=30 -o ServerAliveCountMax=6 -i "$key")
}

sshv()  { ssh "${SSH_OPTS[@]}" "$REMOTE_USER@$FIP_ADDR" "$@"; }
scpto() { scp "${SSH_OPTS[@]}" "$@" "$REMOTE_USER@$FIP_ADDR:$REMOTE_DIR/"; }

do_start() {
  ensure_vsi
  log "Preparing remote workspace on $FIP_ADDR…"
  sshv "mkdir -p '$REMOTE_DIR'"

  # SRC_BUILD=1 → build roksbnkctl from this repo (linux/amd64) and ship it as
  # roksbnkctl.bin so the screenplay installs the SOURCE build. Use this to e2e-test an
  # unreleased change (e.g. the bnkforge REST fix). Unset → demo installs the
  # latest release binary.
  local extra=()
  if [[ "${SRC_BUILD:-0}" == "1" ]]; then
    local repo; repo="$(cd "$SCRIPT_DIR/../.." && pwd)"
    log "SRC_BUILD=1 — building roksbnkctl from source (linux/amd64)…"
    ( cd "$repo" && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$SCRIPT_DIR/roksbnkctl.bin" ./cmd/roksbnkctl ) \
      || { echo "✗ source build failed" >&2; exit 1; }
    ok "built $(du -h "$SCRIPT_DIR/roksbnkctl.bin" | cut -f1) binary from $(cd "$repo" && git rev-parse --abbrev-ref HEAD)"
    extra+=("$SCRIPT_DIR/roksbnkctl.bin")
  else
    # No source build → make sure a stale roksbnkctl.bin from a prior SRC_BUILD
    # run doesn't linger (the screenplay would install it instead of the release).
    sshv "rm -f '$REMOTE_DIR/roksbnkctl.bin'" 2>/dev/null || true
  fi

  # far-replication-demo.sh targets a registry built off-camera (its address +
  # admin password come from demo.env), so it ships no extra files.
  scpto "$SCRIPT_DIR/$SCREENPLAY" "$SCRIPT_DIR/demo-lib.sh" "$SCRIPT_DIR/forge-register.sh" "$ENV_FILE" "${extra[@]}"
  sshv "chmod +x '$REMOTE_DIR/$SCREENPLAY' '$REMOTE_DIR/forge-register.sh'"
  [[ "${SRC_BUILD:-0}" == "1" ]] && sshv "chmod +x '$REMOTE_DIR/roksbnkctl.bin'"

  log "Ensuring asciinema + tmux on the VSI…"
  # runs as root on the VSI; prefix sudo only if REMOTE_USER was overridden to
  # non-root. Use `env` for DEBIAN_FRONTEND so an empty $S doesn't turn the
  # assignment into the command word.
  sshv 'S=""; [ "$(id -u)" -eq 0 ] || S=sudo; command -v asciinema >/dev/null && command -v tmux >/dev/null || { $S apt-get update -qq && $S env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq asciinema tmux; }'

  log "Launching recording in tmux session '$TMUX_SES'…"
  sshv "tmux has-session -t '$TMUX_SES' 2>/dev/null && tmux kill-session -t '$TMUX_SES' || true"
  # Record the screenplay; stamp exit code + a DONE sentinel the poller watches for.
  # window-size manual BEFORE the session so no attaching client can resize it;
  # -x/-y set the size; resize-window forces it belt-and-suspenders.
  sshv "tmux start-server; tmux set-option -g window-size manual 2>/dev/null || true; \
        rm -f '$REMOTE_DIR/DONE' '$REMOTE_DIR/exit_code' '$REMOTE_DIR/demo_exit'; \
        tmux new-session -d -x $REC_COLS -y $REC_ROWS -s '$TMUX_SES' \
        \"asciinema rec --overwrite -c '$REMOTE_DIR/$SCREENPLAY' '$REMOTE_CAST'; \
          echo \\\$? > '$REMOTE_DIR/exit_code'; touch '$REMOTE_DIR/DONE'\"; \
        tmux resize-window -t '$TMUX_SES' -x $REC_COLS -y $REC_ROWS 2>/dev/null || true"

  # Verify the ACTUAL recorded width from the cast header (the source of truth
  # agg renders from) — fail loudly rather than discover an 80-col video later.
  log "Verifying recorded terminal width…"
  local hdr w i
  for i in 1 2 3 4 5; do
    hdr="$(sshv "head -1 '$REMOTE_CAST' 2>/dev/null" || true)"
    w="$(printf '%s' "$hdr" | sed -n 's/.*"width"[: ]*\([0-9]\{1,\}\).*/\1/p')"
    [[ -n "$w" ]] && break; sleep 1
  done
  if [[ "$w" == "$REC_COLS" ]]; then
    ok "Recorded width = ${w} cols (height ${REC_ROWS}). Don't 'attach' — it can resize mid-recording."
  else
    echo "✗ recorded width is '${w:-unknown}', expected ${REC_COLS}. The video would wrap at that width." >&2
    echo "  tmux sizing didn't take. Stop the run (tmux kill-session -t $TMUX_SES on the VSI) and retry, or check 'tmux -V' >= 3.0." >&2
  fi
}

do_wait() {
  ensure_vsi
  log "Waiting for the recording to finish (poll ${POLL_SECS}s, ceiling ${MAX_WAIT_SECS}s)…"
  local waited=0
  while ! sshv "test -f '$REMOTE_DIR/DONE'" 2>/dev/null; do
    if ! sshv "tmux has-session -t '$TMUX_SES' 2>/dev/null"; then
      # session gone without a DONE sentinel — surface it rather than spin
      sshv "test -f '$REMOTE_DIR/DONE'" 2>/dev/null || { echo "✗ tmux session ended without completing." >&2; exit 1; }
    fi
    sleep "$POLL_SECS"; waited=$((waited + POLL_SECS))
    [[ $waited -ge $MAX_WAIT_SECS ]] && { echo "✗ timed out after ${MAX_WAIT_SECS}s." >&2; exit 1; }
    printf '\033[2m  …still recording (%ds elapsed)\033[0m\n' "$waited"
  done
  # demo_exit holds the screenplay's REAL status (asciinema's own exit is always 0).
  local rc; rc="$(sshv "cat '$REMOTE_DIR/demo_exit' 2>/dev/null" || echo '?')"
  [[ "$rc" == "0" ]] && ok "$SCREENPLAY finished cleanly (exit 0)." || echo "⚠ $SCREENPLAY exited $rc — cast still captured; review it." >&2
}

do_fetch() {
  ensure_vsi
  mkdir -p "$OUT_DIR"
  local stamp dest
  stamp="$(date +%Y%m%d-%H%M%S)"
  dest="$OUT_DIR/roksbnkctl-demo-$stamp.cast"
  log "Fetching the cast → $dest"
  scp "${SSH_OPTS[@]}" "$REMOTE_USER@$FIP_ADDR:$REMOTE_CAST" "$dest"
  ln -sf "$(basename "$dest")" "$OUT_DIR/latest.cast"
  ok "Master cast saved: $dest  (also linked as out/latest.cast)"
  echo "Next: ./postprocess.sh $dest"
}

do_attach()   { ensure_vsi; exec ssh -t "${SSH_OPTS[@]}" "$REMOTE_USER@$FIP_ADDR" "tmux attach -t '$TMUX_SES'"; }
# Falls back to demo.sh: a shoot started before the cli-demo.sh rename still has
# the old filename sitting in $REMOTE_DIR on the VSI.
do_teardown() {
  ensure_vsi
  sshv "cd '$REMOTE_DIR' && if [ -x ./$SCREENPLAY ]; then ./$SCREENPLAY teardown; elif [ -x ./demo.sh ]; then ./demo.sh teardown; else echo 'no screenplay on the VSI' >&2; exit 1; fi"
}

case "${1:-run}" in
  run)      do_start; do_wait; do_fetch ;;
  start)    do_start ;;
  wait)     do_wait ;;
  fetch)    do_fetch ;;
  attach)   do_attach ;;
  teardown) do_teardown ;;
  *)        echo "usage: $0 {run|start|wait|fetch|attach|teardown}" >&2; exit 2 ;;
esac
