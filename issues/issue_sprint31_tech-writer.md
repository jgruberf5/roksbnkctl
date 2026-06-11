# Sprint 31 — tech-writer issues (document the runner image as an install target + remote state)

> The all-in-one runner image makes "run roksbnkctl from a container" a
> first-class **install target** — that only counts as valid if the book
> documents how to install/run it and how it holds state. Plus the new COS/S3
> remote state backend needs a user-facing chapter. Specs:
> [PRD 15](../docs/prd/15-RUNNER-IMAGE.md), [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md).
> Architect frames the prose; staff ships the surface to document.

`Status`: in progress — **Issues 1–2 (PRD 15) done 2026-06-11**; Issues 3–4 (PRD 16 remote-state docs) pending

> **PRD 15 docs done (2026-06-11).** `book/src/04-installation.md` gains the
> install-method matrix + **Path C — the all-in-one runner image** (pull/run,
> the `/work` `ROKSBNKCTL_HOME` volume contract incl. the named-volume vs
> bind-mount ownership caveat, `--backend local`, the mdbook/pandoc exclusion,
> air-gap fit). `book/src/18-choosing-backend.md` notes `--backend local`
> in-runner. Book build (mdbook docker image) not run in the authoring env —
> verify HTML/PDF clean at integration.

---

## Issue 1 — Installation chapter: the container install target

**Severity**: high
**Status**: open

The [Installation chapter](../book/src/04-installation.md) currently covers the
binary (release tarball / `go install`). Add the **container** as a peer install
method, with an honest install-method matrix:

- **Pull + run.** `docker run` (and `podman run`) the published
  `roksbnkctl-tools-runner` image; the `ENTRYPOINT` is `roksbnkctl`, so
  `docker run … runner version` works. Show the per-release tag + `:latest`.
- **State is a volume.** The decisive bit for a container: `-v <vol>:/work`
  (named volume or host bind) with `ROKSBNKCTL_HOME=/work/.roksbnkctl`. Cross-link
  the new remote-state chapter (Issue 3) for the no-volume / fleet path.
- **`--backend local` inside the runner.** Every dispatched tool is already in
  the image, so the docker/k8s backends aren't needed in-container — runs are
  local, no docker-in-docker. Say this plainly.
- **What's NOT in it.** Note the runner is a *use* target, not a *dev/docs build*
  target — mdbook/pandoc/texlive live in the separate docs image and are
  deliberately absent here (keeps the runner small).
- **Air-gap fit.** One artifact to stage behind the firewall (cross-link the
  air-gapped-install chapter + PRD 14 registry targets).

The install-method matrix should make the trade-off clear: binary (lightest,
host tools) vs container (self-contained, mount state).

## Issue 2 — Choosing-a-backend chapter note

**Severity**: low
**Status**: open

[Choosing a backend](../book/src/18-choosing-backend.md): add a short note that
*inside the runner image*, `--backend local` is the right default (the bundled
tools are local), and the docker/k8s backends remain for the binary-on-host
case dispatching tools to other network locations.

## Issue 3 — New chapter: remote (COS/S3) state

**Severity**: medium
**Status**: open

A new `book/src/` chapter for the `state:` config block:

- **Why**: local state is workstation/volume-local with no locking; COS/S3 state
  decouples it from the container and adds a single-writer lock — the fleet/CI
  answer.
- **Config**: the `state.backend: s3` block — endpoint, bucket, region, key
  prefix, and the **HMAC** access/secret-key sources (not the IAM API key).
- **Per-phase keys**: how cluster/bnk/testing/gateway each get their own key in
  the one bucket.
- **Migration**: flipping an existing workspace local → s3 (the `state migrate` /
  `init -migrate-state` flow), idempotent, no silent overwrite.
- **Locking + the single-writer caveat**: what the lock does and doesn't
  guarantee for parallel runners.
- Default stays **local** — existing workspaces are unchanged; call that out.

## Issue 4 — Reference + cross-doc

**Severity**: low
**Status**: open

- **Configuration reference** (`28-configuration-reference.md`): the `state:`
  block.
- **Command reference** (`27-command-reference.md`): regenerate if a `state
  migrate` command lands (`go run ./tools/refgen/cobra-md`).
- **Building-from-source** (`31-building-from-source.md`): clarify the runner is
  built via `tools/docker/Makefile build-runner`, separate from the docs image.
- CHANGELOG entry for the Sprint 31 release.

### Scope guards
- Real transcripts only — capture `docker run … runner version` and a bundled-tool
  version against the actually-built image; don't invent output.
- mdbook builds clean (HTML + PDF via the docker image); cspell green (add
  `podman`, `HMAC`, etc. as needed).
- The install-method matrix must be honest about the state-volume requirement —
  do not imply the container is stateless without the remote backend.

### Acceptance criteria
1. Installation chapter documents the container install target + the `/work`
   `ROKSBNKCTL_HOME` volume contract + the `--backend local` story + the
   mdbook/pandoc exclusion, with the install-method matrix.
2. Remote-state chapter authored + linked in `SUMMARY.md`.
3. Choosing-backend + configuration/command references updated.
4. Book builds clean; cspell green.

### Files affected
- **New**: `book/src/<NN>-remote-state.md`.
- **Modified**: `book/src/{04-installation,18-choosing-backend,27-command-reference,
  28-configuration-reference,31-building-from-source,SUMMARY}.md`, `CHANGELOG.md`,
  the cspell dictionary.

### Related
- [PRD 15](../docs/prd/15-RUNNER-IMAGE.md) · [PRD 16](../docs/prd/16-REMOTE-STATE-BACKEND.md)
  · `issue_sprint31_staff.md` (the CLI/image surface to document).
