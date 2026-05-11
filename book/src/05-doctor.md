# Doctor: checking your environment

`roksbnkctl doctor` is the prereq + credentials report. It runs in under five seconds, exits non-zero on any hard error, and prints a tabular report that maps one-to-one to the runtime dependencies the rest of the tool reaches for.

This chapter walks every check, explains what each row's "why we care" blurb means, covers the post-Sprint 2 changes that move kubectl and oc from "needed" to "informational", and describes the `--target` SSH probe added in Sprint 1.

## What `doctor` checks

A bare `roksbnkctl doctor` runs the **general** checks: tooling on `PATH`, kubeconfig location, the resolved workspace, and the IBM Cloud authentication chain. Sample output on a healthy machine post-Sprint 2 looks like this:

```
roksbnkctl doctor
✓  terraform         /usr/bin/terraform (Terraform v1.15.2)                                   (required for `roksbnkctl up`)
✓  helm              /usr/local/bin/helm (v3.20.2)                                            (required for `roksbnkctl up`; terraform `local-exec` shells out to helm)
⚠  iperf3            not on PATH                                                              (needed for `roksbnkctl test throughput`)
✓  kubectl           /usr/local/bin/kubectl (clientVersion:)                                  (internalised in roksbnkctl k *; passthrough still works if installed)
✓  oc                /usr/local/bin/oc (Client Version: 4.21.10)                              (internalised in roksbnkctl k *; passthrough still works if installed)
✓  ibmcloud          /usr/local/bin/ibmcloud (ibmcloud 2.43.0 ...)                            (optional; `roksbnkctl ibmcloud` passthrough)
✓  kubeconfig        /home/jgruber/.kube/config                                               (needed for cluster-side ops)
✓  workspace         default                                                                  (per-environment config + state)
✓  ibmcloud api key  resolved                                                                 (auth for terraform + IBM SDK calls)
✓  ibm cloud auth    OK (account: 1a2b3c..., user: you@example.com)                           (verifies API key works against IBM IAM)
```

Each row has the same shape:

```
<status> <name> <detail> <why we care>
```

- **status** is one of `✓` (green / OK), `⚠` (yellow / warning), or `✗` (red / error). `Skipped` checks render as `⚠`.
- **name** is the dependency or capability being checked.
- **detail** is the resolved value — usually a path, a version line, or an error message.
- **why we care** is a parenthetical clause naming the `roksbnkctl` feature that depends on this row.

The `BackendName` column on the underlying `Check` struct ([`internal/doctor/check.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/doctor/check.go)) is reserved for the per-backend probes that land in Sprint 4 (PRD 03). Until then it stays empty for every general check.

## Each check explained

### `terraform` — required

One of two **hard-required** binaries for the `roksbnkctl up` happy path. `roksbnkctl` shells out to `terraform` via `terraform-exec` for plan/apply/destroy; without it nothing in the cluster lifecycle works.

Pass condition: a binary on `PATH`, version `1.5` or newer.

Failure mode: `not on PATH`. Fix: install Terraform from [terraform.io](https://www.terraform.io/downloads), or your distro's package manager, then re-run `doctor`.

### `helm` — required

The second **hard-required** binary, added in v1.0.2. The bundled terraform modules (`cert_manager`, `flo`, `cne_instance`) use `null_resource` + `local-exec` provisioners that shell out to `helm upgrade --install` from inside terraform's apply phase. Without `helm` on `PATH`, the apply fails partway through the cluster lifecycle with:

```
Error: local-exec provisioner error
Error running command 'helm upgrade --install cert-manager ...':
exit status 127. Output: /bin/sh: 1: helm: not found
```

Pass condition: a `helm` (v3.x) binary on `PATH`. Doctor parses `helm version --short` for the version detail.

Failure mode: `not on PATH`. Fix: install Helm 3 from [helm.sh/docs/intro/install/](https://helm.sh/docs/intro/install/), or via your distro's package manager:

```bash
# Linux (Ubuntu/Debian — official Helm apt repo):
curl https://baltocdn.com/helm/signing.asc | sudo gpg --dearmor -o /usr/share/keyrings/helm.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/helm.gpg] https://baltocdn.com/helm/stable/debian/ all main" | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list
sudo apt-get update && sudo apt-get install -y helm

# macOS:
brew install helm

# Windows:
choco install kubernetes-helm
```

A v1.x effort to refactor the `cert_manager` / `flo` / `cne_instance` modules onto the `helm_release` terraform resource type (which uses the `hashicorp/helm` provider's embedded Helm 3 runtime) would eliminate this host requirement. Tracked in [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0".

### `iperf3` — informational

Used only by `roksbnkctl test throughput` in its host-iperf3 modes. After Sprint 4 lands the k8s execution backend (PRD 03), `iperf3` moves entirely in-cluster and this row goes away for the everyday user.

Failure mode: `not on PATH`. Fix: install iperf3 if you plan to use the throughput test today; otherwise ignore.

### `kubectl` — informational (Sprint 2 change)

**Before Sprint 2:** `kubectl` was an optional warning when missing — useful for the `roksbnkctl kubectl` passthrough.

**After Sprint 2:** `kubectl` is informational. The everyday verbs (`get`, `apply`, `describe`, `delete`, `logs`, `exec`, `port-forward`) are now native Go via `client-go` and live under [`roksbnkctl k`](./24-day-2-ops.md). Missing host `kubectl` no longer disables the happy path; it only disables the `roksbnkctl kubectl <args...>` passthrough.

If `kubectl` is on `PATH`, the row is still `✓` and shows the version line. If it's missing, the row is informational, not a warning, and the detail explains where the equivalent functionality lives.

### `oc` — informational (Sprint 2 change)

Same story as `kubectl` — Sprint 2 internalises the OpenShift-relevant verbs (Phase 2.1 adds `Project`, `Route`, `ImageStream` to `roksbnkctl k get`). Host `oc` is preserved as an escape hatch; missing `oc` no longer warns.

### `ibmcloud` — optional

Required only for the `roksbnkctl ibmcloud <args...>` passthrough. The cluster-lifecycle path uses IBM Go SDKs internally — `roksbnkctl up` does **not** shell out to `ibmcloud` — so you can skip this binary if you don't need the passthrough.

### `kubeconfig`

Resolves the kubeconfig path via `$KUBECONFIG` first, then `~/.kube/config`. Cluster-side commands (`status`, `logs`, every `k <verb>`) need it.

`roksbnkctl up` writes the admin kubeconfig at `~/.kube/config` (mode 0600) on a fresh apply. If you already have a multi-cluster `~/.kube/config`, point `$KUBECONFIG` at the workspace's state directory instead:

```bash
export KUBECONFIG=~/.roksbnkctl/<workspace>/state/kubeconfig
```

Failure mode: `$KUBECONFIG and ~/.kube/config both missing`. Fix: run `roksbnkctl kubeconfig --download` to fetch the admin kubeconfig from IBM Cloud.

### `workspace`

Reports the resolved workspace name and whether its `config.yaml` exists.

- `✓ default` — the current workspace pointer at `~/.roksbnkctl/config.yaml` resolves and the named workspace has a populated `config.yaml`.
- `⚠ "default" not initialised` — the directory may exist (created by `roksbnkctl ws new`) but `config.yaml` is empty. Run `roksbnkctl init` to populate.
- `✗ no config context` — the global config can't be loaded at all.

The one-off `-w / --workspace` flag overrides which workspace `doctor` reports against. See [Chapter 6 — Workspaces](./06-workspaces.md).

### `ibmcloud api key`

Resolves the API key via the chain documented in [Chapter 14 — Credentials](./14-credentials-resolver.md): env var → OS keychain → workspace config (base64) → TTY prompt.

Pass condition: the chain produces a non-empty key. The key value is **never** printed — only the source ("resolved").

Failure mode: `IBMCLOUD_API_KEY unset and no keychain entry for workspace "<name>"`. Fix: either `export IBMCLOUD_API_KEY=...` for the session, or re-run `roksbnkctl init` and accept the keychain-save prompt.

### `ibm cloud auth`

Round-trips the resolved key against IBM IAM via the SDK (`Verify()` call). Confirms the key is not just present but actually authenticates.

Pass condition: IAM accepts the key; the row reports the resolved account and user identity.

Failure modes:
- `BXNIM0415E: Provided API key could not be found` — the key is malformed or has been deleted in IBM Cloud.
- `network is unreachable` / `i/o timeout` — your workstation can't reach `iam.cloud.ibm.com`. Common in customer-firewall scenarios; route through a jumphost ([Chapter 16](./16-on-flag-ssh-jumphosts.md)) to confirm the key works from inside the customer network.

## Common failures and how to fix them

The chapter readers most often land on. Each row maps a real-world symptom to its fix:

| Symptom | Likely cause | Fix |
|---|---|---|
| `terraform not on PATH` | not installed | install Terraform `>= 1.5`; re-run `doctor` |
| `kubeconfig: $KUBECONFIG and ~/.kube/config both missing` | never ran `up` against this workspace | `roksbnkctl kubeconfig --download` or run `roksbnkctl up` |
| `ibmcloud api key: ... no keychain entry` | new shell, key not exported | `export IBMCLOUD_API_KEY=...` or re-run `roksbnkctl init` |
| `ibm cloud auth: BXNIM0415E` | bad / rotated key | regenerate the key in the IBM Cloud console; update the keychain via `roksbnkctl init` |
| `ibm cloud auth: i/o timeout` | corp-firewalled workstation | use [`--on jumphost`](./16-on-flag-ssh-jumphosts.md) to test from inside the customer network |
| `workspace "foo" not initialised` | `ws new` was run but `init` was not | run `roksbnkctl init -w foo` |
| `workspace: no config context` | `~/.roksbnkctl/config.yaml` corrupt | inspect the file; worst case delete it and re-run `init` |

If a fix isn't here, [Chapter 26 — Troubleshooting](./26-troubleshooting.md) covers the longer tail.

## The `--target <name>` SSH check (Sprint 1)

Sprint 1's `--on jumphost` flag introduced an optional second mode for `doctor`: probe an SSH target before you try to use it.

```bash
roksbnkctl doctor --target jumphost
```

This adds one row per resolved target:

```
✓  ssh:jumphost      ubuntu@169.45.91.177:22 (TOFU recorded)            (verifies the target is reachable)
```

The probe:

1. Resolves the target's `host`, `user`, `port`, and key source from `~/.roksbnkctl/<workspace>/config.yaml`.
2. Connects via the `internal/remote` SSH client.
3. Validates the host key against `~/.roksbnkctl/known_hosts` (TOFU prompt on first contact, unless `--insecure-host-key`).
4. Runs a no-op command (`true`) to confirm the channel works end-to-end.

Failure modes specific to the SSH probe:

- `host key mismatch` — the target was rebuilt; edit `~/.roksbnkctl/known_hosts` to clear the entry, then re-probe.
- `unable to authenticate` — the key source resolved but the remote rejected it. Check `key_path` / `key_source` in workspace config; if `key_source: agent`, verify `ssh-add -l` shows the right key.
- `dial tcp: i/o timeout` — the `host:port` is unreachable. Verify with `nc -vz <host> 22` from a known-good network.

Pass `--target all` to probe every target listed in the workspace's `targets:` block. Useful in CI when you want a single command that asserts every entry is reachable.

## Reading the exit code

`doctor` exits with:

- `0` — all checks are green or warnings only. Warnings do not fail `doctor`. The everyday workflow can proceed.
- non-zero — at least one row produced an `✗` error. The first error string is also written to stderr so wrapper scripts can grep it.

This is the contract `scripts/e2e-test.sh` and the `Makefile` rely on: a script that runs `roksbnkctl doctor && roksbnkctl up --auto` will only proceed past `doctor` if the environment is genuinely ready.

The "warnings don't fail" rule is deliberate. After Sprint 2, an `iperf3 not on PATH` warning is informational — the everyday `up` / `test connectivity` flow doesn't need it. Forcing exit-1 on every warning would be too aggressive for the common case.

If you want to gate scripts strictly (e.g. CI workflows that must have iperf3 installed because they run the throughput suite), parse the output rather than relying on the exit code:

```bash
if ! roksbnkctl doctor | grep -q '^✓  iperf3'; then
  echo "iperf3 missing — install it before running test throughput" >&2
  exit 1
fi
```

## What `doctor` is not

A few deliberate non-features worth naming:

- **Not a fix-it tool.** `doctor` reports; it never installs, never modifies workspace config, never calls IBM Cloud APIs that mutate state. The IAM verify call is read-only. If `doctor` could break things, users couldn't run it freely — and "run `doctor`" needs to be a safe first move.
- **Not a backend probe.** Per-backend availability checks (docker daemon reachable, k8s ops pod healthy, ssh target reachable) ship as separate `BackendName`-tagged rows via `doctor --backend <name>` ([PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md)). The `--target` probe was the early prototype of that pattern.
- **Not concurrent-safe.** The CLI invokes `doctor` once per command; the side-channel for "why we care" blurbs in [`internal/doctor/doctor.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/doctor/doctor.go) doesn't synchronise. Don't run two `doctor`s against the same process.

## Cross-references

- [Chapter 4 — Installation](./04-installation.md) introduces `doctor` as the post-install verification step.
- [Chapter 6 — Workspaces](./06-workspaces.md) explains the `workspace` row and the `-w` override.
- [Chapter 14 — Credentials](./14-credentials-resolver.md) is the deep dive on the `ibmcloud api key` resolution chain.
- [Chapter 16 — The `--on` flag](./16-on-flag-ssh-jumphosts.md) covers the `--target` probe's underlying SSH client.
- [Chapter 24 — Day-2 ops](./24-day-2-ops.md) is the canonical reference for the internalised `k <verb>` commands that make `kubectl` / `oc` informational.
