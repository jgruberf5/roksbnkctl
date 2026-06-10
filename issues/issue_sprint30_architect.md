# Sprint 30 — architect issues (config seeding/templating for CI + registry targets ICR/generic)

> **Sprint 30 frame.** Make `roksbnkctl` driveable unattended: seed a workspace
> `config.yaml` from a file or URL, template its secrets from the environment,
> clean up the SSH keys it copied, and replicate FAR into the registries a
> production pipeline actually uses (ICR by default; Artifactory/Harbor via a
> generic OCI target). Staff (`issue_sprint30_staff.md`) owns the Go. This
> ledger owns the **design decisions staff codes against** + the operator prose.
> Specs: [PRD 13](../docs/prd/13-WORKSPACE-CONFIG-SEEDING.md) (issues 2–5),
> [PRD 14](../docs/prd/14-REGISTRY-TARGETS.md) (issue 1).

`Status`: open (draft — not yet dispatched)

---

## Issue 1 — Registry targets: ICR default + generic OCI (BLOCKING for the book example)

**Severity**: high · **Status**: open · PRD 14

`buildTarget` (registry.go:297) hard-codes `kind="openshift"` and errors on
anything else. Pin, before staff builds the impls:

1. **The default flip's blast radius.** A `config.yaml` with no
   `registry.target` currently means openshift; after the default→icr change it
   would mean icr. Decide: migration note + leave existing behavior for already-
   initialized workspaces (read `registry.target` as openshift when absent *and*
   the workspace predates Sprint 30), or accept the switch + document. (PRD 14
   OQ 4.)
2. **ICR addressing + auth.** Host `<region>.icr.io`, namespace (new
   `registry.icr_namespace`, default from `prefix`), credential = the workspace
   IAM key (`iamapikey` + `targets.<t>.api_key_b64`). Confirm push (host) ==
   image-pull (cluster) == chart-pull (host) all collapse to the one ICR host.
3. **ICR cluster pull secret.** Does ROKS's global `*.icr.io` pull secret let
   BNK pods pull `<region>.icr.io/<ns>/images/<name>` with no per-namespace
   secret? Verify live (test-003/test-005). If not, the redirect must provision
   an `iamapikey` secret. (PRD 14 OQ 2.)
4. **Generic target contract.** `generic_host` + `generic_repo_prefix` + static
   auth. Nail how the auth field composes with the PRD 13 env-override (so an
   Artifactory token is templated, not committed).

## Issue 2 — `--config-file` interview behavior (BLOCKING)

**Severity**: high · **Status**: open · PRD 13 OQ A

The fork that shapes the whole feature: when `--config-file` supplies a complete
config, does init **skip the interview entirely** (validate + write — the CI
path) or **seed-then-interview** (confirm/fill gaps)? Recommendation:
**non-interactive when complete** — a supplied config with all required fields
present writes straight through (no TTY needed); init only prompts for *missing*
required fields, and `--override-from-env` is applied after. Pin the
"required-field" set that decides complete-vs-incomplete, and the precedence
when `--config-file` and `--var-file` are both given (independent: config seeds
config.yaml, var-file seeds terraform.tfvars — no conflict).

## Issue 3 — URL fetch policy

**Severity**: medium · **Status**: open · PRD 13 OQ B

Pin: allowed schemes (recommend http+https), auth (recommend **none** in v1 —
presigned COS / raw-git URLs cover CI; revisit if a private-URL need appears),
size cap (10 MB), timeout (30 s), and whether a fetched body is cached or
one-shot. A URL and a local path are the same downstream — the only new surface
is the fetch + its failure messaging.

## Issue 4 — env-override mapping + precedence (BLOCKING for the book table)

**Severity**: high · **Status**: open · PRD 13 OQ C/D

Two decisions staff + tech-writer both need locked:

1. **`api_key_b64` source.** Recommend: `IBMCLOUD_API_KEY` = **raw** key, init
   base64-encodes it (matches the tool's existing `IBMCLOUD_API_KEY` usage in
   ops/doctor); `ROKSBNKCTL_API_KEY_B64` = pre-encoded escape hatch. Env wins
   over the seeded file.
2. **The override table** (env → field) — the authoritative list the book
   publishes. Start from PRD 13's table; decide which `targets.<t>.*` target the
   overrides hit when a config has multiple (recommend: the **default target**,
   with a future `--target` selector). Keep the set **small + explicit** — this
   is a fixed field map, not arbitrary interpolation (non-goal).

## Issue 5 — recording copied `~/.ssh` keys for teardown

**Severity**: medium · **Status**: open · PRD 13 OQ E

`copyKeyToUserSSH` copies but records nothing; `runWSDelete` can't clean up. Pin
the record: a `resources.copied_ssh_key_name` (or `…_paths`) in `config.yaml`,
written **only** when the user accepts the copy prompt, holding what we put in
`~/.ssh/`. `ws delete` deletes exactly those recorded files (never an unrecorded
`~/.ssh` file — the copy never overwrites, so a recorded path is always ours).
Decide the delete-time UX: fold into the existing `ws delete` confirmation vs. a
distinct "also remove ~/.ssh/<name>?" prompt (recommend: mention in the existing
confirmation, delete without a second prompt; skip silently if the file is gone).

## Cross-cutting

- **No silent field drops.** `--config-file` parse + env override must error
  loudly on unknown/garbage input — the inverse of Terraform's
  undeclared-variable warning that masked the `cluster_vpc_id` bug.
- **Secrets never logged.** `api_key_b64` / Artifactory tokens are
  redaction-class; the existing config redaction guards `api_key` — confirm the
  new fields are covered.
