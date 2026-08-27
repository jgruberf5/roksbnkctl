#!/usr/bin/env bash
# branch-hygiene.sh — refuse a branch that carries files it has no business
# carrying.
#
# WHY. Other agent sessions work in this same checkout. `git add -A` sweeps up
# their in-progress untracked files, and the first sign is their document failing
# YOUR spellcheck. That happened twice in one session with
# docs/prd/19-SUPPORT-SUBCOMMAND.md — the second time AFTER a rule against
# `git add -A` was written into CLAUDE.md.
#
# A rule that has been broken twice is not a control. This is: it exits non-zero,
# so it can gate a push.
#
#   ./scripts/branch-hygiene.sh              # check HEAD against origin/main
#   ./scripts/branch-hygiene.sh <base>       # against another base
set -uo pipefail

BASE="${1:-origin/main}"
branch="$(git rev-parse --abbrev-ref HEAD)"

case "$branch" in main|gh-pages) echo "==> $branch: nothing to check"; exit 0 ;; esac

changed="$(git diff --name-only "$BASE...HEAD" 2>/dev/null)"
if [ -z "$changed" ]; then
    echo "==> $branch: no changes against $BASE"
    exit 0
fi

echo "==> $branch changes $(printf '%s\n' "$changed" | wc -l) file(s) against $BASE"

# A file is suspect when this branch changes it but has no commit message
# mentioning it and no other file from the same area. Rather than guess, the check
# is concrete: anything under docs/prd/ that the branch did not also reference in a
# commit subject or body is almost certainly someone else's draft, because PRDs are
# written deliberately and named in their own commits.
suspect=0
while read -r f; do
    [ -z "$f" ] && continue
    case "$f" in
        docs/prd/*)
            base="$(basename "$f")"
            if ! git log "$BASE..HEAD" --format='%B' | grep -qF "$base"; then
                echo "    ✗ $f"
                echo "       changed by this branch but named in no commit message on it."
                echo "       If it is yours, say so in the commit. If it is another session's,"
                echo "       remove it: git rm --cached '$f' && git commit --amend"
                suspect=$((suspect + 1))
            fi
            ;;
    esac
done <<< "$changed"

# Untracked files owned by nobody in particular are the raw material for the same
# mistake, so they are reported even when nothing is staged.
untracked="$(git ls-files --others --exclude-standard)"
if [ -n "$untracked" ]; then
    echo "==> untracked files present — do NOT 'git add -A' with these here:"
    printf '%s\n' "$untracked" | sed 's/^/    /'
fi

if [ "$suspect" -gt 0 ]; then
    echo ""
    echo "✗ $suspect file(s) look like they belong to another session."
    exit 1
fi
echo "✓ no foreign files on this branch"
