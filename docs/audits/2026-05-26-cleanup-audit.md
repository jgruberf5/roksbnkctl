# awsbnkctl Cleanup Audit — Terraform / IBM-ROKS / Dead Code / Docs

**Date:** 2026-05-26
**Scope:** Read-only inventory to plan removal of (1) Terraform, (2) IBM/ROKS/`roksbnkctl`/`ibmcloud` legacy, (3) other dead code, (4) README + docs rewrite needs.
**Method:** Reference tracing (grep whole-identifier + Go import graph), code reading. No mutations made.

---

## TL;DR — Recommended PR chunks (full detail in Section 5)

| # | PR (→ staging) | Scope | Risk | Size |
|---|---|---|---|---|
| **1** | **Docs/README/CHANGELOG truth-up** | Rewrite README + MIGRATING + top-level docs to the Go-SDK/`--config` reality. Pure docs, no code. | Low | M |
| **2** | **Delete the dead TF dry-run command surface** | Remove `internal/cli/tfvars.go`, `internal/cli/cluster.go` (up/down cluster), the legacy-TF branches in `lifecycle.go`, `internal/cli/remote.go` tf-output signer. Unwires all 3 `internal/tf` importers. | Med | M |
| **3** | **Delete `internal/tf/` + `terraform/` + `embedded.go` + `install_build_dependencies.sh`** | The TF engine + embed shim + 63 `.tf` files + TF host-install script. Depends on #2 (unwire first). | Med | L |
| **4** | **Scrub TF residue from the config layer** | `internal/config/applied_tfvars.go`, `tfstate.go`, `TFSource*` in `workspace.go`, `inspect.go` shape/`tfstate` status lines, `legacy_helpers.go`. Decide what `status` should show post-TF. Depends on #3. | Med-High | M |
| **5** | **Delete `tools/refgen/tfvars-md/` + IBM-cred residue + secrets.go IBM helper** | TF-doc generator (unused), `secrets.go` `ibmcloud_api_key` migration helper, CI `tools-images.yml` IBM comment. | Low | S |
| **6** | **Retire stale fork scaffolding (owner decision)** | `prompts/sprint*/`, `issues/`, `agents/`, `book/`, `A_Project_Managers_Guide...md`. Pure sprint-era artefacts from the fork. Needs owner sign-off (archival vs delete). | Low | L |

**Blocking owner questions** (also at bottom):
1. **`status`/`inspect` post-TF:** the entire `awsbnkctl status` phase-detection reads `terraform.tfstate` mtimes + `DetectShape`. There is **no** equivalent reader for the new `state.env` IDs cache yet. Removing TF here means `status` needs a rewrite, not just a delete. Keep `status` showing TF state until rewritten, or rewrite in the same PR?
2. **`book/`, `prompts/`, `issues/`, `agents/`:** delete outright, or move to an `archive/` branch/tag first? These are the fork's sprint-era audit trail.
3. **`pkg/bnk/`:** exported (`pkg/`, not `internal/`) → may be an intentional external API. Confirm before touching (it is currently used internally and is NOT dead — see §3).
4. **CI `full-up-dryrun` job:** it exists only to exercise the dead TF dry-run path. Delete with #2/#3 or keep as a smoke test pointed at `up --config --dry-run`?

---

## Section 1 — Terraform removal

### 1.1 Is the TF path still reachable? — YES, but only as a no-op `--dry-run` plan; never applies.

The real production path is the **Go-SDK phased path**, gated on `--config`. The TF path is the fallback when `--config` is empty, and it is hard-gated to `terraform plan` only (live apply/destroy return an error).

`internal/cli/lifecycle.go:274-313` (`runUp`):
```go
func runUp(cmd *cobra.Command, _ []string) error {
    // --- New Go-SDK phased path ---
    if flagConfig != "" {
        return runPhasedUp(cmd.Context(), flagConfig, flagUpDryRun, flagSkipActivationPoll)
    }
    // --- Legacy Terraform path ---
    if !flagUpDryRun {
        return errors.New("awsbnkctl up requires --dry-run in Sprint 3: live apply is gated ...")
    }
    ...
    if err := runFullLifecyclePlan(cmd.Context()); err != nil { ... }
```
`runDown` (`:393-414`) mirrors this. `runApply` (`:389-391`) is a hard error stub. So:
- `up --config <f>` / `down --config <f>` → **phased path** (production; TF-free, confirmed below).
- `up` / `down` with no `--config` → **TF path**, but only `--dry-run` works; bare invocation errors out. **No live TF apply exists in the binary.**
- `up cluster` / `down cluster` (`internal/cli/cluster.go`) → TF-only, also `--dry-run`-gated (`:72-94`).
- `plan` → always TF `runFullLifecyclePlan` (`:379-387`).

**Confirmation the phased path is TF-free:** `grep -rln internal/tf internal/aws/ internal/intent/` → **NONE**. The 26 phase functions (`Phase00…Phase25`) and `internal/intent.Load` never touch `internal/tf`.

### 1.2 Everything TF — inventory

| Item | What it is | Disposition |
|---|---|---|
| `terraform/` (63 `.tf` + modules + `.tfvars.example`) | Old TF root + modules (`eks_cluster`, `cert_manager`, `flo`, `cne_instance`, `license`, `testing`). Several modules still carry IBM `schematics_runner.py` + IBM tfvars. | **Confirmed dead.** Delete (after embed shim removed). |
| `internal/tf/` (`terraform.go`, `fetch.go`, `source.go`, `vars.go`, `doc.go` + 3 `_test.go`) | The terraform-exec driver + embedded-FS extractor + tfvars renderer. | **Confirmed dead** once 3 importers unwired. |
| `embedded.go` (module root) | `//go:embed all:terraform` → `EmbeddedTerraform embed.FS`. Consumed ONLY by `internal/tf/fetch.go:85,103`. | **Confirmed dead.** Must be deleted *with* `terraform/` (a `//go:embed` of a deleted dir is a compile error). |
| `internal/cli/tfvars.go` | `awsbnkctl tfvars` command — emits upstream `terraform.tfvars.example`. Imports `internal/tf`. | **Confirmed dead.** Delete command. |
| `internal/cli/cluster.go` | `up/down cluster` TF dry-run commands. Imports `internal/tf`. | **Confirmed dead.** Delete file. |
| `internal/cli/remote.go` | `loadTFOutputsForTarget` — pulls SSH key from `tf-output:<name>` key source. Imports `internal/tf`. | TF-coupled; see §1.3. |
| `internal/cli/lifecycle.go` legacy branches | `runFullLifecyclePlan` caller branches in `runUp`/`runDown`/`runPlan`, plus `flagTFSource`, `flagUpgradeTF`, `flagVarFiles`, `resolveVarFiles`. | Strip legacy-TF branches; keep `runPhasedUp/Down`. |
| `internal/config/applied_tfvars.go` (+`_test.go`) | Writes `terraform.applied.tfvars`. Writer called only from `internal/tf/terraform.go:265`; `SetAppliedTFVarsVersion` wired in `root.go:153`. | **Dead once `internal/tf` gone.** Delete + remove `root.go:153` line. |
| `internal/config/tfstate.go` | `tfstateHasResources`, `DetectShape`, `trialStateHasClusterModules` — reads `terraform.tfstate`. | Used by `inspect.go` (status) + `workspace.go` delete-guard. See §1.3 — **entangled with `status`**. |
| `internal/config/workspace.go` | `TFSource TFSourceCfg` field (`:38`), `TFSourceCfg` type (`:200-214`), tfstate delete-guard (`:353-366`). | Field + type deletable; delete-guard needs a tags-based replacement or removal. |
| `internal/cli/inspect.go` | `status` command: `tfSourceDescription` (`:266`), phase lines reading `terraform.tfstate` (`:147-216`). | **Entangled** — `status` has no `state.env` reader. See §1.3. |
| `internal/cli/legacy_helpers.go:71` | `"terraform": "local"` entry in a tool-backend default map. | Trivial scrub. |
| `tools/refgen/tfvars-md/` | tfvars→markdown doc generator. Not invoked by CI/Makefile/scripts (grep clean). | **Confirmed dead.** Delete dir. |
| `install_build_dependencies.sh` | Host installer that installs `terraform` (+ `ibmcloud`). Named in D-001 deletion list. | **Confirmed dead** for the TF/IBM parts; whole script likely deletable. |
| `.github/workflows/ci.yml` `full-up-dryrun` job (`:250-420`) | Builds binary, runs `up --dry-run`, asserts plan output mentions 7 TF modules; uses `hashicorp/setup-terraform@v3` (`:295`). | **Dead test** — exercises the TF dry-run path only. Delete or repoint at `--config --dry-run`. |
| `.github/workflows/e2e-full.yml` `full-up-dryrun` (`:11,88`) | Same TF dry-run smoke. | Delete or repoint. |
| `.goreleaser.yml` | No TF/embed-specific config (just `main: ./cmd/awsbnkctl` + ldflags). | No change needed. |
| `Makefile` `build:` | `go build ... ./cmd/awsbnkctl` — compiles `embedded.go` (root pkg) transitively. | No explicit TF target; just ensure build still green after embed removal. |
| `docs/prd/06,07,08`, `docs/PLAN.md`, `docs/E2E_TEST.md`, `docs/SHAKEOUT.md`, `docs/NEXT_STEPS.md`, `docs/PRD.md` | TF-era design specs. | Docs rewrite/archive (Section 4). |
| `book/src/*` (many) | Book chapters describing TF-embedded deploy. | Docs rewrite/archive (Section 4). |

### 1.3 Per-file assessment of the 3 live `internal/tf` importers

Only **3** Go files import `internal/tf` (verified — the brief's list of 5 was over-counted; `internal/config/workspace.go` and `internal/remote/keys.go` only mention `tf` in comments, **no import**):

| File | Symbols used | Entanglement | Removal |
|---|---|---|---|
| `internal/cli/cluster.go` | `tf.Open`, `tf.WriteTFVars` (`:137,236,247`) | None — whole file is the dead `up/down cluster` TF surface. | **Delete the file.** Also drop `upClusterCmd`/`downClusterCmd` from `upCmd`/`downCmd` (registered in the file's own `init()`). |
| `internal/cli/tfvars.go` | `tf.FetchSource` (`:66`) | None — whole file is the dead `tfvars` command. | **Delete the file.** |
| `internal/cli/remote.go` | `tf.Open` + `tfws.Output` (`:29-33`) | **Partial.** This wires the SSH backend's `tf-output:<name>` key source — pull a node SSH key out of TF outputs. The non-TF key sources (`agent`, explicit `--key-path`, file) live in `internal/remote` and do **not** need this. | The phased path provisions no TF outputs, so `tf-output:` key source is **dead in practice**. Remove `loadTFOutputsForTarget`+`needsTFOutputs`; in the `SetSSHTargetResolver` closure pass `nil` outputs to `remote.ResolveSigner`. Verify `targets add --key-source tf-output` is no longer advertised. |

`internal/config/applied_tfvars.go` is reachable only through `internal/tf/terraform.go:265` (the apply path) — so it dies with `internal/tf`. `root.go:153` (`config.SetAppliedTFVarsVersion`) is its only other wiring and must be removed in the same change.

### 1.4 Removal ordering (must unwire before delete)

```
1. (PR2) Delete cli/tfvars.go + cli/cluster.go  ........... removes 2 of 3 tf importers
   Strip legacy-TF branches in cli/lifecycle.go (runUp/runDown/runPlan,
     flagTFSource, flagUpgradeTF, flagVarFiles, resolveVarFiles, runFullLifecyclePlan)
   Rework cli/remote.go to drop tf.Open (3rd importer)
   → now NOTHING imports internal/tf except internal/tf itself
2. (PR3) Delete internal/tf/  +  embedded.go  +  terraform/  (together — embed dir dep)
         Delete install_build_dependencies.sh
   → EmbeddedTerraform consumer is gone, embed dir is gone: build stays green
3. (PR4) Delete config/applied_tfvars.go (+test) + remove root.go:153
         Decide+rework config/tfstate.go, workspace.go TFSource*, inspect.go status
4. (PR5) Delete tools/refgen/tfvars-md/
```
PR2 → PR3 is a hard dependency (can't delete `internal/tf` while `cli` imports it). PR3's three deletions (`internal/tf`, `embedded.go`, `terraform/`) **must ship together** — `embedded.go`'s `//go:embed all:terraform` makes a half-delete a compile error.

---

## Section 2 — IBM / ROKS / roksbnkctl / ibmcloud legacy references

Whole-identifier hits only (`roksbnkctl`, `ibmcloud`, `jgruberf5`, ROKS, IBM Cloud). "rks"-substring noise ignored.

### (a) Whole files/dirs that are purely legacy — deletable

| Path | Why |
|---|---|
| `terraform/modules/*/schematics_runner.py` (license, cert_manager, testing, flo, cne_instance) | IBM Schematics runners — pure IBM Cloud. Die with `terraform/` (PR3). |
| `terraform/modules/*/README.md`, `*.tfvars.example`, IBM-named `variables.tf` | IBM-Cloud module docs/vars. Die with `terraform/`. |
| `tools/refgen/tfvars-md/` | TF-doc gen (also IBM-shaped in `main_test.go`). Delete (PR5). |
| `install_build_dependencies.sh` | Installs `terraform` + `ibmcloud` CLI. Delete (PR3). |

### (b) Files that stay but need the reference scrubbed

| Path | What to scrub |
|---|---|
| `internal/config/secrets.go:11-30` | `ibmcloud_api_key` keyring helper — described as a "one-time IBM-residue migration helper". Now obsolete (no IBM cred flow). Remove the helper + `ibmcloud_api_key` constant. |
| `internal/cli/root.go:59-91` | `warnLegacyState` nudges users with leftover `~/.roksbnkctl/`. **Keep the `.roksbnkctl` legacy-state nudge** (genuinely helps fork-era users migrate) but it can drop the `terraform apply` comment at `:91`. Borderline — owner call. |
| `internal/cli/inspect.go:111,146` | `// Ported from roksbnkctl@…` attribution comments — cosmetic; scrub or keep as provenance. |
| `internal/cli/lifecycle.go:232` | `// Ported from roksbnkctl@28ccc59` — dies with the legacy-TF branch removal (PR2). |
| `.github/workflows/tools-images.yml` | IBM `ibmcloud` comment in the tools-image description. Scrub. |
| `.github/ISSUE_TEMPLATE/bug_report.md` | `ibmcloud` reference. Scrub. |
| `cspell.json` | dictionary entries `roksbnkctl`, `ibmcloud`, `jgruberf5` — harmless; can keep or prune. |

### (c) Attribution / license — keep or update

| Path | Note |
|---|---|
| `LICENSE` | MIT, `Copyright (c) 2026 John Gruber`. Inherited from upstream. **Keep** (MIT attribution requirement) — owner may add their own copyright line. Not a blocker. |
| `README.md` "License" section: "inherited from roksbnkctl" | Factually true; keep the inheritance note but the README itself is being rewritten (Section 4). |

### (d) Docs / comments to rewrite — see Section 4

Heavy ROKS/IBM doc surface: `docs/PRD.md`, `docs/PLAN.md`, `docs/NEXT_STEPS.md`, `docs/SHAKEOUT.md`, `docs/E2E_TEST.md`, all of `docs/prd/00-08`, most of `book/src/`, and the entire `prompts/sprint*` + `issues/` trees (fork-era sprint artefacts). These describe the "fork roksbnkctl, keep TF modules" model that D-001 reversed.

### Module path + go.mod
- `go.mod`: `module github.com/JLCode-tech/awsbnkctl`, `terraform-exec v0.21.0` dependency. **The `hashicorp/terraform-exec` require can be dropped** from `go.mod`/`go.sum` once `internal/tf` is deleted (PR3) — run `go mod tidy`. No IBM SDK deps remain in `go.mod` (already AWS-only).
- No Go import paths reference `roksbnkctl`/`ibmcloud` — the identity rewrite (Sprint 0) already renamed the module. Legacy refs are now confined to comments + docs + the TF tree.

### Load-bearing copied-from-upstream patterns to KEEP
- The cobra scaffolding, `internal/k8s` client-go wrapper, `internal/remote` SSH, `internal/exec` backends, doctor framework, `miekg/dns` test probes — all reusable, not IBM-specific. **Keep.**
- `warnLegacyState` `.roksbnkctl`/`.bnkctl` migration nudge (`root.go:79`) — genuinely useful for fork-era users; keep (scrub only the TF comment).

---

## Section 3 — Other dead / unused code

| Item | Status | Evidence |
|---|---|---|
| `prompts/sprint0…sprint10/` (11 dirs, role briefs) | **Stale scaffolding** — sprint-era task briefs from the fork's four-agent model. Not referenced by code. README points at them as "auditable" but the model is dead. | Owner decision (PR6): archive vs delete. |
| `issues/` (81 `issue_*`/`resolved_*` md files) | **Stale scaffolding** — sprint-era issue tracker. Not code-referenced. | Owner decision (PR6). |
| `agents/` (architect/staff/validator/tech-writer .md + README) | **Stale** — the four-role sprint pattern; superseded by `.agent/` MAF + `.claude/agents/`. | Owner decision (PR6). |
| `book/` (mdBook, ~30 chapters) | **Mostly stale** — documents TF-embedded ROKS-fork deploy. Built/validated by `.github/workflows/book.yml` + Makefile `book*` targets. | Rewrite or archive (Section 4 / PR6). |
| `A_Project_Managers_Guide_to_Agentic_Developed_Products.md` (146 KB, root) | Unrelated book-length essay at repo root. Not referenced. | Likely delete/relocate — owner call. |
| `tools/sprintwatch/`, `tools/ciwatch/` | TUI helpers (own `go.mod`, checked-in binaries `sprintwatch`/`ciwatch`). Not part of main build; sprint-era. Checked-in binaries are bloat. | **Likely dead — verify** owner still uses them. At minimum remove the committed binaries. |
| `tools/refgen/cobra-md/` | Cobra→markdown doc gen. Not in CI/Makefile (grep clean). | **Likely dead — verify**; less urgent than tfvars-md (not TF-coupled). |
| `tools/docker/aws|iperf3|mdbook/` | Tool images; `aws`+`iperf3` built by `tools-images.yml`, `mdbook` by book pipeline. | **Live** — keep (used by `test --backend k8s` + book). |
| `scripts/e2e-test*.sh`, `test-integration-aws.sh`, `pre-commit.sh` | e2e/integration runners. `e2e-test.sh` references roksbnkctl. | **Likely stale — verify** which the current CI actually calls. |
| `awsbnkctl` (170 MB binary at repo root) | Committed build artefact. | **Confirmed bloat** — should be gitignored, not committed (verify it's tracked). |
| `cne_pull_64.json`, `license.jwt` (symlinks to `~/Code/aws-gpu-setup/`) | Local dev symlinks. | Should not be committed/tracked — verify gitignore. |
| `pkg/bnk/resync.go` | **NOT dead** — `pkg/` (exported) HTTPRoute resync helper, used by `internal/cli/bnk.go`. Exported surface → confirm before any change. | **Keep.** |
| `internal/intent`, `internal/aws/phases`, `internal/aws/state`, `internal/scenarios`, `internal/forge`, `internal/k8s` | **Live** — the production phased path. | Keep. |
| `embedded.go` | Dead (see §1.2) — only feeds TF. | Delete with PR3. |

**Conservative note:** `tools/sprintwatch`, `tools/ciwatch`, `tools/refgen/cobra-md`, and the `scripts/` runners are marked "likely dead — verify" rather than confirmed; they have own-module/CI-adjacent footprints that warrant an owner glance before deletion.

---

## Section 4 — README + docs

### 4.1 README.md — what's wrong (every numbered item is stale)

| Line(s) | Problem |
|---|---|
| 5 (Status) | "Sprint 6 complete; v0.9-rc … embedded HCL stands up an EKS cluster … goreleaser produces six binary archives" — describes the TF-embedded world. Reality: Go-SDK phased path driven by `cluster.yaml`, validated live on `aws-syd-test`. |
| 7-15 ("Why fork roksbnkctl") | Entire rationale ("3 of 5 TF modules port unchanged") is **reversed by D-001**. TF is being deleted. |
| 19-39 (Quick start) | `awsbnkctl up` shown bare — but bare `up` errors (needs `--config` or `--dry-run`). Real flow is `up --config examples/<topology>/cluster.yaml`. `init` wizard writes a TF workspace, not the new intent file. |
| 41-54 (Target architecture table) | "Terraform provider / module" column + embedded-HCL data-plane decision — all TF-framed. Reality: Go SDK phases + `internal/k8s/manifests/<pattern>/` variant dirs + host-device/SR-IOV pattern. |
| 56-65 (Prerequisites) | "`terraform` (1.5+) on PATH will be the only required host install" — **false now**; single Go binary, no TF. |
| 67-82 (What's in this repo) | Lists `terraform/` as "embedded into the binary", `internal/tf`, the four-agent `agents/`+`prompts/` model. All being removed. |
| 84-94 (Development model + Relationship to roksbnkctl) | Four-role sprint pattern + `upstream` remote cherry-pick model. The fork remote was already detached (per project memory); the sprint model is dead. |
| 96-114 (What this is not / Pointers / License) | "HCL under ./terraform/ is the source of truth" — false. MIGRATING.md + book pointers point at TF-era docs. |

### 4.2 Rewritten README outline (for current Go-SDK/AWS/BNK reality)

```
# awsbnkctl
  one-liner: single Go binary, AWS SDK only, deploys EKS + F5 BNK from a cluster.yaml intent
## Status            (live-validated on aws-syd-test; Go-SDK phased; TF removed)
## How it works       (intent cluster.yaml → phases 00–25 → AWS tags = truth + state.env cache)
## Quick start        (build → write/copy examples/tracer/cluster.yaml → up --config → scenarios run → down --config --yes)
## cluster.yaml       (apiVersion/kind, metadata.name/region, pattern: host-device, forge block)
## Patterns           (host-device SR-IOV requirements: ≥4 ENIs / m5.xlarge min; prefix-delegation CNI)
## Commands           (up/down --config, status, doctor, scenarios, test traffic, forge, k *, validate, topology)
## Forge integration  (write-only handoff; AWS is truth; :8000 REST / :8081 MCP)
## What's in the repo  (cmd/, internal/{cli,aws/phases,intent,k8s,scenarios,forge,remote,exec}, examples/, docs/)
## Prerequisites       (AWS creds via standard chain; no terraform, no host kubectl/aws/dig)
## What this is not
## License             (MIT)
```

### 4.3 docs/ + book/ pages to rewrite or delete

| Path | Action |
|---|---|
| `docs/POST_TERRAFORM_DIRECTION.md`, `.agent/DECISIONS.md` (D-001) | **Keep** — canonical "why" for the cleanup. |
| `docs/PRD.md`, `docs/PLAN.md`, `docs/NEXT_STEPS.md`, `docs/SHAKEOUT.md`, `docs/E2E_TEST.md` | TF/ROKS/sprint-era. **Archive or delete** (superseded). |
| `docs/prd/00-OVERVIEW … 08-S3-SUPPLY-CHAIN-IRSA` | TF-shaped design specs (06/07/08 are referenced by the still-live dead-code error messages — those messages also go away in PR2). **Archive**; salvage the SR-IOV/IRSA *intent* into the new pattern docs. |
| `docs/prd/09-SCENARIOS-FRAMEWORK.md` | **Keep** — scenarios are live. |
| `docs/FORGE_MCP_INTEGRATION.md` | **Keep** — forge handoff is live. |
| `docs/audits/*` | Keep (audit trail; this file joins them). |
| `book/src/02-why-roks.md`, `08-cluster-phase.md`, `04-installation.md`, `09-registering-existing-cluster.md`, `14-credentials-resolver.md`, `33-data-plane-decision.md`, etc. | TF/ROKS-shaped. **Rewrite or archive** the whole book to the Go-SDK shape, or drop the book until rewritten. |

---

## Section 5 — Recommended cleanup plan (PR chunks for `staging`)

Ordered by dependency. Each is independently mergeable except where noted.

### PR1 — Docs/README truth-up (no code) — Low risk, M
- **Scope:** Rewrite `README.md` to the Section 4.2 outline; update `MIGRATING.md`; mark `docs/PRD.md`/`PLAN.md`/`NEXT_STEPS.md`/`SHAKEOUT.md`/`E2E_TEST.md` + `docs/prd/00-08` as archived (header banner) or move to `docs/archive/`.
- **Files:** `README.md`, `MIGRATING.md`, `CHANGELOG.md` (add cleanup entry), `docs/*`.
- **Risk:** Low — no code. Can ship first to stop misleading new users.

### PR2 — Delete dead TF command surface (unwire) — Med risk, M
- **Scope:** Delete `internal/cli/tfvars.go`, `internal/cli/cluster.go`. Strip legacy-TF branches from `internal/cli/lifecycle.go` (`runFullLifecyclePlan` callers in `runUp`/`runDown`/`runPlan`, `flagTFSource`, `flagUpgradeTF`, `flagVarFiles`/`resolveVarFiles`, the `up cluster`/`down cluster` registration). Rework `internal/cli/remote.go` to drop `tf.Open` (remove `loadTFOutputsForTarget`/`needsTFOutputs`, pass `nil` outputs). Remove/repoint the CI `full-up-dryrun` jobs.
- **Files:** `internal/cli/{tfvars,cluster,lifecycle,remote}.go`, `.github/workflows/{ci,e2e-full}.yml`.
- **Decision:** `plan`/`apply` commands — keep as friendly "use `up --config --dry-run`" stubs, or delete? Recommend keep-as-stub.
- **Risk:** Med — touches the up/down command wiring + `--var-file`/`--tf-source` flags (advertised in help). After this, `grep internal/tf` = 0 importers outside the package.

### PR3 — Delete the TF engine + embed + tree — Med risk, L (depends on PR2)
- **Scope:** Delete `internal/tf/` (all files), `embedded.go`, `terraform/` (63 `.tf` + modules), `install_build_dependencies.sh`. `go mod tidy` to drop `hashicorp/terraform-exec`.
- **Files:** above + `go.mod`/`go.sum`.
- **Risk:** Med — large deletion but mechanically safe once PR2 lands. The three deletions must be one commit (embed-dir dependency). Verify `go build ./...` + `go test ./...` green.

### PR4 — Scrub TF from the config/status layer — Med-High risk, M (depends on PR3)
- **Scope:** Delete `internal/config/applied_tfvars.go` (+test) + remove `root.go:153`. Decide the fate of `internal/config/tfstate.go` + `inspect.go` status phase-detection + `workspace.go` `TFSource*`/delete-guard. **This needs the owner decision (Q1):** `status` currently reports deploy state purely from `terraform.tfstate` — there's no `state.env` reader. Either (a) rewrite `status` to read the new IDs cache / AWS tags in this PR, or (b) split the `status` rewrite into its own follow-up and leave a thin shim.
- **Files:** `internal/config/{applied_tfvars,tfstate,workspace}.go`, `internal/cli/{inspect,root,legacy_helpers}.go`.
- **Risk:** Med-High — `status` behaviour change; not a pure delete.

### PR5 — Delete refgen/tfvars-md + IBM residue — Low risk, S
- **Scope:** Delete `tools/refgen/tfvars-md/`. Remove `internal/config/secrets.go` IBM `ibmcloud_api_key` helper. Scrub IBM comments in `.github/workflows/tools-images.yml` + `.github/ISSUE_TEMPLATE/bug_report.md`. Optionally prune `cspell.json` IBM/roks entries.
- **Risk:** Low — isolated.

### PR6 — Retire fork-era scaffolding (OWNER DECISION) — Low risk, L
- **Scope:** `prompts/sprint*/`, `issues/`, `agents/`, `book/`, `A_Project_Managers_Guide_to_Agentic_Developed_Products.md`, committed `awsbnkctl` binary, `tools/sprintwatch`+`ciwatch` binaries, stale `scripts/e2e-test.sh`. Also gitignore-and-untrack the 170 MB root binary + dev symlinks (`cne_pull_64.json`, `license.jwt`).
- **Risk:** Low (no code impact) but **needs owner sign-off** on archive-vs-delete for `book/`, `prompts/`, `issues/`, `agents/`.

---

## Blocking owner questions (recap)
1. **`status`/`inspect` after TF:** rewrite the phase-detection onto `state.env`/AWS-tags in PR4, or ship a thin shim and rewrite later? (No new-state reader exists today.)
2. **Archive vs delete** for `book/`, `prompts/`, `issues/`, `agents/`, the PM-guide essay.
3. **`tools/sprintwatch` + `tools/ciwatch` + `tools/refgen/cobra-md`:** still used? (Marked likely-dead.)
4. **`plan`/`apply` commands:** keep as friendly redirect stubs or delete entirely?
5. **Committed 170 MB `awsbnkctl` binary + dev symlinks:** confirm they should be untracked + gitignored.

---

## Appendix — verification notes
- `internal/tf` importers (Go): **exactly 3** — `cli/cluster.go`, `cli/tfvars.go`, `cli/remote.go`. (`config/workspace.go` and `remote/keys.go` only *mention* tf in comments — no import; the brief's count of 5 was high.)
- Phased path TF-free: `grep -rln internal/tf internal/aws/ internal/intent/` → none.
- `EmbeddedTerraform` consumers: only `internal/tf/fetch.go:85,103`.
- `WriteAppliedTFVars` caller: only `internal/tf/terraform.go:265` (+ `root.go:153` version setter).
- `refgen`/`tfvars-md` invoked by CI/Makefile/scripts: none.
- D-001 (`.agent/DECISIONS.md:13-26`) already prescribes deleting `terraform/`, `internal/tf/`, `embedded.go`, `install_build_dependencies.sh`, `internal/cli/tfvars.go` — this audit confirms feasibility + adds the config-layer + remote.go + applied_tfvars + cluster.go entanglements D-001 didn't enumerate.
