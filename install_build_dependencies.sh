#!/usr/bin/env bash
# install_build_dependencies.sh — Ubuntu/Debian host prereqs for the
# roksbnkctl build, test, and release workflow.
#
# Intended audience: integrators / contributors who clone the repo and
# need their host set up to run `make build`, `make release`,
# `scripts/e2e-test-full.sh`, and the binary itself (`roksbnkctl up`
# shells out to terraform + optionally ibmcloud).
#
# End users who only need to run a pre-built `roksbnkctl` binary from
# a GitHub release can skip the dev-utility installs (jq, unzip,
# python3, gnupg) and just install `terraform`. See chapter 4 of the
# book for the runtime-only install path.
#
# Run as a normal user; the script invokes sudo internally for the
# system-level changes (adding HashiCorp's apt repo + signing key,
# installing packages). You'll be prompted for your sudo password
# once at the start.
#
# Re-running is safe — each step skips if the tool is already
# installed at a suitable version. Sudo prompts are batched up front
# via `sudo -v` so the script doesn't pause halfway through.
#
# What this installs:
#   - terraform      (REQUIRED — roksbnkctl up's local backend needs it;
#                     installed via HashiCorp's apt repo, pinned to
#                     amd64 + your distro release)
#   - ibmcloud CLI   (REQUIRED — roksbnkctl ibmcloud … passthrough with
#                     the default --backend local; plus e2e Phase B/I
#                     steps; installed via IBM's clis.cloud.ibm.com/
#                     install/linux installer; plugins ks + cloud-
#                     object-storage installed after)
#   - jq             (e2e scripts parse `roksbnkctl test … -o json` output)
#   - unzip          (goreleaser windows archives + ad-hoc artifact unpack)
#   - gnupg          (needed to import HashiCorp's apt signing key)
#   - openssh-client (Phase I SSH backend coverage; usually present)
#   - python3        (find_bare_tags.py + ad-hoc scripting)
#
# What this DOES NOT install (deliberately):
#   - mdbook / mdbook-mermaid / mdbook-pandoc / pandoc / texlive / mermaid-cli
#       → bundled in `tools/docker/mdbook/Dockerfile`. Build once via
#         `make -C tools/docker build-mdbook`.
#   - goreleaser
#       → pulled at run-time from `goreleaser/goreleaser:latest` by
#         `make goreleaser-check` / `make goreleaser-snapshot`.
#   - iperf3
#       → bundled in `tools/docker/iperf3/` and runs via --backend k8s
#         (the throughput suite's one-shot Job). Host install unneeded.
# What this also installs:
#   - helm (Helm 3)   (REQUIRED — terraform's null_resource +
#                      local-exec provisioner for cert_manager / flo
#                      / cne_instance shells out to `helm upgrade
#                      --install`. Earlier doc framing claimed the
#                      terraform helm provider handled this with an
#                      embedded runtime; that's incorrect for the
#                      current HCL — the modules genuinely require
#                      host helm. Installed via Helm's apt repo
#                      pinned to amd64 + your distro.)
#   - kubectl
#       → Sprint 2 internalised the kubectl surface into `roksbnkctl k
#         get/apply/logs/exec/port-forward` via client-go; the binary
#         doesn't need a host kubectl. However the e2e scripts SHELL
#         OUT to host kubectl for cred-audit assertions (Phase M3/M4
#         scan `kubectl get events` + `kubectl logs <ops-pod>`), so
#         this script verifies kubectl is present and warns if it's
#         missing — but doesn't install it (kubectl install paths vary
#         widely: snap, krew, the official IBM Cloud ks plugin, etc.).
#
# What this also installs:
#   - oc (Red Hat OpenShift CLI) — REQUIRED for the e2e flow's
#     Phase B5 step (`roksbnkctl oc whoami` passthrough verifies
#     cluster-admin auth). The `roksbnkctl oc <args>` passthrough
#     shells out to host oc; without it the passthrough errors with
#     `Error: oc not found on PATH`. The everyday `roksbnkctl k *`
#     verbs (Sprint 2 internalised surface) don't need host oc and
#     work fine without it. Installed via Red Hat's official mirror
#     tarball (no apt package); we extract only the `oc` binary into
#     /usr/local/bin/ and leave the bundled kubectl alone to avoid
#     drift with the host's existing kubectl.
#
# After this script completes, run:
#   make build              # builds bin/roksbnkctl with ldflags
#   ./bin/roksbnkctl doctor # confirms green on this host
#
# Then proceed with `make release` (release-prep) or
# `scripts/e2e-test-full.sh` (live e2e against IBM Cloud).

set -euo pipefail

# ── log helpers ────────────────────────────────────────────────────────
log()  { printf "\n\033[1;34m▸ %s\033[0m\n" "$*"; }
ok()   { printf "  \033[1;32m✓\033[0m %s\n" "$*"; }
warn() { printf "  \033[1;33m!\033[0m %s\n" "$*"; }
err()  { printf "\n\033[1;31m✗ %s\033[0m\n" "$*" >&2; }

# ── platform gate ──────────────────────────────────────────────────────
require_ubuntu() {
    if ! command -v lsb_release >/dev/null 2>&1; then
        err "lsb_release not found — this script targets Ubuntu/Debian."
        exit 1
    fi
    local id
    id=$(lsb_release -is)
    if [[ "$id" != "Ubuntu" && "$id" != "Debian" ]]; then
        err "this script targets Ubuntu/Debian; detected $id"
        exit 1
    fi
    ok "platform: $id $(lsb_release -rs) ($(dpkg --print-architecture))"
}

# ── sudo priming ───────────────────────────────────────────────────────
# `sudo -v` prompts once and keeps the timestamp fresh for the rest of
# the script. Avoids mid-run prompts when the script is being driven
# unattended (e.g. by `make` or by Claude's Bash tool).
prime_sudo() {
    log "sudo priming (you'll be prompted once)"
    if ! sudo -v; then
        err "sudo authentication failed; aborting"
        exit 1
    fi
    ok "sudo cached"
}

# ── apt update guard ──────────────────────────────────────────────────
# Run apt-get update at most once, and only if at least one repo was
# newly added in this run.
_apt_dirty=0
_apt_updated=0
apt_mark_dirty() { _apt_dirty=1; }
apt_update_once() {
    if [[ "$_apt_updated" == "1" ]]; then return; fi
    if [[ "$_apt_dirty" == "0" ]]; then return; fi
    log "apt-get update"
    sudo apt-get update -qq
    _apt_updated=1
}

# ── installers (each idempotent) ───────────────────────────────────────

install_base_pkgs() {
    log "base packages — gnupg, jq, unzip, openssh-client, python3, ca-certificates, software-properties-common"
    local need=()
    for pkg in gnupg jq unzip openssh-client python3 ca-certificates software-properties-common; do
        if ! dpkg -s "$pkg" >/dev/null 2>&1; then
            need+=("$pkg")
        fi
    done
    if [[ ${#need[@]} -eq 0 ]]; then
        ok "all base packages already installed"
        return
    fi
    ok "installing: ${need[*]}"
    sudo apt-get update -qq
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${need[@]}"
    ok "base packages installed"
}

install_terraform() {
    log "terraform (HashiCorp apt repo)"
    if command -v terraform >/dev/null 2>&1; then
        local v
        v=$(terraform version | head -1 | awk '{print $2}')
        ok "terraform already installed: $v"
        return
    fi

    # 1. Import HashiCorp's signing key into a keyring file (the
    #    apt-key way is deprecated on noble).
    if [[ ! -f /usr/share/keyrings/hashicorp-archive-keyring.gpg ]]; then
        ok "importing HashiCorp GPG signing key"
        wget -qO- https://apt.releases.hashicorp.com/gpg \
            | sudo gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
    else
        ok "HashiCorp keyring already present"
    fi

    # 2. Add the repo definition. Pin to detected arch + distro
    #    codename so an Ubuntu lts upgrade doesn't silently break it.
    if [[ ! -f /etc/apt/sources.list.d/hashicorp.list ]]; then
        ok "adding HashiCorp apt repo for $(lsb_release -cs) $(dpkg --print-architecture)"
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
            | sudo tee /etc/apt/sources.list.d/hashicorp.list >/dev/null
        apt_mark_dirty
    else
        ok "HashiCorp apt repo already configured"
    fi

    # 3. apt-get update (if a new repo was added above)
    apt_update_once

    # 4. install
    ok "installing terraform"
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq terraform
    ok "terraform installed: $(terraform version | head -1)"
}

install_ibmcloud() {
    log "ibmcloud CLI + plugins (ks, cloud-object-storage)"
    if command -v ibmcloud >/dev/null 2>&1; then
        ok "ibmcloud already installed: $(ibmcloud --version | head -1)"
    else
        # IBM's official Linux installer. Drops `ibmcloud` at
        # /usr/local/bin/ibmcloud + supporting files at
        # /usr/local/Bluemix/. Needs sudo (the installer asks
        # interactively if not root; with sudo cached from prime_sudo,
        # piping through `sudo sh` avoids the prompt).
        ok "downloading + running IBM Cloud installer"
        curl -fsSL https://clis.cloud.ibm.com/install/linux | sudo sh
        if ! command -v ibmcloud >/dev/null 2>&1; then
            err "ibmcloud install completed but binary not on PATH; check /usr/local/bin/ + your shell rc"
            exit 1
        fi
        ok "ibmcloud installed: $(ibmcloud --version | head -1)"
    fi

    # Suppress the first-run telemetry / metrics prompts so plugin
    # installs and downstream e2e steps don't pause on stdin.
    ibmcloud config --check-version=false --usage-stats-collect false >/dev/null 2>&1 || true

    # Plugins: install if missing, skip if present. The `-f` flag
    # forces a non-interactive install (no "are you sure" prompt).
    local plugin
    for plugin in kubernetes-service cloud-object-storage; do
        if ibmcloud plugin list -q 2>/dev/null | awk '{print $1}' | grep -qx "$plugin"; then
            ok "ibmcloud plugin already installed: $plugin"
        else
            ok "installing ibmcloud plugin: $plugin"
            ibmcloud plugin install "$plugin" -f -q
        fi
    done
}

install_oc() {
    log "oc (Red Hat OpenShift CLI, from the Red Hat mirror)"
    if command -v oc >/dev/null 2>&1; then
        ok "oc already installed: $(oc version --client 2>/dev/null | head -1)"
        return
    fi

    # No apt package on Ubuntu/Debian; pull the stable tarball from
    # Red Hat's mirror and extract only the `oc` binary into
    # /usr/local/bin/. Skip the bundled kubectl (the host already has
    # one from snap or apt; overwriting it would risk version drift).
    local url=https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz
    ok "downloading $url"
    if curl -sSL "$url" | sudo tar -xz -C /usr/local/bin oc; then
        ok "oc installed: $(oc version --client 2>/dev/null | head -1)"
    else
        err "oc install failed; check network access to mirror.openshift.com"
        exit 1
    fi
}

install_helm() {
    log "helm (Helm 3, official apt repo)"
    if command -v helm >/dev/null 2>&1; then
        ok "helm already installed: $(helm version --short 2>/dev/null || helm version)"
        return
    fi

    # Import Helm's signing key into a keyring file (apt-key deprecated
    # on noble).
    if [[ ! -f /usr/share/keyrings/helm.gpg ]]; then
        ok "importing Helm GPG signing key"
        curl -fsSL https://baltocdn.com/helm/signing.asc \
            | sudo gpg --dearmor -o /usr/share/keyrings/helm.gpg
    else
        ok "Helm keyring already present"
    fi

    # Add Helm's apt repo. The "all main" pattern is Helm's standard
    # (single distribution covering every Debian-derived release).
    if [[ ! -f /etc/apt/sources.list.d/helm-stable-debian.list ]]; then
        ok "adding Helm apt repo for $(dpkg --print-architecture)"
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/helm.gpg] https://baltocdn.com/helm/stable/debian/ all main" \
            | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list >/dev/null
        apt_mark_dirty
    else
        ok "Helm apt repo already configured"
    fi

    apt_update_once
    ok "installing helm"
    sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq helm
    ok "helm installed: $(helm version --short)"
}

# ── verification ───────────────────────────────────────────────────────
verify() {
    log "verification — required"
    local missing=0
    # Required: terraform (local backend), ibmcloud (--backend local
    # passthrough), jq (script JSON parsing), the base utilities the
    # e2e scripts rely on.
    for cmd in terraform ibmcloud helm oc jq unzip ssh python3 gpg docker gh go git make; do
        if command -v "$cmd" >/dev/null 2>&1; then
            ok "$cmd: $(command -v "$cmd")"
        else
            warn "$cmd: MISSING"
            missing=1
        fi
    done

    log "verification — recommended (NOT installed by this script)"
    # kubectl: the e2e scripts shell out for cred-audit assertions
    # (Phase M3/M4). roksbnkctl itself has client-go internalised so
    # the binary doesn't need it, but the .sh drivers do.
    if command -v kubectl >/dev/null 2>&1; then
        ok "kubectl: $(command -v kubectl)"
    else
        warn "kubectl: MISSING — install via snap (\`sudo snap install kubectl --classic\`)"
        warn "                  or via the IBM Cloud ks plugin (\`ibmcloud ks cluster ls\` will fetch it)"
        warn "                  Required by e2e Phase M cred-audit assertions."
    fi

    if [[ "$missing" == "1" ]]; then
        err "some required tools are still missing — re-run or install manually"
        exit 1
    fi
}

# ── post-install hints ─────────────────────────────────────────────────
hints() {
    log "next steps"
    cat <<'EOF'
  Confirm doctor green:
      make build && ./bin/roksbnkctl doctor

  Build the release-time book image (one-time, ~7 min):
      make -C tools/docker build-mdbook

  Dry-run the full e2e against your .env:
      set -a && . ./.env && set +a && DRY_RUN=1 ./scripts/e2e-test-full.sh

  Real run (4-6 hours, ~$5-10 IBM Cloud spend):
      set -a && . ./.env && set +a && ./scripts/e2e-test-full.sh

  Release-prep driver (book + PDF + goreleaser-snapshot + Pages assure):
      make release
EOF
}

# ── main ───────────────────────────────────────────────────────────────
main() {
    require_ubuntu
    prime_sudo
    install_base_pkgs
    install_terraform
    install_ibmcloud
    install_helm
    install_oc
    verify
    hints
}

main "$@"
