#!/usr/bin/env bash
# scripts/archive-sprint.sh — move a completed sprint's issue + prompt
# files into .archive/, preserving git history.
#
# Once a sprint closes, its working ledger (issues/issue_sprint<N>_*.md +
# issues/resolved_sprint<N>_*.md) and dispatch prompts (prompts/sprint<N>/)
# stop being live working files but stay worth keeping for auditability —
# see issues/README.md and prompts/README.md ("Why these are checked in").
# This script relocates them under .archive/ in one shot instead of the
# by-hand `git mv` runs that produced the sprint 0-9 / 11 archive.
#
#   scripts/archive-sprint.sh 10              # archive sprint 10
#   scripts/archive-sprint.sh 10 12           # archive several sprints
#   scripts/archive-sprint.sh --dry-run 10    # show the moves, change nothing
#   scripts/archive-sprint.sh --force 10      # skip the open-issue prompt
#
# Layout produced (mirrors the existing archive):
#
#   issues/issue_sprint<N>_*.md     -> .archive/issues/
#   issues/resolved_sprint<N>_*.md  -> .archive/issues/
#   prompts/sprint<N>/              -> .archive/prompts/sprint<N>/
#
# Tracked files move via `git mv` (history preserved, like the prior
# archive's R100 renames); untracked files (e.g. freshly written
# resolved_*.md) move via plain `mv` so the script works mid-flight too.
# The top-level issues/README.md and prompts/README.md are sprint-agnostic
# and never match the sprint-scoped globs, so they stay put.

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

# The four roles a sprint dispatches (prompts/README.md §"The four roles").
# Used only for the completeness advisory — the actual move globs every
# matching file, so an unexpected role still gets archived.
ROLES=(architect staff tech-writer validator)

dry_run=false
force=false
sprints=()

usage() {
  sed -n '2,28p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) dry_run=true ;;
    --force)   force=true ;;
    -h|--help) usage 0 ;;
    --) shift; break ;;
    -*) echo "archive-sprint: unknown flag: $1" >&2; usage 1 ;;
    *)
      if ! [[ "$1" =~ ^[0-9]+$ ]]; then
        echo "archive-sprint: sprint number must be an integer, got: $1" >&2
        exit 1
      fi
      sprints+=("$1")
      ;;
  esac
  shift
done
# Any operands after a literal `--`.
for arg in "$@"; do
  if ! [[ "$arg" =~ ^[0-9]+$ ]]; then
    echo "archive-sprint: sprint number must be an integer, got: $arg" >&2
    exit 1
  fi
  sprints+=("$arg")
done

if [ "${#sprints[@]}" -eq 0 ]; then
  echo "archive-sprint: no sprint number given" >&2
  usage 1
fi

# is_tracked PATH — true if git has the path in the index/HEAD.
is_tracked() {
  git ls-files --error-unmatch -- "$1" >/dev/null 2>&1
}

# move_file SRC DESTDIR — git mv when tracked, plain mv otherwise.
move_file() {
  local src=$1 destdir=$2
  if $dry_run; then
    echo "  would move: $src -> $destdir/"
    return
  fi
  mkdir -p "$destdir"
  if is_tracked "$src"; then
    git mv -- "$src" "$destdir/"
  else
    mv -- "$src" "$destdir/"
  fi
  echo "  moved: $src -> $destdir/"
}

archived_any=false

for n in "${sprints[@]}"; do
  echo "=== sprint $n ==="

  # Collect sources. Sprint-scoped globs only — README.md never matches.
  issue_files=()
  while IFS= read -r f; do issue_files+=("$f"); done < <(
    find issues -maxdepth 1 -type f \
      \( -name "issue_sprint${n}_*.md" -o -name "resolved_sprint${n}_*.md" \) \
      2>/dev/null | sort
  )
  prompt_dir="prompts/sprint${n}"
  has_prompt_dir=false
  [ -d "$prompt_dir" ] && has_prompt_dir=true

  if [ "${#issue_files[@]}" -eq 0 ] && ! $has_prompt_dir; then
    echo "  nothing to archive (already archived, or sprint never existed) — skipping"
    continue
  fi

  # --- Completeness advisory (warn-only; never blocks) -------------------
  # Sprint 11 was archived with open/accepted statuses and zero resolved_*
  # files (resolved inline); sprint 10 carried "acceptable for vX" opens.
  # So neither a missing role file nor an open status proves a sprint is
  # unfinished — surface them and let the operator decide.
  missing_roles=()
  for role in "${ROLES[@]}"; do
    [ -f "issues/issue_sprint${n}_${role}.md" ] || missing_roles+=("$role")
  done
  if [ "${#missing_roles[@]}" -gt 0 ]; then
    echo "  warning: no issue file for role(s): ${missing_roles[*]}"
  fi

  open_count=0
  for f in "${issue_files[@]}"; do
    case "$f" in issues/issue_sprint*) ;; *) continue ;; esac
    c=$(grep -c -iE '^\*\*Status\*\*:[[:space:]]*(open|in-progress)\b' "$f" 2>/dev/null || true)
    open_count=$((open_count + c))
  done
  if [ "$open_count" -gt 0 ]; then
    echo "  warning: $open_count issue(s) still marked open/in-progress across sprint $n"
  fi

  if { [ "${#missing_roles[@]}" -gt 0 ] || [ "$open_count" -gt 0 ]; } \
     && ! $force && ! $dry_run; then
    printf "  archive sprint %s anyway? [y/N] " "$n"
    read -r reply </dev/tty || reply=""
    case "$reply" in
      y|Y|yes|YES) ;;
      *) echo "  skipped sprint $n"; continue ;;
    esac
  fi

  # --- Move issue/resolved files ----------------------------------------
  for f in "${issue_files[@]}"; do
    move_file "$f" ".archive/issues"
  done

  # --- Move the prompt directory (whole dir, including its README) ------
  if $has_prompt_dir; then
    dest="$prompt_dir"  # .archive/prompts/sprint<N>
    if [ -e ".archive/${dest}" ]; then
      echo "  warning: .archive/${dest} already exists — moving files into it"
    fi
    while IFS= read -r f; do
      move_file "$f" ".archive/$(dirname "$f")"
    done < <(find "$prompt_dir" -type f | sort)
    # Drop the now-empty source dir (and any empty subdirs).
    if ! $dry_run; then
      find "$prompt_dir" -type d -empty -delete 2>/dev/null || true
    fi
  fi

  archived_any=true
  echo "  sprint $n archived"
done

if $dry_run; then
  echo
  echo "dry run — no files moved. Re-run without --dry-run to apply."
elif $archived_any; then
  echo
  echo "Done. Review with: git status   then commit, e.g.:"
  echo "  git commit -m \"chore: archive completed sprint(s) ${sprints[*]}\""
fi
