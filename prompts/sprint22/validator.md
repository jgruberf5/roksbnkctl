You are the **validator** agent for Sprint 22 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint22/README.md` — integrator decisions; the
   release-gate on Sprint 23 (DO NOT trigger any tag-push
   side-effects).
2. `docs/PLAN.md` §"Sprint 22" — full sprint scope.
3. `.github/workflows/tools-images.yml` — the workflow you
   edit. Currently has `matrix.image: [ibmcloud, iperf3]`;
   you add `mdbook`. The leading comment block (lines 1-17)
   names only the first two images and needs the third added.
4. `tools/docker/mdbook/Dockerfile` — confirm it builds
   against `context: .` without referencing repo-root paths.
   Same situation as `iperf3` per the existing comment at
   `tools-images.yml:49-54`.
5. `Makefile:54` — the line referencing
   `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`. No edits
   needed there; just confirm the image name you push to in
   the matrix expansion matches.

## Tasks

1. **Add `mdbook` to `matrix.image`** at
   `.github/workflows/tools-images.yml:33`:
   ```yaml
   image: [ibmcloud, iperf3, mdbook]
   ```
2. **Update the workflow's leading comment** (lines 1-17 or
   wherever the image list is enumerated) to add `mdbook` to
   the published-image bullet list:
   ```
   #   - ghcr.io/jgruberf5/roksbnkctl-tools-mdbook
   ```
   Keep the comment's tone + structure unchanged otherwise.
3. **Update the inline comment at lines 49-54** about the
   broader context being harmless to add `mdbook` alongside
   `iperf3`:
   ```
   # The iperf3 and mdbook Dockerfiles don't reference repo-
   # root paths and ignore the broader context. The build-args
   # below are ibmcloud-specific and harmlessly ignored by
   # iperf3 and mdbook.
   ```
   Verify by inspection that `tools/docker/mdbook/Dockerfile`
   does NOT reference any repo-root paths (no `COPY ./cmd`,
   `COPY ./internal`, etc.) before making this claim. If it
   DOES reference repo-root paths, STOP and file a finding
   instead of editing the comment — the broader context might
   not be harmless for mdbook.
4. **Trigger a manual `workflow_dispatch` run** after the
   integrator merges your change to `main`. DO NOT trigger
   anything yourself — your closure documents the
   integrator's post-merge step (one `gh workflow run
   tools-images.yml` invocation), with the expected outcome
   (three matrix jobs, all green, three `:dev` tags
   published to `ghcr.io/jgruberf5/roksbnkctl-tools-*`).
5. **Do NOT touch the tag-push path.** Sprint 22's release
   tag is gated on Sprint 23. The workflow's `startsWith(
   github.ref, 'refs/tags/v')` step will fire on the next
   `v*` tag push regardless of which sprint cuts it; nothing
   for you to gate here.

## Out of scope

- `internal/`, `cmd/`, `tools/docker/mdbook/Dockerfile` —
  staff/architect surfaces (and the Dockerfile is not edited
  this sprint; you only confirm by inspection it doesn't
  reference repo-root paths).
- `Makefile` — append-only-shared per `prompts/README.md`;
  no edits this sprint.
- The book, CONTRIBUTING.md — architect/tech-writer surfaces.
- Triggering the `workflow_dispatch` run yourself — that's
  integrator-owned post-merge.

## Acceptance criteria

1. `.github/workflows/tools-images.yml`'s `matrix.image`
   includes `mdbook` alongside `ibmcloud` and `iperf3`.
2. The workflow's leading comment + the broader-context
   inline comment both name `mdbook` accurately.
3. `tools/docker/mdbook/Dockerfile` does NOT reference
   repo-root paths (verified by inspection; documented in
   closure).
4. No other file edits.
5. `gh workflow run tools-images.yml` is documented in
   closure as the integrator's post-merge verification step.

## Closure

Write your closure to
`issues/issue_sprint22_validator.md` §"Closure — validator,
<date>". Include: the workflow diff (3 edits), the Dockerfile
inspection result, the `gh workflow run` invocation for the
integrator, and the expected outcome of that manual dispatch.
Flip status `open` → `resolved`. Create the issue file if it
doesn't exist yet — Sprint 22's only existing issue file is
`issue_sprint22_staff.md`; yours is new.
