# Sprint 3 — tech-writer issues, resolution notes

Ten issues filed: 1 high, 3 medium, 6 low. All ten resolved in this
integration pass — no code changes, doc-and-Dockerfile-comment fixes
only.

## Issue 1 (HIGH — chapter 14 kubeconfig discovery chain doesn't match the code) — resolved by integrator

Rewrote chapter 14 §"kubeconfig discovery" to reflect the actual
two-tier kubectl-style chain implemented in
`internal/k8s/client.go::DefaultKubeconfigPath()`:

1. `$KUBECONFIG` env var (first existing path in colon-separated list)
2. `~/.kube/config`

Dropped the spurious `~/.roksbnkctl/<ws>/state/kubeconfig`
"workspace-local file is first" claim. Added a callout noting that
the same-named **directory** under workspace state is a Terraform
`kubeconfig_dir` tfvar input populated by upstream HCL with
per-component sub-files, not a kubeconfig the resolver reads from.

Also fixed:

- Chapter 14's table of "four secrets" row for kubeconfig (line 12 in
  the original) to read `KUBECONFIG env → ~/.kube/config (kubectl-style)`.
- Chapter 14 line 162 §"File permissions" — now references
  `~/.kube/config` mode `0600`, not "workspace-local kubeconfigs".
- The error remediation text — drops the "workspace state/kubeconfig
  is missing" line.

**Status**: ✅ resolved (chapter 14 now matches
`internal/k8s/client.go:71-92` byte-for-byte)

## Issue 2 (medium — chapters 12 and 17 document a per-tool default for iperf3 that the binary doesn't implement) — resolved by integrator (option a, align to code)

Rewrote chapter 12 line 256's "Behaviour when fields are missing"
table row for `exec.*` to match chapter 17's hedged language: defaults
to `local` for every tool today (Sprint 3); PRD 03's design intent of
`iperf3`→`k8s` lands when the k8s backend lands in Sprint 4. Chapter
17 line 84 was already hedged; left as-is.

The alternative (option b, add the per-tool default map to
`resolveBackendSpecWith`) is deferred to Sprint 4 alongside the k8s
backend's actual landing — adding the map before the backend is wired
would just make `roksbnkctl test throughput` fail with "backend k8s
not implemented" for everyone with a default config.

**Status**: ✅ resolved (chapter 12 ↔ chapter 17 ↔
`internal/cli/cluster.go::resolveBackendSpecWith` now consistent)

## Issue 3 (medium — chapter 17 `docker run` example bind-mounts a workspace-state directory the implementation doesn't bind-mount) — resolved by integrator

Replaced the inaccurate `docker run` example in chapter 17 (which
showed `--workdir /work` and `-v <workspace-state>:/work`) with a
realistic Sprint 3 `ibmcloud` passthrough shape: kubeconfig single-file
mount + `-e IBMCLOUD_API_KEY` env-by-reference + image + cmd. Added an
explanatory paragraph noting that `RunOpts.Files`, kubeconfig, and
`RunOpts.WorkDir` are the three knobs `buildMountsAndEnv` actually
honours, and that Sprint 3's `ibmcloud` passthrough sets none of them
except the kubeconfig and env var.

**Status**: ✅ resolved (chapter 17 §"docker" now matches
`internal/exec/docker.go::buildMountsAndEnv` lines 286-360)

## Issue 4 (medium — README has no `--backend docker` highlight bullet for Sprint 3) — resolved by integrator

Added a `**--backend docker** (v0.9)` bullet to the README's
"Highlights" section, immediately after the v0.8 internalised-kubectl
bullet, in the same per-sprint version-tagged shape established by
Sprint 1 (v0.7) and Sprint 2 (v0.8). The text covers the four
talking points: frozen toolchain, no host install, cred-by-reference,
per-tool `exec:` defaults — and links to chapter 17.

**Status**: ✅ resolved (README "Highlights" now has a Sprint-3 entry
matching the established cadence)

## Issue 5 (low — chapter 14 describes the redactor as "regex-based" but the implementation is byte-comparison) — resolved by integrator

Rewrote chapter 14 §"The redactor" implementation paragraph (line 275
in the original) from "wrapping `io.Writer` with regex-based
redaction. The regex matches the resolved API key value…" to
"wrapping `io.Writer` with byte-comparison redaction and cross-write
prefix buffering (so a secret split across two `Write` calls still
gets masked). The matcher uses the resolved API key value verbatim…".

The cross-write buffering detail also flags the design property a
reader would otherwise have to discover by reading the code:
`internal/exec/redact.go`'s `isPotentialPrefix` (lines 160-170) buffers
trailing bytes that *might* be the start of a secret across a `Write`
boundary.

**Status**: ✅ resolved (matches `internal/exec/redact.go` lines
124-170)

## Issue 6 (low — chapter 17 documents a 126 vs 127 split the implementations don't make) — resolved by integrator (option a, collapse and note future split)

Rewrote chapter 17 §"Backend-failure semantics" to reflect what
Sprint 3's `local` and `docker` backends actually return:

- `127` for backend-side failure of any kind (Docker daemon down,
  image pull failed, container create/start error, binary-not-on-PATH
  for `local`)
- Tool exit code 1:1 for actual tool failures
- `137` for context cancellation / timeout (the conventional
  SIGKILL code)

Added a paragraph noting that PRD 03 reserves both `126` and `127`
for the finer-grained "started-then-failed" vs "failed-to-start"
distinction, and that Sprint 4 may split the cases if real use cases
motivate it.

The alternative (option b, change `docker.go` and `local.go` to honour
the PRD 03 split today) is a staff item — flagged in the chapter
text as a Sprint 4 candidate.

**Status**: ✅ resolved (chapter 17 §"Backend-failure semantics" now
matches `internal/exec/docker.go:76,130,138,153,196,220,223` and
`internal/exec/local.go:64,139`)

## Issue 7 (low — `tools/docker/iperf3/Dockerfile` has a stale "wired up in Sprint 3" comment) — resolved by integrator

Updated `tools/docker/iperf3/Dockerfile` line 3-4 comment from "wired
up in Sprint 3" to "wired up in Sprint 4 (PLAN.md §"iperf3 backend
selection")".

**Status**: ✅ resolved

## Issue 8 (low — `tools/docker/ibmcloud/Dockerfile` mis-describes the GitHub Actions tag scheme) — resolved by integrator

Rewrote `tools/docker/ibmcloud/Dockerfile` lines 9-10 to describe the
actual tagging scheme: the CI workflow publishes `:latest` and
`:<git-tag>` (e.g. `:v0.9.0`) on tag push events. Local development
builds via `tools/docker/Makefile` use the `:dev` tag, which matches
what `internal/exec/docker.go::toolImages` resolves at runtime in
this build.

The secondary bug flagged in the issue text — `internal/exec/docker.go`
hard-coding `:dev` means a clean `go install ./cmd/roksbnkctl` on a
fresh host fails to pull the image — is a Sprint 3 staff follow-up
to either pin `toolImages` to the binary's `version` package value
or have the workflow additionally publish `:dev` on `main` pushes.
Tracked here for the Sprint 4 polish pass; not blocking v0.9.

**Status**: ✅ resolved (Dockerfile comment); ⏸ tracked for Sprint 4
polish (the `:dev` runtime-resolution mismatch)

## Issue 9 (low — chapter 14 file-mode advice contradicts chapter 12's documented default) — resolved by integrator (option a, doc-side clarification)

Added a paragraph to chapter 14's `api_key_b64` section noting that
`roksbnkctl init` writes `config.yaml` mode `0644` by default (per
chapter 12 §"File permissions"); when the user populates
`api_key_b64`, they should `chmod 0600` themselves and re-chmod
after any `roksbnkctl init` that re-writes the file. Also notes that
the keychain and env-var paths sidestep the issue entirely (nothing
sensitive lands in `config.yaml`, so the default `0644` is fine).

The alternative (option b, change `workspace.go` to write `0600`
when `api_key_b64` is non-empty) is a staff item — left for a future
sprint if user friction with the chmod step surfaces.

**Status**: ✅ resolved (chapter 14 ↔ chapter 12 round-trip now
explicitly addressed)

## Issue 10 (low — chapter 15 ssh-agent platform claim covers only Linux/macOS but isn't well-cited for v0.7) — resolved by integrator (option a, drop the version qualifier)

Rewrote chapter 15 line 97 to drop the `v0.7` version qualifier (the
restriction is structural to the Go SSH library, not roksbnkctl-
specific) and to cite [`golang.org/x/crypto/ssh/agent`](https://pkg.go.dev/golang.org/x/crypto/ssh/agent)
with a note that "upstream tracking issues for status" is where a
reader interested in the Windows named-pipe gap should look. The
"full Windows support is a v2 item" framing stays.

**Status**: ✅ resolved

## Summary

10 issues filed, 10 resolved. No code changes needed for the
tech-writer pass — all fixes were in `book/src/`, `README.md`, and
two Dockerfile comments. Build, vet, gofmt, and the new `internal/cred`
+ `internal/exec` test suites all remain green after the integration.
