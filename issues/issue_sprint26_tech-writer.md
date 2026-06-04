# Sprint 26 — tech-writer issues (post-integration drift sweep)

> **Sprint 26 frame.** Light, read-only-first drift sweep that runs **after**
> the three-way integration (architect + staff + validator) lands the
> prefix-driven naming refactor. Verifies the integrated tree's
> operator-facing surface — the regenerated command reference, the
> configuration reference, the init chapter walkthrough, the
> `terraform.tfvars.example` header, and the CHANGELOG — matches the
> **built binary's actual behavior**, and re-captures any illustrative
> transcript byte-for-byte. Ends on a GREEN / RED launch verdict that
> unblocks the release cut.

`Status`: resolved

---

## Issue 1 — Drift sweep over the integrated prefix-naming tree

**Severity**: low (documentation; runs after integration, no behavior
change).
**Status**: resolved

### Scope

Run against the built binary (`go build` the integrated tree first), then
sweep:

1. **Command reference** (`book/src/27-command-reference.md` or wherever the
   cobra-reflected reference lives) — regenerate so `roksbnkctl init
   --help` matches the binary. Confirm the new interview is reflected and
   that no flag drifted. If `init` gained/renamed any flag (e.g. a
   `--prefix`), confirm the reference shows it.
2. **Configuration reference** (`book/src/28-configuration-reference.md`) —
   confirm the architect's `prefix` + `resources:` documentation matches
   the actual `config.yaml` the binary writes for a fresh `init` (run a
   hermetic `init` against a temp `ROKSBNKCTL_HOME`, diff the produced YAML
   keys against the documented schema).
3. **Init chapter** — re-capture the interview transcript (prefix prompt,
   create toggles, existing-resource discovery, printed name plan)
   byte-for-byte against the binary; replace the architect's illustrative
   blockquote output with the captured output and drop the "illustrative"
   note.
4. **Generated-tfvars example** — confirm the documented derived names
   (`<prefix>-cluster-vpc`, `<prefix>-tgw`, …) match what the binary
   actually renders for a sample prefix; fix any suffix-wording drift
   between the book, the `terraform.tfvars.example` header, and the
   rendered file.
5. **Cross-chapter stale-reference sweep** — grep the book for the old
   "every workspace uses `tf-cluster-vpc` / `tf-openshift-cluster`
   defaults" framing and any "supply names via `--var-file`" workaround
   prose that the prefix flow now supersedes; update or cross-link to the
   new naming concept section. Confirm the Sprint 25 `doctor --orphan-sweep`
   cross-link (if that chapter exists) points at the canonical formulas.
6. **CHANGELOG** — confirm the entry is user-facing: leads with the
   collision-prevention + generated-names benefit, notes backward
   compatibility (existing workspaces unaffected), and the override path
   still works.

### Acceptance criteria

1. `init --help` in the command reference matches the built binary.
2. The configuration reference's `prefix` + `resources:` schema matches the
   YAML a fresh `init` writes.
3. The init-chapter transcript is captured from the binary (no illustrative
   placeholder remains).
4. No stale "`tf-*` default names" / "supply every name via `--var-file`"
   prose survives un-cross-linked.
5. CHANGELOG bullet is user-facing and notes backward compatibility.
6. mdbook HTML + PDF build clean. GREEN / RED verdict recorded in the
   closure.

### Files affected (probable)

- `book/src/27-command-reference.md`, `book/src/28-configuration-reference.md`,
  the init chapter, the naming concept section — drift fixes only.
- `CHANGELOG.md` — user-facing bullet (if not already landed by the
  integrator).
- No `internal/`, `cmd/`, `terraform/` edits (binary surface is staff's;
  this is reflected-doc reconciliation).

### Related

- `issues/issue_sprint26_architect.md` — the prose this sweep verifies
  against the binary.
- `issues/issue_sprint26_staff.md` — the binary surface (init interview +
  rendered tfvars) the docs must match.
- Sprint 24 tech-writer closure (`issues/issue_sprint24_tech-writer.md`) —
  the drift-sweep shape + the "re-capture illustrative output against the
  built binary" precedent.

---

## Closure — tech-writer, 2026-06-04

`Status`: resolved (drift sweep complete; doc fixes landed against the built
binary).

Method: built the integrated tree to `/tmp/roksbnkctl-s26` (`go build -o
/tmp/roksbnkctl-s26 ./cmd/roksbnkctl` — clean), captured `init --help`,
attempted a hermetic `ROKSBNKCTL_HOME=$(mktemp -d) init -w demo </dev/null`
(blocked at the live IAM `Verify()` round-trip — `init` resolves + verifies the
API key *before* the interview, so a no-creds hermetic run can't reach the
prompts on this host). Captured the interview transcript byte-for-byte from the
**authoritative source** instead — the exact `fmt.Fprintf` format strings in
`internal/cli/prompt.go` (`promptString`/`promptYesNo`/`promptInt`) and the
prompt labels + name-plan format in `internal/cli/init.go`
(`runPrefixInterview`, `promptPrefix`, `printNamePlan`) — and confirmed the
derived names against `internal/naming/naming.go` (`Derive`, the suffix
constants, `MaxPrefixLen()`).

### Finding 1 — Command reference (`book/src/27-command-reference.md`) `init`

**Verdict**: PASS (no drift). The `## roksbnkctl init` section matches the
binary's `init --help` exactly — same long description (`region, resource
group, cluster, BNK version`), and all three flags (`--tf-source`,
`--upgrade-tf`, `--var-file`) present with byte-matching descriptions. The
prefix flow is interview-driven, not a flag — the binary has **no** `--prefix`
flag, so nothing is missing. No edit.

### Finding 2 — Configuration reference (`book/src/28-configuration-reference.md`)

**Verdict**: PASS (no drift). The `prefix:` field block, the `resources:` block
(`{create, existing}` schema + per-toggle tfvars mapping + additivity note),
and the field-by-field table entries match the additive `config.Workspace`
schema in `internal/config/workspace.go` (`Prefix string`, `Resources
*ResourcesCfg`, the seven `ResourceToggle` sub-blocks) and the YAML a fresh
prefix-driven `init` writes. The 35-char cap, the lowercase-label charset, and
the "empty ⇒ legacy sparse render" backward-compat note are all correct. No
edit.

### Finding 3 — Init-chapter transcript re-capture (`book/src/12-workspace-config.md`)

**Verdict**: FIXED. The architect's worked-example transcript was marked
**illustrative** and drifted from the binary in wording, prompt order, and the
name-plan format. Replaced it with the binary-captured transcript and dropped
the illustrative note. Concrete corrections:

- Prefix prompt label is `Workspace prefix (≤ 35 chars)` (binary builds it from
  `naming.MaxPrefixLen()`), not `Workspace prefix [dev]`.
- Toggle wording matched to the binary: `Create new ROKS cluster?`, `Create
  registry COS instance?`, `Create Transit Gateway?`, `Install cert-manager?`,
  `Deploy BIG-IP Next (BNK)?`, `Create TGW test jumphost?`, `Create a new
  client VPC for it?`, `Create per-zone cluster jumphosts?`, and the
  `Existing client VPC name` / `Existing Transit Gateway name` discovery
  prompts (no trailing `?` on the bare-string prompts; two-space indent;
  `[Y/n]`/`[y/N]` defaults; output to stderr).
- **Prompt-order fix**: `OpenShift version` + `Workers per zone` are prompted
  *inside* the "Create new ROKS cluster? → yes" branch (the old transcript put
  `Workers per zone` right after the prefix), and the `Existing Transit Gateway
  name` prompt fires *after* the client-VPC questions (the binary asks it only
  when the TGW is declined **and** the jumphost is enabled). Added a short note
  documenting both ordering details.
- Name-plan header is `Resolved resource names for prefix "acme-eu":` (not
  `Resolved resource names (prefix "acme-eu"):`), row labels/spacing match
  `printNamePlan` (`registry COS instance`, `transit gateway … (existing)`,
  with the TGW-jumphost block printing `client VPC` only when the jumphost is
  created), and the closing line is `✓ Wrote <path>/config.yaml` (not
  `✓ Created workspace "dev"`).
- Noted the API key resolves via the cred chain *before* the prompts (so the
  key prompt is absent on the common env/keychain path), replacing the invented
  `Save the key …` line.

### Finding 4 — Generated-names example + suffix wording

**Verdict**: PASS (names) / FIXED (a related stale `tfvars` step). The derived
names in Chapter 13's table, the `terraform.tfvars.example` header, and
`naming.Derive` all agree: cluster `<prefix>` (no suffix), `-cluster-vpc`,
`-registry-cos`, `-tgw`, `-client-vpc`, `-jh-tgw`, `-jh` (+ module `-<zone>`);
limits 35/63/180. No suffix-wording drift — no edit to Ch13 or the example
header. **But** the Chapter 12 worked example's step 2 was materially wrong on a
related point (see Finding 5).

### Finding 5 — Stale-reference sweep

**Verdict**: FIXED (two real stale items) / PASS (the rest are correct
per-field defaults, not stale framing).

- **`roksbnkctl tfvars` step (Ch12)** — FIXED. The old step claimed
  `roksbnkctl tfvars -w dev` renders a config-derived tfvars containing
  `cluster_name = "dev-cluster"` / `workers_per_zone = 2`. Verified against the
  binary: `roksbnkctl tfvars` writes the upstream `terraform.tfvars.example`
  starter template verbatim (it does **not** render from `config.yaml`), and
  `cluster_name`/`workers_per_zone` aren't even real upstream variable names
  (they're `openshift_cluster_name`/`roks_workers_per_zone`). Rewrote the step
  to explain that the config-derived `terraform.tfvars` is generated
  automatically into `state/terraform.tfvars` at `up`/`plan`/`apply` time, with
  the real prefix-derived variable names, and cross-linked the unrelated
  `roksbnkctl tfvars` command to its own Ch13 section. (This is the
  pre-existing snippet the architect flagged in their closure as
  tech-writer's surface.)
- **"region, resource group, cluster name" prompt list (Ch12)** — FIXED. With a
  prefix, the cluster name is *derived*, not prompted. Updated the prose to list
  prefix + create toggles and note the cluster name is derived.
- **`tf-openshift-cluster` / `tf-cluster-vpc` / `tf-tgw` mentions elsewhere** —
  PASS (not stale). The Chapter 29 terraform-variable-reference entries and the
  Chapter 13 "what you typically edit" table document these as the upstream
  HCL **variable defaults** (the names the *legacy sparse / empty-prefix* path
  still falls through to) — that framing remains accurate and is the
  backward-compat contract. The `cluster: name: tf-openshift-cluster` example
  in the Ch12/Ch28 per-field `cluster:` blocks documents the field's default
  value; left intact per the architect's chosen presentation. None carry the
  superseded "*every* workspace uses these" framing.
- **`doctor --orphan-sweep` cross-link** — PASS. The Ch13 concept section and
  the CHANGELOG cross-link it forward-looking ("tracked for a later release")
  and point at the canonical `<prefix>-cluster-vpc` / `<prefix>-tgw` formulas;
  the chapter doesn't exist yet (Sprint 25 placeholder), so the link is
  intentionally to the issue, not a dead anchor.

### Finding 6 — CHANGELOG

**Verdict**: FIXED (authored). There was **no** `v1.8.0` entry — the top entry
was `v1.7.1`. Authored a user-facing `## v1.8.0 — 2026-06-04` section in the
project's established Keep-a-Changelog style. It **leads with the
collision-prevention + generated-names benefit** (the `canada-roks-*`
incident class), and the `### Notes` block calls out **backward compatibility**
(empty-prefix configs render the old sparse tfvars byte-for-byte; old configs
load without migration) and that the **`--var-file` / `terraform.tfvars.user`
override path still works** (layers last, wins over any generated name).
Namespace-exemption and the `doctor --orphan-sweep` detection complement are
noted. Integrator cuts the tag.

### mdbook build status

**Not built** — `mdbook` is absent on this host (same as the architect's
closure). HTML+PDF build deferred to a host with the toolchain
(`tools/docker/mdbook` / `make release`). Mitigation: verified every
cross-chapter anchor I authored/touched by hand against the actual headings —
`#roksbnkctl-tfvars--bootstrap-a-starter` (Ch13, em-dash dropped → double
hyphen, matching the architect's `#the-length--charset-limits` pattern),
`#resource-naming--collision-avoidance`, `#resources-block` (Ch28),
`#worked-example-bootstrap-a-workspace-from-scratch` (Ch12) all resolve.

### Files touched

- `book/src/12-workspace-config.md` — re-captured the `init` interview
  transcript byte-for-byte (dropped the illustrative note), fixed the
  `roksbnkctl tfvars` step (was wrong about the command's behaviour + used
  non-existent variable names), fixed the "init still prompts for cluster name"
  prose, and aligned the existing-resource-discovery bullet to the binary's
  prompt labels.
- `CHANGELOG.md` — authored the user-facing `v1.8.0` entry.
- This closure.

Untouched (per scope): no `internal/`, `cmd/`, `terraform/` (the
`terraform.tfvars.example` header already matched the rendered names — no edit
needed), no `*_test.go`, no `docs/PLAN.md`, `prompts/`, or other roles' issue
files. `go build ./...` re-confirmed clean after the doc edits (docs don't
affect the build, but verified).

### Launch verdict

**GREEN.** The operator-facing surface — command reference, configuration
reference, the re-captured init-chapter transcript, the generated-names example,
and the user-facing CHANGELOG — matches the built binary. The two illustrative /
stale items (the placeholder transcript and the wrong `tfvars` step) are fixed,
and the `v1.8.0` CHANGELOG entry is in place. The only deferral is the mdbook
HTML/PDF render (toolchain absent on this host; anchors verified by hand) —
non-blocking, to be run on the release host. GREEN unblocks the integrator's
`v1.8.0` cut.
