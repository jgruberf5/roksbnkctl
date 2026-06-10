#!/usr/bin/env bash
# scripts/test-integration-aws.sh — convenience wrapper for the
# `-tags integration` test pass over `internal/aws/...` plus the
# end-to-end `awsbnkctl up --dry-run` regression check.
#
# `internal/aws/{client,sts,ec2,eks,vpc}.go` ships with companion test
# files that exercise the helpers against mocked aws-sdk-go-v2 clients
# (no live AWS — middleware-test wiring). The same package includes
# `internal/aws/{s3,iam}.go` covering the S3 supply-chain (FAR
# archive + JWT upload via `PutObject` / `HeadObject`) and the IRSA /
# OIDC reader paths (`GetOIDCProvider`, `HasIRSARole`). The wildcard
# `./internal/aws/...` below picks all of them up — no per-file
# invocation needed.
#
# A full-up dry-run pass runs on top of the per-package tests — same
# shape as the `full-up-dryrun` job in .github/workflows/ci.yml.
# Toggle with `FULL_UP_DRYRUN=1` (default); set `FULL_UP_DRYRUN=0` to
# run only the per-package suite, useful when iterating on a single
# internal/aws helper without paying the binary-build + terraform-plan
# cost on every iteration. The full-up pass requires `terraform` on PATH.
#
# This script sets the env vars the suite expects so a contributor can
# run the same matrix CI runs without remembering the incantation:
#
#   $ ./scripts/test-integration-aws.sh
#   $ ./scripts/test-integration-aws.sh -run TestIntegration_STS
#   $ ./scripts/test-integration-aws.sh -run 'TestIntegration_S3|TestIntegration_IAM'
#   $ FULL_UP_DRYRUN=0 ./scripts/test-integration-aws.sh   # skip full-up-dryrun gate
#
# Extra args (after the script name) are forwarded to `go test`, not
# to the full-up gate — keep the contract narrow.
#
# Live-AWS validation is a separate operator-run path (spike protocol);
# this script is mocked-only and never touches a real AWS endpoint.
# The fake creds + `AWS_EC2_METADATA_DISABLED=true` below match
# `.github/workflows/ci.yml` jobs `aws-mocked` and `full-up-dryrun`.

set -euo pipefail

# Move to repo root so the relative `./internal/aws/...` path resolves
# regardless of which subdirectory the caller invokes from.
repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

# Fake creds — only ever consumed by SDK signer construction. If a
# test path leaks past its mock and hits AWS, the 403 from these fake
# creds is the test author's signal to add a mock.
export AWS_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID:-testing}
export AWS_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY:-testing}
export AWS_SESSION_TOKEN=${AWS_SESSION_TOKEN:-testing}
export AWS_DEFAULT_REGION=${AWS_DEFAULT_REGION:-us-east-1}
export AWS_REGION=${AWS_REGION:-us-east-1}

# Disable IMDS lookup — without this the SDK's default credential
# chain will block for ~5s probing the IMDS endpoint (169.254.169.254)
# on a developer laptop, slowing every test run.
export AWS_EC2_METADATA_DISABLED=true

FULL_UP_DRYRUN=${FULL_UP_DRYRUN:-1}

# ── per-package integration suite ───────────────────────────────────
# -v keeps the per-test output visible; -timeout 3m matches the CI
# job's budget. Extra args (`-run`, `-count`, `-race`) pass through.
echo "→ go test -tags integration ./internal/aws/..." >&2
go test -tags integration -timeout 3m -v ./internal/aws/... "$@"

# ── full-up dry-run gate ─────────────────────────────────────────────
# Mirrors the `full-up-dryrun` job in CI: builds the binary, runs
# `awsbnkctl up --dry-run`, asserts the plan output mentions every
# expected module. Skips cleanly when `terraform` is not on PATH so a
# contributor without it installed can still iterate on the per-package
# suite above.
if [[ "$FULL_UP_DRYRUN" != "1" ]]; then
    echo "→ full-up-dryrun: skipped (FULL_UP_DRYRUN=$FULL_UP_DRYRUN)" >&2
    exit 0
fi

if ! command -v terraform >/dev/null 2>&1; then
    echo "⚠ full-up-dryrun: skipped — terraform not on PATH" >&2
    echo "  Install terraform >= 1.0 to run the full-up-dryrun gate." >&2
    exit 0
fi

echo "→ full-up-dryrun: go build -o bin/awsbnkctl ./cmd/awsbnkctl" >&2
mkdir -p bin
go build -o bin/awsbnkctl ./cmd/awsbnkctl

echo "→ full-up-dryrun: ./bin/awsbnkctl up --dry-run" >&2
artifacts_dir="${TMPDIR:-/tmp}/awsbnkctl-test-integration-aws"
mkdir -p "$artifacts_dir"
log="$artifacts_dir/up-dryrun.log"
HOME_OVERRIDE="$artifacts_dir/home"
mkdir -p "$HOME_OVERRIDE"

# Run with a fresh HOME so the workspace config-yaml path doesn't
# inherit the contributor's local awsbnkctl state.
if ! HOME="$HOME_OVERRIDE" ./bin/awsbnkctl up --dry-run 2>&1 | tee "$log"; then
    echo "✗ full-up-dryrun: awsbnkctl up --dry-run exited non-zero" >&2
    echo "  log: $log" >&2
    exit 1
fi

# Module list: eks_cluster, cert_manager, s3_supply_chain, iam_irsa,
# flo, cne_instance, license, testing — all 8 names are checked.
fail=0
for mod in \
    eks_cluster \
    cert_manager \
    s3_supply_chain \
    iam_irsa \
    flo \
    cne_instance \
    license \
    testing
do
    if ! grep -qE "(module\.${mod}\b|\"${mod}\")" "$log"; then
        echo "✗ full-up-dryrun: module not found in plan output: ${mod}" >&2
        fail=1
    else
        echo "  ✓ ${mod}" >&2
    fi
done

if [[ "$fail" -ne 0 ]]; then
    echo "✗ full-up-dryrun: one or more modules missing from plan output" >&2
    echo "  log: $log" >&2
    exit 1
fi

echo "✓ full-up-dryrun: all modules present in plan output" >&2
