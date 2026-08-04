# PRD 13 — Workspace config seeding & CI templating

> **Sprint 30.** Today `roksbnkctl init -w <ws>` builds a workspace's
> `config.yaml` from an interactive interview, optionally pre-seeded from a
> Terraform **`--var-file`** (Sprint 19). That's fine for a human at a terminal,
> but unattended/CI callers want to **hand init a finished config**, **fetch it
> from a URL**, and **inject secrets from the environment** so a single committed
> template can be reused across accounts without baking in an API key. This PRD
> covers four related changes to `init` + `ws delete`:
>
> 1. **`--config-file <path|url>`** — seed the workspace `config.yaml` directly
>    (sibling of `--var-file`, which seeds `terraform.tfvars`).
> 2. **URL inputs** — both `--var-file` and `--config-file` accept a local path
>    **or** an `http(s)://` URL that init fetches.
> 3. **`--override-from-env`** — after seeding, overlay specific `config.yaml`
>    fields from environment variables (e.g. `api_key_b64` ← the IBM API key in
>    the env), so a committed template carries placeholders, not secrets.
> 4. **`ws delete` SSH cleanup** — if a workspace's generated SSH key was copied
>    into `~/.ssh/`, `ws delete` removes those copied files too.
>
> Registry-target work (Sprint 30 Issue 1) is a separate feature:
> [PRD 14](14-REGISTRY-TARGETS.md). Implementation stages + design decisions:
> `issues/issue_sprint30_staff.md` / `issues/issue_sprint30_architect.md`.

`Status`: draft (not yet dispatched)

---

## Motivation

The driving use case is a CI pipeline (or a fleet operator) standing up many
workspaces from one source of truth:

```
config.yaml.tmpl  ──init --config-file <url> --override-from-env──▶  ~/.roksbnkctl/<ws>/config.yaml
   (committed, no secrets)                (api_key_b64 ← $IBMCLOUD_API_KEY, region ← $REGION, …)
```

The template lives in git with `api_key_b64: ""` (and any other env-bound
fields blank or placeholder); the pipeline supplies the real key as an
environment variable. No secret ever lands in version control, and the same
template seeds every account.

## Scope

### Issue 2 — `--config-file <path|url>`

- New flag on `init`: `--config-file`. Supplies a complete (or partial)
  workspace `config.yaml` that init writes to `~/.roksbnkctl/<ws>/config.yaml`.
- Parallel to `--var-file` (which seeds `terraform.tfvars`); the two are
  independent and may be combined.
- Precedence + interview behavior (Open Question A): does `--config-file` make
  init fully non-interactive (skip the interview, validate the supplied file),
  or seed-then-interview (interview fills gaps / confirms)? Default assumption:
  **a complete `--config-file` runs non-interactively** (validate + write);
  the interview is skipped when every required field is present, and only
  prompts for what's missing.
- Validation: parse against `config.Workspace`, reject malformed YAML
  with a clear error (don't silently drop fields, unlike Terraform's
  undeclared-variable warnings — see the `cluster_vpc_id` lesson).

### Issue 3 — URL inputs for `--var-file` / `--config-file`

- Both flags accept a value that is either a local filesystem path or an
  `http(s)://` URL.
- URL → fetch (with a timeout), then treat the body identically to a local file.
- Open Question B: auth for private URLs (a bearer header / `.netrc` / none),
  allowed schemes (https-only vs http permitted), max size, and whether to
  verify a checksum. Default assumption: **https + http both allowed, no auth in
  v1** (a presigned/COS URL or a public raw-git URL covers the CI case), 10 MB
  cap, 30 s timeout.

### Issue 4 — `--override-from-env`

- New boolean flag on `init`: `--override-from-env`. After the `config.yaml` is
  assembled (from `--config-file` and/or interview), overlay a fixed set of
  fields from environment variables when those env vars are set + non-empty.
- The canonical case: `api_key_b64`. Open Question C: does the env var hold the
  **raw** IBM API key (init base64-encodes it into `api_key_b64`) or the
  **already-base64** value? Default assumption: **`IBMCLOUD_API_KEY` holds the
  raw key; init base64-encodes it** (matches the existing `IBMCLOUD_API_KEY`
  convention elsewhere in the tool), with `ROKSBNKCTL_API_KEY_B64` as an
  explicit pre-encoded escape hatch.
- The **full override table** (env var → `config.yaml` field) is owned by the
  tech-writer ledger and published in the book. Initial proposed set:

  | env var | config.yaml field | encoding |
  |---|---|---|
  | `IBMCLOUD_API_KEY` | `targets.<t>.api_key_b64` | base64-encode raw key |
  | `ROKSBNKCTL_API_KEY_B64` | `targets.<t>.api_key_b64` | verbatim (pre-encoded) |
  | `ROKSBNKCTL_PREFIX` | `prefix` | verbatim |
  | `ROKSBNKCTL_REGION` | `targets.<t>.region` | verbatim |
  | `ROKSBNKCTL_RESOURCE_GROUP` | `targets.<t>.resource_group` | verbatim |
  | `ROKSBNKCTL_TESTING_SSH_KEY_NAME` | `resources.testing_ssh_key_name` | verbatim |

  Open Question D: which target the `targets.<t>.*` overrides apply to when a
  config has multiple targets (default target only? all? a `--target` arg?).

  **As shipped** the map is considerably larger than this initial set — cluster
  identity, the CIS BIG-IP target, the per-zone TMM networking, the registry
  mirror, the FLP (including the standalone **VSI appliance**: `ROKSBNKCTL_FLP_MODE`
  + `ROKSBNKCTL_FLP_VSI_*`), and the FAR supply chain (`ROKSBNKCTL_COS_*`,
  `ROKSBNKCTL_{FAR_AUTH,SUBSCRIPTION_JWT}_{FILE,LOCAL_FILE}`). The driver for the
  last two groups is the **argv-only runner**: BNK Forge's container engine and CI
  jobs run the tool with an env map and no shell, so anything not reachable from
  env is unreachable from those callers entirely. The canonical table lives in
  [Chapter 7a](../../book/src/07a-unattended-setup.md); the code is
  `internal/config/envoverride.go` + `envoverride_flp.go`.

- Precedence: **env override wins** over the seeded file (it's the explicit
  late-binding step). Document it.

### Issue 5 — `ws delete` removes copied `~/.ssh/` key files

- Today `copyKeyToUserSSH` (init.go:633) copies `<name>{,.pub}` into `~/.ssh/`
  but **nothing records that it did**, so `runWSDelete` (workspaces.go:160)
  can't clean them up.
- Record the copied paths at copy time — e.g. a `resources.copied_ssh_key_paths`
  (or a single `copied_ssh_key_name`) field in `config.yaml`, written only when
  the user accepts the copy prompt.
- `runWSDelete` reads that record and deletes the recorded `~/.ssh/<name>{,.pub}`
  — **only files roksbnkctl itself copied** (the copy never overwrites a
  pre-existing file, so a recorded path is always one we created). Never delete
  an unrecorded `~/.ssh` file. Open Question E: prompt before deleting the
  `~/.ssh` copies, or fold into the existing `ws delete` confirmation.

## Open questions (architect to resolve before dispatch)

- **A** — `--config-file` interview behavior (non-interactive vs seed-then-fill).
- **B** — URL fetch auth / schemes / size / checksum.
- **C** — `IBMCLOUD_API_KEY` raw-vs-encoded for the `api_key_b64` override.
- **D** — multi-target override addressing.
- **E** — `ws delete` ~/.ssh cleanup confirmation UX.

## Non-goals

- A general templating language (Jinja/Go-template) inside `config.yaml` — env
  override is a fixed field map, not arbitrary interpolation.
- Secret managers (Vault/Secrets Manager) integration — env is the v1 surface.
