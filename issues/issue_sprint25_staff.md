# Sprint 25 — staff issues (orphan-sweep doctor diagnostic)

> **Surfaced 2026-05-28** during the v1.7.1 post-release canada-roks
> cleanup. After the release shipped and the verify cluster was torn
> down, a `roksbnkctl up --auto` failed with three `Provided Name …
> is not unique` / `gateway with the same name already exists`
> collisions: `canada-j-vpc`, `canada-roks-vpc`, and `canada-roks-tgw`
> already existed in IBM Cloud but were NOT in any terraform state.
> They predated the v1.7.1 work entirely — stranded by a pre-Sprint-22
> `down` (before the composite-dispatch fix landed in `18415eb`), which
> destroyed the trial phase, never chained to the cluster phase, and
> left cluster-shared infrastructure orphaned. No subsequent `down`
> reclaimed them because terraform only destroys what's in its state,
> and these resources were never recorded there. The operator had to
> identify + delete them by hand before `up` would succeed.

`Status: open` (forward placeholder — not yet dispatched).

---

## Issue 1 — `roksbnkctl doctor --orphan-sweep` cross-check of cloud resources vs. terraform state

**Severity**: low-medium (diagnostic / safety-net, not a correctness
fix). The underlying bug class that STRANDS orphans is already closed
(Sprint 22 composite-down dispatch + Sprint 23 phase-separation
hardening). This issue is about DETECTING pre-existing orphans —
resources stranded by pre-fix `down` runs that linger in the account,
cost money, and block future `up` runs with name collisions. It does
not prevent new orphans (that's what Sprint 22+23 did); it surfaces
the damage already done.
**Status**: open

### Motivation

The 2026-05-28 incident is the concrete case: three cluster-shared
resources (`canada-j-vpc`, `canada-roks-vpc`, `canada-roks-tgw`) sat
in the operator's IBM Cloud account for an unknown duration, invisible
to every `roksbnkctl` command because:

1. They were not in `~/.roksbnkctl/canada-roks/state/terraform.tfstate`
   or `state-cluster/terraform.tfstate` — so `down` never touched them.
2. `roksbnkctl status` reports on the workspace's terraform state +
   cluster identity, not on a reverse-lookup of cloud resources that
   *should* belong to the workspace but aren't tracked.
3. The only symptom was a downstream `up` failure: terraform tried to
   CREATE `canada-roks-vpc`, the IBM API rejected the duplicate name,
   and the operator had to manually run `ibmcloud is vpcs | grep
   canada`, identify the orphans, and delete them (subnets → public
   gateways → SGs → VPC; connections → TGW).

A `doctor --orphan-sweep` subcommand would have surfaced this in one
read-only command: "3 cloud resources match this workspace's naming
prefix but are not in terraform state — possible orphans from a
pre-fix teardown."

### Proposed surface

```
roksbnkctl doctor --orphan-sweep [-w <workspace>]
```

Read-only by default. For each workspace (or the `-w` target), derive
the resource-naming prefix(es) the workspace's tfvars would produce —
`openshift_cluster_name` (e.g. `canada-roks`), the VPC names
(`<cluster>-vpc`, the testing client VPC name), the TGW name
(`<cluster>-tgw`), the registry COS name (`<cluster>-cos-instance` /
`<cluster>-cos`) — then query IBM Cloud for resources matching those
prefixes and cross-check against what's in the workspace's terraform
state. Report three buckets:

- **Tracked + present**: in state AND in cloud → healthy, no action.
- **Tracked + missing**: in state but NOT in cloud → drift (terraform
  will recreate on next apply; usually benign, surfaced for awareness).
- **Untracked + present (ORPHAN)**: in cloud, matches the workspace
  naming prefix, but NOT in state → the damage class this issue
  targets. Print the resource type + id + name + the
  `ibmcloud … delete` command the operator would run, OR (future
  `--fix` flag) offer to delete them after confirmation.

### Scope guards (do NOT relitigate)

- **Detection only in the first cut.** No auto-delete. A
  `--orphan-sweep --fix` that walks the VPC dependency graph
  (instances → FIPs → SGs → public gateways → subnets → VPC;
  connections → TGW) and deletes after a confirmation prompt is a
  SEPARATE, higher-blast-radius scope decision deferred to a later
  sprint. The 2026-05-28 manual cleanup proved the dependency-graph
  walk is non-trivial and error-prone; auto-delete needs its own
  careful design + live verify. First cut FLAGS, the operator deletes.
- **Naming-prefix heuristic is best-effort.** A resource named
  `canada-roks-vpc` is *probably* this workspace's orphan, but the
  match is a heuristic, not proof of ownership (an operator could
  have another workspace with the same cluster name, or a manually-
  created resource that happens to collide). The report must phrase
  findings as "possible orphans matching prefix X" and never assert
  ownership. This is why `--fix` is deferred — auto-deleting on a
  heuristic match is dangerous.
- **Out of scope**: account-wide sweep (no `--all-workspaces` in the
  first cut), cross-region sweep (honor the workspace's
  `ibmcloud.region`), non-prefix-matchable resources (resources with
  operator-customized names that don't follow the `<cluster>-*`
  convention — those can't be heuristically attributed).

### Acceptance criteria

1. `roksbnkctl doctor --orphan-sweep -w <ws>` runs read-only against
   the workspace's region, derives the naming prefixes from the
   workspace config + tfvars, queries IBM Cloud (VPCs, subnets,
   public gateways, security groups, floating IPs, instances,
   transit gateways, COS instances), and prints the three-bucket
   report.
2. The ORPHAN bucket prints, per finding: resource type, id, name,
   region, and the manual `ibmcloud … delete` command (or a pointer
   to the dependency-graph order for VPCs).
3. Exit code: 0 when no orphans found; non-zero (e.g. 3) when orphans
   ARE found, so CI / scripted flows can assert clean state. Mirrors
   the `test` suite's exit-code-as-assertion convention.
4. Hermetic test class: mock the IBM Cloud client (the
   `internal/ibm` package's interface) so the cross-check logic is
   testable without a live account. Pin the three-bucket
   classification + the exit-code contract.

### Files affected (probable)

- `internal/cli/doctor.go` (or wherever `doctorCmd` lives) — add the
  `--orphan-sweep` flag + its RunE branch.
- `internal/ibm/` — possibly a new list-by-prefix helper if the
  existing client doesn't expose the needed list calls.
- `internal/config/` — a helper to derive the naming prefixes from
  workspace config + tfvars (the `<cluster>-vpc` / `<cluster>-tgw` /
  `<cluster>-cos-instance` formulas live in the upstream HCL; mirror
  them here or read them from the rendered tfvars).
- `internal/cli/doctor_orphan_sweep_test.go` — new additive hermetic
  test with a mocked IBM client.
- `book/src/` — a doctor chapter subsection documenting the flag.
  (Architect/tech-writer scope, not staff.)

### Related

- `issues/issue_sprint22_staff.md` + `issues/issue_sprint23_staff.md`
  — the composite-down dispatch fix + phase-separation hardening that
  STOP new orphans from being created. This issue is the detection
  complement: it finds orphans created by PRE-fix `down` runs that
  those sprints can't retroactively clean.
- The 2026-05-28 canada-roks incident — three orphans
  (`canada-j-vpc`, `canada-roks-vpc`, `canada-roks-tgw`) blocked
  `up` with name collisions; manual `ibmcloud is`/`tg` inventory +
  delete was the only recovery. A `doctor --orphan-sweep` would have
  surfaced them proactively.
- Integrator memory [[live-verify-high-issues]] — applies to the
  future `--fix` flag (auto-delete is resource-affecting and needs a
  live verify), NOT to the detection-only first cut (read-only,
  hermetic-test sufficient).
- Integrator memory [[no-piling-into-active-release]] — this was
  correctly NOT folded into the v1.7.1 cut; it's a fresh
  forward-sprint candidate filed after the release shipped.
