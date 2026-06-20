# AGENTS.md — roksbnkctl deployment reference for agentic operators

> This file is scaffolded into a **workspace** by `roksbnkctl agent init`. It
> is the shared knowledge base every persona reads first when driving a ROKS +
> BIG-IP Next for Kubernetes (BNK) trial with an agentic CLI. `roksbnkctl`
> itself embeds no LLM — you bring your own coding agent (`roksbnkctl agent
> <cli>` prints the invocation) and it acts under one of the personas in
> `personas/`.

---

## Quick orientation

```
config.yaml            single source of truth for THIS deployment (the contract)
decisions.md           why we chose what we chose — alternatives rejected + rationale
journal/<date>-*.md    append-only timeline (one entry per significant action)
report.md              the customer-facing deliverable (doc-specialist writes it)
personas/              role definitions — act as exactly ONE at a time
```

State `roksbnkctl` manages for you (read, don't hand-edit):

```
state-cluster/  state/  state-testing/  state-gateway/   per-phase terraform state
cluster-outputs.json                                     persisted cluster identity
forge/kubeconfig.yaml                                    token-based kubeconfig (auto-refreshed)
terraform.applied.tfvars                                 snapshot of the last apply's inputs
```

The deployment lives under `$ROKSBNKCTL_HOME` (default `~/.roksbnkctl/<workspace>/`).

---

## The phased lifecycle (and the only correct order)

```
init  →  cluster up  →  bnk up   ∥  testing up   →  gateway up
                              (bnk and testing are independent; gateway needs a healthy BNK)
teardown is REVERSE order:  gateway down → bnk down → testing down → cluster down
```

- `cluster up` provisions the ROKS cluster + transit gateway + registry COS +
  cert-manager + the TGW jumphost. **~30 minutes and it costs money.** This is
  the heaviest, most expensive, least reversible step — gate it explicitly.
- `bnk up` deploys the BNK trial (FLO / CNEInstance / license) on top of the
  cluster. ~5 minutes; the common iteration loop is `bnk down && bnk up`.
- `testing up` provisions jumphosts for connectivity/DNS/throughput probes
  (optional; pure IBM VPC, no Kubernetes).
- `gateway up` applies the data-plane config (Gateway API + SnatPool + Egress +
  routes); **opt-in**, only after BNK is healthy.

`roksbnkctl up` / `down` are shape-aware composites that do the right phases in
the right order — prefer them unless a persona's checklist says otherwise.

---

## Required gates on every destructive command

Apply/destroy commands prompt for confirmation. `--auto` skips the prompt — only
pass it when a persona's checklist (and, for `cluster up`, the customer) has
granted consent, and journal that consent first.

- `cluster up` (provision, ~30 min, billable), `down`, `cluster down`,
  `cleanup`, `bnkforge register` are **destructive / outward-facing**.
- `bnk down` / `testing down` / `gateway down` **no-op-succeed** (exit 0) when
  the phase was never deployed — safe to run in a reverse-order teardown.
- `cluster down` **refuses** while BNK trial state exists (run `bnk down`
  first); `down` refuses on an empty workspace. These refusals are guard-rails,
  not errors to force past.

Never add `--auto` to `cluster up` or `cleanup` without explicit, journaled
operator/customer consent.

---

## Credentials — never print or commit secrets

- The IBM Cloud API key is resolved through `config.yaml.ibmcloud.api_key_source`
  (`env` → keychain → config → prompt). `roksbnkctl apikey` PRINTS the resolved
  key — treat it as secret-tier; the solution-architect and doc-specialist
  personas must not run it.
- `config.yaml` holds *references and sources*, not secret values. Don't paste
  keys, JWTs, or kubeconfig tokens into `config.yaml`, `decisions.md`, the
  journal, or the report.
- The token in `forge/kubeconfig.yaml` is a short-lived IAM bearer token by
  design — `roksbnkctl kubeconfig --refresh` (and BNK Forge) re-mint it. Don't
  try to make it long-lived, and don't copy it into deliverables.

---

## Recurring gotchas (real ones, seen in the field)

- **Fresh runner `$HOME` not writable** → Helm chart pulls and the kubeconfig
  fetch must not depend on `$HOME`. `roksbnkctl` exports `HELM_CACHE_HOME` (and
  friends) + `KUBECONFIG` under `$ROKSBNKCTL_HOME`; if you see
  `could not download chart: ... <HOME>/.cache/helm/...index.yaml: no such file`,
  you're on a build that predates that fix — upgrade the runner image.
- **IBM ROKS masters use a publicly-trusted cert** → the admin kubeconfig has no
  `certificate-authority-data`, and that's normal. The token kubeconfig at
  `forge/kubeconfig.yaml` omits the CA field; system trust validates the server.
- **`cluster up` timeout while waiting for state `normal`** → usually IBM's
  control plane lagging; `up` retries, and re-running is safe (terraform state
  is durable). Don't tear down on the first timeout.
- **Workers show `No resources found` right after `cluster up`** → workers come
  up minutes after the master reports Ready. Wait and re-check.
- **Region matters**: the cluster region is `ibmcloud.region`; the testing
  client VPC has its own region (`resources.client_region`). Don't conflate them
  when sweeping orphans or reading `cluster-outputs.json`.
- **`terraform destroy` can leave orphans** (LBs, security groups, VPEs) → use
  `roksbnkctl cleanup` (preview with `--dry-run`, add `--all-regions`).

---

## Coordination protocol (how the personas work together)

1. The **solution-architect** owns `config.yaml` (scope) and `decisions.md`, and
   talks to the customer. It delegates infra work by writing a request in the
   journal.
2. The **cloud-operator** executes the lifecycle, gates the destructive steps,
   and records results in the journal. It never talks to the customer.
3. The **test-engineer** runs the validation probes and records pass/fail.
4. The **doc-specialist** reads everything and produces `report.md`. It writes
   no infrastructure.

Hand off through `journal/` (append-only — never edit another persona's entry;
add a follow-up). `roksbnkctl journal add "<note>"` appends; `roksbnkctl journal
list` shows the timeline; `roksbnkctl journal report` assembles `report.md`.

---

## Style

- One persona at a time. If a task needs another role, journal the handoff and
  switch — don't silently cross the allowlist boundary in `personas/*.md`.
- Confirm scope against `config.yaml` before any apply. If the customer asks for
  something outside it, update `config.yaml` (with consent) first, then proceed.
- Report outcomes faithfully: if an apply failed, journal the real output; if a
  step was skipped, say so.
