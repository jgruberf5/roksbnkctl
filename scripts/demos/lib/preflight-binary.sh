# shellcheck shell=bash
# ── preflight_binary (#143) ──────────────────────────────────────────────────
#
# ROKSBNKCTL_BIN defaults to whatever `roksbnkctl` is on PATH. During the v1.50.0
# validation that was v1.43.0 — eighteen releases old — and the CLI half of a
# demo ran against it for two full passes before anyone checked.
#
# That is worse than an ordinary stale pin, because the demo LOOKS right: the
# banner says the current version, the Argo runner image is correct, and the CLI
# steps quietly exercise an old build. A validation pass can report green for a
# release that was never run.
#
# So: put the resolved path and version on screen — and therefore in the
# recording — and say loudly when it is not the newest CHANGELOG entry. The
# runner-image preflight is the precedent, and the reason the Argo half never
# had this problem.
#
# WARN, do not die. Demoing a locally built binary is normal while developing,
# and a hard refusal would block it. The point is that the mismatch cannot pass
# unnoticed, not that it is forbidden.
#
# Relies on ok/note/die from the caller (demo-format.sh, or a demo's own inlined
# copies). Those resolve at call time, so sourcing order does not matter.
#
# Usage: preflight_binary "$ROKSBNKCTL_BIN"   (or "$RBK")
preflight_binary(){
  local bin="${1:?preflight_binary needs the binary}"
  local resolved ver installed latest changelog here

  resolved="$(command -v "$bin" 2>/dev/null)" \
    || { die "$bin not found on PATH — set ROKSBNKCTL_BIN to the binary you mean to demo"; return 1; }

  # `version` prints e.g. "roksbnkctl v1.50.0 (commit abc1234, built ...)".
  #
  # Field 2 VERBATIM, not a semver-shaped substring of it. A `make build` stamps
  # `git describe --dirty`, so a local build reports v1.50.0-3-gdeadbee-dirty;
  # grepping out just the vX.Y.Z part reported that as "v1.50.0" and warned
  # about nothing — an uncommitted build rendering on camera as the release,
  # which is the exact failure this function exists to prevent. A plain
  # `go build` reports "dev", which must also not look like a release.
  #
  # `|| true` throughout: a demo sourcing this under `set -e` with `pipefail`
  # must not die because a grep found nothing.
  ver="$("$bin" version 2>/dev/null | head -1)" || true
  installed="$(printf '%s\n' "$ver" | awk '{print $2}')" || true

  ok "preflight: roksbnkctl ${installed:-unknown} at $resolved"

  # Resolve the repo root PHYSICALLY (readlink -f + cd -P). Logical resolution
  # walks back through a symlinked lib/ into the wrong tree, the CHANGELOG is
  # not found, and the version check vanishes while the tick above still prints
  # — a silent no-op wearing the disguise of a passing check.
  here="$(cd -P "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
  changelog="${DEMO_REPO_ROOT:-$(cd -P "$here/../../.." && pwd)}/CHANGELOG.md"
  if [[ ! -r "$changelog" ]]; then
    note "could not read $changelog — cannot confirm which release this demo is exercising"
    return 0
  fi

  # Same extraction as TestDemoRunnerTagMatchesTheCurrentRelease, so the shell
  # check and the Go guards cannot disagree about what "current" means.
  latest="$(grep -m1 -oE '^## v[0-9]+\.[0-9]+\.[0-9]+' "$changelog" | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+')" || true
  if [[ -z "$latest" ]]; then
    note "no release heading found in $changelog — cannot confirm which release this demo is exercising"
    return 0
  fi

  if [[ -z "$installed" ]]; then
    note "could not read a version from \`$bin version\` — cannot confirm this demo is exercising $latest"
    return 0
  fi
  if [[ "$installed" != "$latest" ]]; then
    note "VERSION MISMATCH: this demo will run roksbnkctl $installed, but the newest release is $latest.
      Binary:  $resolved
      If you meant to demo the release, install it and re-run; if you are testing a
      local build, carry on — but the recording will show $installed, not $latest."
  fi
}
