You are the **architect** agent for Sprint 26 of the roksbnkctl
project. Repo root: `/mnt/d/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in this order)

1. `prompts/sprint26/README.md` — integrator decisions.
2. `issues/issue_sprint26_architect.md` — your full issue (the
   suffix scheme, the constraint table, the book/config-reference
   authoring) + scope guards.
3. `issues/issue_sprint26_staff.md` — what staff codes against your
   table; keep your deliverables aligned with the names/fields
   staff will implement.
4. `terraform/variables.tf` — the root variables you're naming
   (`openshift_cluster_name`, `roks_cluster_vpc_name`,
   `roks_transit_gateway_name`, `roks_cos_instance_name`,
   `testing_client_vpc_name`, `testing_tgw_jumphost_name`,
   `testing_cluster_jumphost_name_prefix`, the `create_*` toggles).
5. `terraform/terraform.tfvars.example` — the override reference
   whose header you'll update.
6. `book/src/SUMMARY.md` + the existing init chapter + the
   configuration-reference chapter — the prose you'll extend.
   Check SUMMARY for whether the naming concept warrants its own
   entry vs. a subsection (mirror Sprint 24 architect's SUMMARY
   check — it lists only chapter titles, no sub-bullets).

## Tasks

1. **Suffix scheme** — finalize the table in your issue (cluster
   name == prefix; `<prefix>-cluster-vpc`, `-registry-cos`, `-tgw`,
   `-client-vpc`, `-jh-tgw`, `-jh`). Document the rationale: cluster
   name takes no suffix so the prefix-length limit equals the
   tightest resource limit, guaranteeing every other derived name
   fits.
2. **Constraint table (BLOCKING input to staff)** — pin the exact
   length + charset limit for each kind: ROKS/IKS cluster name
   (verify ~35), IS resources (VPC/VSI/TGW/SSH key — 63), COS
   resource instance (verify ~180). **Cite the source** for each
   (the IBM Terraform provider `github.com/IBM-Cloud/terraform-provider-ibm`
   validators — e.g. `validateClusterName` / the IS-name validator —
   and/or the IBM Cloud API docs). If a verified number differs from
   the starting values in your issue, the verified value wins; flag
   the delta. Land the verified table in your closure (this ledger
   or a `resolved_sprint26_architect.md`) so staff can code it.
3. **Book authoring**:
   - Rewrite the init chapter interview walkthrough: the workspace
     **prefix** prompt (validation + re-prompt + max length), the
     per-resource **create toggles**, the **existing-resource
     discovery** prompts (e.g. "Existing Transit Gateway name?"),
     and the printed resolved name plan. Mark any captured terminal
     output **illustrative** (tech-writer re-captures byte-for-byte
     against the binary later).
   - Add a "Resource naming & collision avoidance" concept section:
     prefix→name derivation, the suffix table, the length limits,
     why namespaces are NOT prefixed, and the `--var-file` /
     `terraform.tfvars.user` override path. Cross-link to Sprint 25's
     `doctor --orphan-sweep` (the detection complement) if/when that
     chapter exists.
   - Configuration reference: document `prefix` (string) + the
     `resources:` block (`transit_gateway`, `registry_cos`,
     `cert_manager`, `bnk`, `tgw_jumphost`, `cluster_jumphosts`,
     `client_vpc`, each `{create, existing}`). Note additivity.
   - Update `terraform/terraform.tfvars.example`'s header: names are
     now prefix-generated; this file is the override reference
     layered as `terraform.tfvars.user`. Keep per-variable examples.

## Critical constraints

- **No Go.** `internal/naming`, `internal/config`, `internal/tf`,
  `internal/cli` are staff's. You ship the table + scheme as a spec
  and the prose.
- Namespaces stay fixed in the docs.
- Prefer extending the existing init + configuration-reference
  chapters; only add a SUMMARY entry if you create a new chapter
  file. mdbook HTML + PDF must build clean.
- Do not commit; do not tag; no `gh issue create`. Append a
  **Closure — architect, <date>** section to
  `issues/issue_sprint26_architect.md` listing files touched + the
  verified constraint table.
