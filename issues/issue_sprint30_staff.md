# Sprint 30 — staff issues (config seeding/templating + registry targets)

> Make `init` / `ws delete` CI-driveable and `registry replicate` target ICR +
> generic OCI. Specs: [PRD 13](../docs/prd/13-WORKSPACE-CONFIG-SEEDING.md),
> [PRD 14](../docs/prd/14-REGISTRY-TARGETS.md). Design decisions (BLOCKING):
> `issue_sprint30_architect.md`.

`Status`: resolved — implemented + committed; release deferred to the combined Sprint 30+31 cut

### Locked decisions (integrator; confirm before dispatch)
- `init` flags extend the existing Sprint 19 `--var-file` machinery
  (`flagInitVarFile` / `absVarFilePath` / `varFileSeeds`, init.go:127) rather
  than a parallel path.
- A URL is resolved to bytes *before* the existing file-seed logic — one
  `resolveSeedInput(pathOrURL) ([]byte, error)` shared by `--var-file` +
  `--config-file`.
- Registry targets sit behind the existing `mirror.Target` interface; only
  `buildTarget` (registry.go:297) + new impls under `internal/registry/` change.
- Env override + the copied-key record are plain `config.Workspace` fields — no
  new on-disk format.

---

## Issue 2 — `--config-file <path|url>` [Stage A]

- **internal/cli/init.go**: add `flagInitConfigFile`; when set, resolve to bytes,
  parse into `config.Workspace`, and write `~/.roksbnkctl/<ws>/config.yaml`
  (via `config.WorkspacePath`/save). Non-interactive when the parsed config has
  all required fields (per architect Issue 2); otherwise interview only the gaps.
- Validation: strict YAML parse (error on malformed; do not silently drop).
- Tests: complete config → no-TTY write; partial config → gaps prompted; bad
  YAML → clear error.

## Issue 3 — URL inputs for `--var-file` / `--config-file` [Stage B]

- New `internal/cli/seedinput.go` (or in init.go): `resolveSeedInput(s string)
  ([]byte, error)` — if `s` matches `^https?://`, HTTP GET (30 s timeout, 10 MB
  cap, scheme allow-list); else `os.ReadFile`. Reuse in both `absVarFilePath`'s
  caller and the new `--config-file` path.
- Tests: `httptest` server for the URL branch (200, 404, oversize, timeout);
  local-path branch unchanged.

## Issue 4 — `--override-from-env` [Stage C]

- **internal/config** (new `envoverride.go`): `OverrideFromEnv(ws *Workspace,
  target string) []string` — apply the fixed env→field map (architect Issue 4),
  return a list of applied overrides for logging (redacting values). Encodes
  `IBMCLOUD_API_KEY` → base64 → `targets[t].APIKeyB64`; passes through
  `ROKSBNKCTL_*`.
- **internal/cli/init.go**: `flagInitOverrideFromEnv`; call after the config is
  assembled (post `--config-file`/interview, pre-save). Log
  `applied N overrides from environment` (no secret values).
- Tests: each mapped var sets its field; unset vars are no-ops; raw-key encoding
  round-trips; precedence (env beats seeded file).

## Issue 5 — `ws delete` removes copied `~/.ssh` keys [Stage D]

- **internal/config/workspace.go**: add `ResourcesCfg.CopiedSSHKeyName string`
  (`copied_ssh_key_name,omitempty`).
- **internal/cli/init.go** `copyKeyToUserSSH` caller (~line 623): on a successful
  copy, set `res.CopiedSSHKeyName = keyName` and persist.
- **internal/cli/workspaces.go** `runWSDelete` (line 160): before removing the
  workspace dir, if `CopiedSSHKeyName != ""`, delete `~/.ssh/<name>` +
  `~/.ssh/<name>.pub` (ignore not-exist); mention it in the confirmation output.
- Tests: delete with a recorded key removes the `~/.ssh` files (temp HOME);
  delete with no record touches nothing in `~/.ssh`.

## Issue 1 — Registry targets: ICR + generic OCI [Stage E, larger]

- **internal/registry/icr/** (new): an `icr.Target` impl of `mirror.Target` —
  host `<region>.icr.io`, namespace `registry.icr_namespace` (default from
  prefix), `iamapikey` auth from the workspace API key. Push==pull==chart on the
  one host. `Prepare` ensures the ICR namespace exists (IAM/ICR API).
- **internal/registry/generic/** (new): a `generic.Target` — `generic_host` +
  `generic_repo_prefix` + static basic/token auth; `Prepare` is a no-op /
  connectivity check.
- **internal/config/workspace.go** `RegistryCfg`: add `ICRNamespace`,
  `GenericHost`, `GenericRepoPrefix`, and a generic-auth field (composes with
  Issue 4 env override).
- **internal/cli/registry.go** `buildTarget` (297): default `kind` →
  `"icr"`; dispatch `openshift`/`icr`/`generic` to the right impl; keep the
  `--target` + `registry.target` overrides. Return type must widen from
  `*openshift.Target` to the `mirror.Target` interface.
- **redirect**: `registry-mirror.json` rendering already reads the target's pull
  refs — confirm ICR/generic refs flow into `far_image_repo_url` /
  `far_chart_repo_url` unchanged.
- Tests: each target's ref methods; `buildTarget` dispatch + default; redirect
  render for icr/generic.

## Verification gates (every stage)
`gofmt -l`, `go vet ./...`, `go test ./...`, `go build -o ~/.local/bin/roksbnkctl
./cmd/roksbnkctl` (binary-path memory). Live ICR/Artifactory replicate is the
validator's gated step.
