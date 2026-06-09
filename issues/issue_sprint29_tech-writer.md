# Sprint 29 — tech-writer issues (air-gap registry mirror: the air-gapped-install chapter + references)

> **Sprint 29 frame.** Document the new air-gapped install path: `roksbnkctl
> registry` (the CRUD/COS-client-shaped mirror) + the optional **Registry phase**
> that replicates the BNK bill-of-materials from FAR into the cluster's OpenShift
> internal registry, after which `bnk up` installs with no external egress. Staff
> builds (`issue_sprint29_staff.md`); architect frames the prose
> (`issue_sprint29_architect.md` Issue 5). Spec:
> [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md).

`Status`: open (draft — not yet dispatched)

---

## Issue 1 — New chapter: the air-gapped install

**Severity**: medium
**Status**: open

A new `book/src/` chapter (+ `SUMMARY.md` entry), placed after the BNK-deploy
chapter and before/with the troubleshooting material:

- The customer scenario: why mirror (no external egress at deploy time), and the
  CRUD/COS-client framing of `registry {bom, replicate, list, diff, verify,
  prune}`.
- The bill-of-materials: the `f5-bigip-k8s-manifest` as the source of truth (25
  charts + 56 images) + the non-F5 deps (cert-manager, `bitnami/kubectl`).
- The walk-through: `cluster up` → `registry replicate --target openshift` →
  `registry verify` → `bnk up` (now pulling from the internal registry) → the
  optional `gateway`/`testing` phases.
- The two-address reality for the curious: charts pulled by the host over the
  route, images by pods over the in-cluster service via `system:image-puller`.
- The pluggable-target note: OpenShift internal registry now; ICR / generic OCI
  later.

## Issue 2 — Reference updates

**Severity**: low
**Status**: open

- **Configuration reference** (`book/src/28-configuration-reference.md`): the new
  `registry:` block (target, namespace, creds, include-deps).
- **Command reference** (`book/src/27-command-reference.md`): regenerate via
  `go run ./tools/refgen/cobra-md` so the `registry` group is included.
- **Tearing-down chapter** (`book/src/11-tearing-down.md`): note `registry prune`
  + how the mirror relates to `bnk down`/`cluster down` (per the namespace-model
  decision, architect Issue 3).
- CHANGELOG entry for the release that ships Sprint 29.

### Scope guards
- Real transcripts only (re-captured against the shipped binary) — no invented
  output. Mark any placeholder illustrative.
- mdbook builds (docker image) clean; cspell green (add `BOM`, `ORAS`, `crane`,
  `dockerconfigjson`, etc. to the dictionary).
- Wait for staff's CLI to stabilize before regenerating the command reference.

### Acceptance criteria
1. Air-gapped-install chapter authored + linked in `SUMMARY.md`.
2. Configuration + command references updated/regenerated.
3. Tearing-down + CHANGELOG updated.
4. Book builds clean (HTML + PDF via the docker backend); cspell green.

### Files affected
- **New**: `book/src/<NN>-air-gapped-install.md`; `book/src/SUMMARY.md`.
- **Modified**: `book/src/{27-command-reference,28-configuration-reference,11-tearing-down}.md`,
  `CHANGELOG.md`, the cspell dictionary.

### Related
- [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md) · `issue_sprint29_architect.md`
  (Issue 5 prose framing) · `issue_sprint29_staff.md` (the CLI surface to document).
