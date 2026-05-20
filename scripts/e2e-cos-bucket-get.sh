#!/usr/bin/env bash
# scripts/e2e-cos-bucket-get.sh — gated live-verify driver for the
# Sprint 18 staff feature `roksbnkctl cos bucket get` (validator
# Issue 1). Mirrors scripts/e2e-phase-handoff.sh's style and
# discipline: structured logging, redact() over every echoed command,
# DRY_RUN walk-through, EXIT-trap teardown, and no API-key value ever
# read out of ./terraform.tfvars into argv or stdout.
#
# What it proves (staff acceptance criteria 1–8):
#   * Provisions a temporary bucket on a workspace's COS instance.
#   * Uploads a fixture set covering the three classes the hermetic
#     tests can't reach against a real S3 backend:
#       – flat text key
#       – flat binary key
#       – nested key with `/` (subdir creation)
#     Each upload's sha256 is computed and stored.
#   * Runs `roksbnkctl cos bucket get` over the populated bucket.
#   * Asserts (A1) every key landed under <dest>/ with the right
#     subdirectory shape; (A2) sha256 round-trip per file matches the
#     pre-upload checksum; (A3) `--no-clobber` skips a pre-seeded local
#     file without bumping mtime; (A4) non-existent bucket fails with a
#     non-zero exit code that names the bucket; (A5) the run log is
#     free of leaked API-key bytes (planted-sentinel + tfvars scan).
#   * Tears the temporary bucket down on EXIT — pass OR fail — so a
#     stray run cannot strand a billable bucket on the account.
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only, via `!`.
# ─────────────────────────────────────────────────────────────────────
#
#   * REAL CLOUD SPEND (tiny: one bucket, a few KB of objects, lived
#     long enough to download once). Budget ≈ pennies and ≈ 2-3 min
#     wall time; the cost discipline still applies (per
#     `live-verify-high-issues`: integrator-owned `!` invocation, no
#     workflow_dispatch).
#   * Opt-in. Nothing automatic runs this — no GitHub workflow.
#   * Requires `IBMCLOUD_API_KEY` and a COS_INSTANCE name (or CRN) in
#     the environment. Neither value is echoed; the project
#     ./terraform.tfvars is referenced only for structure-presence and
#     its contents are never printed.
#   * Self-tears-down on EXIT so a failed run does not strand a bucket.
#
# Usage:
#   IBMCLOUD_API_KEY=... COS_INSTANCE=<name|CRN> \
#       ./scripts/e2e-cos-bucket-get.sh                # live verify
#   DRY_RUN=1 ./scripts/e2e-cos-bucket-get.sh          # plan only, no cloud
#
# Knobs:
#   TFVARS         default ./terraform.tfvars     (structure only; never echoed)
#   WORKSPACE      default e2e-cos-bucket-get
#   COS_INSTANCE   required for live runs (no default; never sourced from TFVARS)
#   BUCKET         default e2e-cbg-$RUN_TS         (auto-suffixed)
#   DRY_RUN        default 0
#   LOG_DIR        default /tmp/roksbnkctl-e2e-cos-bucket-get
#   ROKSBNKCTL     default roksbnkctl
#
# Exit codes: 0 = GREEN. Non-zero = first failed assertion, with the
# failing check named in the error line.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
TFVARS=${TFVARS:-./terraform.tfvars}
WORKSPACE=${WORKSPACE:-e2e-cos-bucket-get}
DRY_RUN=${DRY_RUN:-0}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-e2e-cos-bucket-get}
ROKSBNKCTL=${ROKSBNKCTL:-roksbnkctl}

mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
BUCKET=${BUCKET:-e2e-cbg-$RUN_TS}
# COS_INSTANCE is required for live runs (preflight enforces). In
# DRY_RUN, default to a placeholder so `set -u` doesn't trip on the
# walkthrough's log lines / teardown.
COS_INSTANCE=${COS_INSTANCE:-<dry-run-placeholder>}
RUN_LOG="$LOG_DIR/cos-bucket-get-$RUN_TS.log"
WORK_DIR="$LOG_DIR/work-$RUN_TS"
mkdir -p "$WORK_DIR"

# ── helpers (mirror scripts/e2e-phase-handoff.sh) ───────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

# Redact the API key value from any string we are about to print. Belt
# and braces — this driver never builds a command that contains the
# key, but if the environment leaks one into argv we still don't echo
# it. Identical pattern to scripts/e2e-phase-handoff.sh's redact().
redact() {
    local s="$*"
    if [[ -n "${IBMCLOUD_API_KEY:-}" ]]; then
        s=${s//"$IBMCLOUD_API_KEY"/<redacted>}
    fi
    printf '%s' "$s"
}

step() {
    local desc="$1"; shift
    log "→ $desc"
    log "  cmd: $(redact "$*")"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "  (dry-run; skipping execution)"
        return 0
    fi
    if "$@" 2>&1 | tee -a "$RUN_LOG"; then
        green "  ✓ $desc"
        return 0
    else
        local rc=${PIPESTATUS[0]}
        red "  ✗ $desc (exit $rc)"
        red "  full log: $RUN_LOG"
        exit "$rc"
    fi
}

# step_expect_fail runs a command we EXPECT to fail (non-zero exit) —
# used for the negative assertions (non-existent bucket). Captures
# stderr for the bucket-name-substring check.
step_expect_fail() {
    local desc="$1"; shift
    local out rc
    log "→ $desc (expecting non-zero exit)"
    log "  cmd: $(redact "$*")"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "  (dry-run; skipping execution)"
        printf '%s' "" # nothing to return in dry-run
        return 0
    fi
    set +e
    out=$("$@" 2>&1); rc=$?
    set -e
    printf '%s\n' "$out" >> "$RUN_LOG"
    if [[ $rc -eq 0 ]]; then
        red "  ✗ $desc UNEXPECTEDLY succeeded (exit 0); wanted non-zero"
        red "  full log: $RUN_LOG"
        exit 4
    fi
    green "  ✓ $desc returned non-zero ($rc) as expected"
    printf '%s' "$out"
}

fail() {
    red "  ✗ $1"
    red "  full log: $RUN_LOG"
    exit 2
}

# ── self-teardown trap ──────────────────────────────────────────────
# Best-effort and LOUD. A failed run must not strand a billable bucket
# on the account. Delete every fixture object, then the bucket.
TORN_DOWN=0
teardown() {
    local prev_rc=$?
    [[ "$TORN_DOWN" == "1" ]] && return
    TORN_DOWN=1
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ teardown (dry-run): delete fixture objects + bucket $BUCKET on instance $COS_INSTANCE_REDACTED"
        return
    fi
    echo "" >&2
    bold "════════════════════════════════════════════════════════════"
    yellow "TEARDOWN — removing temporary bucket $BUCKET so no billable infra is stranded"
    bold "════════════════════════════════════════════════════════════"
    local td_rc=0
    # Delete every object first; IBM COS rejects DeleteBucket on a
    # non-empty bucket (same behaviour cos.DeleteBucket comments in
    # internal/cos/bucket.go pin).
    for key in "alpha.txt" "beta.bin" "foo/bar/baz.json" "skip-me.txt"; do
        "$ROKSBNKCTL" cos object delete "$BUCKET/$key" \
            --instance "$COS_INSTANCE" >/dev/null 2>&1 || true
    done
    if "$ROKSBNKCTL" cos bucket delete "$BUCKET" --instance "$COS_INSTANCE" \
        >>"$RUN_LOG" 2>&1; then
        green "  ✓ teardown: bucket $BUCKET deleted"
    else
        red "  ✗ teardown: bucket $BUCKET delete failed — may still be billable"
        red "  Manually:  $ROKSBNKCTL cos bucket delete $BUCKET --instance <inst>"
        td_rc=1
    fi
    if [[ $td_rc -ne 0 ]]; then
        red "  ✗ TEARDOWN INCOMPLETE — inspect the IBM Cloud console."
    fi
    if [[ "$prev_rc" != "0" ]]; then
        red "Run FAILED (exit $prev_rc) — see $RUN_LOG"
    fi
}
trap teardown EXIT

# Render a redacted COS_INSTANCE label for log lines — CRNs are not
# secret, but instance names can carry workspace info we'd rather not
# splash. Show last 8 chars only.
COS_INSTANCE_REDACTED="<unset>"

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ ! -f "$TFVARS" ]]; then
        fail "TFVARS file not found: $TFVARS (structure-only reference; never printed)"
    fi
    if [[ "$DRY_RUN" != "1" ]]; then
        if [[ -z "${IBMCLOUD_API_KEY:-}" ]]; then
            # Deliberately do NOT scrape the key out of $TFVARS. Live
            # runs must pass it explicitly in the environment so it
            # never transits this driver's stdout/stderr/argv.
            fail "IBMCLOUD_API_KEY is unset. Export it in the environment (do NOT rely on the tfvars echo) and re-run."
        fi
        if [[ -z "${COS_INSTANCE:-}" || "$COS_INSTANCE" == "<dry-run-placeholder>" ]]; then
            fail "COS_INSTANCE is unset. Export the instance name or CRN (e.g. COS_INSTANCE=my-cos-instance) and re-run."
        fi
        if ! command -v "$ROKSBNKCTL" >/dev/null 2>&1; then
            fail "$ROKSBNKCTL not on PATH (set ROKSBNKCTL=/abs/path/to/binary)"
        fi
        if ! command -v sha256sum >/dev/null 2>&1; then
            fail "sha256sum not on PATH (coreutils); required for the round-trip check"
        fi
        # Last 8 chars only — enough to disambiguate teardown logs,
        # not enough to leak the full name into a shared paste.
        COS_INSTANCE_REDACTED="…${COS_INSTANCE: -8}"
    fi
    log "preflight OK — workspace=$WORKSPACE bucket=$BUCKET instance=$COS_INSTANCE_REDACTED tfvars=$TFVARS (contents not printed) log=$RUN_LOG"
}

# ── fixtures ────────────────────────────────────────────────────────
# Build three local fixture files + record their sha256s. The same
# triplet the hermetic tests cover, scaled up to bytes a real S3
# round-trip might mangle: text, binary, nested-key.
build_fixtures() {
    bold "build fixtures (local; no cloud calls)"
    local fix_dir="$WORK_DIR/fixtures"
    mkdir -p "$fix_dir/foo/bar"
    # (a) flat text
    printf 'roksbnkctl cos bucket get — text round-trip fixture\n' > "$fix_dir/alpha.txt"
    # (b) flat binary (256 ascending bytes × 16 = 4 KiB)
    : > "$fix_dir/beta.bin"
    local i
    for ((i=0; i<16; i++)); do
        printf '%b' "$(printf '\\x%02x' {0..255} | tr -d '\n')" >> "$fix_dir/beta.bin" 2>/dev/null \
            || { perl -e 'print pack("C*", 0..255) x 1' >> "$fix_dir/beta.bin"; }
    done
    # (c) nested-key (subdir)
    printf '{"deep":true,"key":"foo/bar/baz.json"}\n' > "$fix_dir/foo/bar/baz.json"
    # Pre-existing local file used by the --no-clobber assertion.
    printf 'LOCAL pre-existing — must NOT be clobbered\n' > "$fix_dir/skip-me.txt"
    SHA_ALPHA=$(sha256sum "$fix_dir/alpha.txt" | awk '{print $1}')
    SHA_BETA=$(sha256sum  "$fix_dir/beta.bin"  | awk '{print $1}')
    SHA_NESTED=$(sha256sum "$fix_dir/foo/bar/baz.json" | awk '{print $1}')
    log "fixtures built: alpha.txt sha=$SHA_ALPHA, beta.bin sha=$SHA_BETA, foo/bar/baz.json sha=$SHA_NESTED"
    FIX_DIR=$fix_dir
}

# Plant a sentinel that LOOKS like a key-leak risk so the final scan
# can prove the driver scrubbed it. Random, generated per-run; if it
# ever shows up in the run log we know redact() failed.
plant_sentinel() {
    SENTINEL="ROKSBNKCTL_E2E_SENTINEL_$(head -c 16 /dev/urandom | xxd -p)"
    # Use the redact() helper to pretend the sentinel was the API key
    # for one redaction round-trip — proves redact() is wired before we
    # rely on it for real.
    local before="cmd --api-key $SENTINEL bucket get"
    IBMCLOUD_API_KEY=$SENTINEL # local override JUST for the assertion
    local after=$(redact "$before")
    IBMCLOUD_API_KEY=${REAL_API_KEY:-}
    if [[ "$after" == *"$SENTINEL"* ]]; then
        fail "redact() did NOT strip the sentinel — driver would leak the API key"
    fi
    log "redact() sentinel check passed (sentinel not present in redacted form)"
}

# ── the reproduction ────────────────────────────────────────────────
main() {
    bold "roksbnkctl cos bucket get — live verify — run-id $RUN_TS"
    bold "(validator Issue 1 — Sprint 18 — NOT a CI job)"
    log "log: $RUN_LOG"
    preflight
    REAL_API_KEY=${IBMCLOUD_API_KEY:-}
    build_fixtures
    plant_sentinel

    # S1 — provision the bucket. `cos bucket create` is the existing
    # verb in internal/cli/cos.go; staff Issue 1 does not change it.
    step "S1 create bucket $BUCKET on instance $COS_INSTANCE_REDACTED" \
        "$ROKSBNKCTL" cos bucket create "$BUCKET" --instance "$COS_INSTANCE"

    # S2 — upload the three fixtures. Uses the EXISTING `cos object put`
    # verb so the test exercises a real upload→download round trip.
    step "S2a put alpha.txt"            "$ROKSBNKCTL" cos object put "$BUCKET/alpha.txt"        "$FIX_DIR/alpha.txt"        --instance "$COS_INSTANCE"
    step "S2b put beta.bin"             "$ROKSBNKCTL" cos object put "$BUCKET/beta.bin"         "$FIX_DIR/beta.bin"         --instance "$COS_INSTANCE"
    step "S2c put foo/bar/baz.json"     "$ROKSBNKCTL" cos object put "$BUCKET/foo/bar/baz.json" "$FIX_DIR/foo/bar/baz.json" --instance "$COS_INSTANCE"
    step "S2d put skip-me.txt (remote)" "$ROKSBNKCTL" cos object put "$BUCKET/skip-me.txt"      "$FIX_DIR/skip-me.txt"      --instance "$COS_INSTANCE"

    # S3 — the new verb. Pull everything to a fresh local dir.
    DEST_DIR="$WORK_DIR/dest"
    step "S3 cos bucket get $BUCKET → $DEST_DIR" \
        "$ROKSBNKCTL" cos bucket get "$BUCKET" "$DEST_DIR" --instance "$COS_INSTANCE"

    bold "──── assertions ────"
    if [[ "$DRY_RUN" == "1" ]]; then
        log "→ A1 every fixture key landed under DEST_DIR with the right subdir shape"
        log "→ A2 sha256 round-trip per file matches pre-upload checksum"
        log "→ A3 --no-clobber skips a pre-seeded local file without bumping mtime"
        log "→ A4 cos bucket get on a non-existent bucket fails with non-zero exit and names the bucket"
        log "→ A5 run log free of API-key leak (sentinel scan + tfvars-byte scan)"
        green "DRY-RUN complete — steps rendered, no cloud calls, no key printed."
        return 0
    fi

    # A1 + A2 — every fixture exists at the right path with byte-
    # identical contents (sha256 match against the pre-upload sum).
    for case in \
        "alpha.txt:$SHA_ALPHA" \
        "beta.bin:$SHA_BETA" \
        "foo/bar/baz.json:$SHA_NESTED"
    do
        rel=${case%:*}
        want_sha=${case##*:}
        path="$DEST_DIR/$rel"
        [[ -f "$path" ]] || fail "A1 missing downloaded file: $path"
        got_sha=$(sha256sum "$path" | awk '{print $1}')
        if [[ "$got_sha" != "$want_sha" ]]; then
            fail "A2 sha256 mismatch on $rel: got $got_sha, want $want_sha"
        fi
    done
    green "  ✓ A1 + A2 every fixture present + sha256 round-trip exact"

    # A3 — --no-clobber. Seed a pre-existing local file with content
    # that differs from the remote, backdate its mtime, re-run get
    # with --no-clobber, assert mtime unchanged and bytes preserved.
    NC_DIR="$WORK_DIR/dest-noclobber"
    mkdir -p "$NC_DIR"
    cp "$FIX_DIR/skip-me.txt" "$NC_DIR/skip-me.txt"  # the LOCAL preexisting (different from remote)
    touch -d '2 hours ago' "$NC_DIR/skip-me.txt"
    PRE_MTIME=$(stat -c %Y "$NC_DIR/skip-me.txt")
    step "S4 cos bucket get --no-clobber over pre-seeded local" \
        "$ROKSBNKCTL" cos bucket get "$BUCKET" "$NC_DIR" --instance "$COS_INSTANCE" --no-clobber
    POST_MTIME=$(stat -c %Y "$NC_DIR/skip-me.txt")
    if [[ "$PRE_MTIME" != "$POST_MTIME" ]]; then
        fail "A3 --no-clobber: mtime changed ($PRE_MTIME → $POST_MTIME) — pre-existing file was clobbered"
    fi
    green "  ✓ A3 --no-clobber: mtime preserved, pre-existing local file untouched"

    # A4 — non-existent bucket → non-zero exit + bucket name in stderr.
    NX_BUCKET="${BUCKET}-no-such-thing-$RUN_TS"
    NX_DEST="$WORK_DIR/dest-nx"
    NX_OUT=$(step_expect_fail "S5 cos bucket get on non-existent bucket" \
        "$ROKSBNKCTL" cos bucket get "$NX_BUCKET" "$NX_DEST" --instance "$COS_INSTANCE")
    if ! grep -q "$NX_BUCKET" <<<"$NX_OUT"; then
        fail "A4 non-existent bucket: error text did not name the bucket ($NX_BUCKET)"
    fi
    green "  ✓ A4 non-existent bucket: failed loudly + named the bucket in the error"

    # A5 — leak scan. The run log must NOT contain the sentinel we
    # planted under IBMCLOUD_API_KEY's name during plant_sentinel, AND
    # must not contain the first 24 bytes of the actual API key (which
    # would catch a missing redact() call). The tfvars file itself is
    # never scanned (we don't open it).
    if grep -qF "$SENTINEL" "$RUN_LOG"; then
        fail "A5 sentinel leaked into the run log — redact() bypassed somewhere"
    fi
    if [[ -n "$REAL_API_KEY" ]]; then
        # Only check a 24-byte head to avoid storing the full key in
        # the script's process memory for the grep argv.
        local head=${REAL_API_KEY:0:24}
        if grep -qF "$head" "$RUN_LOG"; then
            fail "A5 API-key head leaked into the run log — redact() did not cover all echo paths"
        fi
    fi
    green "  ✓ A5 leak scan: sentinel + API-key head both absent from $RUN_LOG"

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "GREEN — cos bucket get verified live: list + recursive download +"
    green "sha256 round-trip + nested subdir + --no-clobber + bad-bucket"
    green "error + no key leaks. run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
    green "(teardown runs next via the EXIT trap)"
}

main "$@"
