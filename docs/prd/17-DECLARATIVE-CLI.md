# PRD 17 — Declarative CLI: config.yaml as the single input

- **Status:** Proposed (direction).
- **Motivation:** the original command syntax was tied 1:1 to the terraform
  phasing (`cluster up`, `bnk up`, `testing up`, `gateway up`). Now that the
  terraform is internal (embedded / `tf_source`) and **config.yaml is the
  canonical input**, the CLI should be tied to the *lifecycle*, not the phases —
  and optimized for CI / fleet use.

## Goal

`config.yaml` declares the desired state (cluster create-or-attach, which phases,
reuse-by-name, addressing, BIG-IP/CIS, …). The CLI **reconciles to it** with a
small lifecycle vocabulary, and phase orchestration is *derived from config*, not
hand-scripted by the operator. The phase-named verbs become targeted escape
hatches, not the primary interface.

## Current state (already true)

- **config.yaml is the on-disk contract**; phases read only it (+ resolved creds).
- **`roksbnkctl up` is already partly declarative** — it brings up the cluster
  (create *or* attach, per `cluster.create`), then **BNK ∥ Testing concurrently**,
  bringing up only the phases config enables. `gateway` is the one phase still
  outside `up`.
- **`init --non-interactive`** assembles config.yaml from `ROKSBNKCTL_*` env alone
  (no file, no prompt) — the argv+env runner path.
- **`--config-file` / `--var-file` / `--override-from-env`** seed config.yaml.
- **Every phase has `status` + `output`** — `<phase> status [--json]` (live readiness
  envelope) and `<phase> output [name] [--json] [--show-sensitive]` (tfstate outputs,
  terraform-output semantics). Delivered for cluster/bnk/testing/gateway.

## Proposed

### Vocabulary

| Verb | Meaning |
|---|---|
| `up` (alias `apply`) | reconcile **everything config declares**, in dependency order — **fold `gateway` in** so `up` is the whole deployment |
| `down` (alias `destroy`) | tear down everything declared, in reverse |
| `plan` | dry-run: what `up` would change |
| `status` | declared-vs-actual across all phases |
| `output` | structured outputs — **delivered**: `<phase> output` scoped to that phase's own attributes (terraform/outputs.tf ownership), and a top-level `output` merging all phases (each value from its owning phase's state) |
| `validate` | schema-check config.yaml; no cloud calls |

### Phase verbs → targets

Keep `cluster`/`bnk`/`testing`/`gateway` as **selectors on the lifecycle verb**,
not separate workflows:

```
roksbnkctl up                 # reconcile all declared phases (the CI path)
roksbnkctl up --only bnk       # re-run one phase
roksbnkctl up --skip testing   # everything except testing
roksbnkctl down --only gateway
```

Retain `cluster up` / `bnk up` / … as **back-compat aliases** for `up --only <phase>`.

### Reuse = config, not verbs

Adopt-a-TGW / BYO-VPC / existing-cluster are **config**
(`resources.*.{create:false, existing:…}`), never commands. The only CLI addition
reuse motivates is a discovery helper that replaces the interview's live lookup:

```
roksbnkctl discover tgw|vpc|clusters|cos    # list adoptable resources (id/name)
```

### CI-first primitives

- `roksbnkctl up --config <file>` — apply straight from a config file (no separate
  `init`); collapses the 2-step CI flow to 1.
- `roksbnkctl output -o json` / `plan -o json` / `status -o json` — machine output;
  supersedes consumers reading `cluster-outputs.json` directly.
- Global `--non-interactive` (or auto-detect no-TTY) ⇒ `--auto` implied, **never
  prompts**.
- **Stable, categorized exit codes** (config error vs auth error vs infra error)
  so CI can branch.

### `init` shrinks to authoring

`init` becomes the *interactive author* of config.yaml. CI doesn't need it —
`up --config` reads the file directly. Canonical flows:

```
# human, first run
roksbnkctl init        # interactive → writes config.yaml
roksbnkctl up

# CI / fleet
roksbnkctl up --config config.yaml --non-interactive
```

## Payoff: the BNK Forge container artifact collapses

Phase selection moves from step `when:` gates into config (which the env already
sets), so the artifact goes from a 5-step phase script to ~2:

```
# today                          # declarative
init --non-interactive           init --non-interactive
cluster up --auto  (when …)       up --auto      # reconciles exactly what config declares
bnk up --auto      (when …)
testing up --auto  (when …)
gateway up --auto  (when …)
```

## File-by-file plan (when implemented)

- `internal/cli/lifecycle.go` — fold gateway into `up`/`down`; add `--only`/`--skip`.
- `internal/cli/*_phase.go` — make the phase commands thin aliases for
  `up --only <phase>`.
- new `output`/`validate` commands; `-o json` on `plan`/`status`/`output`.
- global `--non-interactive` + TTY detection; exit-code taxonomy.
- `discover` command over the IBM client (TGW/VPC/cluster/COS listing).

## Test plan

- `up` with a gateway-bearing config runs all four phases; `up --only bnk` runs
  one; `--skip testing` omits it. Back-compat: `bnk up` still works.
- `validate` rejects an unknown field / incomplete config without cloud calls.
- `output -o json` emits the cluster identity; exit codes distinguish
  config/auth/infra failures.

## Acceptance criteria

1. `roksbnkctl up` reconciles the full declared deployment (incl. gateway) from
   config.yaml; phase verbs are aliases.
2. A CI runner deploys with `init --non-interactive` + `up --auto` (two steps) and
   consumes `output -o json`.
3. Reuse is expressed only in config; `discover` lists adoptable resources.
4. No prompts in `--non-interactive`; exit codes are categorized and stable.

## Open questions

- `up`/`down` vs `apply`/`destroy` as the canonical names (keep up/down + alias).
- Whether `gateway` folds into `up` unconditionally or stays opt-in via a
  `gateway:` block presence check (lean: presence of the block enables it).
