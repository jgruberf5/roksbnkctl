# Contributing to awsbnkctl

Thanks for your interest in awsbnkctl! This document covers the basics for getting set up locally, running tests, and shipping changes.

## Prerequisites

Tested on Linux and macOS hosts with:

- **Go 1.25+** — `go.mod` is the source of truth.
- **git**, **make**, **docker**.
- Standard dev utilities used by the test scripts: `jq`, `unzip`, `gnupg`, `openssh-client`, `python3`, and `helm` 3 for chart operations.

What is **not** required on the host:

- `terraform` — removed; awsbnkctl uses the AWS SDK directly.
- `kubectl` — internalised via `client-go`. Install only if you want a host-level kubectl alongside.
- `aws` CLI — internalised via the AWS SDK. Install only if you want SSO login flows (`aws sso login`).
- `goreleaser` — pulled at release time from `goreleaser/goreleaser:latest`.
- `mdbook` / `pandoc` — bundled in `tools/docker/mdbook/Dockerfile` for book builds.

## Building

```bash
go build -o awsbnkctl ./cmd/awsbnkctl
./awsbnkctl --help
```

`make` targets are available for common workflows — see `Makefile` for the full list.

## Running tests

The unit suite lives under `internal/...` and runs without any external dependencies:

```bash
go test ./...
```

CI runs the standard Go gates on every PR:

```bash
gofmt -l .         # must be empty
go vet ./...      # must be clean
staticcheck ./... # must be clean
go test ./...     # must pass
```

Run these locally before pushing — CI enforces them.

### Integration tiers

| Tier | What it exercises | When it runs |
|---|---|---|
| Unit | Pure Go packages, fakes for external IO | Every PR (CI) |
| `kind`-based integration | Apply manifests against a local kind cluster, exercise the K8s code paths | PR (CI) |
| AWS-SDK mocked | aws-sdk-go-v2 middleware fakes — exercise phase orchestration without real AWS calls | PR (CI) |
| `testcontainers` (sshd) | SSH backend integration via a containerised sshd | PR (CI) |
| Live e2e | Real AWS account + real EKS cluster | On demand only |

Integration tests are gated by build tags so they don't run by default:

```bash
go test -tags integration ./...
```

### Live e2e

The full e2e tier spins up a real EKS cluster, runs the full lifecycle (`up` → `scenarios run` → `down`), and tears down. It costs real money and takes ~25 minutes per cycle.

```bash
export AWS_PROFILE=my-profile
aws sso login --profile $AWS_PROFILE
./scripts/e2e-test-full.sh    # set AWSBNKCTL_E2E_* env vars first; see the script
```

Use it sparingly and only against a sandbox account.

## Code style

- **Surgical changes** — touch only what the change requires; match surrounding style.
- **Clarity over cleverness** — prefer maintainable code over impressive one-liners.
- **No comments explaining WHAT** the code does, only WHY when non-obvious.
- **No half-finished implementations** — if you can't finish a code path in this PR, document the limitation and gate the surface.

The codebase uses standard `gofmt` formatting + `staticcheck` linting. See [`docs/design/`](docs/design/) for architectural design notes per subsystem.

## Adding a new phase

If you're extending the provisioning graph, please:

1. Read the existing phase you're closest to in shape (e.g. `internal/aws/phases/phase17_secondary_enis.go` for an AWS resource phase).
2. Add a new file `phaseNN_<name>.go` and corresponding `phaseNN_<name>_test.go`.
3. Wire it into `internal/cli/lifecycle.go:runPhasedUp` (and the inverse in `runPhasedDown`) at the correct ordering.
4. Make sure the phase is **idempotent** on healthy re-runs — tag-discovery should be the source of truth.
5. Update the relevant design spec under `docs/design/specs/`.

## Adding a new scenario or demo use-case

- **Scenario** (validation suite): create `internal/scenarios/<name>/` implementing the `scenarios.Scenario` interface. Self-register via `init()` calling `scenarios.Register(&scenario{})`. Side-effect import from `internal/cli/scenarios.go`. Use the existing `httproutee2e` or `proxyprotocoll4` packages as references.
- **Demo use-case** (audience walkthrough): create `internal/demo/<name>/` implementing the same `scenarios.Scenario` interface. Self-register via `init()` calling `demo.Register(&scenario{})`. Side-effect import from `internal/cli/demo.go`. Each demo owns a **dedicated VIP** via a `const scnVIP = "10.0.10.<N>"` (see existing demos for the canonical map).

For both, include:
- A `VerifyDeps` struct with seam fields for the load-bearing steps + a `TestVerifyCallOrder` test that regresses if the order changes.
- An idempotent `Cleanup` that tolerates an already-absent namespace.
- Templated embedded manifests via `//go:embed`.

## Pre-commit

A simple pre-commit hook lives at `scripts/pre-commit.sh`:

```bash
ln -sf ../../scripts/pre-commit.sh .git/hooks/pre-commit
```

It runs `gofmt`, `go vet`, and a quick test pass on the changed files.

## Releasing

Releases are built and published by [`.github/workflows/release.yml`](.github/workflows/release.yml) via `goreleaser` when a `vX.Y.Z` tag is pushed:

```bash
git tag -a vX.Y.Z -m "release vX.Y.Z"
git push origin vX.Y.Z
```

The workflow builds darwin/linux/windows × amd64/arm64 binaries, attaches them to the GitHub release with `README.md` + `LICENSE` + `CHANGELOG.md`, and publishes checksums.

## Reporting issues

Open an issue using the templates in `.github/ISSUE_TEMPLATE/`. For bugs, please include:

- `awsbnkctl --version`
- The minimal `cluster.yaml` that reproduces the issue (redact credentials).
- The full stderr output of the failing command.
- Whether the issue reproduces against a fresh `up` or only on re-run / specific state.

## Questions

For design questions, the per-subsystem PRDs under [`docs/design/specs/`](docs/design/specs/) are usually the right starting point — each is a self-contained "why we built it this way" narrative.
