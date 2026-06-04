# Sprint 26 — staff issues (prefix-driven generated tfvars + init interview rewrite)

> **Surfaced 2026-06-04** as an integrator feature request. `roksbnkctl
> init` collects a few values into `config.yaml`, and at `up`-time
> `internal/tf/vars.go:RenderTFVars` renders a **sparse**
> `terraform.tfvars` — it emits only the handful of fields the operator
> set and lets every resource *name* fall through to the upstream
> Terraform module defaults (`tf-cluster-vpc`, `tf-openshift-cluster`,
> `tf-tgw`, `tf-testing-jumphost-*`, …). Because every workspace inherits
> the **same** default names, two workspaces that both create infra
> collide at the IBM Cloud account level (duplicate VPC / cluster / TGW /
> COS / VSI names). This is the same collision class that stranded
> `canada-j-vpc` / `canada-roks-vpc` / `canada-roks-tgw` in the
> 2026-05-28 incident behind `issues/issue_sprint25_staff.md` — Sprint 25
> *detects* orphans after the fact; this sprint *prevents* the collisions
> in the first place by making a single **workspace prefix** the base for
> every account-scoped resource name, deriving + length-validating all of
> them, and rendering a complete `terraform.tfvars` that drives a full
> `up`. There is also **no length validation** anywhere today, so an
> over-long name is only rejected by IBM Cloud mid-`apply`.

`Status: resolved` (integrated + live-verified GREEN 2026-06-04).

---

## Issue 1 — `internal/naming` package + full prefix-derived tfvars render + prefix-driven `init` interview

**Severity**: medium (collision-prevention + UX; changes default `init`
behavior and the rendered tfvars, but is backward-compatible for existing
workspaces — see scope guards).
**Status**: resolved

### Motivation

See the frame above. Three concrete defects this closes:

1. **Cross-workspace name collisions.** Every new workspace renders the
   same upstream default names, so the second workspace to `up` in an
   account hits `Provided Name … is not unique` / `gateway with the same
   name already exists`. The operator's only workaround today is to
   hand-author a `--var-file` overriding every name (Sprint 19's
   `terraform.tfvars.user` path).
2. **No length validation.** An over-long cluster/VPC/TGW name is only
   rejected mid-`apply` by IBM Cloud, after minutes of provisioning.
3. **Sparse, name-less generated tfvars.** The rendered `terraform.tfvars`
   omits every name variable, so `roksbnkctl` has no record of what it
   asked IBM Cloud to create — which is also why Sprint 25's
   `doctor --orphan-sweep` has to re-derive the names from a formula.

### Design source

The authoritative naming scheme + the per-resource length/charset
**constraint table** (exact IBM Cloud limits, with citations) are owned by
`issues/issue_sprint26_architect.md` Issue 1. Staff implements
`internal/naming` against that table — do **not** invent limits here;
consume the architect's pinned values. The user-confirmed design
decisions:

- **config.yaml is authoritative.** Store `prefix` + per-resource toggles
  + existing-resource refs in `config.yaml`; derive / validate / render
  the full name set into `terraform.tfvars` at every `up`/`plan`/`apply`.
  Names are deterministic from the prefix, so regeneration is safe.
- **Namespaces stay fixed** (`cert-manager`, `f5-bnk`, `f5-utils`) — only
  account-scoped IBM Cloud infra names are prefixed.
- **Reject + re-prompt on length overflow** — no silent truncation;
  non-TTY → hard error.

### Work

**1. New package `internal/naming/`** (`naming.go`):

- A constraint table keyed by resource kind (cluster / IS-resource (VPC,
  VSI, TGW, SSH key) / COS instance), each entry
  `{MaxLen int, Pattern *regexp.Regexp, Describe string}`. Values + source
  citations come from the architect's Issue 1 table.
- `type Plan struct { ClusterName, ClusterVPCName, COSInstanceName,
  TransitGatewayName, ClientVPCName, TGWJumphostName, ClusterJumphostPrefix string }`.
- `Derive(prefix string) Plan` — compact suffix scheme so the binding
  constraint is the ~35-char cluster limit (cluster name == prefix,
  everything else ≤ 63):
  - `ClusterName            = prefix`
  - `ClusterVPCName         = prefix + "-cluster-vpc"`
  - `COSInstanceName        = prefix + "-registry-cos"`
  - `TransitGatewayName     = prefix + "-tgw"`
  - `ClientVPCName          = prefix + "-client-vpc"`
  - `TGWJumphostName        = prefix + "-jh-tgw"`
  - `ClusterJumphostPrefix  = prefix + "-jh"`  (module appends `-<zone>`;
    longest zone `us-south-1` → ≤ 63 — validate including the zone suffix)
- `ValidatePrefix(prefix string) error` — validate the prefix label
  (lowercase, start with a letter, `[a-z0-9-]`, no trailing hyphen) **and**
  that every `Derive`d name passes its constraint. On overflow return one
  actionable error naming the offending resource, its computed length, its
  limit, and the **max allowable prefix length** (computed from the table,
  not hard-coded).
- `SanitizeToPrefix(workspaceName string) string` — default-prefix seed
  from the workspace name: lowercase, map `_`/`.`→`-`, strip leading
  non-letter, collapse repeats, trim trailing `-`, cap length. Idempotent.

**2. `config.Workspace` schema** (`internal/config/workspace.go`) — purely
additive (`omitempty`), so old `config.yaml` loads unchanged:

```go
Prefix    string        `yaml:"prefix,omitempty"`
Resources *ResourcesCfg `yaml:"resources,omitempty"`
```
```go
type ResourcesCfg struct {
    TransitGateway   ResourceToggle `yaml:"transit_gateway"`
    RegistryCOS      ResourceToggle `yaml:"registry_cos"`
    CertManager      ResourceToggle `yaml:"cert_manager"`
    BNK              ResourceToggle `yaml:"bnk"`
    TGWJumphost      ResourceToggle `yaml:"tgw_jumphost"`
    ClusterJumphosts ResourceToggle `yaml:"cluster_jumphosts"`
    ClientVPC        ResourceToggle `yaml:"client_vpc"`
}
type ResourceToggle struct {
    Create   bool   `yaml:"create"`
    Existing string `yaml:"existing,omitempty"` // existing name/ID when Create=false
}
```
Reuse the existing `ClusterCfg.Create` + `ClusterCfg.Name` for the cluster
(Name doubles as the existing id/name when `Create=false`, as today).

**3. Full render** (`internal/tf/vars.go:RenderTFVars`). When
`ws.Prefix != ""`, emit the complete **de-duplicated** set in the single
`terraform.tfvars` file: region, RG, `create_roks_cluster` +
(`openshift_cluster_name` | `roks_cluster_id_or_name`),
`roks_cluster_vpc_name`, `create_roks_registry_cos_instance` +
`roks_cos_instance_name` (or `.Existing`), `create_roks_transit_gateway` +
`roks_transit_gateway_name` (or `.Existing`), `install_cert_manager`,
`deploy_bnk`, `testing_create_tgw_jumphost` + `testing_tgw_jumphost_name`,
`testing_create_client_vpc` + `testing_client_vpc_name` (or `.Existing`),
`testing_create_cluster_jumphosts` + `testing_cluster_jumphost_name_prefix`,
plus the existing `kubeconfig_dir` / `scratch_dir` / BNK fields. Each
variable emitted **exactly once** (in-file duplicates are a terraform
error). The toggles here are the operator's first-phase intent; the
existing separate `bnk-phase-override.tfvars` (live second-phase handoff,
`internal/orchestration/second_phase_reuse.go`) still layers last as its
own `-var-file` and forces `create_roks_transit_gateway=false`,
`testing_create_client_vpc=false`, `use_existing_cluster_vpc=true`,
`existing_cluster_vpc_id` — cross-file override, unchanged, no in-file
duplicate.

**Backward compat:** when `ws.Prefix == ""` (legacy config), keep the
current sparse rendering verbatim. Delete the **vestigial**
`RenderTFVarsWithClusterOutputs` / `WriteTFVarsWithClusterOutputs` (no live
callers — confirmed; live handoff is the separate override file) and let
validator drop their dead test `internal/tf/secondphase_handoff_test.go`.

**4. Interview rewrite** (`internal/cli/init.go`, helpers in `prompt.go`).
No-`--var-file` flow (credential verify, TF-source prompt, and API-key
persistence stay byte-unchanged):
- **Prefix loop**: default `= ws.Prefix` (re-init) else
  `naming.SanitizeToPrefix(workspaceName)`; prompt → `ValidatePrefix`; on
  failure print the actionable message + re-prompt. Non-TTY: validate the
  default, hard-error if invalid (mirror the
  `cred.Resolver{NonInteractive:true}` pattern at `lifecycle.go:829`).
- **Toggle prompts** (`promptYesNo`), each with a sensible default; on
  "no" + a dependent that needs the name, prompt for the existing
  resource's name/ID. Dependency edges (only prompt for an existing name
  when an enabled resource consumes it):
  - Create new ROKS cluster? `[Y]` → no ⇒ "Existing cluster name or ID?" → `Cluster.Name`.
  - Create registry COS instance? `[Y]` (only when cluster created) → no ⇒ "Existing COS instance name?" → `RegistryCOS.Existing`.
  - Create Transit Gateway? `[Y]` → no ⇒ "Existing Transit Gateway name?" → `TransitGateway.Existing` (needed when the TGW jumphost is on).
  - Install cert-manager? `[Y]` → `CertManager.Create`.
  - Deploy BIG-IP Next (BNK)? `[Y]` → `BNK.Create`.
  - Create TGW test jumphost? `[Y]` → `TGWJumphost.Create`; if yes → Create a new client VPC for it? `[n]` → no ⇒ "Existing client VPC name?" → `ClientVPC.Existing`.
  - Create per-zone cluster jumphosts? `[n]` → `ClusterJumphosts.Create`.
- Build `config.Workspace{Prefix, Cluster, Resources}`, **print the
  resolved `naming.Plan`** to stderr so the operator sees the generated
  names, then `SaveWorkspace`.
- **`--var-file` path**: keep the Sprint 19 seeding + verbatim copy to
  `terraform.tfvars.user`, **plus** set `Prefix =
  SanitizeToPrefix(workspaceName)` (or seed from the file's
  `openshift_cluster_name`) non-interactively + default `Resources` to
  all-create, so the generated base is collision-safe and the user's file
  still overrides via layering.

### Scope guards (do NOT relitigate)

- **No namespace prefixing** — `cert_manager_namespace`, `flo_namespace`,
  `flo_utils_namespace` stay at their conventional fixed values.
- **No auto-truncation / hashing** — overflow rejects + re-prompts.
- **Backward compatible** — empty-`Prefix` legacy configs keep the old
  sparse render; old `config.yaml` loads without migration.
- **Don't touch the phase-handoff override files** — the cluster-phase
  override and `bnk-phase-override.tfvars` stay as-is; the full render only
  changes the *base* `terraform.tfvars`.
- **Don't change credential / TF-source / API-key-persistence code** in
  `init.go` — only the interview body between RG resolution and
  `SaveWorkspace`.

### Acceptance criteria

1. `roksbnkctl init -w demo` (accept defaults) writes `config.yaml` with
   `prefix: demo` + a `resources:` block, prints the resolved name plan,
   and a subsequent render produces `state*/terraform.tfvars` containing
   `openshift_cluster_name = "demo"`, `roks_cluster_vpc_name =
   "demo-cluster-vpc"`, `roks_transit_gateway_name = "demo-tgw"`, etc. — no
   `tf-*` defaults.
2. An over-long prefix is rejected with the offending-resource + max-prefix
   message and re-prompts (TTY) or hard-errors (non-TTY).
3. Answering "no" to a create toggle with a live dependent prompts for the
   existing resource's name and renders it into the matching `*_name`
   variable with the `create_* = false` toggle.
4. A supplied `--var-file` still seeds + copies `terraform.tfvars.user`
   and that file's values override the generated base via `varFiles()`
   layering.
5. Each variable appears at most once in the rendered `terraform.tfvars`;
   the second-phase `bnk-phase-override.tfvars` still forces reuse.
6. An existing pre-Sprint-26 `config.yaml` (no `prefix`) renders the old
   sparse tfvars unchanged.
7. `go build ./...`, `go vet ./...` clean.

### Files affected

- **New**: `internal/naming/naming.go`.
- `internal/config/workspace.go` — additive `Prefix` + `ResourcesCfg` /
  `ResourceToggle`.
- `internal/tf/vars.go` — full render under `Prefix != ""`; keep sparse for
  legacy; delete vestigial `RenderTFVarsWithClusterOutputs`; trim
  `internal/tf/terraform.go:WriteTFVarsWithClusterOutputs`.
- `internal/cli/init.go` (+ small additions to `prompt.go` if a
  validate-and-reprompt helper is warranted).
- `terraform/terraform.tfvars.example` — header note that names are now
  prefix-generated (file remains the override reference). *(Coordinate with
  architect, who owns the prose.)*
- Tests are validator's surface (Issue below).

### Related

- `issues/issue_sprint26_architect.md` — the constraint table + naming
  scheme + book/config-reference authoring this implements.
- `issues/issue_sprint26_validator.md` — hermetic + gated-live coverage.
- `issues/issue_sprint25_staff.md` — `doctor --orphan-sweep` (detection
  complement; once names are config-recorded, the orphan-sweep can read
  them from the rendered tfvars instead of re-deriving a formula).
- `internal/tf/vars.go:RenderTFVars` + `internal/orchestration/second_phase_reuse.go`
  (`writeBnkPhaseOverride`) — the render + handoff seam.
- Sprint 19 (`issues/issue_sprint19_*` / `docs/PLAN.md`) — the
  `terraform.tfvars.user` override-layering this preserves.
- Integrator memory [[live-verify-high-issues]] — the rendered-tfvars
  change is `up`-affecting; gate closure on a live `init → plan` verify
  (validator builds the gated-live driver).

---

## Closure — staff, 2026-06-04

### What shipped

All four work items in Issue 1 are implemented; the Go surface is
complete and `go build ./...` is clean.

1. **New package `internal/naming` (`naming.go`).**
   - Single clearly-commented constraint table (cluster 35 / IS 63 / COS
     180) with the `// Sprint 26: verify against architect's pinned
     constraint table in issues/issue_sprint26_architect.md` reconcile
     marker. Charset rule is the shared lowercase-label regex
     `^([a-z]|[a-z][-a-z0-9]*[a-z0-9])$` (single `[a-z]` valid), reused
     for cluster + IS + COS (COS reuses the lowercase subset since names
     derive from a lowercase prefix).
   - `Plan` struct + `Derive(prefix)` (compact suffix scheme:
     cluster==prefix, `-cluster-vpc`, `-registry-cos`, `-tgw`,
     `-client-vpc`, `-jh-tgw`, `-jh`).
   - `ValidatePrefix(prefix)` — label rule + every derived name fits
     (including the module-appended `-<zone>` on the cluster-jumphost
     prefix, budgeted with the longest zone `-us-south-1`). Overflow
     error names the offending resource, its computed length, its limit,
     and the table-computed max prefix length. `MaxPrefixLen()` is
     exported for the prompt label (currently 35, bound by the cluster
     limit — verified by a scratch driver).
   - `SanitizeToPrefix(workspaceName)` — lowercase, `_`/`.`→`-`, strip
     other non-`[a-z0-9-]`, collapse hyphen runs, strip leading
     non-letters, trim trailing `-`, cap length; idempotent (verified).

2. **`config.Workspace` schema (`internal/config/workspace.go`).** Added
   `Prefix string` + `Resources *ResourcesCfg` (both `omitempty`) and the
   `ResourcesCfg` / `ResourceToggle{Create, Existing}` types. Purely
   additive — old `config.yaml` loads unchanged. Cluster reuses
   `ClusterCfg.Create`/`.Name` per spec.

3. **Full render (`internal/tf/vars.go:RenderTFVars`).** Branches on
   `ws.Prefix`: empty → legacy sparse body (factored into
   `renderSparseBody`, byte-identical to pre-Sprint-26); non-empty →
   `renderFullBody` emitting the complete de-duplicated set (region, RG,
   `create_roks_cluster` + name carrier, `roks_cluster_vpc_name`,
   `create_roks_registry_cos_instance` + name/existing,
   `create_roks_transit_gateway` + name/existing, `install_cert_manager`,
   `deploy_bnk`, `testing_create_tgw_jumphost` + name,
   `testing_create_client_vpc` + name/existing,
   `testing_create_cluster_jumphosts` +
   `testing_cluster_jumphost_name_prefix`, plus the shared kubeconfig /
   scratch / BNK fields). A scratch driver confirmed **each variable is
   emitted exactly once** and the legacy path is unchanged. A nil
   `Resources` on a prefix-set config defensively defaults to all-create.
   Created resources get the prefix-derived name; declined-but-depended-on
   resources get the operator's `Existing` name/ID.

4. **Vestigial deletions.** Removed
   `RenderTFVarsWithClusterOutputs` (vars.go) and
   `WriteTFVarsWithClusterOutputs` (terraform.go) — no live callers
   (confirmed by grep; the live second-phase handoff is the separate
   `bnk-phase-override.tfvars` in `internal/orchestration/second_phase_reuse.go`,
   left untouched).

5. **Interview rewrite (`internal/cli/init.go`).** Between RG resolution
   and `SaveWorkspace` (credential verify / TF-source prompt / API-key
   persistence untouched):
   - `--var-file` flow → `seedVarFileInterview`: keeps the Sprint 19
     seed-driven cluster block + verbatim `terraform.tfvars.user` copy
     (unchanged), additionally sets `Prefix =
     SanitizeToPrefix(openshift_cluster_name else workspaceName)` and
     `Resources` to all-create.
   - interactive flow → `runPrefixInterview`: `promptPrefix` loop
     (default = existing `ws.Prefix` else `SanitizeToPrefix(workspaceName)`;
     validate + re-prompt on failure with the actionable message; non-TTY
     validates the default and hard-errors if invalid), then the cluster
     create toggle (name derived from prefix when creating; existing
     id/name prompt when not) and the per-resource toggles with the
     dependency-edge existing-resource prompts (cluster→`Cluster.Name`;
     registry COS→`RegistryCOS.Existing` when cluster created; TGW→
     `TransitGateway.Existing` when the jumphost is on; client VPC→
     `ClientVPC.Existing` when the TGW jumphost is on and the client VPC
     is declined).
   - both flows then `printNamePlan` (resolved `naming.Plan` to stderr)
     and `SaveWorkspace`.

### Files touched

- **New** `internal/naming/naming.go`.
- `internal/config/workspace.go` — additive `Prefix` + `ResourcesCfg` /
  `ResourceToggle`.
- `internal/tf/vars.go` — full/sparse split; deleted vestigial
  `RenderTFVarsWithClusterOutputs`.
- `internal/tf/terraform.go` — deleted vestigial
  `WriteTFVarsWithClusterOutputs`.
- `internal/cli/init.go` — interview rewrite + helpers (`promptPrefix`,
  `runPrefixInterview`, `seedVarFileInterview`, `printNamePlan`,
  `allCreateResources`, `planNameOrExisting`).

Not touched (per scope guards): `prompt.go` needed no new helper
(`promptString` already returns the default and re-prompting is a simple
loop in `promptPrefix`), `init_var_file.go`,
`internal/orchestration/second_phase_reuse.go`, all phase-override files,
`book/**`, `terraform/terraform.tfvars.example`, every `*_test.go`,
`docs/PLAN.md`, `prompts/`.

### Build / vet results

- `go build ./...` — **clean.**
- `go vet ./...` — clean for **every package except `internal/tf`**,
  which fails ONLY because the validator-owned
  `internal/tf/secondphase_handoff_test.go` still references the now-deleted
  `RenderTFVarsWithClusterOutputs`. This is the known cross-role ordering
  dependency: the README (decision/scope) and the staff dispatch both
  state that `secondphase_handoff_test.go`'s **deletion is the validator's
  to land**, and staff must not edit/delete `*_test.go`. Once the
  validator removes that file, `go vet ./...` is fully green.
  `go vet $(go list ./... | grep -v '/internal/tf$')` → exit 0 confirms
  the production code in every package (including `internal/tf` itself,
  which `go build ./internal/tf` compiles cleanly) is vet-clean. The
  pre-existing `internal/tf/vars_test.go` only exercises the empty-prefix
  legacy path, so its assertions remain valid against the unchanged sparse
  render.

### Implementation notes / deviations

- **No deviation from the variable set or the suffix scheme.** Names,
  toggles, and the each-variable-once guarantee match the issue and the
  architect's Deliverable-1 table.
- **Constraint values** are the dispatch starting values (cluster 35 / IS
  63 / COS 180) with the reconcile marker in the table; the architect's
  closure was not yet present on disk at implementation time, so any
  verified delta is a one-line change in the single commented table.
- **`MaxPrefixLen` is computed from the table** (min over all resources of
  `maxLen - len(suffix) - len(zoneExtra)`), currently 35 — the cluster
  limit binds, exactly the design intent (cluster name == prefix).
- **Cluster-jumphost zone budget**: validation includes the longest
  module-appended zone suffix `-us-south-1` so a prefix that would overflow
  `<prefix>-jh-<zone>` is rejected up front.
- **Nil-Resources defense** in `renderFullBody`/`printNamePlan`: a
  prefix-set config with no `resources:` block renders as all-create
  rather than panicking, matching the `--var-file` default.
- **Verification**: a throwaway in-repo driver (since I may not add
  `_test.go`) confirmed Sanitize/Validate behavior, idempotency,
  each-variable-once in the full render, and byte-stable sparse render;
  the driver was removed afterward (no artifact left in the tree).
- Did NOT run a live `init → plan` (no cloud creds / per the
  hermetic-only constraint); the `live-verify-high-issues` gate is the
  integrator's to run with the validator's gated-live driver.
