# Sprint 26

**Theme:** Prefix-driven, fully-generated, length-validated `terraform.tfvars` + an `init` interview rewrite. A single **workspace prefix** becomes the base for every account-scoped IBM Cloud resource name; `roksbnkctl` derives all names from it, validates each against its resource type's length/charset constraints, and renders a complete `terraform.tfvars` that drives a full `up`. A no-arg `init` runs a short interview (prefix → which resources to create → existing-resource names for declined dependencies). A supplied `--var-file` still overrides everything via the Sprint 19 `terraform.tfvars.user` layering.

_Filed 2026-06-04 as an integrator feature request. Today every workspace inherits the same upstream module default names (`tf-cluster-vpc`, `tf-openshift-cluster`, `tf-tgw`, …) because `internal/tf/vars.go:RenderTFVars` renders a sparse tfvars with no name variables, so two workspaces that both create infra collide at the IBM Cloud account level — the same collision class that stranded `canada-roks-*` in the 2026-05-28 incident behind `issues/issue_sprint25_staff.md`. Sprint 25 detects orphans after the fact; this sprint prevents the collisions. There is also no name length validation — over-long names are only rejected by IBM Cloud mid-`apply`._

## Integrator decisions baked in (do not relitigate)

1. **`config.yaml` is authoritative.** Store `prefix` + per-resource toggles + existing-resource refs in `config.yaml`; derive/validate/render the full name set into `terraform.tfvars` at every `up`/`plan`/`apply`. Names are deterministic from the prefix, so regeneration is safe. Do NOT write a hand-editable `terraform.tfvars` artifact at `init` time.
2. **Namespaces stay fixed.** `cert_manager_namespace`, `flo_namespace`, `flo_utils_namespace` keep their conventional values. Only account-scoped IBM Cloud infra names (cluster, VPCs, TGW, COS, jumphosts) are prefixed.
3. **Reject + re-prompt on length overflow.** No silent truncation or hashing. Non-TTY with an invalid default → hard error.
4. **Backward compatible.** Empty-`prefix` legacy `config.yaml` keeps the old sparse render. Old configs load without migration (additive `omitempty` fields).
5. **Override path preserved.** A supplied `--var-file` still seeds + copies `terraform.tfvars.user`, which the lifecycle layers last as its own `-var-file` and overrides any generated name. Do not touch that Sprint 19 machinery beyond also setting a sanitized `prefix`.
6. **Constraint table is architect's, codified by staff.** The exact IBM Cloud length/charset limits are a blocking design input owned by the architect (cited from the IBM Terraform provider's validators). Staff codes the architect's verified values — do NOT invent limits.
7. **`live-verify-high-issues` applies.** The rendered-`terraform.tfvars` change is `up`-affecting. The integrator runs a live `init → plan` (and optionally a dual-workspace no-collision check) before flipping staff Issue 1 to `resolved`.

## Per-role scope

See `docs/PLAN.md` Sprint 26 block for the role table and `issues/issue_sprint26_<role>.md` for each role's full issue. Summary:

| Role | Scope |
|---|---|
| **Architect** Issue 1 | Suffix scheme + the cited length/charset constraint table (blocking input to staff); book authoring (init chapter rewrite, "resource naming & collision avoidance" concept section, configuration-reference `prefix` + `resources:` entries, `terraform.tfvars.example` header). No Go. |
| **Staff** Issue 1 | New `internal/naming` package; additive `config.Workspace` fields; full prefix render in `internal/tf/vars.go` (legacy sparse preserved; delete vestigial `RenderTFVarsWithClusterOutputs`); `init.go` interview rewrite (prefix loop + create toggles + existing-resource discovery + printed name plan); `--var-file` prefix derivation. |
| **Validator** Issues 1 + 2 | Hermetic: `internal/naming/naming_test.go`, full-render + legacy `vars_test.go` updates, delete `secondphase_handoff_test.go`, `init_prefix_test.go`, `Prefix` expectation in `init_var_file*_test.go`. Gated-live: `scripts/e2e-init-prefix.sh` (generated-names + no-collision + override proof). |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep: command reference (`init --help`), configuration reference (`prefix` + `resources:` vs. the YAML a fresh `init` writes), init-chapter transcript re-capture, stale-`tf-*`-default prose sweep, user-facing CHANGELOG bullet. GREEN/RED verdict. |

## Constraints (binding on every role)

- Repo root: `/mnt/d/project/roksbnkctl`.
- **No namespace prefixing**; **no auto-truncation**; **backward-compatible** empty-prefix path.
- Staff does NOT edit the phase-handoff override files (`bnk-phase-override.tfvars`, the cluster-phase override) or the credential / TF-source / API-key code in `init.go`.
- No edits to pre-existing `_test.go` EXCEPT the two intended ones (`internal/tf/vars_test.go` shape change, `internal/cli/init_var_file*_test.go` added `Prefix` expectation) and the one intended deletion (`internal/tf/secondphase_handoff_test.go`). New test files otherwise.
- Do NOT tag a release; the integrator cuts (expected `v1.8.0`, integrator-confirmed). Do not commit; the integrator commits. No `gh issue create`.
- Do NOT run `roksbnkctl up`/`down` against real cloud — hermetic tests use `t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())`; the gated-live driver is operator-run only.
