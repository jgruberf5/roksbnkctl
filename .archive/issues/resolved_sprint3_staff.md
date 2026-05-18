# Sprint 3 — staff engineer issues, resolution notes

Five issues filed: 2 resolved during the agent's own work (1, 3), 1 informational not-a-code-issue (2), 2 accepted with rationale (4, 5).

## Issue 1 (`--backend` persistent flag swallowed by DisableFlagParsing) — resolved by agent

Same pattern as Sprint 1's `--on` flag — `kubectl`/`oc`/`ibmcloud` use `DisableFlagParsing: true` so cobra forwards downstream flags. The persistent `--backend` flag never reaches `flagBackend`. Agent added an inline `extractBackendFlag()` helper in `internal/cli/cluster.go` that scans `args` for `--backend <name>` before dispatch, mirroring Sprint 1's `extractOnFlag()`. Documented in inline comment.

`roksbnkctl --backend docker ibmcloud iam` (flag before subcommand) hits cobra's normal persistent-flag path; only `roksbnkctl ibmcloud --backend docker iam` (flag after subcommand) needs the manual extractor.

**Status**: ✅ resolved by agent during sprint

## Issue 2 (Docker daemon SSL intercept on dev host) — informational

The agent's dev environment has a corporate SSL inspection cert that's not trusted by the Docker daemon's image registry pull, blocking `docker build` against IBM's apt repo. Worked around by building inside Docker itself (which has its own cert chain). Not a code issue; the produced image works correctly when built in any CI environment without SSL inspection.

**Status**: ⚠️ informational, not actionable — flagged for integrator awareness

## Issue 3 (`observe-service` plugin removed from IBM apt repo) — resolved by agent

The Sprint 0 placeholder Dockerfile suggested `ibmcloud plugin install observe-service`. IBM removed that plugin from their default repository in 2024. Agent dropped it from the install list; the remaining plugins (container-service / ks) are sufficient for the Sprint 4+ k8s + ssh backend work that will exercise this image.

**Status**: ✅ resolved by agent during sprint

## Issue 4 (legacy `config.ResolveAPIKey` shim retained) — accepted, integrator note

The new `internal/cred/Resolver` is the canonical resolver going forward. The legacy `internal/config.ResolveAPIKey` function still exists for non-passthrough call sites (cluster lifecycle, init flow). Agent retained the shim to keep the Sprint 3 diff tractable — full migration is mechanical and a Sprint 4 polish-pass candidate.

**Status**: ✅ accepted; deferred to Sprint 4 polish

## Issue 5 (go.mod additions — `moby/moby/{api,client}` promoted to direct deps) — accepted

The Docker backend uses `github.com/moby/moby/client` for daemon communication. Both `moby/moby/api` and `moby/moby/client` were already indirect deps via `testcontainers-go`; this sprint promotes them to direct. Sized appropriately; documented for v0.9 release notes alongside Sprint 1's `gliderlabs/ssh` and `testcontainers-go` additions.

**Status**: ✅ accepted; documented for release notes
