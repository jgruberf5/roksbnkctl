# Changelog

All notable changes to `awsbnkctl` are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html). Pre-`v1.0.0` minor versions may include breaking changes — see the per-version notes.

## Unreleased

### Added

- **Demo experience subsystem** — `awsbnkctl up --demo` provisions the identical cluster as a normal `up`, plus pre-stages a demo client on the jumphost, tags resources with an absolute expiry, and renders a rocket-themed staged launch UI on interactive terminals. Non-TTY and `--no-color` runs fall back to the plain per-phase log byte-for-byte unchanged.
- **`demo {list,run,clean}` command group** — curated audience catalogue alongside the `scenarios` validation suite. The two registries stay disjoint; `demo list` shows the union (demos + Green scenarios) with a `KIND` column.
- **`http2` demo use-case** — proves end-to-end HTTP/2 (h2c) through TMM, asserting both legs (client→TMM wire HTTP/2 + TMM→backend body `HTTP/2.0`) via SSH+EICE curl from the pre-staged jumphost.
- **`diameter` demo use-case** — proves Diameter (RFC 6733) CER→CEA Result-Code 2001 transit across an L4 BNK Gateway, pushing the embedded Python client via `CopyFileViaEICE` and running it via `RunStagingCommands`.
- **Jumphost staging primitives** — exported `jumphost.RunStagingCommands` + `jumphost.CopyFileViaEICE` that mint+push ephemeral EICE keys internally (no operator key dance), shared by demo use-cases and the demo-client pre-staging phase.
- **VPC CNI prefix delegation (Phase 08b)** — moved before the node group so nodes boot in prefix mode. Eliminates the cold-start hang caused by secondary-ENI asymmetric drop on the EKS CNI.
- **Phase 11b** — EBS CSI managed addon + `gp3` StorageClass + hugepages-2Mi DaemonSet, in front of the BNK install.
- **Phases 17b/c/d** — multi-ENI jumphost provisioning + interface discovery + (under `--demo`) jumphost client pre-staging.
- **Phase 23b** — `F5SPKVlan` + `GatewayClass` for the host-device pattern, completing the TMM data-plane plumbing.

### Changed

- **Terraform removed entirely.** The production path is now AWS-SDK-only across all phases. The repository no longer carries Terraform sources, lock files, or vendored modules. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the rationale.
- **`-f/--config` flag unification** — `bnk resync` now accepts `-f/--config`; `status` reads the targeted cluster's `state.env` instead of the host default kubeconfig.
- **CNE Instance auto-resync** — `awsbnkctl bnk resync` ships as a first-class subcommand to work around the upstream HTTPRoute pool-member stale bug (see [`docs/upstream-issues/`](docs/upstream-issues/)).
- **`cluster.yaml` validation** — strict YAML parsing (`KnownFields(true)`); unknown top-level fields fail loud rather than silently being ignored.

### Fixed

- **Phase 14 + Phase 24b idempotency** — both phases now skip cleanly on healthy re-runs (FLO Helm upgrade was unconditional; DSSM overlay reverted its own marker). Healthy `up -f <existing>` is now a true no-op for the BNK install path.
- **Phase 17c on TMM-owns-ENI re-runs** — guard added so an `up` re-run against a healthy cluster no longer fails with `MAC not found` when TMM has already claimed the secondary ENIs into its netns.
- **Pool-member stale workaround in scenarios** — `pkg/bnk.ResyncHTTPRoutes` is now wired into every scenario's Verify step (before the data-plane probe) so probes observe a healed pool.

## v0.x

The pre-`v1.0` series is captured in git history. Each `feat()` / `fix()` commit on `main` / `staging` includes a self-contained design note.
