You are the **staff** agent for Sprint 26 of the roksbnkctl
project. Repo root: `/mnt/d/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in this order)

1. `prompts/sprint26/README.md` — integrator decisions.
2. `issues/issue_sprint26_staff.md` — your full issue + scope guards
   + acceptance criteria.
3. `issues/issue_sprint26_architect.md` — the **constraint table +
   suffix scheme you code against** (architect pins the exact IBM
   Cloud limits + citations; do NOT invent limits). If the
   architect's closure is present, use its verified values.
4. `internal/tf/vars.go` — `RenderTFVars` (sparse render you extend),
   and the **vestigial** `RenderTFVarsWithClusterOutputs` you delete.
5. `internal/tf/terraform.go` — `WriteTFVars`, `TFVarsPath`,
   `varFiles()`, and the vestigial `WriteTFVarsWithClusterOutputs`.
6. `internal/orchestration/second_phase_reuse.go` — `writeBnkPhaseOverride`
   (the LIVE second-phase handoff = a separate `bnk-phase-override.tfvars`
   layered last; do NOT touch it).
7. `internal/config/workspace.go` — the `Workspace` / `ClusterCfg`
   structs you extend (additively, `omitempty`).
8. `internal/cli/init.go` + `internal/cli/prompt.go` +
   `internal/cli/init_var_file.go` — the interview you rewrite + the
   `--var-file` seeding/copy you preserve.

## Tasks

1. **New package `internal/naming/naming.go`**: the constraint table
   (architect's verified values), `Plan` struct, `Derive(prefix)`,
   `ValidatePrefix(prefix)` (label rule + every derived name fits;
   overflow error names the resource + max prefix length; account for
   the appended `-<zone>` on the cluster-jumphost prefix), and
   `SanitizeToPrefix(workspaceName)` (lowercase, `_`/`.`→`-`, strip
   leading non-letter, trim trailing `-`, cap length, idempotent).
2. **`config.Workspace`**: add `Prefix string` + a `ResourcesCfg`
   (`transit_gateway`, `registry_cos`, `cert_manager`, `bnk`,
   `tgw_jumphost`, `cluster_jumphosts`, `client_vpc`) of
   `ResourceToggle{Create bool, Existing string}`. All `omitempty`.
   Reuse `ClusterCfg.Create`/`.Name` for the cluster.
3. **Full render in `RenderTFVars`** when `ws.Prefix != ""`: emit
   region, RG, `create_roks_cluster` + (`openshift_cluster_name` |
   `roks_cluster_id_or_name`), `roks_cluster_vpc_name`,
   `create_roks_registry_cos_instance` + `roks_cos_instance_name`
   (or `.Existing`), `create_roks_transit_gateway` +
   `roks_transit_gateway_name` (or `.Existing`), `install_cert_manager`,
   `deploy_bnk`, `testing_create_tgw_jumphost` +
   `testing_tgw_jumphost_name`, `testing_create_client_vpc` +
   `testing_client_vpc_name` (or `.Existing`),
   `testing_create_cluster_jumphosts` +
   `testing_cluster_jumphost_name_prefix`, plus existing
   `kubeconfig_dir` / `scratch_dir` / BNK fields. **Each variable
   exactly once.** When `ws.Prefix == ""`, keep the current sparse
   render byte-identical. Delete the vestigial
   `RenderTFVarsWithClusterOutputs` + `WriteTFVarsWithClusterOutputs`
   (no live callers).
4. **Interview rewrite in `init.go`** (no-`--var-file` flow only;
   leave credential verify / TF-source prompt / API-key persistence
   untouched): prefix loop (default `ws.Prefix` else
   `SanitizeToPrefix(workspaceName)`; validate + re-prompt; non-TTY
   invalid default → hard error); per-resource create toggles; on a
   declined toggle with a live dependent, prompt for the existing
   resource's name/ID (cluster → `Cluster.Name`; TGW →
   `TransitGateway.Existing` when the jumphost is on; registry COS →
   `RegistryCOS.Existing` when the cluster is created; client VPC →
   `ClientVPC.Existing` when the TGW jumphost is on and the client
   VPC is declined); print the resolved `naming.Plan`; `SaveWorkspace`.
5. **`--var-file` path**: keep Sprint 19 seeding + verbatim copy to
   `terraform.tfvars.user`; additionally set `Prefix =
   SanitizeToPrefix(workspaceName)` (or seed from the file's
   `openshift_cluster_name`) non-interactively + default `Resources`
   to all-create.

## Critical constraints

- Acceptance criteria + files-affected are in your issue. Each
  variable emitted at most once per file. Don't touch the
  phase-handoff override files.
- No namespace prefixing; no auto-truncation; backward-compatible
  empty-prefix render.
- New tests are validator's surface — do NOT write `_test.go`. (The
  intended `vars_test.go` shape change + the
  `secondphase_handoff_test.go` deletion are validator's to land.)
- `go build ./...` + `go vet ./...` clean before you close.
- Do not commit; do not tag. Append a **Closure — staff, <date>**
  section to `issues/issue_sprint26_staff.md` (what shipped, files
  touched, test/vet/build results, implementation notes).
