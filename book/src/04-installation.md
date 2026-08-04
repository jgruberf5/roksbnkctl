# Installation

This chapter gets `roksbnkctl` onto your machine and verifies it works. Three install paths are covered: build-from-source (native Go), build-with-Docker (no host Go required), and the **all-in-one container image** (no install at all — run roksbnkctl and every tool it needs from one container).

Pre-built binaries are attached to every [GitHub Release](https://github.com/jgruberf5/roksbnkctl/releases) (Linux, macOS, Windows × amd64, arm64). The book also ships as an offline PDF (`roksbnkctl-book-<tag>.pdf`) on the same release page. A Homebrew tap is on the v1.x roadmap; until then macOS users grab the binary from the release page or build from source.

## Quick install (one line)

The fastest path — downloads the latest release for your OS/arch and puts it on `PATH`:

**Linux / macOS:**

```bash
curl -fsSL https://raw.githubusercontent.com/jgruberf5/roksbnkctl/main/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/jgruberf5/roksbnkctl/main/install.ps1 | iex
```

Each script downloads the release archive, verifies its checksum, extracts the binary, runs `roksbnkctl install` to copy it into `~/.local/bin` (Linux/macOS) or a `PATH` directory (Windows) — **replacing** any existing copy — then deletes the archive, leaving only the installed binary. Options:

- **Pin a version:** set `VERSION=v1.23.1` before the command (e.g. `curl -fsSL … | VERSION=v1.23.1 sh`, or `$env:VERSION='v1.23.1'` on Windows).
- **Change the install directory / pass install flags:** set `ROKSBNKCTL_INSTALL_ARGS="--dir $HOME/bin"`.

Manage the installed binary with the built-in verbs:

- `roksbnkctl version` — prints the release (e.g. `v1.23.1`) for a release binary, or a git-derived build string for a local build.
- `roksbnkctl upgrade` — on a terminal, lists the newer releases on GitHub and lets you pick one; `--version vX.Y.Z` pins a specific release; `--yes` takes the latest non-interactively.
- `roksbnkctl uninstall` — removes the installed binary (the opposite of `install`).

The manual methods below (build-from-source, Docker build, runner container) remain available.

### Choosing an install method

| Method | You get | Host needs | Best for |
|---|---|---|---|
| **A — native build / release binary** | a `roksbnkctl` binary | Go 1.26+ (build) or nothing (release binary); the runtime tools you use ([above](#installing-prerequisites)) | a developer laptop |
| **B — Docker build** | a `roksbnkctl` binary | Docker only (for the build) | a host without Go; CI that wants a binary artefact |
| **C — runner container** | a container with roksbnkctl **+ every tool** | Docker/Podman + a volume for state | CI runners, fleet provisioning, air-gapped sites |

Methods A and B put a binary on `PATH` and assume the runtime tools are installed too. Method C ([Path C below](#path-c--run-from-the-all-in-one-container-image-no-install)) is self-contained: nothing on the host but a container runtime and a place to keep state.

## Prerequisites

- **Linux, macOS, or native Windows.** The full deploy path — cluster, BNK, and testing phases — runs on **native Windows without WSL**: roksbnkctl drives Terraform's provisioning glue itself (no `curl`/`tar`/`grep`/`bash` on the host), and the whole `up` → shared-Transit-Gateway → `down` flow is validated on Windows PowerShell. A few *interactive* conveniences (the TTY-bound SSH shell, ssh-agent integration) are still not first-class on Windows; use `--on`/backends or a Linux/macOS host for those.
- **Git** to clone the repository (only if building from source — not needed if you grab a pre-built binary).
- **Go 1.26 or newer** if you want a native build. If you don't have Go (or have an older version), use the Docker-based build or a pre-built release binary.
- **Terraform >= 1.5 on PATH** at runtime — required for `roksbnkctl up` / `plan` / `apply` / `down`.
- **Helm 3 on PATH** at runtime — `doctor` flags it as required during `roksbnkctl up`. The charts install via the `helm_release` resource (in-process via the `hashicorp/helm` provider — see [Chapter 10 §"The terraform-native deployment model"](./10-deploying-bnk-trials.md#the-terraform-native-deployment-model)), but FAR chart-version discovery still shells out to host `helm`: the `data.external.versions` lookup runs `helm registry login` + `helm pull` to read the FLO/CIS chart versions. So `helm` is genuinely required. Keep `helm` installed to stay `doctor`-green.

The remaining tools (`ibmcloud`, `kubectl`, `oc`, `iperf3`, `docker`) are optional and only needed for the corresponding passthrough or backend.

You do not need Docker installed to *use* `roksbnkctl` with the default `local` backend. Docker is required only if you opt in to `--backend docker` for `terraform` / `ibmcloud`. The k8s and ssh backends are alternatives that need neither host Docker nor host Go.

## Installing prerequisites

Install paths per platform. `terraform` and `helm` are flagged required by `doctor` (`helm` is shelled out for FAR chart-version discovery during `roksbnkctl up` — keep it installed to stay green); the rest are optional, install only what you need.

### macOS — Homebrew

```bash
brew install terraform               # required
brew install helm                    # flagged required by doctor — FAR chart-version discovery shells out to `helm`
brew install --cask ibmcloud-cli     # optional — only for `roksbnkctl ibmcloud …` passthrough
brew install kubectl                 # optional — only for `roksbnkctl kubectl …` passthrough (`roksbnkctl k *` is internalised)
brew install iperf3                  # optional — only for `--backend local`/`--backend ssh:<t>` throughput tests

# oc (Red Hat OpenShift CLI) — optional, only for `roksbnkctl oc …` passthrough.
# No brew formula; install via the Red Hat mirror tarball:
curl -sSL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-mac.tar.gz \
  | sudo tar -xz -C /usr/local/bin oc
```

If you installed `ibmcloud-cli`, add the plugins roksbnkctl uses:

```bash
ibmcloud plugin install kubernetes-service -f
ibmcloud plugin install cloud-object-storage -f
```

### Linux — Ubuntu / Debian

```bash
# terraform — required
wget -qO- https://apt.releases.hashicorp.com/gpg \
  | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] \
https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
  | sudo tee /etc/apt/sources.list.d/hashicorp.list
sudo apt-get update && sudo apt-get install -y terraform

# helm 3 — flagged required by doctor (FAR chart-version discovery shells out to `helm` to read the FLO/CIS chart versions)
curl https://baltocdn.com/helm/signing.asc \
  | sudo gpg --dearmor -o /usr/share/keyrings/helm.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/helm.gpg] \
https://baltocdn.com/helm/stable/debian/ all main" \
  | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list
sudo apt-get update && sudo apt-get install -y helm

# ibmcloud CLI + plugins — optional, for `roksbnkctl ibmcloud …` passthrough with --backend local
curl -fsSL https://clis.cloud.ibm.com/install/linux | sudo sh
ibmcloud plugin install kubernetes-service -f
ibmcloud plugin install cloud-object-storage -f

# kubectl — optional, only for `roksbnkctl kubectl <args>` passthrough (`roksbnkctl k *` is internalised and needs no host install)
sudo snap install kubectl --classic
# or via direct download:
# curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
# chmod +x kubectl && sudo mv kubectl /usr/local/bin/

# oc (Red Hat OpenShift CLI) — optional, only for `roksbnkctl oc <args>` passthrough.
# No apt package; install via the Red Hat mirror tarball:
curl -sSL https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz \
  | sudo tar -xz -C /usr/local/bin oc

# iperf3 — optional, only for `--backend local` / `--backend ssh:<t>` throughput tests
sudo apt-get install -y iperf3
```

Instructions above target Ubuntu and Debian. For other Linux distributions (RHEL, Fedora, Arch, openSUSE, Alpine, …), a quick online search for "install terraform on _&lt;your distro&gt;_" — and the same pattern for `ibmcloud`, `kubectl`, and `iperf3` — yields the equivalent commands. HashiCorp ships an RPM repo at <https://rpm.releases.hashicorp.com> covering RHEL/Fedora, and most distributions package `kubectl` and `iperf3` in their official repos; the IBM Cloud CLI installer at <https://clis.cloud.ibm.com/install/linux> is a single curl-pipe-sh that works across distros.

### Windows — Chocolatey

```powershell
choco install terraform
choco install kubernetes-helm  # required — terraform local-exec provisioner shells out to `helm`
choco install ibmcloud-cli     # optional
choco install kubernetes-cli   # optional, provides kubectl
choco install openshift-cli    # optional, provides oc (Red Hat OpenShift CLI)
choco install iperf3           # optional
```

Or via [Scoop](https://scoop.sh/):

```powershell
scoop install terraform helm ibmcloud-cli kubernetes-cli openshift-cli iperf3
```

If `choco`/`scoop` don't carry `openshift-cli` for your version, grab the Windows tarball from the Red Hat mirror directly:

```powershell
Invoke-WebRequest -Uri https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-windows.zip -OutFile oc.zip
Expand-Archive oc.zip -DestinationPath "$env:USERPROFILE\bin\"
# then add %USERPROFILE%\bin to your PATH
```

After installing `ibmcloud-cli`, add the plugins:

```powershell
ibmcloud plugin install kubernetes-service -f
ibmcloud plugin install cloud-object-storage -f
```

Windows TTY-bound SSH features (the `roksbnkctl shell --on <target>` interactive path) have known limitations on Windows; file-based SSH keys + non-interactive commands work, but `ssh-agent` named-pipe integration is a v1.x item. See [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0".

## Path A — native build (requires Go 1.26+)

If `go version` reports `1.26` or newer, this is the simplest path:

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

If `make build` fails, the most likely cause is **Go too old**. The module declares `go 1.26.0` in `go.mod` (forced by transitive deps from the SSH/integration test layers); older versions error out with `go: module requires Go 1.26`. Either upgrade Go or fall back to the Docker path below.

## Path B — Docker-based build (no host Go required)

This path is ideal for sealed CI workstations, custom VM images, or anywhere installing Go on the host is awkward. The official `golang:1.26-alpine` image has everything needed (the minimum tracks whatever the transitive dependencies demand — currently the `k8s.io/*` v0.36 modules, which declare `go 1.26`); the build artefact lands in `./bin/` owned by your host user.

```bash
git clone https://github.com/jgruberf5/roksbnkctl.git
cd roksbnkctl

docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  golang:1.26-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl ./cmd/roksbnkctl'

./bin/roksbnkctl install
```

Anatomy of the docker invocation:

| Flag | Why |
|---|---|
| `-v "$PWD:/work"` | Bind-mount the repo into the container at `/work`. |
| `-w /work` | Container working directory matches the mount. |
| `--user "$(id -u):$(id -g)"` | Output binary is owned by your host user, not root. |
| `-e HOME=/tmp` | Go writes its module cache under `$HOME`; `/tmp` is writable by any user. Without this, `go mod tidy` fails on a writable-`/root` permission error. |
| `golang:1.26-alpine` | Pinned major version; matches `go.mod`'s minimum. |

### Cross-compile via Docker

Set `GOOS` / `GOARCH` env vars in the same `docker run` to produce binaries for other platforms:

```bash
# macOS arm64 (Apple Silicon)
docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOOS=darwin -e GOARCH=arm64 \
  golang:1.26-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl-darwin-arm64 ./cmd/roksbnkctl'

# Windows amd64 (compile-only; not tested at runtime)
docker run --rm -v "$PWD:/work" -w /work \
  --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e GOOS=windows -e GOARCH=amd64 \
  golang:1.26-alpine sh -c 'go mod tidy && go build -o bin/roksbnkctl.exe ./cmd/roksbnkctl'
```

Each binary is statically linked (Alpine + `CGO_ENABLED=0` is the cross-compile default) so the produced file has no runtime library dependencies.

## Path C — run from the all-in-one container image (no install)

If you'd rather not put a binary and its tools on the host at all — CI runners, fleet provisioning, air-gapped sites — use the **runner image**. It carries `roksbnkctl` plus every tool it dispatches (`terraform`, `helm`, `kubectl`, `oc`, `ibmcloud`, `iperf3`, `h2load`), so one `docker run` is a complete roksbnkctl with nothing on the host:

```bash
docker run --rm \
  -v bnk-state:/work \
  -e IBMCLOUD_API_KEY="$IBMCLOUD_API_KEY" \
  ghcr.io/jgruberf5/roksbnkctl-tools-runner:latest version
```

`podman run` works identically. The image's `ENTRYPOINT` is `roksbnkctl`, so everything after the image name is roksbnkctl's own arguments (`version`, `up`, `test matrix --dry-run`, …). Tags: `:latest` and a per-release `:vX.Y.Z` are published to GHCR on each release; `:dev` tracks `main`.

### State lives in a volume, not the image

roksbnkctl keeps its workspaces — `config.yaml`, terraform state, kubeconfigs, keys — under `ROKSBNKCTL_HOME`, which the image sets to `/work/.roksbnkctl`. **The image itself is stateless**; mount durable storage at `/work` or every run starts empty:

- **A named volume (recommended)** — `-v bnk-state:/work`. Docker seeds a fresh named volume from the image's `/work`, which is owned by the image's `uid 1000`, so it's writable out of the box.
- **A host bind-mount** — `-v "$PWD/bnk-state:/work"` — carries the *host* directory's ownership, which may not be writable by `uid 1000`. Pre-create it writable (`mkdir -p bnk-state && chmod 0777 bnk-state`, or `chown` it to `1000`), or add `--user "$(id -u):$(id -g)"`. This is the usual docker bind-mount caveat, not a roksbnkctl quirk.

For a *truly* stateless runner — no volume at all — a COS/S3 remote state backend is the planned answer (PRD 16); until it lands, mount a volume.

### Use `--backend local` inside the runner

Every tool roksbnkctl dispatches is already on `PATH` in the image, so the docker/k8s execution backends (whose whole job is to fetch a tool from *elsewhere*) aren't needed here — runs are local, with no docker-in-docker. [Chapter 18 — Choosing a backend](./18-choosing-backend.md) covers when the other backends matter (the binary-on-host case).

### What's not in it

The runner is a *use* image, not a docs/dev-build image — the book toolchain (mdbook, pandoc, texlive) is deliberately excluded to keep it lean. Build the book with the separate `roksbnkctl-tools-mdbook` image (see [Chapter 31 — Building from source](./31-building-from-source.md)).

Because it's one self-contained artefact, the runner is also the natural thing to mirror behind a firewall for an air-gapped install — pair it with the registry mirror ([Chapter 10a — Air-gapped install](./10a-air-gapped-install.md)).

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
roksbnkctl v1.0.0 (commit abc1234, built 2026-05-10T14:22:08Z)
Docs: https://jgruberf5.github.io/roksbnkctl/book/
```

The version string is populated via `-ldflags` at build time; `make build VERSION=v1.0.0` injects an explicit tag. A bare `make build` produces something like `dev (commit abc1234, built ...)`. The `Docs:` URL is a compile-time constant (`internal/cli/meta.go::DocsURL`) — every binary built from this tree points at the same book URL.

### `roksbnkctl doctor`

```bash
roksbnkctl doctor
```

`doctor` runs the prereq + credentials report. Sample output on a healthy machine looks like this (yours will differ depending on which optional binaries you have installed and whether you've initialised a workspace):

```
✓  terraform         /usr/bin/terraform (Terraform v1.15.2)                                   (required for `roksbnkctl up`)
✓  helm              /usr/local/bin/helm (v3.20.2)                                            (required for `roksbnkctl up`; terraform `local-exec` provisioners shell out to helm)
⚠  iperf3            not on PATH                                                              (needed for `roksbnkctl test throughput`)
✓  kubectl           /usr/local/bin/kubectl (clientVersion:)                                  (optional; `roksbnkctl kubectl` passthrough)
✓  oc                /usr/local/bin/oc (Client Version: 4.21.10)                              (optional; `roksbnkctl oc` passthrough)
✓  ibmcloud          /usr/local/bin/ibmcloud (ibmcloud 2.43.0 ...)                            (optional; `roksbnkctl ibmcloud` passthrough)
✓  kubeconfig        /home/jgruber/.kube/config                                               (needed for cluster-side ops)
✓  workspace         default                                                                  (per-environment config + state)
✓  ibmcloud api key  resolved via OS keychain                                                 (auth for terraform + IBM SDK calls)
✓  ibm cloud auth    OK (account: Main F5 Account)                                            (verifies API key works against IBM IAM)
```

Each row is `<status> <name> <detail> <why we care>`. Failures are red `✗` and exit non-zero; warnings are yellow `⚠` and don't fail the run. `terraform` and `helm` are the hard-required checks at v1.0 — the rest are either optional passthroughs or specific to test suites. [Chapter 5](./05-doctor.md) walks through what each check is verifying and how to fix common failures.

## OS support matrix

| OS | Native build | Docker build | Cross-compile target | Runtime status |
|---|---|---|---|---|
| Linux (amd64, arm64) | yes | yes | yes | first-class |
| macOS (amd64, arm64) | yes | yes | yes | first-class |
| Windows (amd64, arm64) | yes | yes | yes | compile-only; `roksbnkctl shell --on` and `roksbnkctl exec --on jumphost` PTY behaviour limited |

"First-class" means the v1.0 acceptance criteria are validated on those platforms; "compile-only" means the binary builds and runs but interactive features (notably TTY-bound SSH) have known limitations and are not part of the v1.0 release gate.

The Windows limitations are tracked in PRD 01 (the SSH client design) and largely come down to `golang.org/x/crypto/ssh`'s incomplete PTY handling on Windows and the absence of an SSH agent named-pipe protocol. File-based SSH keys work; full PTY and ssh-agent integration on Windows are on the v1.x roadmap (see [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0").

## Required prerequisites — `terraform` and `helm` at v1.0

The v1.0 cluster lifecycle needs two binaries on `PATH`:

- **`terraform` (>= 1.5)** — hard-required for any cluster lifecycle command (`up`, `down`, `plan`, `apply`).
- **`helm` (3.x)** — flagged required by `doctor` during `roksbnkctl up`. The charts install via the `helm_release` resource — the `hashicorp/helm` provider speaks the Helm 3 protocol via an embedded Go runtime, so no host `helm` is shelled out for the chart installs (see [Chapter 10](./10-deploying-bnk-trials.md#the-terraform-native-deployment-model)). FAR chart-version discovery does shell out to host `helm`, though: the `data.external.versions` lookup runs `helm registry login` + `helm pull` to read the FLO/CIS chart versions, so `helm` is genuinely required. Keeping `helm` installed keeps `doctor` green.

Optional binaries — only needed for the corresponding passthrough or fallback path:

- **`iperf3`** — only needed for `--backend local` and `--backend ssh:<target>` throughput modes. The default `--backend k8s` runs iperf3 entirely in cluster (no host binary needed).
- **`kubectl`** / **`oc`** — only needed for the `roksbnkctl kubectl <args...>` / `roksbnkctl oc <args...>` passthroughs. The everyday verbs (`get`, `apply`, `describe`, `delete`, `logs`, `exec`, `port-forward`) are internalised under `roksbnkctl k` and need no host binary.
- **`ibmcloud`** — only needed for the `roksbnkctl ibmcloud <args...>` passthrough on `--backend local`. The cluster-lifecycle path uses IBM Go SDKs internally and does *not* shell out to `ibmcloud`. The `docker`, `k8s`, and `ssh` backends ship their own ibmcloud — no host install needed.
- **`docker`** — only needed for `--backend docker`. Optional; the `k8s` and `ssh` backends are alternatives if docker isn't available.

Run `roksbnkctl doctor` to see exactly what your environment is missing for the workflow you intend to run.

## Updating

`git pull && make build` is the source-build update mechanism (or re-run the Docker build for the containerised path).

`roksbnkctl self update` upgrades from a tagged GitHub release. Use it once you've installed an initial v1.0 binary:

```bash
roksbnkctl self update
# Checks https://github.com/jgruberf5/roksbnkctl/releases/latest, downloads
# the matching asset for your OS+arch, verifies the checksum, swaps the
# binary atomically.
```

## Next

With a working binary on PATH, [Chapter 5 — Doctor](./05-doctor.md) explains what every doctor check is looking at, [Chapter 6 — Workspaces](./06-workspaces.md) explains the `~/.roksbnkctl/<workspace>/` layout, and [Chapter 7 — Quick start](./07-quick-start.md) walks the 4-command lifecycle end-to-end.
