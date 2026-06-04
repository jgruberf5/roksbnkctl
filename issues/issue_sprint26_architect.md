# Sprint 26 — architect issues (naming scheme, constraint table, book + config-reference authoring)

> **Sprint 26 frame.** Feature sprint making a single **workspace prefix**
> the base for every account-scoped IBM Cloud resource name, with
> per-resource-type length/charset validation, so the recurring
> cross-workspace name collisions (the class that stranded
> `canada-roks-*` in the 2026-05-28 incident — see
> `issues/issue_sprint25_staff.md`) stop happening. Staff
> (`issues/issue_sprint26_staff.md`) owns all the Go: `internal/naming`,
> the `config.Workspace` additions, the full `terraform.tfvars` render,
> and the rewritten `init` interview. This issue owns the **design inputs
> staff codes against** — the suffix scheme and the authoritative
> length/charset constraint table — and the **operator-facing prose** in
> the book + the configuration reference.

`Status`: resolved

---

## Issue 1 — Authoritative naming scheme + constraint table, and book / config-reference authoring

**Severity**: medium (design + docs; no Go — staff owns the binary
surface). The constraint table is a **blocking input** to staff: staff
must not invent IBM Cloud limits.
**Status**: resolved

### Motivation

The refactor hinges on two design artifacts that must be correct and
sourced before (or alongside) staff's implementation, plus the operator
documentation that explains the new `init` flow and the new `config.yaml`
shape:

1. **A compact, collision-free suffix scheme** so prefix-derived names stay
   within the tightest IBM Cloud limit (the ROKS cluster name).
2. **A per-resource-type length/charset constraint table** with the exact
   limits **cited** from IBM Cloud's own validators, so staff's
   `internal/naming` rejects bad prefixes at `init` time rather than
   letting IBM Cloud reject them mid-`apply`.
3. **Docs** — the init chapter, a new "resource naming & collision
   avoidance" concept section, and the configuration-reference entry for
   the new `prefix` + `resources` fields.

### Deliverable 1 — suffix scheme (the design staff's `Derive` implements)

| Resource | tfvars variable | Derived name | Binding limit |
|----------|-----------------|--------------|---------------|
| ROKS cluster | `openshift_cluster_name` | `<prefix>` | cluster (~35) |
| Cluster VPC | `roks_cluster_vpc_name` | `<prefix>-cluster-vpc` | IS-name (63) |
| Registry COS | `roks_cos_instance_name` | `<prefix>-registry-cos` | COS (180) |
| Transit Gateway | `roks_transit_gateway_name` | `<prefix>-tgw` | IS-name (63) |
| Client VPC | `testing_client_vpc_name` | `<prefix>-client-vpc` | IS-name (63) |
| TGW jumphost | `testing_tgw_jumphost_name` | `<prefix>-jh-tgw` | IS-name (63) |
| Cluster jumphosts | `testing_cluster_jumphost_name_prefix` | `<prefix>-jh` (+ `-<zone>` appended by the module) | IS-name (63) incl. zone |

Cluster name == prefix (no suffix) intentionally makes the prefix length
limit equal the tightest resource limit, so a valid prefix guarantees every
other derived name fits. Document this rationale so a future contributor
doesn't "tidy" the cluster name into `<prefix>-cluster` and silently shrink
the usable prefix length.

### Deliverable 2 — constraint table (the authoritative limits staff codes)

Pin the exact values + cite the source for each. Confirm against the IBM
Terraform provider's validators
(`github.com/IBM-Cloud/terraform-provider-ibm`, e.g. the
`validateClusterName` / IS-resource name validators) and/or the IBM Cloud
API docs; record the citation inline so the numbers are auditable. Starting
values to verify (do not ship unverified):

| Kind | MaxLen | Charset rule | Source to cite |
|------|--------|--------------|----------------|
| ROKS/IKS cluster name | **35** (verify) | `^[a-z][-a-z0-9]*[a-z0-9]$` | provider `validateClusterName` / `ibmcloud oc cluster create` help |
| IS resource (VPC, VSI, TGW, SSH key) | **63** | same IS rule (start letter, lower-alnum + hyphen, no trailing `-`) | provider IS-name validator / VPC API |
| COS resource instance | **180** (verify) | permissive; safe to reuse the lower-alnum subset since names derive from a lowercase prefix | Resource Controller / COS docs |

If a verified limit differs from the above, the table is the single source
of truth and staff codes the verified value — flag any delta in the
closure.

### Deliverable 3 — book + configuration-reference authoring

Author/refresh the operator-facing prose (architect owns book content;
tech-writer's later drift sweep re-captures binary-reflected chapters like
the command reference):

1. **Init chapter** — rewrite the interview walkthrough: the workspace
   **prefix** prompt (with the validation/re-prompt behavior and the max
   length), the per-resource **create-toggle** prompts, and the
   **existing-resource discovery** prompts (e.g. "Existing Transit Gateway
   name?" when TGW creation is declined but the jumphost needs it). Show the
   printed resolved name plan.
2. **New concept section — "Resource naming & collision avoidance"** —
   explain the prefix → name derivation, the suffix table, the length
   limits, why namespaces are *not* prefixed (cluster-internal, only
   collide on a shared cluster), and how `--var-file` /
   `terraform.tfvars.user` overrides any generated name. Cross-link to
   Sprint 25's `doctor --orphan-sweep` (the detection complement) when that
   lands.
3. **Configuration reference** — document the new `config.yaml` keys:
   `prefix` (string) and the `resources:` block (`transit_gateway`,
   `registry_cos`, `cert_manager`, `bnk`, `tgw_jumphost`,
   `cluster_jumphosts`, `client_vpc`, each `{create, existing}`). Note
   additivity (old configs load unchanged).
4. **`terraform/terraform.tfvars.example` header** — add a note that
   `roksbnkctl` now generates these names from the workspace prefix and
   that this file is the override reference layered as
   `terraform.tfvars.user`. Leave the per-variable examples intact.

### Scope guards

- **No Go.** `internal/naming`, `internal/config`, `internal/tf`,
  `internal/cli` are staff's surface. Architect ships the table + scheme as
  a spec and the prose.
- **Namespaces stay fixed** in the docs — don't document prefixed
  namespaces.
- **No new chapter file unless `SUMMARY.md` already has the slot** — prefer
  extending the existing init + configuration-reference chapters; check
  `book/src/SUMMARY.md` for whether the naming concept warrants its own
  entry vs. a subsection (mirror the Sprint 24 architect's SUMMARY check).
- Worked transcript output is **illustrative** until tech-writer
  re-captures it byte-for-byte against the built binary before the cut.

### Acceptance criteria

1. The suffix table + the verified constraint table land in this ledger's
   closure (or a `resolved_sprint26_architect.md`) with source citations,
   ready for staff to code against.
2. Init chapter covers prefix prompt + create toggles + existing-resource
   discovery + the printed name plan.
3. A "resource naming & collision avoidance" section explains derivation,
   limits, namespace exemption, and override path.
4. Configuration reference documents `prefix` + the `resources:` block.
5. `terraform.tfvars.example` header updated.
6. mdbook builds (HTML + PDF) clean; no broken cross-links.

### Files affected (probable)

- `book/src/` — the init chapter, configuration-reference chapter, a
  naming concept section (subsection or new chapter per the SUMMARY check),
  and `book/src/SUMMARY.md` only if a new entry is added.
- `terraform/terraform.tfvars.example` — header note (coordinate with staff
  so the wording matches the rendered file).
- This ledger / a `resolved_sprint26_architect.md` — the verified tables.

### Related

- `issues/issue_sprint26_staff.md` — consumes the table + scheme.
- `issues/issue_sprint25_staff.md` — `doctor --orphan-sweep` derives the
  same `<prefix>-vpc` / `<prefix>-tgw` formulas this sprint makes canonical;
  cross-link in the concept section.
- `book/src/15-ssh-targets.md` / Sprint 24 architect closure — the
  chapter-authoring tone + the SUMMARY-no-subentries precedent.

---

## Closure — architect, 2026-06-04

`Status`: resolved (design + docs delivered; staff codes against the verified
table below).

### Verified constraint table (BLOCKING input to staff — code these values)

All three starting values **verified and unchanged** — **no delta**. Staff
codes these exact `{MaxLen, charset}` pairs into `internal/naming`.

| Kind | MaxLen | Charset rule | Cited source (verified 2026-06-04) |
|------|--------|--------------|-------------------------------------|
| ROKS / OpenShift cluster name | **35** | start with a letter; letters, digits, hyphen `-`; ≤ 35 chars | IBM-Cloud/terraform-provider-ibm provider docs, `ibm_container_cluster` `name` argument: *"must start with a letter, can contain letters, numbers, and hyphen (-), and must be 35 characters or fewer."* (`website/docs/r/container_cluster.html.markdown`) |
| IS resource (VPC, VSI, TGW, SSH key) | **63** | start with lowercase letter; lowercase letters, digits, hyphen `-`; must end alphanumeric; ≤ 63 | IBM-Cloud/terraform-provider-ibm `ValidateISName` in `ibm/validate/validators.go` — regex `^[a-z][-a-z0-9]*$`, end-check `.*[a-z0-9]$`, `if length <= 63`. |
| COS / Resource Controller instance | **180** | server-side permissive; `roksbnkctl` reuses the lowercase-alnum IS subset (name derives from an already-validated lowercase prefix) | IBM Cloud Resource Controller create-instance `name` field; corroborated by IBM/platform-services-go-sdk `resourcecontrollerv2` (sibling `CreateResourceAliasOptions.Name`: *"Must be 180 characters or less"*). The terraform-provider-ibm `ibm_resource_instance` schema applies **no** client-side `ValidateFunc` on `name`, so the 180 cap is the Resource Controller server constraint, not a provider validator. |

**Delta flag:** none. The starting values in Deliverable 2 (cluster 35, IS 63,
COS 180) all held on verification. The only nuance to record: the COS/Resource
Controller limit is **server-side** (no provider-side validator), so staff
should treat 180 as the authoritative API cap and apply the lowercase-alnum IS
subset for charset (safe because the COS name derives from the prefix, which is
already validated against the stricter cluster rule).

### Suffix scheme (final — `Derive(prefix)` implements this)

Cluster name == prefix (no suffix). This makes the prefix-length limit equal the
**tightest** resource limit (cluster, 35), so a valid prefix guarantees every
derived name fits its own limit. Verified arithmetic: at a worst-case 35-char
prefix, the longest derived IS name is the zone-appended cluster jumphost
`<prefix>-jh-us-south-1` = 49 chars, well inside 63.

| Resource | tfvars variable | Derived name | Suffix | Binding limit |
|----------|-----------------|--------------|--------|---------------|
| ROKS cluster | `openshift_cluster_name` | `<prefix>` | *(none)* | cluster (35) |
| Cluster VPC | `roks_cluster_vpc_name` | `<prefix>-cluster-vpc` | `-cluster-vpc` | IS (63) |
| Registry COS | `roks_cos_instance_name` | `<prefix>-registry-cos` | `-registry-cos` | COS (180) |
| Transit Gateway | `roks_transit_gateway_name` | `<prefix>-tgw` | `-tgw` | IS (63) |
| Client VPC | `testing_client_vpc_name` | `<prefix>-client-vpc` | `-client-vpc` | IS (63) |
| TGW jumphost | `testing_tgw_jumphost_name` | `<prefix>-jh-tgw` | `-jh-tgw` | IS (63) |
| Cluster jumphosts | `testing_cluster_jumphost_name_prefix` | `<prefix>-jh` (+ module-appended `-<zone>`) | `-jh` | IS (63) incl. zone |

### Files touched

Within the architect's ownership boundary (`book/src/**`,
`terraform/terraform.tfvars.example` header, this closure). **No** Go,
`internal/`, `cmd/`, `*_test.go`, other roles' issues, `docs/PLAN.md`, or
`prompts/` were touched.

- `book/src/13-terraform-variables.md` — new concept section **"Resource naming
  & collision avoidance"** (prefix→name derivation table, the no-suffix-cluster
  rationale, the cited length/charset limits, namespace-exemption rationale,
  override path, backward-compat, forward cross-link to Sprint 25's
  `doctor --orphan-sweep`).
- `book/src/12-workspace-config.md` — teaching `prefix:` + `resources:`
  sections; rewrote the worked-example `init` interview (prefix prompt +
  validation/re-prompt note, create toggles, existing-resource discovery,
  printed resolved name plan, marked **illustrative**); updated top-level
  structure, the `cluster:` note, and the missing-field behaviour table.
- `book/src/28-configuration-reference.md` — `prefix:` field block, `resources:`
  block (with the `{create, existing}` schema + per-toggle tfvars mapping +
  additivity note), and both new entries added to the field-by-field reference
  table; updated top-level structure listing.
- `book/src/06-workspaces.md` — `init --var-file` note: it now also sets a
  sanitized `prefix` + default all-create `resources` non-interactively.
- `terraform/terraform.tfvars.example` — header note: names are now
  prefix-generated; this file is the override reference layered as
  `terraform.tfvars.user`; per-variable examples kept intact.

No `SUMMARY.md` edit — extended existing chapters (12/13/28/6) rather than
adding a new chapter file, per the SUMMARY-no-subentries precedent.

### mdbook build status

**Not built** — `mdbook` is not installed on this host (`mdbook` and
`mdbook-pandoc` both absent; `pandoc` present but unusable without
`mdbook-pandoc`). HTML+PDF build deferred to a host with the toolchain (the
`tools/docker/mdbook` image / `make release`). Mitigation: cross-chapter anchor
targets were verified by hand against the actual `##`/`###` headings —
`#resource-naming--collision-avoidance`, `#why-the-cluster-name-takes-no-suffix`,
`#the-length--charset-limits`, `#resources-block`,
`#worked-example-bootstrap-a-workspace-from-scratch`, and the pre-existing
`#skip-the-interview-init---var-file` all resolve.

### Notes for staff / integrator

- The `doctor --orphan-sweep` chapter does **not** yet exist in `book/src/`
  (Sprint 25 filed it as a staff placeholder). The concept section cross-links
  to it **forward-looking** ("when that lands") rather than to a live anchor, so
  there's no broken link.
- The ch12 worked example's later `roksbnkctl tfvars` snippet still shows the
  old non-canonical var names (`cluster_name`, `workers_per_zone`) — that
  pre-existing snippet is tech-writer's drift-sweep surface (binary-reflected),
  left untouched here.
