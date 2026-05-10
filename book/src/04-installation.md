# Installation

This chapter gets a `roksbnkctl` binary onto your machine and verifies it works. Two install paths are covered: build-from-source (native Go, the canonical path until release artefacts ship) and build-with-Docker (no host Go required).

A tagged release with pre-built binaries, a `brew` tap, and an `install.sh` one-liner is on the roadmap but not yet shipped. Until then, building from source is the supported install path.

## Prerequisites

- **Linux or macOS** for the day-to-day developer experience. Windows compiles cleanly but interactive features (TTY-bound SSH shell, ssh-agent integration) are not first-class on Windows yet.
- **Git** to clone the repository.
- **Go 1.25 or newer** if you want a native build. If you don't have Go (or have an older version), use the Docker-based build below.
- **Terraform >= 1.5 on PATH** at runtime — required for `roksbnkctl up` / `plan` / `apply` / `down`. This is the only required external prerequisite for the v0.7 happy path; everything else (`ibmcloud`, `kubectl`, `oc`, `iperf3`) is optional and only needed for the corresponding passthrough or test command.

You do not need Docker installed to *use* `roksbnkctl`. Docker is only used here as a convenience for building the binary without touching your host Go install.

## Path A — native build (requires Go 1.25+)

If `go version` reports `1.25` or newer, this is the simplest path:

```bash
git clone https://github.com/jgruberf5/roksbnkctl.git
cd roksbnkctl

go mod tidy                          # first time only — populates go.sum
make build                           # → bin/roksbnkctl

# Install via roksbnkctl itself (recommended — copies into ~/.local/bin):
./bin/roksbnkctl install
```

That's the whole thing. The `install` subcommand is idempotent and copies the running binary into a directory on your `PATH`. Default destination is `~/.local/bin/roksbnkctl`.

Make targets you'll use most often:

```
make build      # go build -ldflags ... -o bin/roksbnkctl ./cmd/roksbnkctl
make test       # go test ./...
make vet        # go vet ./...
make tidy       # go mod tidy
make clean      # rm -rf bin/
```

If `make build` fails, the most likely cause is **Go too old**. The module declares `go 1.25.0` in `go.mod` (forced by transitive deps from the SSH/integration test layers); older versions error out with `go: module requires Go 1.25`. Either upgrade Go or fall back to the Docker path below.

## Path B — Docker-based build (no host Go required)

This path is ideal for sealed CI workstations, custom VM images, or anywhere installing Go on the host is awkward. The official `golang:1.25-alpine` image has everything needed (Sprint 1 bumped the minimum Go version from 1.23 to 1.25 because of `testcontainers-go` and `gliderlabs/ssh` transitive dependencies); the build artefact lands in `./bin/` owned by your host user.

```bash
git clone https://github.com/jgruberf5/roksbnkctl.git
cd roksbnkctl

docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  golang:1.25-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl ./cmd/roksbnkctl'

./bin/roksbnkctl install
```

Anatomy of the docker invocation:

| Flag | Why |
|---|---|
| `-v "$PWD:/work"` | Bind-mount the repo into the container at `/work`. |
| `-w /work` | Container working directory matches the mount. |
| `--user "$(id -u):$(id -g)"` | Output binary is owned by your host user, not root. |
| `-e HOME=/tmp` | Go writes its module cache under `$HOME`; `/tmp` is writable by any user. Without this, `go mod tidy` fails on a writable-`/root` permission error. |
| `golang:1.25-alpine` | Pinned major version; matches `go.mod`'s minimum. |

### Cross-compile via Docker

Set `GOOS` / `GOARCH` env vars in the same `docker run` to produce binaries for other platforms:

```bash
# macOS arm64 (Apple Silicon)
docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOOS=darwin -e GOARCH=arm64 \
  golang:1.25-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl-darwin-arm64 ./cmd/roksbnkctl'

# Windows amd64 (compile-only; not tested at runtime)
docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOOS=windows -e GOARCH=amd64 \
  golang:1.25-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl.exe ./cmd/roksbnkctl'
```

Each binary is statically linked (Alpine + `CGO_ENABLED=0` is the cross-compile default) so the produced file has no runtime library dependencies.

## The `install` subcommand

```bash
roksbnkctl install [--dir PATH] [--force]
```

`install` copies the running binary into a directory on `PATH`. Defaults:

- **Destination**: `~/.local/bin/roksbnkctl` — this directory is on the default `PATH` for most modern Linux and macOS user environments and does not require sudo.
- **Mode**: `0755`.
- **Idempotent**: if the running binary is already at the destination, no-op (no error).

Override the destination with `--dir`:

```bash
./bin/roksbnkctl install --dir ~/bin
sudo ./bin/roksbnkctl install --dir /usr/local/bin
```

`--force` overwrites an existing file at the destination. Without it, `install` refuses if the destination is a different binary.

If `~/.local/bin` is not on your `PATH`, add it. On bash:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
exec $SHELL -l
```

On zsh, swap `~/.bashrc` for `~/.zshrc`.

## Verifying the install

Two quick checks: version (proves the binary runs) and `doctor` (proves the runtime environment is set up for actual work).

### `roksbnkctl version`

```bash
roksbnkctl version
```

Sample output:

```
roksbnkctl v0.7.0 (commit abc1234, built 2026-05-08T14:22:08Z)
```

The version string is populated via `-ldflags` at build time; `make build VERSION=v0.7.0` injects an explicit tag. A bare `make build` produces something like `dev (commit abc1234, built ...)`.

### `roksbnkctl doctor`

```bash
roksbnkctl doctor
```

`doctor` runs the prereq + credentials report. Sample output on a healthy machine looks like this (yours will differ depending on which optional binaries you have installed and whether you've initialised a workspace):

```
✓  terraform         /usr/bin/terraform (Terraform v1.15.2)                                   (required for `roksbnkctl up`)
⚠  iperf3            not on PATH                                                              (needed for `roksbnkctl test throughput`)
✓  kubectl           /usr/local/bin/kubectl (clientVersion:)                                  (optional; `roksbnkctl kubectl` passthrough)
✓  oc                /usr/local/bin/oc (Client Version: 4.21.10)                              (optional; `roksbnkctl oc` passthrough)
✓  ibmcloud          /usr/local/bin/ibmcloud (ibmcloud 2.43.0 ...)                            (optional; `roksbnkctl ibmcloud` passthrough)
✓  kubeconfig        /home/jgruber/.kube/config                                               (needed for cluster-side ops)
✓  workspace         default                                                                  (per-environment config + state)
✓  ibmcloud api key  resolved via OS keychain                                                 (auth for terraform + IBM SDK calls)
✓  ibm cloud auth    OK (account: Main F5 Account)                                            (verifies API key works against IBM IAM)
```

Each row is `<status> <name> <detail> <why we care>`. Failures are red `✗` and exit non-zero; warnings are yellow `⚠` and don't fail the run. `terraform` is the only check that's hard-required for v0.7 — the rest are either optional passthroughs or specific to test suites. [Chapter 5](./05-doctor.md) walks through what each check is verifying and how to fix common failures.

## OS support matrix

| OS | Native build | Docker build | Cross-compile target | Runtime status |
|---|---|---|---|---|
| Linux (amd64, arm64) | yes | yes | yes | first-class |
| macOS (amd64, arm64) | yes | yes | yes | first-class |
| Windows (amd64, arm64) | yes | yes | yes | compile-only; `roksbnkctl shell --on` and `roksbnkctl exec --on jumphost` PTY behaviour limited |

"First-class" means the v0.7 acceptance criteria are validated on those platforms; "compile-only" means the binary builds and runs but interactive features (notably TTY-bound SSH) have known limitations and are not part of the v0.7 release gate.

The Windows limitations are tracked in PRD 01 (the SSH client design) and largely come down to `golang.org/x/crypto/ssh`'s incomplete PTY handling on Windows and the absence of an SSH agent named-pipe protocol. File-based SSH keys work; ssh-agent integration on Windows is post-v1.0.

## Required prerequisites — only `terraform`, post-v0.8

The v0.7 surface still requires a few external binaries on `PATH` if you want everything to work:

- **`terraform` (>= 1.5)** — hard-required for any cluster lifecycle command.
- **`iperf3`** — required for `roksbnkctl test throughput` until v0.9 internalises it via the k8s execution backend.
- **`kubectl`** — required for `roksbnkctl kubectl <args...>` passthrough until v0.8 internalises it.
- **`oc`** — same as `kubectl`, passthrough only.
- **`ibmcloud`** — required for `roksbnkctl ibmcloud <args...>` passthrough; the cluster-lifecycle path uses IBM Go SDKs internally and does *not* shell out to `ibmcloud`, so you can skip this binary if you don't need the passthrough.

After v0.8 lands the picture simplifies: `terraform` is the only required external dependency, and every other tool moves into the "optional passthrough" category. Until then, run `roksbnkctl doctor` and install whatever it warns about for the workflow you intend to run.

## Updating

For now, `git pull && make build` is the update mechanism (or re-run the Docker build).

The `roksbnkctl self update` subcommand exists but only works against tagged GitHub releases; it'll be useful once v0.7 is tagged.

## Next

With a working binary on PATH, [Chapter 5 — Doctor](./05-doctor.md) explains what every doctor check is looking at, [Chapter 6 — Workspaces](./06-workspaces.md) explains the `~/.roksbnkctl/<workspace>/` layout, and [Chapter 7 — Quick start](./07-quick-start.md) walks the 3-command happy path end-to-end.
