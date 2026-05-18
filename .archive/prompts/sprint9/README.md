# Sprint 9

**Theme:** PRD 04 cred-passing closure + CI polish — `v1.2.0`

_Drafted from `docs/PLAN.md` Sprint 9 section. Sprint 9 is the **first post-v1.1.x maintenance + closure cycle**: it ships the two PRD 04 deferred items that surfaced as integration-test gaps during the v1.1.x cycle (cred-tmpfile-bind-mount for docker, trusted-profile auto-provisioning for k8s), plus the smaller CI / Makefile polish that would have prevented the v1.1.0 → v1.1.1 → v1.1.2 patch cascade. Releases `v1.2.0` at end of sprint._

The two `t.Skip` markers on `776fe56` (`internal/exec/docker_integration_test.go::TestIntegration_DockerBackend_NoLeakInInspect` and `internal/exec/k8s_integration_test.go::TestIntegration_K8sBackend_JobMode_Echo`) are the explicit Sprint 9 inputs — both their TODO comments name the design choices Sprint 9 closes. Skip-removal is the v1.2.0 acceptance signal.

PRD 04's §"Open questions" §"Trusted profile auto-provisioning" item has been open since the v0.9 cycle (Phase M2 cred audit deferred it). Sprint 9 resolves it with the recommended `--trusted-profile=auto` default + static-key fallback when IAM perms don't allow.

The four-agent dispatch shape is the same as Sprints 1-8:

- **Architect** — close PRD 04's §"Open questions" items by adding a §"Resolved in Sprint 9" subsection (mirrors PRD 03's §"Resolved in Sprint 4"); update chapter 14 (Credentials) with a tmpfile-bind-mount paragraph + `--trusted-profile` flag docs; update chapter 19 (Ops pod) with the auto-provisioning flow + verification commands; write the `v1.2.0` CHANGELOG entry under `## Unreleased (v1.x)`.
- **Staff engineer** — implement the cred-tmpfile-bind-mount pattern in `internal/exec/docker.go`; implement the trusted-profile auto-provisioning path in `internal/exec/k8s.go` + `internal/cli/ops.go` + a new `internal/ibm/trusted_profile.go`; remove the two `t.Skip` markers (the docker test on its own; the k8s Job test gains a tools-image switch from `busybox:1.36` to `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` so the strict `RunAsNonRoot` SecurityContext stays intact). Unit tests cover the tmpfile lifecycle + the trusted-profile path's fallback shape.
- **Validator** — add `TESTCONTAINERS_RYUK_DISABLED=true` to `.github/workflows/ci.yml`'s integration job env (kills the docker-hub anonymous-pull flake on `TestIntegration_Connect_Whoami`); extend the `make release` target with `staticcheck ./...` and `go build -tags integration ./...` steps; run the full regression sweep on the integrated tree; live-verify the trusted-profile path against a real IBM Cloud workspace (sandbox-permitting); cross-link audit on architect's chapter 14 + 19 edits.
- **Tech-writer** — read-only review at end of sprint; dogfooding loop on the new chapters from a first-time-reader perspective ("how does `roksbnkctl --backend k8s` get a token without me handing it a static API key?"); drift sweep between PRD 04 ↔ staff source ↔ chapter quotes ↔ CHANGELOG; launch-readiness audit against PLAN.md §"Gate to `v1.2.0` tag".

The release tag itself (`v1.2.0`) is **integrator-owned** — Sprint 9 lands all the prep; the integrator runs the now-extended `make release` pre-tag checklist, cuts the tag, kicks off goreleaser, and pushes after the four agents' work merges.
