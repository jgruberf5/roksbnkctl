# PRD 19 — `roksbnkctl support`: triaged GitHub issues and BNK qkview collection

**Status:** plan. Nothing here is implemented. Tracking issue: see "Issues" at the end.

**Goal.** Give an operator two one-command paths to help when something goes wrong:

1. `roksbnkctl support issue` — file a bug or enhancement request against
   `jgruberf5/roksbnkctl` that is *triageable on arrival*: the workspace's
   redacted config, the last failed phase's terraform diagnostics, the
   environment block, and the phase/presence table are gathered from the
   workspace (the current one, or `-w <name>`) and slotted into the repo's
   issue template. Interactive by default; scriptable with flags.
2. `roksbnkctl support qkview` — collect an F5 BNK qkview from the cluster a
   workspace deployed (`bnk up` done) into a local `.tar.gz` ready for an F5
   support case / iHealth upload.

Both verbs are read-mostly. Neither mutates a workspace; the only cluster
writes are the qkview job itself (created, then deleted) and, opt-in, the CWC
REST API certificate Secret that the qkview API needs (§4.3).

---

## 0. Non-negotiable: Windows-native, no shell-outs

Everything in this PRD runs inside the Go binary on Windows, macOS and Linux with no
helper processes. This is the same reason `tfx` replaced `curl`/`kubectl` local-exec
provisioners (`docs/prd/native-windows-tfx.md`) and why `self update` and the terraform
source fetch carry their own HTTP clients:

- GitHub: `net/http` (the `internal/tf/source.go` client shape). **No `gh`.**
- CWC qkview API: `net/http` + `crypto/tls` over an in-process `client-go` port-forward
  (`k8s.PortForwardOptions`, SPDY/WebSocket through the API server). **No `curl`, no
  NodePort, no security-group change, no `kubectl`.**
- Certificates: `crypto/x509` / `crypto/ecdsa` / `encoding/pem`. **No `openssl`, no `sh
  gen_cert.sh`, no `helm pull` at runtime.**
- Archives: `archive/tar` + `compress/gzip`. **No `tar`.**
- Paths: `filepath` throughout; the workspace lives under `%USERPROFILE%\.roksbnkctl` on
  Windows; Secret material written with `config.SecretFileMode` (ACL-limited on Windows
  via the existing `perms.go` handling).
- The one optional process is `--open`'s browser launch (`rundll32 url.dll,FileProtocolHandler`
  / `open` / `xdg-open`); its failure prints the URL and exits 0.
- CI: the `internal/support` and `internal/config` unit tests run in the `windows-latest`
  matrix leg; the e2e support phase runs once from a Windows host before release.

The spike (F) may use `curl`/`openssl` as *investigation tools* on Linux; nothing it
produces may depend on them.

---

## 1. What exists today (measured, 2026-08-27)

| Need | State of the tree | Consequence |
|---|---|---|
| GitHub client | Two hand-rolled `net/http` clients: `internal/cli/self.go:271-319` (releases; `GITHUB_TOKEN` bearer, `User-Agent: roksbnkctl`) and `internal/tf/source.go:14-66` (`githubAPIBase` overridable for tests, `X-GitHub-Api-Version`, rate-limit-vs-403 distinction). No `gh` shell-out anywhere in Go; `gh` is used only by the Makefile. | Reuse the `tf/source.go` shape. Filing needs `POST /repos/{owner}/{repo}/issues` with a token that has `repo`/`issues:write`; fall back to a prefilled browser URL when no token. |
| Interactive prompts | `internal/cli/prompt.go`: `promptString/Int/Select/YesNo`, all return the default when `!isTTY()`. Prompt text goes to stderr. No stdin-injection test harness exists; `init.go` keeps the non-interactive path as a separately callable function so it is testable. | Same split for `support issue`: a pure `buildIssue(inputs) (title, body)` that both the interview and the flag path feed. |
| "Failed logs from state" | **Nothing is persisted.** `internal/tf/diagnostics.go` tees terraform stderr into a bounded ring buffer (`diagCapture`), `Reset()`s it per attempt, folds it into the returned error, and drops it. No per-phase log files; failure is conveyed only by exit code + stderr. `tfx wait` diagnoses (`tfx_wait.go:439,501`) are stdout-only. | Persist the last failure per phase state dir (§3.1) *before* `support` can read it. This is the one prerequisite that touches the lifecycle path. |
| Secret redaction | Path-driven `isSecretPath` (`internal/cli/config_cmd.go:226-245`: any `*_b64` leaf except `bnkforge.ca_b64`; leaf containing `password|secret|token|api_key`). Value-driven `exec.NewRedactor` (`internal/exec/redact.go:60`: raw + base64 variants, alignment-shifted). Name-driven `redactedVarNames` in `config/applied_tfvars.go:185` (hand-maintained; missed two names for three releases). `roksbnkctl config yaml` prints secrets verbatim. | Promote `isSecretPath` into `internal/config` as the single predicate, add a reflection-driven test that every secret-shaped struct field is covered, and layer `NewRedactor` seeded with the *decoded* secrets over every file that leaves the machine. |
| Archive writer | None outside tests (readers only: `self.go`, `registry/source`, `ibm/cluster_config.go`). | `support bundle` is the first `tar.Writer` in the tree; keep it in `internal/support`. |
| Environment facts | `cli.Version/Commit/BuildDate` (`meta.go`), `doctor.Run` (text-only, `lastWhys` global not concurrency-safe), `config.DetectPresence` (tfstate-derived), `phase_status.go` probes (live). | Add `doctor.RunStructured` returning `[]Check` with `why` attached; render both text and JSON. |
| Kube access per workspace | `workspaceKubeTarget()` (`kubeconfig_scope.go`) resolves kubeconfig+context from tfstate outputs and refuses IBM's anonymous decoy context. Namespace from tfstate `flo_namespace`/`flo_utils_namespace` outputs, else `Workspace.BNKNamespaces()`. `k8s.PortForwardOptions` with `ReadyCh` exists. | `support qkview` uses exactly these; never the ambient kubeconfig. |
| CWC qkview API | Zero references to `qkview` in the repo. Live 2.4.0-EA cluster (`sm-cli`): CWC pod has `f5-csm-qkview` sidecar, PVC `cluster-wide-controller` at `/var/qkview`, `cwc-qkview-cm` ConfigMap (created by FLO), `cwc-auth-token` Secret (`token`, 32 random bytes b64), Service `f5-spk-cwc` port `cwc-rest 38081`. **Port 38081 refuses connections** — the REST server only starts when the `cwc-license-certs` Secret (F5 `f5-cert-gen` output) exists, and this repo's install never creates it. | §4. The install has to grow the "Create CWC REST API Certificates (for Qkview)" step from the 2.4 EA guide, or `support qkview` has to do it on demand. Both. |

The F5-documented flow (2.4 EA install guide, "How to Collect Qkview"; clouddocs
`spk-ihealth.html`; API spec `r_spk_qkview.html`):

```
TOKEN=$(kubectl get secret cwc-auth-token -n f5-utils -o jsonpath='{.data.token}' | base64 -d)
curl -k --cert client_certificate.pem --key client_key.pem --cacert ca_certificate.pem \
     -H "Authorization: Bearer $TOKEN" -X POST   https://<cwc>:30881/v1/qkview            # -> {"id":"<uuid>","filename":"<uuid>"}
curl ... -X GET    https://<cwc>:30881/v1/qkview/<id>/status                              # -> {"status":"Running|Completed|Failed", "nested":{...}}
curl ... -X GET    https://<cwc>:30881/v1/qkview/<id>/download -o qkview.tar.gz
curl ... -X DELETE https://<cwc>:30881/v1/qkview/<id>                                     # 204; 409 while in progress
```

POST body (all optional): `namespace`, `filename` (`[a-zA-Z0-9_-]+`), `pod_patterns[]`,
`log_queries[]{file_pattern,query}`, `core_files{max_files,file_patterns[]}`,
`kube_resources[]`. `QKVIEW_TIMEOUT=1800` on the CWC. The guide exposes the API as a
NodePort (30881) and opens the cluster SG; we port-forward to the pod's 38081 over the
API server instead, so no SG or NodePort change is needed. Certs come from
`helm pull oci://repo.f5.com/utils/f5-cert-gen --version 0.9.3` +
`sh cert-gen/gen_cert.sh -s=api-server -a=f5-spk-cwc.<cwc-ns> -n=1`, which emits
`cwc-license-certs.yaml` (server Secret, applied to the CWC namespace) and
`cwc-license-client-certs.yaml` (client cert/key/CA for the caller). "If BNK is already
installed, delete the cwc pod for the certs to take effect." iHealth upload limit: 8 GB.

---

## 2. Command surface

```
roksbnkctl support issue   [-w <ws>] [--kind bug|feature] [--title <t>] [--body-file <f>]
                           [--no-config] [--no-diagnostics] [--with-cluster]
                           [--dry-run] [--out <file.md>] [--open] [--yes]
roksbnkctl support bundle  [-w <ws>] [--out <file.tar.gz>] [--with-cluster] [--since <dur>]
roksbnkctl support qkview  [-w <ws>] [--out <file.tar.gz>] [--namespace <ns>]... [--all-namespaces]
                           [--timeout <dur>] [--include-cores] [--keep-job] [--enable-api] [--yes]
roksbnkctl support redact  [-w <ws>] [--file <config.yaml>]        # print the redacted config; what `issue` embeds
```

Global flags apply: `-w/--workspace` is the "CLI override" for which workspace is
targeted; `-o json` on `bundle`/`qkview` prints the manifest/result as JSON;
`--on` is rejected (`rejectOnFlag("support")`) — the workspace tree is always local
(PRD 15: the runner image holds no state). `-o` is taken, so per-verb output paths are
`--out`.

### 2.1 `support issue`

Interview (TTY) or flags (non-TTY / `--yes`). Both produce the same body via one pure
function, rendered against the repo's issue template sections so a human-filed and a
CLI-filed issue look identical.

1. Kind: `bug` | `feature` (`promptSelect`).
2. Title (`promptString`; bug default `bug: <last failed phase>: <first diagnostic line>` when a persisted failure exists).
3. Bug: symptom, reproduction (default: the last recorded verb + argv), expected, actual.
   Feature: motivation, proposed surface, behavior.
4. Auto-collected, shown for review before posting:
   - Environment: `roksbnkctl version` line, OS/arch, `bnk.manifest_version` + line, region,
     backend, terraform/helm versions from `doctor`.
   - Phase table: `DetectPresence` + tfstate mtime per phase (`inspect` shape).
   - Last failure: `<stateDir>/last-error.txt` + `last-run.json` for the most recent failed phase (§3.1), in a `<details>` block.
   - Redacted `config.yaml` in a `<details>` block (§3.2). `--no-config` omits it.
5. Preview the full markdown; `promptYesNo("Post this issue?")`. `--dry-run`/`--out`
   write the body and stop; `--open` prints (and on a desktop opens) the
   `issues/new?template=…&title=…&body=…` URL for the no-token case — the body is
   truncated to the URL limit with a note to paste the `--out` file.
6. Post via `POST /repos/jgruberf5/roksbnkctl/issues` with `GITHUB_TOKEN` (`.env` is
   already loaded by `Execute()`), labels `bug`/`enhancement`. Print the issue URL.
   Files cannot be attached through the API: if a bundle/qkview exists, the closing
   line tells the user to drag it onto the issue.

Exit codes: `Usage` for bad flag combos, `AuthFailed` (126) on 401/403 from GitHub,
`ConnectFailed` (127) on network failure, `Failure` otherwise.

### 2.2 `support bundle`

Local-only tarball, `roksbnkctl-support-<ws>-<UTC ts>.tar.gz`, with a printed manifest:

```
config.yaml.redacted            §3.2
workspace.json                  presence, phase mtimes, outputs with Sensitive=true replaced by <sensitive>
cluster-outputs.json            as-is (ids/CRNs only) — identifiers, not secrets; --redact-identifiers hashes them
state-*/last-error.txt          §3.1
state-*/last-run.json
state-*/terraform.applied.tfvars   already redacted at write time
doctor.txt / doctor.json
version.txt
cluster/ (only with --with-cluster)  pods -o wide, describe --show-events, logs --tail 500 --previous,
                                     CNEInstance/Infra/GatewaySettings/License YAML, events — for the flo + utils namespaces
```

Never included: `ssh/`, any `kubeconfig/`, `terraform.tfstate*`, `tf-source/`,
`scratch/`, `.terraform/`, `journal/`, `registry-mirror.json`'s CA, keychain contents.
Every text member passes through `exec.NewRedactor` seeded with the decoded secrets.

### 2.3 `support qkview`

1. Gate: `config.DetectPresence(ws).BNK` else "BNK is not installed in workspace X — run `roksbnkctl up`".
   `workspaceKubeTarget()` for kubeconfig; utils namespace from tfstate outputs.
2. Preflight: CWC Deployment exists and has a Ready pod; `cwc-auth-token` Secret present
   (2.4+; optional on 2.3); `cwc-license-certs` Secret present. If absent →
   without `--enable-api`: exit `Failure` with the exact remediation (`--enable-api`, or the
   F5 cert-gen steps); with `--enable-api` (+ `--yes` off-TTY): §4.3 creates the Secret,
   rolls the CWC (the `license` module's annotation-patch pattern), waits Ready.
3. Client certs: read from the workspace-held client Secret written by §4.3
   (`<ws>/cwc-api/{ca,client}.pem`, 0600) or from `<cwc-ns>/cwc-license-client-certs`.
4. Port-forward `pod:38081` (`k8s.PortForwardOptions`, `ReadyCh`), TLS `ServerName:
   f5-spk-cwc.<ns>`, root CA = generated CA, `Authorization: Bearer <token>` when present.
5. `POST /v1/qkview` with `namespace` per `--namespace` (default: both BNK namespaces;
   `--all-namespaces` sends none) and `filename: roksbnkctl-<ws>-<ts>`; poll
   `/status` every 5 s with a spinner line on stderr until `Completed`/`Failed` or
   `--timeout` (default 30 m = CWC's `QKVIEW_TIMEOUT`); stream `/download` to `--out`
   (default `qkview-<ws>-<ts>.tar.gz`) with size + sha256; `DELETE` the job unless
   `--keep-job`.
6. Print: path, size, sha256, and the iHealth upload steps (8 GB limit, case number field).

`-o json` → `{"path","bytes","sha256","job_id","namespaces","duration_s"}`.

---

## 3. Cross-cutting pieces

### 3.1 Persist the last failure per phase (prerequisite)

At the `internal/tf/diagnostics.go` seam, on a failed apply/destroy write, into the
phase's state dir (0600):

- `last-error.txt` — the deduplicated diagnostics text plus the raw tail (bounded, e.g. 256 KiB).
- `last-run.json` — `{verb, argv (redacted), phase, started, finished, exit_code, roksbnkctl_version, terraform_version, backend}`.

Also written on success (`exit_code: 0`, no `last-error.txt`; stale `last-error.txt`
removed) so `support` can tell "failed then fixed" from "failed". `tfx wait`'s
terminal diagnoses append to the same file. Retention: one previous copy
(`last-error.1.txt`). The bundle/issue read these; `inspect` gains a
`last run: failed 2h ago (exit 1) — see support issue` line.

### 3.2 One redaction predicate, tested by reflection

Move `isSecretPath` to `config.IsSecretPath(path)`; `config_cmd.go` calls it. Add
`config.RedactWorkspace(ws) map[string]any` (the `marshalTree` walk, secrets replaced by
`<redacted:sha256-8>` so two issues from the same workspace can be correlated without
revealing the value) and `config.SecretValues(ws) []string` (decoded `*_b64` values +
resolved API key) to seed `exec.NewRedactor`. Test: walk `config.Workspace` with
`reflect`; every leaf whose yaml name matches `_b64$|password|secret|token|api_key|key_path`
must be redacted **and** a fixture with a known value must not appear in the redacted
YAML, the bundle, or the issue body (mutation: drop one name from the predicate → test fails).

### 3.3 Issue templates → issue forms

Convert `.github/ISSUE_TEMPLATE/{bug_report,feature_request}.md` to YAML issue forms
with required fields (symptom, reproduction, expected, environment: version / OS /
region / backend / BNK line) plus a collapsed "Support data (generated by `roksbnkctl
support issue`)" textarea. `blank_issues_enabled: false` stays. The CLI renders the
same section headings so forms and CLI filings triage the same way. Add a
`support-data` hint in the form for humans: "run `roksbnkctl support issue --dry-run`
and paste".

---

## 4. qkview enablement in the install

### 4.1 What the CWC needs

`cwc-license-certs` Secret in the CWC namespace (utils, or the single BNK namespace
under #66's one-namespace mode) carrying the `f5-cert-gen` "api-server" output (server
cert/key + CA). With it present at pod start the `f5-spk-cwc` container serves
`/v1/qkview` on 38081 with mTLS against that CA, and (2.4) additionally checks the
`cwc-auth-token` bearer.

### 4.2 Spike (must land first)

Pull `f5-cert-gen` 0.9.3 from FAR with the workspace's FAR service account (the mirror
tooling already authenticates), run `gen_cert.sh`, and record: the Secret key names
and types, subject/SAN requirements (`f5-spk-cwc.<ns>`), key sizes, validity, and
whether a Go-generated equivalent (`crypto/x509`, self-signed CA + server + client)
is accepted by the CWC. Verify on the live cluster that after applying the Secret and
bouncing the CWC, 38081 answers, and capture the exact `/status` JSON while Running /
Completed / Failed, and a 409 on early DELETE. Output: a short `docs/prd/19-*` §4
addendum + golden JSON fixtures for the client tests.

### 4.3 Generate in Go, apply in the BNK phase

Config: `bnk.cwc_api.enabled` (default `true`; env `ROKSBNKCTL_CWC_API_ENABLED`) and
`bnk.cwc_api.validity_days` (default 825). The BNK phase generates the CA/server/client
material with `crypto/x509` (no FAR pull, no helm), applies `cwc-license-certs`
**before** the CNEInstance (so the CWC starts with the API up — the guide's ordering),
and keeps the client cert/key/CA in the workspace (`<ws>/cwc-api/`, 0600, not in any
bundle). Terraform side: a `kubectl_manifest` in `modules/cne_instance` fed by a tfvar
`cwc_api_server_secret_b64` rendered from the workspace (same shape as the FLP root CA
plumbing in `modules/license`). `roksbnkctl down` leaves nothing behind: the Secret is
in the module's state.

For existing installs `support qkview --enable-api` does the same via the k8s client
and rolls the CWC, and records the material in the workspace so a later `bnk up` does
not regenerate it (idempotent on hash, like `cwc_flp_rollout`).

Line gating: none — the certificate step exists in both the 2.3 and 2.4 guides; only
the bearer token is 2.4+.

---

## 5. Sequencing

```
A  issue forms (3.3)             ─┐
B  last-failure persistence (3.1) ├─► D  support bundle ─► E  support issue
C  redaction predicate (3.2)     ─┘
F  qkview spike (4.2) ─► G  install-time CWC API certs (4.3) ─► H  support qkview
I  docs + changelog + e2e (after E and H)
```

A/B/C are independent and small (≤1 day each). D and E ≈ 2–3 days together. F ≈ 1 day
on a live cluster; G ≈ 2 days (terraform + config + tests); H ≈ 2–3 days. I ≈ 1 day.
About three working weeks end to end for one engineer.

Landing checklist per command (from the tree's guards): `init()` registration +
`Args: cobra.NoArgs` row in `argv_strictness_test.go`'s sweep; `exitcode` errors, no
`os.Exit`; `make generate` for `book/src/27-command-reference.md`; CHANGELOG Unreleased
entry naming the issue; `qkview`, `ihealth` added to `cspell.json`.

## 6. Out of scope (deliberately)

- Uploading to iHealth or opening F5 cases automatically — no public API; print the steps.
- Attaching files to GitHub issues — not possible via the REST API; the user drags the file.
- Redacting IP addresses / CIDRs / cluster names by default — they are what makes a
  network bug triageable. `--redact-identifiers` hashes them on request.
- Collecting qkviews from clusters a workspace did not deploy (`--kubeconfig` override).
  Follow-up if asked; the `k` verbs' resolution is the only supported path for now.
- `--backend`/`--on` for `support` — the workspace tree is local by construction.
- Debug-API (`spk-cwc-debug-apis`) access — different endpoint, separate feature.

## Issues

Filed 2026-08-27. Tracking: **#260**.

| | Issue | Depends on |
|---|---|---|
| A | #251 — issue forms with the fields `support issue` fills | — |
| B | #252 — persist the last terraform failure per phase (`last-run.json` / `last-error.txt`) | — |
| C | #253 — one secret-redaction predicate in `internal/config`, reflection-verified | — |
| D | #254 — `support bundle` | B, C |
| E | #255 — `support issue` | A, C, D |
| F | #256 — spike: CWC REST API certificate schema + live behaviour on 2.3 / 2.4 | — |
| G | #257 — CWC REST API certificate Secret created by `bnk up` (`bnk.cwc_api.enabled`) | F |
| H | #258 — `support qkview` | F, G |
| I | #259 — Getting-support chapter, reference regen, e2e | E, H |
