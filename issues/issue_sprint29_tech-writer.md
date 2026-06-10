# Sprint 29 — tech-writer issues (air-gap registry mirror + the `test matrix` perf grid)

> **Sprint 29 frame.** Two documentation workstreams this sprint:
>
> 1. **Air-gap registry mirror** (Issues 1–2): the new air-gapped install path —
>    `roksbnkctl registry` (the CRUD/COS-client-shaped mirror) + the optional
>    **Registry phase** that replicates the BNK bill-of-materials from FAR into
>    the cluster's OpenShift internal registry, after which `bnk up` installs with
>    no external egress. Staff builds (`issue_sprint29_staff.md`); architect
>    frames the prose (`issue_sprint29_architect.md` Issue 5). Spec:
>    [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md).
> 2. **The `test matrix` performance grid** (Issues 3–4, added mid-sprint): the
>    new `roksbnkctl test matrix` — a declarative iperf3 (L4) + h2load (L7)
>    performance grid run against a deployed cluster, with both generators now
>    preinstalled on the jumphosts. Spec:
>    [PRD 10](../docs/prd/10-PERF-TEST-MATRIX.md) (this ships its in-scope subset,
>    h2load substituted for OSLO).

`Status`: open (draft — not yet dispatched)

---

## Issue 1 — New chapter: the air-gapped install

**Severity**: medium
**Status**: open

A new `book/src/` chapter (+ `SUMMARY.md` entry), placed after the BNK-deploy
chapter and before/with the troubleshooting material:

- The customer scenario: why mirror (no external egress at deploy time), and the
  CRUD/COS-client framing of `registry {bom, replicate, list, diff, verify,
  prune}`.
- The bill-of-materials: the `f5-bigip-k8s-manifest` as the source of truth (25
  charts + 56 images) + the non-F5 deps (cert-manager, `bitnami/kubectl`).
- The walk-through: `cluster up` → `registry replicate --target openshift` →
  `registry verify` → `bnk up` (now pulling from the internal registry) → the
  optional `gateway`/`testing` phases.
- The two-address reality for the curious: charts pulled by the host over the
  route, images by pods over the in-cluster service via `system:image-puller`.
- The pluggable-target note: OpenShift internal registry now; ICR / generic OCI
  later.

## Issue 2 — Reference updates

**Severity**: low
**Status**: open

- **Configuration reference** (`book/src/28-configuration-reference.md`): the new
  `registry:` block (target, namespace, creds, include-deps).
- **Command reference** (`book/src/27-command-reference.md`): regenerate via
  `go run ./tools/refgen/cobra-md` so the `registry` group is included.
- **Tearing-down chapter** (`book/src/11-tearing-down.md`): note `registry prune`
  + how the mirror relates to `bnk down`/`cluster down` (per the namespace-model
  decision, architect Issue 3).
- CHANGELOG entry for the release that ships Sprint 29.

### Scope guards
- Real transcripts only (re-captured against the shipped binary) — no invented
  output. Mark any placeholder illustrative.
- mdbook builds (docker image) clean; cspell green (add `BOM`, `ORAS`, `crane`,
  `dockerconfigjson`, etc. to the dictionary).
- Wait for staff's CLI to stabilize before regenerating the command reference.

### Acceptance criteria
1. Air-gapped-install chapter authored + linked in `SUMMARY.md`.
2. Configuration + command references updated/regenerated.
3. Tearing-down + CHANGELOG updated.
4. Book builds clean (HTML + PDF via the docker backend); cspell green.

### Files affected
- **New**: `book/src/<NN>-air-gapped-install.md`; `book/src/SUMMARY.md`.
- **Modified**: `book/src/{27-command-reference,28-configuration-reference,11-tearing-down}.md`,
  `CHANGELOG.md`, the cspell dictionary.

### Related
- [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md) · `issue_sprint29_architect.md`
  (Issue 5 prose framing) · `issue_sprint29_staff.md` (the CLI surface to document).

---

## Issue 3 — New chapter: the performance matrix

**Severity**: medium
**Status**: open

> Added mid-sprint. `roksbnkctl test matrix` runs a declarative grid against an
> already-deployed cluster — an **iperf3 (L4)** family over a TCPRoute VIP with
> content-size knobs, and an **h2load (L7)** family over an HTTPRoute (http +
> https, TLS terminate at TMM), in cps / tps / throughput modes. Locality
> (same-zone / diff-zone / diff-VPC) is implicit in which `vsi` jumphost a cell
> names as its client. The runner owns only ephemeral fixtures (iperf3 server,
> nginx file backend, and optional TCPRoute / HTTPRoute / TLS objects that attach
> to the existing Gateway by name) and tears them down after; it changes no
> Terraform. Both generators are preinstalled on every jumphost (`iperf3` +
> `nghttp2-client`). Code: `internal/test/{matrix,l7,fixtures,throughput}.go`,
> `internal/cli/test_matrix*.go`; example grid
> `internal/test/testdata/matrix.example.yaml`.

A new `book/src/` chapter (+ `SUMMARY.md` entry), placed immediately after
[Throughput testing](../book/src/22-throughput-testing.md) (suggest
`22a-performance-matrix.md`) — `test throughput` is one cell; `test matrix` is
the whole grid, so they belong adjacent and cross-linked.

Cover:
- **What it is and why**: turning the hand-run BNK-on-ROKS perf plan (CPS / TPS /
  throughput across locality) into one `matrix.yaml` + one command with a
  diffable `roksbnkctl.v1` report. Name the divergence from the source plan
  honestly: **h2load, not OSLO** — req/s + transfer rate + request-time
  min/max/mean, *not* OSLO's CPS/TPS or p50/p95/p99 (no fabricated percentiles).
- **The two families**: iperf3 over a TCPRoute VIP with the `length` content-size
  knob ("128" vs "512K" as the L4 analog of the 128 B / 512 KB payload axis);
  h2load over an HTTPRoute, http and https, with the cps / tps / throughput mode
  presets (and how the presets map to `-c` / `-m` / `--h1` / `-n` / `-D`).
- **The locality axis = jumphost placement**: same-zone / diff-zone / diff-VPC is
  whichever `vsi` endpoint a cell names as client, resolving to
  `ssh:jumphost` / `ssh:jumphost-<zone>` (cross-link Chapters 15/16).
- **The `matrix.yaml` schema**: `gateway` / `fixtures` / `endpoints` / `cells`,
  walked off the shipped `internal/test/testdata/matrix.example.yaml`.
- **Fixtures + the no-Terraform boundary**: the runner owns ephemeral objects
  only and **attaches routes to the existing Gateway by name** — it never adds
  listeners or mutates the gateway phase. Spell out the prerequisite: the
  Gateway must already expose the `http` / `https` / `tcp` listener sections the
  routes bind to; the self-signed TLS secret; label-selected teardown; `--keep`.
- **The workflow**: `--dry-run` (plan + fixtures, no cluster calls) → real run →
  `-o json|text|md` report shape. Show a dry-run transcript (it's cluster-free,
  so it's safe to capture verbatim).

## Issue 4 — Reference + sibling-chapter updates (perf matrix)

**Severity**: low
**Status**: open

- **Command reference** (`book/src/27-command-reference.md`): regenerate via
  `go run ./tools/refgen/cobra-md` so the `test matrix` subcommand + flags
  (`--file` / `--only` / `--dry-run` / `--keep` / `-o`) are included.
- **Configuration reference** (`book/src/28-configuration-reference.md`):
  document the `matrix.yaml` schema (it is workspace-sibling, not part of
  `config.yaml` — call that out) — `gateway{...}`, `fixtures{...}`,
  `endpoints{kind: vsi|address|url}`, `cells{family: iperf3|l7, ...}`.
- **SSH-targets / jumphosts** (`book/src/15-ssh-targets.md`,
  `16-on-flag-ssh-jumphosts.md`): note the jumphosts now preinstall `h2load`
  (`nghttp2-client`) alongside `iperf3`, so `test matrix` over `ssh:<target>`
  needs no `--bootstrap`.
- **Execution backends** (`book/src/17-execution-backends.md`): add the bundled
  `roksbnkctl-tools-h2load` image to the per-tool image list (built by
  `tools/docker/h2load` + the `tools-images` workflow).

### Scope guards (perf matrix)
- Real transcripts only — capture the `--dry-run` plan against the shipped
  example grid (it makes no cluster calls). Mark any live-run numbers as
  illustrative; the fixture *apply* path is not yet validated against a live
  ROKS cluster (see the CHANGELOG caveat) — say so rather than imply proven runs.
- mdbook builds clean (docker backend, HTML + PDF); cspell green (`h2load`,
  `nghttp2`, `TCPRoute`, `cps`, `tps` already added to `cspell.json` where the
  word-length gate requires it).
- Regenerate the command reference only after the CLI surface is final.

### Acceptance criteria (perf matrix)
1. Performance-matrix chapter authored + linked in `SUMMARY.md`, cross-linked
   with Throughput testing and the SSH-targets/jumphost chapters.
2. Command + configuration references updated/regenerated.
3. Jumphost preinstall + the h2load image noted in the SSH/backends chapters.
4. Book builds clean (HTML + PDF); cspell green.

### Files affected (perf matrix)
- **New**: `book/src/22a-performance-matrix.md`.
- **Modified**: `book/src/SUMMARY.md`,
  `book/src/{27-command-reference,28-configuration-reference,15-ssh-targets,16-on-flag-ssh-jumphosts,17-execution-backends}.md`,
  the cspell dictionary.

### Related (perf matrix)
- [PRD 10 — perf-test matrix](../docs/prd/10-PERF-TEST-MATRIX.md) (design; this
  ships its in-scope subset).
- Code: `internal/test/{matrix,l7,fixtures}.go`, `internal/cli/test_matrix*.go`,
  `internal/test/testdata/matrix.example.yaml`.
- `terraform/modules/testing/main.tf` (jumphost `user_data` preinstall),
  `tools/docker/h2load/Dockerfile`, `.github/workflows/tools-images.yml`.
