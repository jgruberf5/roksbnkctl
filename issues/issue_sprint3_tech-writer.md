# Sprint 3 — tech writer issues

Findings cover the 5 new chapters (12, 13, 14, 15, 17 intro), the staff
agent's `internal/cred/` and `internal/exec/` implementation, the
validator's tests + workflows, and README / CONTRIBUTING / PLAN.md /
PRD drift. All findings are doc-and-example-correctness only — no
code changes proposed.

## Issue 1: chapter 14 documents a kubeconfig discovery chain that doesn't exist in the implementation

**Severity**: high
**Status**: open
**Description**: Chapter 14 § "kubeconfig discovery" (lines 132-146)
states a three-tier precedence:

```
1. Workspace-local file: ~/.roksbnkctl/<workspace>/state/kubeconfig
2. KUBECONFIG environment variable
3. ~/.kube/config
```

and asserts at lines 142-146 that "The workspace-local file is **first**
because it's the kubeconfig the tool itself wrote — produced by
`cluster up`, fetched from IBM Cloud's API as the cluster came online."

The actual implementation in
`/mnt/d/project/roksbnkctl/internal/k8s/client.go::DefaultKubeconfigPath()`
(lines 71-92) walks only **two** sources, in kubectl order:

1. `$KUBECONFIG` env var (first existing path in colon-separated list)
2. `~/.kube/config`

There is no workspace-local kubeconfig precedence anywhere in the
discovery chain. The path
`~/.roksbnkctl/<ws>/state/kubeconfig` referenced in the chapter is in
fact a **directory** (a Terraform tfvars input — see
`internal/tf/terraform.go:68` `kcDir := filepath.Join(stateDir, "kubeconfig")`
and `internal/tf/vars_test.go:94` `kubeconfig_dir = "/home/user/.roksbnkctl/default/state/kubeconfig"`),
populated by the upstream HCL with per-component sub-files
(`cert_manager`, `cne_instance`, `flo`, `license`). It is not, and never
has been, a single kubeconfig file the tool reads from.

The actual location the post-`up` flow writes to is `~/.kube/config`
(see `runKubeconfigDownload` in `internal/cli/cluster.go:247-261`,
which calls `k8s.DefaultKubeconfigPath()` and falls through to
`filepath.Join(home, ".kube", "config")`).

Chapter 14's error-remediation message at lines 152-156 also names
"workspace state/kubeconfig is missing" as the first thing to fix —
which steers the user toward a non-existent code path.

This is the most user-facing factual error in the new chapters: a
reader following chapter 14 to understand "where does my kubeconfig
come from" gets an inaccurate answer that doesn't match what the
binary does.

**Files affected**: `book/src/14-credentials-resolver.md`
**Proposed fix**: rewrite the kubeconfig section to reflect actual
order (`$KUBECONFIG` env → `~/.kube/config`), drop the
`~/.roksbnkctl/<ws>/state/kubeconfig` first-line claim, and clarify
that `cluster up`'s post-apply step writes to `~/.kube/config` (mode
0600). The "Workspace-local kubeconfigs are written `chmod 0600`" line
at chapter 14:162 should reference `~/.kube/config` instead.

## Issue 2: chapters 12 and 17 document a per-tool default for iperf3 (k8s) that the binary doesn't implement

**Severity**: medium
**Status**: open
**Description**: Chapter 12's "Behaviour when fields are missing" table
(line 256) asserts:

> `exec.*` | Per-tool defaults built into the binary apply (`iperf3`→`k8s`, everything else→`local`).

Chapter 17's "Per-tool defaults from `exec:`" table (line 84) is more
hedged but still says iperf3's default is "k8s (Sprint 4) / local (today)".

The actual implementation in
`/mnt/d/project/roksbnkctl/internal/cli/cluster.go::resolveBackendSpecWith`
(lines 338-348) has **no** per-tool default map. It returns:

1. `flagOverride` if set,
2. `cctx.Workspace.Exec[tool].Backend` if set,
3. literal `"local"` for every tool, including iperf3.

There is no built-in "iperf3 → k8s" default anywhere in the codebase.
The chapter 17 text at line 84 ("today's default falls back to
`local`") is closer to the truth, but chapter 12's flat assertion that
iperf3 defaults to k8s is wrong both at "today" and in the future
(Sprint 4 may add the per-tool default; Sprint 3 didn't). PRD 03 §"Why"
proposes the iperf3=k8s default as a design intent, not a shipped
behaviour.

If a user reads chapter 12, doesn't put an `exec:` block in
`config.yaml`, and runs `roksbnkctl test throughput` expecting iperf3
to fall through to k8s, they'll get the local backend instead — the
exact opposite of what chapter 12 promises. Today the difference is
academic (k8s isn't wired anyway, so they'd get a "backend not
implemented" error if the default were honoured), but the chapters
will keep being wrong after Sprint 4 lands the k8s backend unless
either (a) the default map is added or (b) the chapters are corrected.

**Files affected**: `book/src/12-workspace-config.md` (line 256),
`book/src/17-execution-backends.md` (line 84, line 211)
**Proposed fix**: align chapter 12's table row with chapter 17's
hedged language, OR file a Sprint 4 follow-up to add the per-tool
default map in `resolveBackendSpecWith`. Pick one and fix the other
chapter to match.

## Issue 3: chapter 17's `docker run` example bind-mounts a workspace-state directory the implementation doesn't bind-mount

**Severity**: medium
**Status**: open
**Description**: Chapter 17 lines 113-121 advertise the docker
backend's `docker run` shape as:

```
docker run --rm \
  --workdir /work \
  -v <workspace-state>:/work \                # for terraform state, etc.
  -v <kubeconfig-path>:/root/.kube/config:ro \ # if the tool needs a kubeconfig
  -e IBMCLOUD_API_KEY \                       # env var name only; value inherits
  ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<v> \
  ks cluster ls
```

The `-v <workspace-state>:/work` bind-mount and the literal `--workdir
/work` are presented as the always-on shape. The actual
implementation in
`/mnt/d/project/roksbnkctl/internal/exec/docker.go::Run` does neither.
Mount construction is in `buildMountsAndEnv` (lines 286-360):

- No bind-mount of any workspace directory at `/work` ever happens.
- Individual files from `RunOpts.Files` are mounted at
  `/work/<basename>` only when `Files` is non-empty (Sprint 3's
  ibmcloud passthrough doesn't set `Files`).
- `WorkingDir` comes from `opts.WorkDir` directly, not hard-coded
  `/work`.
- The kubeconfig single-file mount at `/root/.kube/config:ro` matches
  the chapter (good).
- The `-e IBMCLOUD_API_KEY` bare-name form matches (good).

A reader following chapter 17 to understand "what does --backend
docker do under the hood" comes away with a mental model that
includes a workspace-wide bind-mount the tool never makes. Likely to
generate confused issues from users who `docker inspect` a running
container and see no `/work` mount.

**Files affected**: `book/src/17-execution-backends.md` (lines
107-127)
**Proposed fix**: drop the `-v <workspace-state>:/work` line and the
literal `--workdir /work` from the example; replace with a
realistic shape that matches `internal/exec/docker.go`'s actual
output (kubeconfig mount only, env name only, image + cmd). The
"frozen toolchain version" + "no host install" + "credential by
reference" call-outs (lines 124-127) all stand on their own without
the inaccurate bind-mount line.

## Issue 4: README has no `--backend docker` highlight bullet for Sprint 3

**Severity**: medium
**Status**: open
**Description**: The README's "Highlights" section (lines 28-36)
follows a per-sprint version-tagged bullet pattern:

- Sprint 1 (v0.7): `**--on jumphost** (v0.7)` bullet (line 35)
- Sprint 2 (v0.8): `**Internalised kubectl verbs (v0.8)**` bullet (line 36)

Sprint 3 introduces `--backend docker` for `roksbnkctl ibmcloud ...` —
the first non-local execution backend, the cred resolver+redactor
spine, and the per-tool `exec:` workspace config. None of this gets a
"Highlights" bullet. A reader scanning the README to understand "what
does v0.9 add?" sees no entry for the work that landed.

The Sprint 3 tech-writer prompt § Task 8 explicitly flags this as a
candidate addition (medium severity, analogous to Sprint 1 Issue 10).

**Files affected**: `README.md` (the "Highlights" bullet list,
~line 35-36)
**Proposed fix**: add a single bullet after the v0.8 line, in the same
shape:

> - **`--backend docker` (v0.9)** — run wrapped tools (`ibmcloud`,
>   `iperf3`) inside vendored container images instead of as host
>   processes. Frozen toolchain version, no host install required.
>   Credentials propagated by reference — `IBMCLOUD_API_KEY` value
>   never appears in `docker inspect`. Per-tool defaults via
>   workspace `config.yaml`'s `exec:` block. See [chapter
>   17](https://jgruberf5.github.io/roksbnkctl/book/17-execution-backends.html).

The bullet text mirrors the staff agent's Sprint 3 deliverables and
fits the established README cadence. Defer-until-Sprint-4 (when k8s +
ssh land and the four-backend matrix is complete) is also a defensible
choice — pick one explicitly rather than leave the README silent.

## Issue 5: chapter 14 describes the redactor as "regex-based" but the implementation is byte-comparison

**Severity**: low
**Status**: open
**Description**: Chapter 14 line 275 reads:

> "The implementation is `internal/exec/redact.go` — a wrapping
> `io.Writer` with regex-based redaction."

The actual `redact.go` does not use any regex. The match path is in
`scan` (lines 124-155) and `isPotentialPrefix` (lines 160-170): a
nested loop using `bytes.Equal(data[i:i+len(s)], s)` for full matches
and `bytes.HasPrefix(s, tail)` for buffered-prefix matches. There is
no `regexp.Compile` or `regexp.Regexp` anywhere in the file (a `grep
-n regex internal/exec/redact.go` returns nothing).

The semantic claim is right — the redactor matches the resolved key
value exactly, not a generic IBM-key pattern — but calling that
"regex-based" misrepresents the implementation. A developer asked to
"add a second secret type to the redactor" who reads chapter 14 may
look for an existing regex and not find one.

**Files affected**: `book/src/14-credentials-resolver.md` (line 275)
**Proposed fix**: replace "regex-based redaction" with "exact-string
substitution" or "byte-comparison redaction with cross-write
buffering". The rest of the paragraph (matches resolved value, not
generic pattern) is accurate and should stay.

## Issue 6: chapter 17 documents an exit-code 126 vs 127 split the implementation doesn't make

**Severity**: low
**Status**: open
**Description**: Chapter 17 §"Backend-failure semantics" (lines 57-64)
distinguishes:

- 127 — "Backend startup failure (Docker daemon down, k8s cluster
  unreachable, SSH target down)"
- 126 — "Backend-internal error during a successful run (e.g., the
  docker container started but failed to bind-mount the kubeconfig)"

This claim mirrors the PRD 03 spec ("126/127 reserved for
backend-specific failures"), but the actual implementations don't
allocate the codes that way:

- `internal/exec/docker.go` returns `127` for daemon unreachable
  (line 76), image inspect/pull failure (line 130), `ContainerCreate`
  failure (line 138), `ContainerAttach` failure (line 153),
  `ContainerStart` failure (line 196), `ContainerWait` error
  (line 220). It returns `137` for ctx cancellation (line 223). It
  does **not** return `126` anywhere.
- `internal/exec/local.go` returns `127` for "binary not on PATH"
  (line 64), `137` for ctx cancel (line 139), and the child's actual
  exit code otherwise. Also no `126`.

A reader who treats the chapter's 126 vs 127 split as a real
diagnostic surface ("if my CI script sees rc=126, I have a
bind-mount problem; rc=127 means Docker isn't running") will be
disappointed: they'll see only 127 for both cases.

**Files affected**: `book/src/17-execution-backends.md` (lines 57-64)
**Proposed fix**: either (a) collapse the chapter's distinction —
note that "Sprint 3 implementations return 127 for backend-side
failures of any kind; the 126/127 split is reserved per PRD 03 for
future use", or (b) flag this as a Sprint 4 staff follow-up (split
the cases in `docker.go` so callers actually get the documented
distinction). The chapter as written promises something neither
backend delivers today.

## Issue 7: tools/docker/iperf3/Dockerfile has a stale "wired up in Sprint 3" comment

**Severity**: low
**Status**: open
**Description**: `tools/docker/iperf3/Dockerfile` line 4 reads:

> `# execution backend's iperf3 server pod (see PRD 03). Skeleton image;`
> `# client/server invocation is wired up in Sprint 3.`

PLAN.md Sprint 3 (lines 279-332) does **not** list iperf3 migration as
a deliverable. PLAN.md Sprint 4 (lines 350-381) does:

> | 6 | iperf3 backend selection: default `k8s`, supports `local`/`ssh` — wire in `cli/test.go test throughput` | edit |

And `internal/cli/cluster.go::resolveBackendSpecWith` confirms iperf3
is not wired through any backend yet — only the ibmcloud passthrough
goes through the `dispatchBackend` path (cluster.go:325). The `iperf3`
key in `internal/exec/docker.go::toolImages` (line 54) is registered
but never resolved by any caller in Sprint 3.

The Dockerfile comment promises a Sprint-3 wiring that didn't happen.
Future readers cross-checking the Dockerfile against the actual
iperf3 wiring path will be confused.

**Files affected**: `tools/docker/iperf3/Dockerfile` (line 4)
**Proposed fix**: change "wired up in Sprint 3" → "wired up in
Sprint 4 (PLAN.md §"iperf3 backend selection")".

## Issue 8: tools/docker/ibmcloud/Dockerfile mis-describes the GitHub Actions tag scheme

**Severity**: low
**Status**: open
**Description**: `tools/docker/ibmcloud/Dockerfile` lines 9-10 read:

> `# The dev tag (`:dev`) is what the CI workflow publishes on `tag: v*``
> `# events; `latest` and `vX.Y.Z` follow on releases.`

The actual `.github/workflows/tools-images.yml` (lines 41-44) tags
each image with two tags only:

```yaml
tags: |
  ghcr.io/jgruberf5/roksbnkctl-tools-${{ matrix.image }}:${{ github.ref_name }}
  ghcr.io/jgruberf5/roksbnkctl-tools-${{ matrix.image }}:latest
```

`:dev` is **not** published by the workflow. The `:dev` tag is what
`tools/docker/Makefile` builds locally (`TAG ?= dev` on line 4) and
what `internal/exec/docker.go::toolImages` (lines 53-54) hard-codes
for ibmcloud + iperf3. After a release tag push, the workflow
publishes `ghcr.io/.../roksbnkctl-tools-ibmcloud:v0.9.0` and
`ghcr.io/.../roksbnkctl-tools-ibmcloud:latest`, but the docker
backend keeps trying to pull `:dev` (which only exists locally for
developers who ran `make build-ibmcloud`).

There are two bugs entangled here:

1. The Dockerfile comment is wrong about the workflow tagging.
2. The docker backend's `toolImages` map will fail for end users on
   a clean install (no `:dev` available on GHCR).

For this tech-writer pass, only #1 is in scope — flag the Dockerfile
comment. #2 is a Sprint 3 staff/integrator follow-up (the `:dev` tag
in `internal/exec/docker.go` should either be a release-version
constant tracked in lock-step with the binary's `version` package, or
the workflow should additionally publish `:dev` on `main` pushes).

**Files affected**: `tools/docker/ibmcloud/Dockerfile` (lines 9-10);
secondary impact in `internal/exec/docker.go:53-54` (out of scope for
tech-writer fix but flagging for the integrator)
**Proposed fix**: rewrite the Dockerfile comment to reflect the
actual workflow tagging:

> `# The CI workflow `.github/workflows/tools-images.yml` publishes`
> `# `:latest` and `:<git-tag>` (e.g. `:v0.9.0`) on tag push events.`
> `# Local development builds via tools/docker/Makefile use the `:dev``
> `# tag, which matches what `internal/exec/docker.go::toolImages``
> `# resolves at runtime in this build.`

## Issue 9: chapter 14 file-mode advice contradicts chapter 12's documented default

**Severity**: low
**Status**: open
**Description**: Chapter 12 line 15 documents the workspace config
file's mode:

> "The file is created mode `0644` — readable by your user, the same
> trust posture as the surrounding workspace directory."

Chapter 14 line 86 advises the reader to:

> "Treat the file like a plaintext credential: `chmod 0600`, never
> commit, never share."

Both are true individually — `internal/config/workspace.go:219` does
write `0o644`, and chapter 14 is right that a config containing
`api_key_b64` deserves 0600 — but the two chapters together leave the
reader confused: "the tool writes 0644, but I should chmod 0600 it
myself when I add a key, except `roksbnkctl init` may regenerate it
later at 0644 again". The chapters don't address the round-trip
question.

This isn't strictly wrong, just under-explained. A reader who relies
on the keychain path never has this problem (the config doesn't hold
secrets). A reader on WSL2-without-libsecret who falls back to
`api_key_b64` needs to know the chmod is their responsibility *and*
that subsequent `roksbnkctl init` calls may revert the mode.

**Files affected**: `book/src/12-workspace-config.md` (line 15),
`book/src/14-credentials-resolver.md` (line 86, line 240)
**Proposed fix**: add one sentence to chapter 14's `api_key_b64`
section noting that `roksbnkctl` writes the file `0644` by default;
when populating `api_key_b64`, the user should chmod to `0600` and
re-chmod after any `roksbnkctl init` re-write. OR change the file mode
to `0600` in `workspace.go` whenever `api_key_b64` is non-empty (a
staff follow-up; out of scope for tech-writer). Either resolves the
inconsistency.

## Issue 10: chapter 15 ssh-agent platform claim covers only Linux/macOS but the SSH backend behaviour isn't actually verified for v0.7

**Severity**: low
**Status**: open
**Description**: Chapter 15 line 97 reads:

> "**Platform note**: ssh-agent integration is **Linux/macOS-only** in
> v0.7. Windows users should use `key_path` to a file."

The chapter is otherwise grounded against `internal/remote/` (Sprint 1
code), but the v0.7 ssh-agent restriction isn't called out in any
visible Sprint 1 issue file or PRD 01 acceptance criterion the
tech-writer can cross-check against. The claim may be true (the Go
`golang.org/x/crypto/ssh/agent` package's named-pipe support on
Windows is famously incomplete), but the chapter doesn't cite or
ground it. A reader on Windows who wants to know "did this actually
fail in testing or is this a defensive claim" has no evidence trail.

This isn't a doc-vs-code drift — it's an under-cited claim. Low
severity because Windows is not in scope for v0.x targets per
PLAN.md, but worth flagging in case the chapter is later extracted
into platform-coverage matrix work.

**Files affected**: `book/src/15-ssh-targets.md` (line 97)
**Proposed fix**: either drop the `v0.7` version qualifier (the claim
is structural to the Go ssh library, not a roksbnkctl-specific
limitation), or cite the upstream issue
(`golang/go#XXXXX` if it exists) so a future reader can confirm the
restriction is still in force.

## Cross-document drift check — clean

The following spot-checks ran clean:

- **`docs/PLAN.md`** Sprint 3 section (lines 279-332) accurately
  describes the cred + first-backend deliverables that landed.
- **`docs/prd/03-EXECUTION-BACKENDS.md`** and
  **`docs/prd/04-CREDENTIALS.md`** are still accurate as design
  surfaces — no obsolete details introduced by the implementation
  shift.
- **`book/src/SUMMARY.md`** chapter titles match the h1 of each
  chapter file (12: "Workspace config (config.yaml)" / "Workspace
  config (config.yaml)"; 13: "Terraform variables (terraform.tfvars)"
  match; 14: "Credentials and the resolver chain" match; 15: "SSH
  targets" match; 17: "Execution backends: local, docker, k8s, ssh"
  match).
- **Go version pin**: `go.mod` declares `go 1.25.0`; chapter 4
  ("Installation") at lines 11, 16, 18, 43, 47 references `Go 1.25+`;
  README at line 36 references `Go 1.25 or newer`. All three agree.
  The `docker/docker/client` migration to `moby/moby/api v1.54.1` /
  `moby/moby/client v0.4.0` (go.mod:12-13) didn't bump the floor; no
  follow-up Go-version edits needed in chapter 4 or README.
- **Cross-references in the new chapters all resolve** — every
  relative link target is either a real chapter (06, 09, 12, 13, 14,
  15, 16, 17, 25) or a Sprint 0 stub (18, 19, 28, 29, 31). The
  forward-link-to-stub pattern matches Sprint 1 + 2.
- **PRD links use GitHub-canonical URLs** per Sprint 1 Issue 9 fix —
  every `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/...`
  link in the new chapters is the canonical form, not the relative
  `../../docs/prd/...` form that breaks under mdBook publish.

## Test code readability — clean

`internal/cred/resolver_test.go` and the `internal/exec/*_test.go`
files (audit, docker, docker_integration, local, redact) are all
well-documented:

- Each test has a leading comment explaining the contract it pins down
  (PRD reference, what the assertion enforces).
- Magic constants (`auditSecret`, `redactedMarker`,
  `keychainService`) are either named or come with adjacent comments.
- Test names use the
  `Test<Subject>_<ConditionOrCase>` convention consistently.
- Skips for platform incompatibility (`runtime.GOOS == "windows"`)
  and missing prerequisites (`dockerAvailable()`,
  `keyring.MockInit()` fallback) are graceful and explained.

The validator's Issue 2-4 (test-vs-implementation signature drift
flags) all turned out to be moot once staff's implementation merged —
signatures matched. No tech-writer findings here.

## Notable observations not filed as issues

1. **Chapter 14's mention of `internal/cred/resolver.go` "extracted
   this sprint from the formerly scattered logic in
   `internal/config/secrets.go`"** is accurate — both files exist
   side-by-side post-Sprint 3, with `secrets.go::ResolveAPIKey`
   acting as a thin shim. The chapter's framing is correct.

2. **CONTRIBUTING.md additions** — both Sprint 3 sections ("Running
   cred-audit tests" and "Building tool images locally") are present,
   well-grounded, and reference the right files. The validator did a
   thorough job here; no follow-ups.

3. **Chapter 17's intentional `*Coming in Sprint 4.*` markers** for
   the k8s deep-dive, ssh deep-dive, and per-backend "when to use
   it" table are all preserved, exactly as the tech-writer prompt
   requested. No "Coming in Sprint 3" placeholders remain in any of
   the five new chapters.
