You are the **validator** agent for Sprint 26 of the roksbnkctl
project. Repo root: `/mnt/d/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in this order)

1. `prompts/sprint26/README.md` — integrator decisions.
2. `issues/issue_sprint26_validator.md` — your full issue
   (Issue 1 hermetic + Issue 2 gated-live) + the sub-case map.
3. `issues/issue_sprint26_staff.md` — the surface under test
   (`internal/naming`, the full render, the interview).
4. `internal/tf/vars_test.go` — the existing render tests you
   UPDATE (intended shape change to the full prefix render +
   a preserved legacy sparse case).
5. `internal/tf/secondphase_handoff_test.go` — the test you
   DELETE (it pins the vestigial `RenderTFVarsWithClusterOutputs`
   staff removes; confirm nothing else references it).
6. `internal/cli/init_var_file_test.go` +
   `init_var_file_helpers_test.go` — the in-process cobra harness
   (`runRootCmd`) + the Sprint 19 expectations you minimally update
   for the added `Prefix` field.
7. `scripts/e2e-init-var-file.sh` — the gated-live driver shape
   (gating, `redact()`, `DRY_RUN`) you mirror for the new driver.

## Tasks

### Issue 1 — hermetic

1. **`internal/naming/naming_test.go`** (new): table tests for
   `Derive` (exact suffix scheme), `ValidatePrefix` (accept normal;
   reject uppercase / leading digit-or-hyphen / trailing hyphen /
   illegal chars / overflow — and the overflow error names the
   resource + max prefix length; include the appended `-<zone>`
   case), and `SanitizeToPrefix` (lowercasing, `_`/`.`→`-`,
   leading-non-letter strip, trailing-`-` trim, length cap,
   idempotence).
2. **`internal/tf/vars_test.go`** (update): a full-prefix-render
   case asserting every derived name + `create_*` toggle is present
   **exactly once** (assert no duplicate variable lines) with no
   `tf-*` default leak; existing-resource cases
   (`create_*_= false` + `*_name = "<existing>"`); a preserved
   legacy case (`Prefix == ""`) asserting the old sparse output is
   byte-unchanged; api key never rendered.
3. **Delete `internal/tf/secondphase_handoff_test.go`**.
4. **`internal/cli/init_prefix_test.go`** (new): default-accept run
   persists `prefix` + `resources:` + the expected `ClusterCfg`;
   over-long prefix re-prompts on TTY / hard-errors on non-TTY with
   an invalid default (CI contract); declined toggle with a live
   dependent captures the existing-resource name into the right
   field; `--var-file` path sets a sanitized `Prefix`.
5. **`internal/cli/init_var_file*_test.go`** (minimal update): add
   the `Prefix` expectation (intended, not a regression).

### Issue 2 — gated-live

6. **`scripts/e2e-init-prefix.sh`** (new): `init`→`plan` for two
   distinct prefixes (`e2e-prefix-a`, `e2e-prefix-b`); assert the
   rendered `state*/terraform.tfvars` carries the full generated
   name set (`<prefix>-cluster-vpc`, `<prefix>-tgw`, …) with no
   `tf-*` defaults; prove the two plans reference disjoint names
   (no-collision); prove a `terraform.tfvars.user` override wins.
   Gate on `IBMCLOUD_API_KEY`, honor `DRY_RUN`, redact secrets, exit
   non-zero on any assertion miss.

## Critical constraints

- Additive new test files only, EXCEPT the two intended updates
  (`vars_test.go`, `init_var_file*_test.go`) + the one intended
  deletion (`secondphase_handoff_test.go`). `git diff --stat --
  '**/*_test.go'` should show only those.
- Tests hermetic via `t.Setenv(ROKSBNKCTLHomeEnv, t.TempDir())`;
  do NOT run `roksbnkctl up`/`down` against real cloud (that's the
  gated-live driver's operator-run job).
- `go test ./...` PASS + `go vet ./...` clean before you close.
- Do not commit; do not tag. Append a **Closure — validator, <date>**
  section to `issues/issue_sprint26_validator.md` with the sub-case
  → assertion map + the `go test` output.
