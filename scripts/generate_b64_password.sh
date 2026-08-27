#!/usr/bin/env bash
# generate_b64_password.sh — turn a typed password into the base64 a
# config.yaml `*_b64` field wants, without it appearing on screen, in your shell
# history, or in the process list.
#
#   ./scripts/generate_b64_password.sh                    # prompt, print base64
#   ./scripts/generate_b64_password.sh bnkforge.password_b64   # prompt + ready-to-paste YAML
#
# WHY A SCRIPT RATHER THAN `echo secret | base64`
#
#   1. `echo` puts the password in your shell history and in `ps` output.
#   2. `echo` appends a NEWLINE, so `echo hunter2 | base64` encodes "hunter2\n".
#      Some consumers tolerate the trailing newline and some do not; the ones
#      that do not fail as "wrong password", which is the least debuggable
#      possible symptom. This uses printf and encodes exactly what you typed.
#   3. GNU base64 wraps at 76 columns. A wrapped value pasted into YAML becomes
#      a multi-line scalar that decodes to something else. `-w0` where it is
#      supported, `tr -d` everywhere else.
#
# THE ENV OVERRIDES DO NOT ALL WANT THE SAME THING, which is the trap this
# prints a reminder about. The rule is in the variable's NAME:
#
#   ROKSBNKCTL_*_B64        want the BASE64      (API_KEY_B64, BNKFORGE_CA_B64, …)
#   ROKSBNKCTL_*_PASSWORD   want the RAW value   (BIGIP_, GTM_, GENERIC_, BNKFORGE_)
#
# Feeding base64 to a *_PASSWORD variable double-encodes it and the credential
# silently does not work.
set -uo pipefail

FIELD="${1:-}"

die() { printf '%s\n' "$*" >&2; exit 1; }

# Encode without a trailing newline and without line wrapping. `base64 -w0` is
# GNU; BSD/macOS base64 has no -w and does not wrap stdin, so the `tr` is the
# portable belt to that braces.
b64() { printf '%s' "$1" | base64 2>/dev/null | tr -d '\n'; }

command -v base64 >/dev/null 2>&1 || die "base64 not found on PATH"

# Read twice and compare. A mistyped password that is only discovered by a
# failed deploy costs far more than the second prompt.
if [ ! -t 0 ]; then
    die "stdin is not a terminal — this prompts for a password and must not be piped.
  For scripted use, encode the value where it already lives:
      printf %s \"\$MY_PASSWORD\" | base64 | tr -d '\\n'"
fi

printf 'Password: ' >&2
IFS= read -rs PASS
printf '\n' >&2
[ -n "$PASS" ] || die "empty password"

printf 'Re-enter:  ' >&2
IFS= read -rs PASS2
printf '\n' >&2

if [ "$PASS" != "$PASS2" ]; then
    unset PASS PASS2
    die "the two entries do not match"
fi
unset PASS2

ENC="$(b64 "$PASS")"
unset PASS
[ -n "$ENC" ] || die "encoding produced nothing"

printf '\n'
if [ -n "$FIELD" ]; then
    # A dotted path yields the leaf key, so the line can be pasted straight under
    # its parent block: bnkforge.password_b64 -> "password_b64: <value>".
    LEAF="${FIELD##*.}"
    printf '  # config.yaml — under the %s block\n' "${FIELD%.*}"
    printf '  %s: %s\n' "$LEAF" "$ENC"
else
    printf '  %s\n' "$ENC"
fi

cat >&2 <<'NOTE'

  Where this goes:
    config.yaml   any *_b64 field — ibmcloud.api_key_b64, bnk.cis.bigip_password_b64,
                  bnk.gtm.password_b64, registry.generic_password_b64,
                  bnkforge.password_b64
    ROKSBNKCTL_*_B64        take THIS base64 value
    ROKSBNKCTL_*_PASSWORD   take the RAW password, NOT this — they base64 it
                            themselves, and pasting this double-encodes it

  base64 is obfuscation, not encryption. Anyone holding the file holds the
  password. chmod 600 the workspace and keep it out of git.
NOTE
