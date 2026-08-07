#!/usr/bin/env bash
# Exercise terraform variable `validation` blocks against real values.
#
# WHY THIS EXISTS. `terraform validate` does NOT evaluate validation conditions
# against values — it only type-checks the configuration. So a condition that is
# syntactically fine but explodes at plan time passes every gate we had, ships,
# and fails on the user's first run.
#
# That is not hypothetical: v1.40.0 shipped
#
#   condition = var.cluster_vpc_cidr == "" || tonumber(split("/", var.cluster_vpc_cidr)[1]) <= 18
#
# on the assumption that `||` short-circuits. It does not — terraform evaluates
# both operands — so with the variable at its default "" the right side ran
# split("/","")[1] and raised "Invalid index" on EVERY plan. The fix is try(...,
# false); this script is what proves it stays fixed.
#
# Each case below runs a real `terraform plan` against a scratch module holding
# only the extracted variable block, so no providers or credentials are needed.
#
# RUN IT AGAINST THE VERSION WE SHIP, NOT THE ONE ON YOUR PATH. This bug was
# invisible locally because terraform CHANGED this behaviour: 1.15 short-circuits
# `||` in a validation condition, 1.10 does not. Dev boxes had 1.15, the runner
# image ships 1.10.5, and roksbnkctl's stated floor is 1.10
# (requireTerraformVersion(…, 1, 10)) — so the local gate was testing a terraform
# no user is required to have. TF_TEST_IMAGE pins the runner image by default;
# TF_TEST_LOCAL=1 falls back to $PATH with a warning.
set -euo pipefail

ROOT="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
fails=0

TF_TEST_IMAGE="${TF_TEST_IMAGE:-ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest}"
if [[ "${TF_TEST_LOCAL:-0}" == 1 ]] || ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  TF() { ( cd "$1" && shift && terraform "$@" ); }
  echo "!!  using \$PATH terraform $(terraform version | head -1 | awk '{print $2}') — NOT the shipped version."
  echo "!!  A condition that only works on newer terraform will pass here and fail for users."
else
  TF() { local d="$1"; shift; docker run --rm -v "$d:/w" -w /w --entrypoint terraform "$TF_TEST_IMAGE" "$@"; }
  echo "==> terraform from $TF_TEST_IMAGE ($(docker run --rm --entrypoint terraform "$TF_TEST_IMAGE" version | head -1 | awk '{print $2}'))"
fi

# extract_var <file> <name> — print the full `variable "<name>" { ... }` block.
extract_var() {
  python3 - "$1" "$2" <<'PY'
import sys, pathlib
src, name = sys.argv[1], sys.argv[2]
t = pathlib.Path(src).read_text()
i = t.index(f'variable "{name}"')
depth = 0; j = i
while True:
    if t[j] == '{': depth += 1
    elif t[j] == '}':
        depth -= 1
        if depth == 0: j += 1; break
    j += 1
print(t[i:j])
PY
}

# check <var> <value> <accept|reject> [expected-message-substring]
check() {
  local name="$1" value="$2" want="$3" msg="${4:-}"
  local dir="$WORK/$name-$(echo "$value" | tr -c 'a-zA-Z0-9' '_')"
  mkdir -p "$dir"
  extract_var "$ROOT/terraform/variables.tf" "$name" > "$dir/main.tf"
  TF "$dir" init -no-color >/dev/null 2>&1
  local out rc=0
  out="$(TF "$dir" plan -no-color -var="$name=$value" 2>&1)" || rc=$?

  if [[ "$want" == accept ]]; then
    if (( rc == 0 )); then
      printf '  ok    %s=%-16s accepted\n' "$name" "'$value'"
    else
      printf '  FAIL  %s=%-16s should be accepted but was rejected:\n%s\n' "$name" "'$value'" "$out"
      fails=$((fails + 1))
    fi
  else
    if (( rc == 0 )); then
      printf '  FAIL  %s=%-16s should be rejected but was accepted\n' "$name" "'$value'"
      fails=$((fails + 1))
    elif [[ -n "$msg" ]] && ! grep -qF "$msg" <<<"$out"; then
      printf '  FAIL  %s=%-16s rejected, but not for the stated reason (%s):\n%s\n' "$name" "'$value'" "$msg" "$out"
      fails=$((fails + 1))
    else
      printf '  ok    %s=%-16s rejected (%s)\n' "$name" "'$value'" "$msg"
    fi
  fi
}

echo "==> terraform variable validation (real plan, not just \`validate\`)"

# The empty default is the case that regressed — it must plan cleanly.
check cluster_vpc_cidr ""              accept
check cluster_vpc_cidr "10.241.0.0/16" accept
check cluster_vpc_cidr "10.242.0.0/16" accept
check cluster_vpc_cidr "10.250.0.0/18" accept
check cluster_vpc_cidr "10.242.0.0/24" reject "needs /18 or larger"
check cluster_vpc_cidr "10.242.0.0/20" reject "needs /18 or larger"
check cluster_vpc_cidr "garbage"       reject "must be empty or a valid CIDR"

if (( fails )); then
  echo "==> $fails variable-validation case(s) FAILED"
  exit 1
fi
echo "==> all variable-validation cases passed"
