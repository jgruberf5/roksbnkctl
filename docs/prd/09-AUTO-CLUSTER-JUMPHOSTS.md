# PRD 09 — per-AZ cluster-jumphost auto-registration

> `roksbnkctl up`'s post-apply hook already auto-seeds the singular TGW `jumphost` target; this PRD extends it to also auto-register one `jumphost-<zone>` target per cluster-VPC availability zone when `testing_create_cluster_jumphosts = true`, so the per-AZ jumphosts are first-class `--on` targets with no manual `targets add`. Estimated effort: small (~120 LOC + tests).

## Why

`tryAutoJumphost` runs in the post-`up` hook and seeds exactly one target — `jumphost` — from the singular `testing_tgw_jumphost_ip` output (PRD 01). When `testing_create_cluster_jumphosts = true`, the deploy *also* creates one cluster jumphost per cluster-VPC AZ (`ibm_is_instance.cluster_jumphost`, `for_each = local.cluster_zones`), each on its own floating IP, all sharing the **same** key (`tls_private_key.jumphost_shared_key`, surfaced as the `jumphost_shared_key` output).

Today the user must *discover* these exist (the deploy says "4 jumphosts created" with instructions for only one), look up each floating IP by hand, and `roksbnkctl targets add jumphost-<zone> …` per AZ. That is exactly the convenience gap the singular `jumphost` target already closes for the TGW jumphost — and the post-v1.4.0 user-testing thread that produced this PRD, PRD 08, and `issues/issue_sprint13_staff.md` Issue 1. Auto-registering the per-AZ jumphosts makes the post-`up` hook do for *all* the jumphosts what it already does for one.

## Goal

After a successful `roksbnkctl up`, in the same post-apply hook that seeds the singular `jumphost`, also upsert one target per AZ named `jumphost-<zone>` from the per-AZ cluster-jumphost floating-IP map output, reusing the shared `jumphost_shared_key`. Best-effort and non-fatal, exactly matching `tryAutoJumphost`'s posture. A `testing_create_cluster_jumphosts = false` (or absent / empty-map) deploy is a silent no-op — behavior unchanged, no warning noise.

After this lands, on a 3-AZ region with cluster jumphosts enabled:

```
roksbnkctl targets list
# → jumphost, jumphost-<z1>, jumphost-<z2>, jumphost-<z3>
roksbnkctl --on jumphost-<z2> kubectl get pods   # full passthrough, no hop
```

## Design

### The output to read

The top-level terraform outputs the post-`up` hook can see (`terraform/outputs.tf`) expose the per-AZ cluster jumphosts as:

- **`testing_cluster_jumphost_ips`** — a terraform **map** `{ zone => floating-IP }`. (This is the top-level output; it forwards the testing module's internal `testing_cluster_jumphost_public_ips`. Its `try(…, [])` default renders as `[]` / empty when `testing_create_cluster_jumphosts = false`.)
- `testing_cluster_jumphost_ssh_commands` — `{ zone => ssh-command }`, informational only.

> **Output-name deviation from the design surface (resolved here).** `issues/issue_sprint13_staff.md` Issue 3 and `issues/issue_sprint13_architect.md` Issue 1 name the output `testing_cluster_jumphost_public_ips`. That name exists **only inside the `testing` module** (`terraform/modules/testing/outputs.tf`); it is *not* a top-level output. The top-level output `roksbnkctl` actually reads is **`testing_cluster_jumphost_ips`** (`terraform/outputs.tf:82`). There is likewise **no** top-level `testing_cluster_jumphost_private_ips`. This PRD, the CHANGELOG, and the chapter 15/16 docs use the real top-level names. Staff code deliverable 3 must read `testing_cluster_jumphost_ips`, not `…_public_ips`; tracked in `issues/issue_sprint13_architect.md` as a staff/validator hand-off so the doc-coupling audit checks the as-landed name.

### The `mapOutput` helper

Add a `mapOutput(outputs, key) map[string]string` helper beside the existing `stringOutput`, modeling the existing `json.Unmarshal(om.Value, &s)` pattern (unmarshal `om.Value` into `map[string]string`). Treat **any** of: unmarshal error, empty map, or the `[]`-default JSON shape as "no cluster jumphosts — skip", exactly the way the existing `ip == "" || ip == "TGW jumphost not created"` guard short-circuits `tryAutoJumphost`. (The `try(…, [])` HCL default means the never-created case arrives as a JSON array `[]`, not a map — unmarshalling that into `map[string]string` fails; treat the failure as the no-op signal, do **not** surface it as a warning.)

### Shared-key reuse

The per-AZ cluster jumphosts share the *same* key as the TGW jumphost. Reuse the exact `keyPEM := stringOutput(outputs, "jumphost_shared_key")` presence check `tryAutoJumphost` already performs — no new terraform output, and every `jumphost-<zone>` target gets `KeySource: "tf-output:jumphost_shared_key"`, identical to the singular `jumphost`. If `jumphost_shared_key` is absent/empty, skip the per-AZ registration too (parity with the singular path).

### Idempotent upsert

For each `zone => fip`:

```go
remote.SetTarget(cctx.WorkspaceName, "jumphost-"+zone, config.TargetCfg{
    Host:      fip,
    User:      "ubuntu",
    KeySource: "tf-output:jumphost_shared_key",
})
```

`SetTarget` is already idempotent/upsert (`internal/remote/targets.go`), so re-running `up` after a floating-IP rotation refreshes the `jumphost-<zone>` host in place — the same "auto-seeded targets follow IP rotation" contract the singular `jumphost` already documents (Chapter 15 §"Auto-discovery from terraform outputs"). Implement as a sibling `tryAutoClusterJumphosts` called immediately after `tryAutoJumphost` from the same post-`up` hook site, so the two share posture but stay independently testable.

### Best-effort / non-fatal

Exactly `tryAutoJumphost`'s posture: any failure (output read, map parse, a single `SetTarget`) logs one `warning:` line to stderr and does **not** fail `up`. `up` succeeded because terraform succeeded; targets are a post-apply convenience, not part of the apply's contract. One summary line on success:

```
✓ Auto-registered N per-AZ cluster jumphost targets (jumphost-<z1>, jumphost-<z2>, …); use `roksbnkctl --on jumphost-<zone> ...`
```

### Stale-target handling — option (a) upsert-only (decided)

**Integrator decision (recorded in `prompts/sprint13/README.md` and `docs/PLAN.md` §"Sprint 13"): stale-target handling is option (a), upsert-only, for `v1.5.0`.** Unlike the singular `jumphost` (always overwritten in place, never removed), the *set* of `jumphost-<zone>` targets can shrink across applies — a zone removed, or `testing_create_cluster_jumphosts` flipped to `false`. An upsert-only loop leaves orphaned `jumphost-<oldzone>` entries pointing at destroyed hosts until the user runs `roksbnkctl targets remove jumphost-<oldzone>` by hand. **This caveat is accepted and documented**, not solved, in `v1.5.0`. It is called out:

- in this PRD (here);
- in the CHANGELOG `v1.5.0` `### Added` bullet;
- where chapter 15/16 describe the auto-registration (Chapter 15 §"Auto-discovery from terraform outputs", cross-linked to Chapter 15 §"Host-key TOFU" for the per-IP known-hosts implication of a re-created host on a recycled FIP).

Option (b) — reconcile (sweep `jumphost-*` targets not present in the current output map, then upsert) — is **explicitly out of scope for `v1.5.0`** and a tracked post-`v1.5.0` follow-up. It cannot be done safely without prefix-ownership semantics: a naive `jumphost-`-prefix sweep would delete a user's hand-named `jumphost-mybox`. Doing (b) safely needs either a constrained zone-pattern match or a `config.TargetCfg` `auto: true` schema marker — a config-schema change, deliberately deferred. See §"Open questions" and §"Out of scope".

## Resolved design decisions

Locked in for `v1.5.0`:

1. **Read `testing_cluster_jumphost_ips`** (the real top-level output), not `…_public_ips` (module-internal only). See the deviation note in §"Design".
2. **Sibling `tryAutoClusterJumphosts`** beside `tryAutoJumphost`, called from the same post-`up` hook site — shared posture, independent test surface.
3. **Reuse `jumphost_shared_key`** — no new terraform output; `KeySource: "tf-output:jumphost_shared_key"` on every per-AZ target, identical to the singular `jumphost`.
4. **Idempotent upsert via the existing `SetTarget`** — re-`up` follows FIP rotation in place, matching the singular `jumphost` contract.
5. **Best-effort / non-fatal** — `warning:` to stderr, never fails `up`; parity with `tryAutoJumphost`.
6. **Empty/`[]`/absent map → silent no-op** — no error, no warning, no spurious targets; behavior identical to pre-v1.5.0.
7. **Stale-target handling = option (a) upsert-only** (integrator decision) with the orphan caveat documented in PRD + CHANGELOG + chapter 15. Option (b) reconcile is a tracked post-v1.5.0 follow-up.
8. **Name format `jumphost-<zone>`** — zone string verbatim from the map key (e.g. `jumphost-ca-tor-1`).

## Open questions

1. **Option (b) reconcile, post-v1.5.0.** When orphaned-target confusion is reported, implement reconcile — but only with unambiguous ownership: either a constrained match against the known zone pattern, or a `config.TargetCfg` `auto: true` (or `managed_by: roksbnkctl`) schema field set on auto-seeded targets so the sweep never touches a user's hand-named entry. The schema-marker route is the cleaner long-term answer and also lets a future `targets list` annotate which targets are auto-managed. Tracked as a post-`v1.5.0` follow-up, not scoped here.
2. **Should the singular `jumphost` also gain the `auto:` marker for symmetry?** If option (b) lands with a schema marker, the singular `jumphost` would want it too for a consistent "what is auto-managed" story. Bundled with the option-(b) follow-up, not this cycle.

## Out of scope

- **Option (b) reconcile and any `config.TargetCfg` schema change** — explicitly deferred to a post-`v1.5.0` follow-up (integrator decision); see §"Open questions".
- **Changing the singular TGW `jumphost` seed behaviour** — unchanged by this PRD.
- **`--on`-time lazy discovery** — this is a post-`up`-hook registration feature, not a lazy resolver; `--on jumphost-<zone>` resolves a config entry that `up` wrote, exactly like `--on jumphost`.
- **Surfacing the per-AZ *private* IPs as targets** — there is no top-level `testing_cluster_jumphost_private_ips` output (module-internal only); the hop-via-`jumphost` pattern using private IPs is a documentation pattern in chapter 16, not an auto-registered target.
- **Removing the manual `targets add` path** — it stays valid; the chapter 15/16 docs keep it as a brief pre-`v1.5.0` fallback aside.

## Cross-references

- [`issues/issue_sprint13_staff.md` Issue 3](../../issues/issue_sprint13_staff.md) — the implementation-ready design surface this PRD formalizes (note the output-name deviation resolved in §"Design").
- [`issues/issue_sprint13_architect.md` Issue 1](../../issues/issue_sprint13_architect.md) — the coupled chapter 15/16 documentation deliverable; ships in lockstep with the code.
- [PRD 08 — read-only `terraform`](./08-TERRAFORM-READONLY.md) — the `roksbnkctl terraform output testing_cluster_jumphost_ips` one-liner the docs use to *show* the IPs; complementary (PRD 08 is the manual-lookup path this PRD automates away).
- [PRD 01 — SSH client + `--on` flag](./01-SSH-AND-ON-FLAG.md) — `tryAutoJumphost` and the singular `jumphost` seed this PRD mirrors.
- [`internal/cli/lifecycle.go`](../../internal/cli/lifecycle.go) — `tryAutoJumphost` (pattern to mirror), the post-`up` hook call site, and where `tryAutoClusterJumphosts` + `mapOutput` land.
- [`internal/remote/targets.go`](../../internal/remote/targets.go) — the idempotent `SetTarget` upsert.
- [`terraform/outputs.tf`](../../terraform/outputs.tf) — `testing_cluster_jumphost_ips` (the real top-level output) and `jumphost_shared_key`.
- [`docs/PLAN.md` §"Sprint 13"](../PLAN.md) — the cycle frame and the option-(a) integrator decision this PRD reflects.
- [Chapter 15 §"Auto-discovery from terraform outputs"](../../book/src/15-ssh-targets.md) / [Chapter 16](../../book/src/16-on-flag-ssh-jumphosts.md) — the user-facing surface for the auto-registered `jumphost-<zone>` targets and the orphan caveat.
