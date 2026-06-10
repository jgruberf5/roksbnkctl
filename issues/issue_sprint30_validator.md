# Sprint 30 — validator issues (config seeding/templating + registry targets)

> Verify each Sprint 30 change at the unit gate, then the live/gated steps that
> need real IBM Cloud + registries. Specs:
> [PRD 13](../docs/prd/13-WORKSPACE-CONFIG-SEEDING.md),
> [PRD 14](../docs/prd/14-REGISTRY-TARGETS.md).

`Status`: open (draft — not yet dispatched)

---

## Unit gates (every stage, pre-merge)
`gofmt -l`, `go vet ./...`, `go test ./...`, `go build -o ~/.local/bin/roksbnkctl
./cmd/roksbnkctl`.

## Issue 2 — `--config-file`
- Complete config → non-interactive write, no TTY; `config.yaml` byte-matches
  the input (modulo normalization). Partial → only gaps prompted. Malformed YAML
  → non-zero exit + clear message (no half-written workspace).

## Issue 3 — URL inputs
- `--var-file <url>` and `--config-file <url>` against an `httptest` server:
  200 seeds correctly; 404 / oversize / timeout fail loudly. Local-path
  behavior unchanged (regression).

## Issue 4 — `--override-from-env`
- `IBMCLOUD_API_KEY=<raw>` → `config.yaml` `api_key_b64` is the base64 of the raw
  key; `ROKSBNKCTL_API_KEY_B64=<enc>` passes through. Each `ROKSBNKCTL_*` mapped
  var lands in its field; unset vars are inert; env beats the seeded file. **No
  secret value appears in stdout/stderr/logs** (grep the run output).

## Issue 5 — `ws delete` SSH cleanup
- With a recorded `copied_ssh_key_name` (temp `$HOME`): `ws delete` removes
  `~/.ssh/<name>{,.pub}`. With no record: nothing under `~/.ssh` is touched.
  Pre-existing unrecorded file with the same name → not deleted.

## Issue 1 — Registry targets (gated-live)
- Default: `registry replicate` with no `--target` selects **icr** (not
  openshift). `--target`/`registry.target` still override all three.
- **ICR live** (real cluster, e.g. test-005): replicate the BOM into ICR; a BNK
  install pulls images from `<region>.icr.io/<ns>/…` (confirm whether the ROKS
  global `*.icr.io` pull secret suffices — architect Issue 1.3) and charts via
  the helm provider. No external `repo.f5.com` pulls (`grep` pod images, as in
  the PRD 11 air-gap assert).
- **Generic/Artifactory live**: follow the book walkthrough end-to-end against a
  real Artifactory OCI repo; replicate + install + verify pulls resolve to
  Artifactory.
- Redirect: `registry-mirror.json` renders ICR/generic hosts into
  `far_image_repo_url` / `far_chart_repo_url`.
