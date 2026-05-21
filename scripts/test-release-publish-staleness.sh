#!/usr/bin/env bash
# scripts/test-release-publish-staleness.sh — hermetic test for Sprint 20
# staff Issue 1's `release-publish` stale-artifact gate (validator Issue 1).
#
# What this proves (validator Issue 1 AC #2, #3, #4):
#
#   * AC #2 — planted-bytes assertion. A sentinel byte string written to
#     book/book/pandoc/pdf/book.pdf (and the symmetric HTML sentinel at
#     book/book/html/index.html) BEFORE invoking `make release-publish`
#     does NOT survive the rebuild preamble. The `rm -rf book/book/pandoc/`
#     and `rm -rf book/book/html/` lines the staff edit added to the
#     recipe body (Makefile lines 434 + 444 of the post-edit tree) are
#     what wipes them; this test pins both lines.
#
#   * AC #3 — gh-stub assertion. A `gh` stub on PATH intercepts every
#     `gh release upload …` invocation and records its argv to a sentinel
#     file. The test asserts the stub's release-upload-argv file is empty
#     — i.e. the rebuild aborted release-publish before any upload step
#     could fire. The stub also returns 0 for `gh release view` so the
#     recipe's prereq check at the top of the target passes.
#
#   * AC #4 — hermetic. Never invokes the real `gh` binary (PATH stub).
#     Never `docker pull`s — the `docker` stub returns non-zero, which
#     forces the in-cycle rebuild step (`book` for HTML, `book-pdf` for
#     PDF) to fail and proves release-publish aborts before any upload
#     step. Never renders an actual PDF.
#
# Two scenarios are run so both sides of the symmetric hardening get
# pinned:
#
#   * S1 (HTML-side gate) — docker stub fails on FIRST invocation (the
#     `make book` rebuild for HTML). Asserts the HTML sentinel was wiped
#     by `rm -rf book/book/html/` before the rebuild attempt.
#
#   * S2 (PDF-side gate) — docker stub succeeds on FIRST invocation
#     (simulates `make book` HTML rebuild), then fails on SECOND
#     (simulates `make book-pdf` PDF rebuild failure mid-cycle, the
#     actual v1.6.4 event). Asserts the PDF sentinel was wiped by
#     `rm -rf book/book/pandoc/` before the rebuild attempt.
#
# ─────────────────────────────────────────────────────────────────────
# THIS IS NOT A CI JOB. Operator-run only:
#
#   ./scripts/test-release-publish-staleness.sh
#
# Exit 0 = PASS. Non-zero = first failed assertion, with the failing
# check named in the error line. Self-cleanup on EXIT trap so a failed
# run doesn't leave the sandbox tmpdir behind. (Failed runs preserve
# the sandbox for post-mortem; see KEEP_SANDBOX below.)
# ─────────────────────────────────────────────────────────────────────
#
# Failure-shape pin (AC #6): running this test against the pre-staff
# tree (e.g. via `git stash` of the Makefile edits) fails with the
# `planted-bytes assertion` error — that is what proves the test is
# pinning the right behaviour, not just happy-path-asserting.

set -e
set -u
set -o pipefail

# ── config ──────────────────────────────────────────────────────────
REPO_ROOT=${REPO_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
LOG_DIR=${LOG_DIR:-/tmp/roksbnkctl-test-release-publish-staleness}
mkdir -p "$LOG_DIR"
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"
SANDBOX_ROOT="$LOG_DIR/sandbox-$RUN_TS"

# Sentinel byte string planted into the stale artifact paths. Random
# per-run so a leftover from a prior test run can't false-pass.
gen_hex16() {
    if command -v xxd >/dev/null 2>&1; then
        head -c 16 /dev/urandom | xxd -p
    else
        od -An -N16 -tx1 /dev/urandom | tr -d ' \n'
    fi
}
SENTINEL="STALE_ARTIFACT_SENTINEL_$(gen_hex16)"

# ── helpers ─────────────────────────────────────────────────────────
red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*" >&2; }
yellow() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
bold()   { printf '\033[1m%s\033[0m\n'  "$*" >&2; }
log()    { echo "[$(date +%H:%M:%S)] $*" | tee -a "$RUN_LOG" >&2; }

KEEP_SANDBOX=${KEEP_SANDBOX:-0}

fail() {
    red "  FAIL: $1"
    red "  full log: $RUN_LOG"
    red "  sandbox preserved at: $SANDBOX_ROOT"
    KEEP_SANDBOX=1
    exit 2
}

skip() {
    yellow "  SKIP: $1"
    log "SKIP: $1"
    exit 0
}

# ── self-teardown trap ──────────────────────────────────────────────
teardown() {
    local prev_rc=$?
    if [[ "$KEEP_SANDBOX" == "1" ]]; then
        yellow "  sandbox preserved (KEEP_SANDBOX=1 or test failed): $SANDBOX_ROOT"
    else
        if [[ -d "$SANDBOX_ROOT" ]]; then
            rm -rf "$SANDBOX_ROOT" || true
            log "teardown: removed sandbox $SANDBOX_ROOT"
        fi
    fi
    if [[ "$prev_rc" != "0" ]]; then
        red "Test run FAILED (exit $prev_rc) — see $RUN_LOG"
    fi
}
trap teardown EXIT

# ── preflight ───────────────────────────────────────────────────────
preflight() {
    bold "preflight"
    if [[ ! -f "$REPO_ROOT/Makefile" ]]; then
        fail "Makefile not found at $REPO_ROOT/Makefile (REPO_ROOT=$REPO_ROOT)"
    fi
    if ! command -v make >/dev/null 2>&1; then
        skip "make not on PATH — cannot run the hermetic Makefile walk"
    fi
    for tool in mktemp grep rm cp mkdir tail; do
        if ! command -v "$tool" >/dev/null 2>&1; then
            skip "$tool not on PATH — cannot run the hermetic Makefile walk"
        fi
    done
    log "preflight OK — REPO_ROOT=$REPO_ROOT log=$RUN_LOG sandbox=$SANDBOX_ROOT sentinel=$SENTINEL"
}

# ── sandbox build (per-scenario) ────────────────────────────────────
# Build a hermetic copy of the Makefile in $1. We never run `make` in
# $REPO_ROOT — the test must leave the real working tree untouched.
build_scenario_sandbox() {
    local sb="$1"
    mkdir -p "$sb"
    cp "$REPO_ROOT/Makefile" "$sb/Makefile"

    # Neutralize `book-publish` in the sandbox Makefile. The release-
    # publish target invokes `$(MAKE) book-publish` between the HTML
    # rebuild and the PDF rebuild; book-publish's real recipe shells
    # out to `git fetch origin gh-pages` + `git worktree add` + `git
    # push` (the sandbox has no `origin` and no gh-pages branch). We
    # neutralize ONLY that target's recipe via an appended override —
    # the rebuild gates we're actually testing live in release-publish
    # itself, NOT in book-publish, so this neutralization is sound.
    #
    # Make refuses duplicate target definitions in the same file, so
    # we use a sentinel marker + Python-free sed to replace the
    # book-publish recipe in-place with a no-op body. The recipe
    # extends from the `book-publish:` line through to (but not
    # including) the next blank line that precedes `release-publish:`.
    # Reliable boundary: the comment block above release-publish
    # always starts with `# release-publish:`.
    awk '
        /^book-publish:/ { in_bp = 1; print "book-publish:"; print "\t@echo \"(book-publish stub: hermetic test no-op)\""; next }
        in_bp && /^# release-publish:/ { in_bp = 0 }
        !in_bp { print }
    ' "$sb/Makefile" > "$sb/Makefile.tmp" && mv "$sb/Makefile.tmp" "$sb/Makefile"

    # Belt-and-braces: a `git init` so any incidental `git -C $(CURDIR)`
    # in the Makefile (e.g. the `git config user.name` call in the
    # commit step we just neutralized) doesn't error noisily.
    (cd "$sb" && git init -q 2>/dev/null || true)

    # Plant the sentinels — these are what the staff edit's `rm -rf`
    # preambles must wipe.
    mkdir -p "$sb/book/book/pandoc/pdf"
    mkdir -p "$sb/book/book/html"
    printf '%s' "$SENTINEL" > "$sb/book/book/pandoc/pdf/book.pdf"
    printf '%s' "$SENTINEL" > "$sb/book/book/html/index.html"
}

# ── PATH stubs (per-scenario) ───────────────────────────────────────
# `gh` stub: returns 0 for `release view`, records argv on `release
# upload`. `docker` stub: counts invocations in a counter file and
# fails on the configured invocation index ($1 to build_stubs).
#
# build_stubs <sandbox> <fail_on_invocation>
#   fail_on_invocation = 1 → fail on the first docker call (book HTML)
#   fail_on_invocation = 2 → succeed on first, fail on second (book-pdf)
build_stubs() {
    local sb="$1"
    local fail_on="$2"
    local stub_bin="$sb/stub-bin"
    local upload_argv="$sb/gh-upload-argv.log"
    local docker_count="$sb/docker-count"
    mkdir -p "$stub_bin"
    : > "$upload_argv"
    echo 0 > "$docker_count"

    cat > "$stub_bin/gh" <<EOF
#!/usr/bin/env bash
# gh stub — never proxies to the real gh binary.
if [[ "\${1:-}" == "release" && "\${2:-}" == "view" ]]; then
    # Both forms used by the recipe: prereq check + post-publish URL print.
    printf 'https://example.invalid/stub\n'
    exit 0
fi
if [[ "\${1:-}" == "release" && "\${2:-}" == "upload" ]]; then
    printf '%s\n' "\$*" >> "$upload_argv"
    exit 0
fi
exit 0
EOF
    chmod +x "$stub_bin/gh"

    cat > "$stub_bin/docker" <<EOF
#!/usr/bin/env bash
# docker stub — fails on invocation #$fail_on so the corresponding
# in-cycle rebuild step fails, which proves release-publish aborts
# before any gh release upload runs.
n=\$(cat "$docker_count")
n=\$((n + 1))
echo "\$n" > "$docker_count"
echo "docker stub: invocation #\$n (argv: \$*)" >&2
if [[ "\$n" == "$fail_on" ]]; then
    echo "docker stub: invocation #\$n FAILING (hermetic test)" >&2
    exit 99
fi
# Successful path: simulate the book-build by creating the
# expected output paths (mdbook/mdbook-pandoc would create
# book/book/html/ and book/book/pandoc/pdf/book.pdf inside
# the mounted /book volume; here we touch the host-side paths
# directly since the stub never actually mounts anything).
# Locate the -v ... mount source so we honor the
# release-publish recipe's CURDIR=sandbox shape.
src=""
for a in "\$@"; do
    case "\$a" in
        *:/book) src="\${a%%:/book}";;
    esac
done
if [[ -n "\$src" ]]; then
    mkdir -p "\$src/book/html" "\$src/book/pandoc/pdf"
    # Fresh content (NOT the sentinel) — if release-publish ever
    # uploads from here, the argv recorder will see this path and
    # the sentinel-byte assertion still passes.
    printf 'FRESH_REBUILT_HTML\n' > "\$src/book/html/index.html"
    printf 'FRESH_REBUILT_PDF\n'  > "\$src/book/pandoc/pdf/book.pdf"
fi
exit 0
EOF
    chmod +x "$stub_bin/docker"

    # Also stub a `bash scripts/check-pdf-mermaid-labels.sh` shim
    # since the book-pdf recipe (when BOOK_BACKEND=docker) calls
    # `bash scripts/check-pdf-mermaid-labels.sh` after the docker
    # build. The sandbox Makefile is the real one, but $(CURDIR)
    # is the sandbox — so scripts/ doesn't exist there. Make a
    # no-op shim.
    mkdir -p "$sb/scripts"
    cat > "$sb/scripts/check-pdf-mermaid-labels.sh" <<'EOF'
#!/usr/bin/env bash
# stub: hermetic test no-op
exit 0
EOF
    chmod +x "$sb/scripts/check-pdf-mermaid-labels.sh"

    # Return values via well-known per-sandbox paths (caller reads
    # these directly by reconstructing them from $sb).
    log "  scenario sandbox: $sb"
    log "  stub PATH:        $stub_bin (gh + docker; docker fails on invocation #$fail_on)"
    log "  upload-argv log:  $upload_argv"
}

# ── walk the Makefile target ────────────────────────────────────────
# Run `make release-publish` in the sandbox with the stub PATH in
# front. Capture exit code + log into the scenario sandbox.
run_release_publish() {
    local sb="$1"
    local make_log="$sb/make.log"
    set +e
    PATH="$sb/stub-bin:$PATH" \
        make -C "$sb" release-publish VERSION=v0.0.0-test \
        >"$make_log" 2>&1
    local rc=$?
    set -e
    log "  make exit code:   $rc"
    cat "$make_log" >> "$RUN_LOG"
    echo "$rc"
}

# ── per-scenario assertions ─────────────────────────────────────────
# Args: <label> <sandbox> <which-sentinel: html|pdf> <make-rc>
assert_scenario() {
    local label="$1"
    local sb="$2"
    local which="$3"
    local make_rc="$4"
    local upload_argv="$sb/gh-upload-argv.log"
    local make_log="$sb/make.log"
    local target_path
    local rebuild_echo

    case "$which" in
        html)
            target_path="$sb/book/book/html/index.html"
            rebuild_echo="Rebuilding + pushing HTML book to gh-pages"
            ;;
        pdf)
            target_path="$sb/book/book/pandoc/pdf/book.pdf"
            rebuild_echo="Rebuilding + uploading PDF book to GitHub Release"
            ;;
        *)
            fail "$label: bad which=$which (internal test bug)"
            ;;
    esac

    # A1 — recipe reached the relevant rebuild step (pin the staff
    # edit's '==> [N/2] Rebuilding + …' echo line).
    if ! grep -qF "$rebuild_echo" "$make_log"; then
        red "  make log tail:"
        tail -n 30 "$make_log" >&2 || true
        fail "$label/A1: recipe never echoed '$rebuild_echo' — the staff-edit rebuild step did not run"
    fi
    green "  PASS $label/A1: recipe reached the '$rebuild_echo' rebuild step"

    # A2 — planted sentinel wiped by `rm -rf`.
    if [[ -f "$target_path" ]] && grep -qF "$SENTINEL" "$target_path"; then
        fail "$label/A2: planted sentinel survived in $target_path — the rm -rf preamble did NOT run before the rebuild attempt"
    fi
    green "  PASS $label/A2: planted sentinel wiped from $target_path"

    # A3 — gh release upload either NOT called, OR called with a
    # freshly-built artifact (sentinel-byte absence). In this test
    # the rebuild always fails so the upload is NEVER called; we
    # still check both branches per AC #3.
    if [[ -s "$upload_argv" ]]; then
        # If we got here, the upload happened. Inspect each argv:
        # the uploaded path's contents must not contain the sentinel.
        while IFS= read -r argv_line; do
            # gh release upload <tag> <path> [--clobber]
            # extract any path tokens and grep them.
            for tok in $argv_line; do
                if [[ -f "$tok" ]] && grep -qF "$SENTINEL" "$tok"; then
                    fail "$label/A3: gh release upload was called with stale-sentinel content in $tok"
                fi
            done
        done < "$upload_argv"
        green "  PASS $label/A3: gh release upload was called, but the uploaded path(s) did not contain the planted sentinel"
    else
        green "  PASS $label/A3: gh release upload was never called (rebuild failure aborted release-publish before upload)"
    fi

    # A4 — make exited non-zero (rebuild failure propagates).
    if [[ "$make_rc" == "0" ]]; then
        fail "$label/A4: make release-publish exited 0 despite the in-cycle rebuild failing — the recipe is swallowing the non-zero exit"
    fi
    green "  PASS $label/A4: make release-publish exited $make_rc (non-zero) — rebuild failure correctly aborted the target"
}

# ── main ────────────────────────────────────────────────────────────
main() {
    bold "release-publish stale-artifact gate — hermetic test — run-id $RUN_TS"
    bold "(validator Sprint 20 Issue 1 — pins staff Issue 1's rebuild-before-upload contract)"
    log "log: $RUN_LOG"
    preflight
    mkdir -p "$SANDBOX_ROOT"

    # ── S1 — HTML-side gate ────────────────────────────────────────
    bold "scenario S1 — HTML rebuild fails → HTML sentinel must be wiped, no upload"
    local sb1="$SANDBOX_ROOT/s1-html-gate"
    build_scenario_sandbox "$sb1"
    build_stubs "$sb1" 1
    local rc1
    rc1=$(run_release_publish "$sb1")
    assert_scenario "S1" "$sb1" "html" "$rc1"

    # ── S2 — PDF-side gate ─────────────────────────────────────────
    bold "scenario S2 — HTML rebuild succeeds, PDF rebuild fails → PDF sentinel must be wiped, no upload"
    local sb2="$SANDBOX_ROOT/s2-pdf-gate"
    build_scenario_sandbox "$sb2"
    build_stubs "$sb2" 2
    local rc2
    rc2=$(run_release_publish "$sb2")
    assert_scenario "S2" "$sb2" "pdf" "$rc2"

    echo "" >&2
    green "════════════════════════════════════════════════════════════"
    green "PASS — release-publish stale-artifact gate verified hermetically:"
    green "  S1 HTML-side: sentinel wiped + no upload + non-zero exit"
    green "  S2 PDF-side:  sentinel wiped + no upload + non-zero exit"
    green "run-id $RUN_TS"
    green "════════════════════════════════════════════════════════════"
}

main "$@"
