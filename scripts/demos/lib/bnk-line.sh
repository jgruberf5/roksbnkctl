# shellcheck shell=bash
# ── bnk_line_of (#169) ───────────────────────────────────────────────────────
#
# Derive the BNK release line from a manifest version, the same way
# config.BNKLine() does in the product: the major.minor prefix, nothing else.
#
# Lives here rather than being spelled out at each call site because the reason
# it exists is a per-line workaround, and four copies of a version test is how
# they drift. `2.4.0-EA` has no build suffix where 2.3 carries four segments, so
# anything matching on segment COUNT rather than prefix gets 2.4 wrong.
#
# Usage:  [[ "$(bnk_line_of "$MANIFEST_VERSION")" == "2.3" ]] && ...
bnk_line_of(){
  local v="${1:-}"
  case "$v" in
    2.3*) echo "2.3" ;;
    2.4*) echo "2.4" ;;
    *)    echo "" ;;   # unknown: callers decide, and should not assume 2.3
  esac
}
