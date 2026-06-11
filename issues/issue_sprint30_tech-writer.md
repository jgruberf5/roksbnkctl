# Sprint 30 — tech-writer issues (book: CI seeding/templating + registry targets)

> Document the unattended-onboarding surface and the registry-target options.
> Specs: [PRD 13](../docs/prd/13-WORKSPACE-CONFIG-SEEDING.md),
> [PRD 14](../docs/prd/14-REGISTRY-TARGETS.md). The book builds via the mdbook
> tools image (`make book`, or `BOOK_BACKEND=docker`).

`Status`: resolved — implemented + committed; release deferred to the combined Sprint 30+31 cut

---

## Page 1 — Unattended workspace seeding (issues 2, 3, 4)

A new book chapter on driving `init` from CI:

- `init -w <ws> --config-file <path|url>` — seed a finished `config.yaml`
  (vs. `--var-file` which seeds `terraform.tfvars`); when + how the interview is
  skipped.
- URL inputs — a committed template fetched from raw-git / a presigned COS URL.
- **`--override-from-env`** — the headline templating story. Include the
  **authoritative override table** (env var → `config.yaml` field → encoding),
  finalized from the architect ledger (Issue 4). Must state the
  `IBMCLOUD_API_KEY` raw-vs-`ROKSBNKCTL_API_KEY_B64`-encoded distinction and
  that **env wins over the seeded file**.
- A worked example: committed `config.yaml.tmpl` with `api_key_b64: ""` →
  `init --config-file <url> --override-from-env` in a pipeline → no secret in git.

## Page 2 — Replicate FAR into a private Artifactory (issue 1, the requested example)

Step-by-step (PRD 14 deliverable):

1. Provision an Artifactory **OCI** repository; note host + repo path + a token.
2. Set the workspace to the generic target: `registry.target: generic`,
   `registry.generic_host`, `registry.generic_repo_prefix`, and the auth —
   shown both inline and via `--override-from-env` (so the token is templated).
3. `roksbnkctl registry diff` / `registry replicate` into Artifactory.
4. Point a BNK install at the Artifactory mirror (the redirect) + verify pulls.

Also: a short note that `registry replicate` now **defaults to ICR**, with the
ICR namespace/credential model and how to keep the old behavior
(`registry.target: openshift`) — covering PRD 14 OQ 4's migration story.

## Page 3 — Workspace teardown note (issue 5)

In the `ws delete` docs: it now also removes any SSH key files it copied into
`~/.ssh/` (recorded at `init` copy time); pre-existing `~/.ssh` files are never
touched.

## Cross-cutting
- Update the command reference / flag tables for the new `init` flags
  (`--config-file`, `--override-from-env`) and the URL form of `--var-file`.
- Keep `CHANGELOG.md` "Unreleased" current as stages land.
