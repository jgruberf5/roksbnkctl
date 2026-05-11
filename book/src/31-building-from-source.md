# Building from source

This chapter is for contributors and operators who want to build `roksbnkctl` themselves — whether to test an unreleased change, to verify a release artefact, or to embed a custom HCL fork into the binary.

For users who just want to *install* the binary, [Chapter 4 — Installation](./04-installation.md) is the right page. This chapter is the build-side companion.

## Go version requirement

The minimum Go version is the one pinned in [`go.mod`](https://github.com/jgruberf5/roksbnkctl/blob/main/go.mod):

```text
go 1.25.0
```

We pin to a recent toolchain for two reasons:

1. The IBM Cloud Go SDKs (`go-sdk-core/v5`, `platform-services-go-sdk`, `ibm-cos-sdk-go`) and `k8s.io/client-go` v0.30+ both make liberal use of Go's modern generics — pre-1.21 toolchains won't build.
2. Several dependencies (`miekg/dns`, `docker/docker`) test on the current and previous minor only; we follow upstream.

Install Go via your package manager (`brew install go`, `apt-get install golang-1.25`, etc.) or from [go.dev/dl](https://go.dev/dl/). Verify with `go version`.

## Quick build

The shortest path from a fresh clone to a working binary:

```bash
git clone https://github.com/jgruberf5/roksbnkctl.git
cd roksbnkctl
go build -o roksbnkctl ./cmd/roksbnkctl
./roksbnkctl --version
```

`go build` produces a static binary in the working directory. Cross-compilation to a different OS/arch needs `GOOS` / `GOARCH` set:

```bash
GOOS=linux   GOARCH=amd64 go build -o roksbnkctl-linux-amd64   ./cmd/roksbnkctl
GOOS=darwin  GOARCH=arm64 go build -o roksbnkctl-darwin-arm64  ./cmd/roksbnkctl
GOOS=windows GOARCH=amd64 go build -o roksbnkctl.exe          ./cmd/roksbnkctl
```

A full multi-platform build is easier through goreleaser:

```bash
goreleaser release --snapshot --clean
# Output lands in dist/
```

The `--snapshot --clean` flags produce a local build without trying to publish to GitHub. The release shape is described in [`.goreleaser.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.goreleaser.yml) — Linux + macOS × amd64 + arm64, plus a Windows compile-only check.

## Build via the Makefile

The repo's [`Makefile`](https://github.com/jgruberf5/roksbnkctl/blob/main/Makefile) wraps the common build steps and stamps version metadata into the binary:

```bash
make build              # builds bin/roksbnkctl with -ldflags version stamping
make test               # go test ./...
make vet                # go vet ./...
make tidy               # go mod tidy
make test-short         # go test -short ./...
make test-integration   # testcontainers-go-backed integration tests (needs Docker)
make test-cred-audit    # the security-spine regression suite
make lint               # gofmt + vet + staticcheck (if installed)
```

The version stamp comes from three ldflags variables baked into `internal/cli`:

```go
var (
    Version   = "dev"
    Commit    = "none"
    BuildDate = "unknown"
)
```

`make build` passes `-X github.com/jgruberf5/roksbnkctl/internal/cli.Version=$VERSION` (and the others) so `roksbnkctl --version` reports the actual git-rev and build timestamp rather than the placeholders. Set `VERSION` explicitly when stamping a release:

```bash
VERSION=v1.0.0 make build
```

## The embedded HCL

The Terraform source tree at [`terraform/`](https://github.com/jgruberf5/roksbnkctl/tree/main/terraform) is compiled into the binary via Go's [`//go:embed`](https://pkg.go.dev/embed) directive. The embed declaration lives at the repo root in [`embedded.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/embedded.go) (and is wired through `internal/tf/` to be served as the `tf_source: embedded` provider).

Two implications:

1. **Rebuilding the binary picks up HCL changes.** If you're hacking on the HCL, `make build` produces a binary that ships your changes embedded. No separate "deploy the HCL" step.
2. **The HCL is read-only at runtime.** The binary extracts it to a temporary directory on first use; the extracted copy is what terraform operates on. The original embedded source is immutable.

For users who want to *not* use the embedded HCL, the `tf_source: github` or `tf_source: local` options in the workspace config bypass it entirely. See [Chapter 12 §"tf_source:"](./12-workspace-config.md#tf_source).

## The bundled tools images

The `tools/docker/` directory holds Dockerfiles for the images the `docker` and `k8s` backends use:

```text
tools/docker/
├── Makefile
├── ibmcloud/
│   └── Dockerfile      # roksbnkctl-tools-ibmcloud
└── iperf3/
    └── Dockerfile      # roksbnkctl-tools-iperf3
```

[`tools/docker/Makefile`](https://github.com/jgruberf5/roksbnkctl/blob/main/tools/docker/Makefile) builds both images locally as `:dev`:

```bash
cd tools/docker
make ibmcloud           # builds roksbnkctl-tools-ibmcloud:dev
make iperf3             # builds roksbnkctl-tools-iperf3:dev
make all                # both
```

The `:dev` tag is what a from-source `roksbnkctl` resolves to when the binary's `Version` is `dev`. A tag-released binary (`v1.0.0`) resolves to `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v1.0.0` instead — the resolver logic lives in [`internal/exec/`](https://github.com/jgruberf5/roksbnkctl/tree/main/internal/exec) (`SetToolImageTag` is wired in `internal/cli/root.go::init`). See [Chapter 17 §":dev tag resolution"](./17-execution-backends.md#dev-tag-resolution).

The GitHub Actions workflow [`tools-images.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/tools-images.yml) builds and pushes the published images on a tag push or when `tools/docker/**` changes.

## The book build

The book is built with [mdBook](https://rust-lang.github.io/mdBook/). Install:

```bash
cargo install mdbook
# or
brew install mdbook
```

The book source lives under [`book/src/`](https://github.com/jgruberf5/roksbnkctl/tree/main/book/src) with [`book.toml`](https://github.com/jgruberf5/roksbnkctl/blob/main/book/book.toml) as the config. Common operations:

```bash
make book-serve         # mdbook serve book/ --open
                        # opens http://localhost:3000 with live-reload
make book               # mdbook build book/
                        # static HTML at book/book/
make book-clean         # rm -rf book/book
```

The published site at `https://jgruberf5.github.io/roksbnkctl/book/` is built and deployed by [`.github/workflows/book.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/book.yml) on every push to `main`. The workflow runs `mdbook build book/` and pushes the output to the `gh-pages` branch via `peaceiris/actions-gh-pages`.

For PR-time verification, `.github/workflows/spellcheck.yml` runs `cspell` on `book/src/**/*.md` — a warning, not a gate, but worth eyeballing the output before merging.

## The auto-generated chapters

Two reference chapters are generated rather than hand-written. The generators live under `tools/refgen/`:

```bash
# Chapter 27 — command reference (walks the cobra command tree)
go run ./tools/refgen/cobra-md > book/src/27-command-reference.md

# Chapter 29 — terraform variable reference (parses terraform/variables.tf)
go run ./tools/refgen/tfvars-md > book/src/29-terraform-variable-reference.md
```

When to re-run:

- **Chapter 27**: any change to the cobra command tree under [`internal/cli/`](https://github.com/jgruberf5/roksbnkctl/tree/main/internal/cli) or [`cmd/roksbnkctl/`](https://github.com/jgruberf5/roksbnkctl/tree/main/cmd/roksbnkctl) — new commands, renamed flags, edited `Long:` / `Example:` strings.
- **Chapter 29**: any change to [`terraform/variables.tf`](https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/variables.tf) or any submodule `variables.tf` referenced from the root — new variables, default-value changes, edited descriptions.

Both generators emit deterministic output — the same input HCL or cobra tree always produces the same markdown — so you can commit the rendered output to source control without worrying about spurious diff churn.

## Cross-compile matrix

`goreleaser` covers the canonical matrix:

| OS | Architecture | Status |
|---|---|---|
| Linux | amd64 | Fully supported |
| Linux | arm64 | Fully supported |
| macOS | amd64 (Intel) | Fully supported |
| macOS | arm64 (Apple Silicon) | Fully supported |
| Windows | amd64 | Compile-only; SSH TTY support degraded |
| Windows | arm64 | Compile-only; same caveat |
| FreeBSD | amd64 | Not tested |

The Windows caveat is real: `golang.org/x/crypto/ssh`'s PTY allocation isn't complete on Windows, so `roksbnkctl shell --on jumphost` falls back to a non-TTY shell. The other commands (exec, ibmcloud, kubectl) work fine on Windows.

Output from `goreleaser release --snapshot --clean` lands in `dist/`:

```text
dist/
├── roksbnkctl_linux_amd64_v1/
│   └── roksbnkctl
├── roksbnkctl_linux_arm64/
│   └── roksbnkctl
├── roksbnkctl_darwin_amd64_v1/
│   └── roksbnkctl
├── roksbnkctl_darwin_arm64/
│   └── roksbnkctl
└── ...
```

Each archive bundles the binary plus `LICENSE`, `README.md`, and the rendered `book/book/` directory (when the snapshot is built from a tagged commit).

## Release process

Tagged releases are cut on the `main` branch:

```bash
# Update CHANGELOG.md with the release notes
git tag -a v1.0.0 -m "v1.0.0 — book launch + full E2E coverage"
git push origin v1.0.0
```

The push triggers [`release.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/release.yml), which runs `goreleaser release` to:

1. Cross-compile the binary for the supported OS/arch matrix.
2. Build the matching tools images and push to `ghcr.io/jgruberf5/roksbnkctl-tools-*:<tag>`.
3. Attach the binaries, checksums (`checksums.txt`), and the rendered book PDF (if mdbook-pdf is configured) to the GitHub release.
4. Generate release notes from the CHANGELOG and the commits since the previous tag.

The release-gate criteria — what has to hold before tagging — are documented in [PLAN.md §"v1.0 (M4)"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md). The most important: full E2E green for 3 consecutive nights on the release branch.

## Cross-references

- [Chapter 4 — Installation](./04-installation.md) — for users who just want the binary, not the source.
- [Chapter 17 §":dev tag resolution"](./17-execution-backends.md#dev-tag-resolution) — how a from-source binary picks tool images.
- [Chapter 32 — Extending roksbnkctl](./32-extending-roksbnkctl.md) — once you've built, what to actually hack on.
- [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) — the release-gate policy.
