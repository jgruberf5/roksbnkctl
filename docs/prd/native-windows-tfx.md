# Native Windows support via a Go `tfx` helper (removing the shell/python runtime)

**Status:** proposed
**Goal:** `roksbnkctl` runs a full deployment on a stock Windows machine with only
`terraform` and `helm` on `PATH` — **no WSL, no Git-Bash, no curl/grep/python**.

## Problem

The Terraform modules were authored as standalone **IBM Schematics** workspaces
(the modules' original delivery vehicle; each still ships a `schematics_runner.py`
and a "Deploying with Schematics" README). Schematics executes Terraform
server-side in a Linux shell, so the modules lean on `local-exec` blocks that call
`bash`/`curl`/`grep`/`tr`/`base64` and, historically, `python3`. On native Windows
Terraform runs `local-exec` through `cmd.exe`, which cannot execute that toolchain,
so today Windows requires WSL.

Inventory of the host-runtime shell surface (embedded cloud-init / `flp-pod-up.sh`
run **on the VSI**, not the host, and are out of scope):

| Module | `local-exec` | `data.external` | PATCH | DELETE | PUT/POST | GET-poll | helm |
|---|---|---|---|---|---|---|---|
| flo | 43 | 1 | 19 | 17 | 3 | 12 | 4 |
| cne_instance | 6 | 0 | 2 | 4 | 0 | 1 | 0 |
| cert_manager | 4 | 0 | — | — | — | — | (helm status/wait) |
| license | 4 | 0 | 2 | 1 | 0 | 1 | 0 |
| flp | 3 | 1 | 0 | 0 | 0 | 0 | 4 |
| flp_vsi | 3 | 1 | 0 | 0 | 0 | 0 | 0 (COS + helm pull) |
| roks_cluster | 1 | 0 | 0 | 1 | 0 | 0 | 0 |
| gateway / testing | 0 | 0 | — | — | — | — | — |
| **total** | **64** | **3** | **44** | **41** | ~6 | ~27 | ~14 |

`data.http` (8 sites) is already the Terraform-native Go HTTP data source — no shell,
Windows-safe today. `kubectl_manifest` (alekc/kubectl) and `helm_release` are Go
providers — Windows-safe today; only the imperative `local-exec` glue is the problem.

## Why this is tractable

`roksbnkctl` already links `client-go` (`k8s.io/{client-go,api,apimachinery}
v0.36.2`) and `ibm-cos-sdk-go`, and already ships **`internal/k8s`** with a full
kubectl-equivalent library:

- `k8s.NewFromKubeconfigFile/Bytes`, `BuildDynamicClient`, `BuildRESTMapper` — a
  dynamic client + REST mapper, so it can operate on arbitrary CRs (CNEInstance,
  License, F5SPKVlan, VAPBinding) by GVK, not just built-ins.
- Apply / Delete / Describe / Exec option types + implementations.

and **`internal/cos.GetBucket`** (recursive object download, sha-verified). So
~61 of the 64 `local-exec`s reduce to "call an SDK the binary already carries."

The proven precedent is `roksbnkctl flp postrender` — helm already invokes the
roksbnkctl binary as its chart post-renderer (added precisely because `python3` was
absent in the tools-runner). `tfx` generalizes that pattern.

## Design: a `roksbnkctl tfx` subcommand family

A hidden command group invoked from Terraform as `${roksbnkctl_binary} tfx <verb>`
with **no `interpreter`** set. On Windows Terraform runs it via `cmd.exe /C`, which
just execs `roksbnkctl.exe` (already on `PATH`; absolute path is also passed via
`TF_VAR_roksbnkctl_binary`, wired today in `internal/tf/terraform.go` from
`os.Executable()`).

### Invocation contract (cross-platform)

- Command is always `binary verb --flag value ...` — **no pipes, no shell builtins**.
- The kube API token is passed via env (`kube_token` is already a `TF_VAR`); never
  inline on the command line.
- Any manifest / large or JSON payload is passed on **stdin** or a temp file the Go
  side writes — never interpolated into the command string — so `cmd.exe` vs `sh`
  quoting cannot diverge.
- All verbs speak to the cluster via `internal/k8s` (kube host + bearer token from
  flags/env), so no in-cluster DNS assumptions.
- Exit code is the contract: `0` success, non-zero failure with a message on stderr.

### Verb surface (6 verbs cover all 64 sites)

| Verb | Replaces | Backed by | Sketch |
|---|---|---|---|
| `tfx wait` | GET-poll loops (cnecontroller_ready, license_active, flo readiness waits) | `internal/k8s` dynamic get | `tfx wait --gvr k8s.f5.com/v1/cneinstances --ns f5-bnk --name X --for 'condition=CNEControllerAvailable=True' --timeout 15m` |
| `tfx patch` | 44 PATCH (rollout-restart, labels, annotations, status) | dynamic patch | `tfx patch --gvr apps/v1/deployments --ns f5-utils --name f5-spk-cwc --type strategic --patch-stdin` (rollout restart = annotation patch) |
| `tfx delete` | 41 DELETE | dynamic delete | `tfx delete --gvr admissionregistration.k8s.io/v1/validatingadmissionpolicybindings --name X --ignore-not-found` |
| `tfx apply` | ~6 PUT/POST server-side-apply | `k8s.Apply` (SSA) | `tfx apply --field-manager roksbnkctl --force --filename -` (manifest on stdin) |
| `tfx cos-get` | COS S3 downloads (far_download, jwt) | `cos.GetBucket` / object get | `tfx cos-get --instance <name|crn> --bucket B --key K --out PATH` |
| `tfx helm-value` | helm pull + grep (resolve_flp_version, extract_prod_jwks, flo prereq checks) | `helm` binary exec + Go parse | `tfx helm-value pull-file --repo repo.f5.com --chart f5-license-proxy --version V --path charts/.../prod_jwks.txt --out PATH` |

`tfx wait --for` accepts `condition=<Type>=<Status>` and `jsonpath=<expr>=<value>`
(covers License `status.state=Active` too). Polling, backoff, and the ~15m bound
live in Go — deterministic and observable (structured log lines), replacing the
brittle provider `wait_for` we already removed.

`tfx helm-value` shells to the **helm binary** (which we require anyway) for the
pull, then does version/file extraction in Go — no `grep`. (`helm` is not linked as
an SDK; only `go-containerregistry` is, so pulling via the binary is simplest.)

## The one imperative case: the OpenShift admission-policy delete-loop

`cne_instance` runs a **detached** `nohup` loop that deletes the
`openshift-ingress-operator-gatewayapi-crd-admission` ValidatingAdmissionPolicy +
Binding every 5s for ~5m, because the ingress operator recreates them within ~1m
and the FLO `crd-installer` must see them gone during its window (~1-3m into the
CNEInstance reconcile). `local-exec` blocks until its process exits, so a
fire-and-forget doesn't map cleanly, and a synchronous block would miss the window.

**Resolution: lift it out of Terraform into roksbnkctl's bnk-up orchestration.**
roksbnkctl is already the parent process that shells to `terraform apply` for the
bnk phase (`RunTrialUp` -> `applyWithRetry`). Before the apply it starts a
**goroutine** that deletes the policy/binding **if present** every 5s, and stops it
(context cancel) when the apply returns. Goroutines are identical on Windows/Linux
— no detached process, no `SysProcAttr`, no OS-specific code — and reuse
`internal/k8s` delete.

Trade-off: roksbnkctl can't know the exact instant the reconcile hits the
crd-installer window, so the goroutine runs for the **whole apply** rather than a
precise 5m slice. That is harmless — an idempotent delete-if-present, a few hundred
no-op API calls over the apply — and it removes one `null_resource` + one `bash`
interpreter. This makes the single hardest piece the easiest one.

## Removing Schematics (dead weight)

roksbnkctl is now the sole driver (no Go or `.tf` reference to Schematics). Remove:

- `terraform/modules/*/schematics_runner.py` (5 files, ~5,000 lines).
- The "Deploying with IBM Schematics" sections + `ibmcloud_schematics_*` naming in
  the module READMEs.
- Any book/docs references to the Schematics deployment path (book ch. 09, 25 mention
  it — reword to the roksbnkctl flow).

This also deletes the last `python3` references in the tree.

## Sequencing

Ordered to ship a Windows story early; `flo` (the 43-site long pole) last.

1. **Foundation.** Add the `tfx` command group (hidden) with the 6 verbs over
   `internal/k8s` + `internal/cos`; unit-test each verb against envtest / a fake
   dynamic client. Confirm `TF_VAR_roksbnkctl_binary` is passed for every phase
   (today only the FLP helm postrender sets it — extend to all phases in
   `internal/tf/terraform.go`).
2. **cluster + roks_cluster.** Convert its 1 `local-exec`; validate `cluster up`
   on Windows.
3. **bnk core: cne_instance + license + cert_manager (16 sites).** Convert; move the
   admission-policy delete-loop into `RunTrialUp`. Validate `bnk up` on Windows.
4. **flp + flp_vsi (6 sites + 2 data.external).** Convert COS/helm sites to
   `tfx cos-get` / `tfx helm-value`. Validate `flp up` (helm) and `flp up mode:vsi`.
5. **flo (43 sites + 1 data.external).** The bulk; collapse into the verbs above.
6. **Cleanup.** Remove Schematics; drop the 3 `bash` interpreters and confirm zero
   `local-exec` uses `interpreter`, zero modules reference `curl|grep|python3` on the
   host path (embedded VSI scripts excepted). Grep-gate this in CI.

Each step is independently shippable: a converted phase works on Windows while
unconverted phases still require the Linux toolchain, so we can land + validate
incrementally.

## `data.external` (3 sites)

Convert to a `tfx` subcommand that prints the JSON `data.external` expects on stdout
(same "binary emits JSON" shape). E.g. `flp_vsi`'s
`printf '{"v":"%s"}' "$(cat flp-version.txt)"` becomes
`tfx read-json --file flp-version.txt --key v`.

## Testing (the real risk)

The Go is mechanical; the risk is Windows-only behavior (path separators, exec,
`cmd.exe` quoting) that unit tests can't reach. Native-Windows validation is a
first-class deliverable, run **manually** on a real Windows host (no WSL) via
`tools/windows-e2e/run-windows-test.ps1` (companion to this plan):

- Preflight: assert `terraform`, `helm`, `roksbnkctl.exe` on `PATH`; assert
  `wsl.exe` / `bash.exe` **absent** (proves native); record versions.
- Drive `roksbnkctl` phases (attach to an existing cluster to skip the ~40m create),
  tee all output.
- Extract every `tfx` invocation from the Terraform log and its exit status.
- Emit `results.json` (structured per-check pass/fail) + `transcript.log` +
  `tfx-calls.log` for the maintainer/agent to consume.

CI keeps the Linux gate; Windows is a manual matrix run recorded against each change
touching the modules or `tfx`.

## Risks / open items

- **`cmd.exe` quoting.** Mitigated by flags-only commands + stdin/env for payloads;
  the PowerShell test is what actually proves it.
- **Binary discoverability.** `TF_VAR_roksbnkctl_binary` (absolute path) must be set
  for every phase's apply/destroy, not just FLP; fallback to `roksbnkctl` on `PATH`.
- **helm binary version skew.** `tfx helm-value` parses `helm` output; pin the
  parse to stable fields (chart `version:`), tolerate helm output changes.
- **Destroy path.** `local-exec` on destroy (`when = destroy`) must also be `tfx`;
  audit that destroy-time provisioners are covered, not just create-time.
- **No Windows CI today.** Until a Windows runner exists, the gate is the manual
  PowerShell run — process discipline, not automation.
