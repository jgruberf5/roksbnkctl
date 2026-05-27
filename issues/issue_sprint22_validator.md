# Sprint 22 — validator issues (mdbook CI matrix expansion)

> **Sprint 22 frame.** Validator owns the CI workflow surface
> (`.github/workflows/tools-images.yml`). Sprint 22's validator
> work folds the existing `mdbook` Dockerfile into the
> already-published `tools-images.yml` matrix so the image
> auto-builds + auto-pushes alongside its `ibmcloud` and
> `iperf3` siblings on `main` pushes, tag pushes, and manual
> dispatch — closing the gap that forced the integrator to
> manually push `roksbnkctl-tools-mdbook:dev` between releases
> (and that intermittently bit `v1.7.0`'s `make release-publish`
> step 2 via Puppeteer/Chromium cold-start in a stale image).

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Fold `mdbook` into the `tools-images.yml` matrix

**Severity**: low
**Status**: resolved

### Motivation

`tools/docker/mdbook/Dockerfile` already exists and is wired
into the root `Makefile` (line 54: `BOOK_IMAGE ?=
ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`), but the
`.github/workflows/tools-images.yml` matrix only enumerates
`ibmcloud` and `iperf3` — the `mdbook` image is published
manually. That gap intermittently surfaces as Puppeteer /
Chromium cold-start failures in a stale image during release
publish (`v1.7.0` `make release-publish` step 2). Adding
`mdbook` to the matrix closes the gap.

### Editable scope

- `.github/workflows/tools-images.yml` — three small edits
  (`matrix.image` list, leading published-image comment,
  inline broader-context comment).
- `issues/issue_sprint22_validator.md` — this closure file
  (new).

### Out of scope

- `tools/docker/mdbook/Dockerfile` (staff/architect surface;
  not edited this sprint — only inspected to confirm it does
  not reference repo-root paths).
- `Makefile` (append-only-shared per `prompts/README.md`).
- `internal/`, `cmd/`, the book, `CONTRIBUTING.md`.
- Triggering the `workflow_dispatch` run (integrator-owned
  post-merge step).
- Any tag-push or release work (Sprint 22's release tag is
  gated on Sprint 23 per `prompts/sprint22/README.md`
  decision 3).

---

## Closure — validator, 2026-05-27

**Status flip**: open → resolved.

### Workflow diff (3 edits to `.github/workflows/tools-images.yml`)

1. **Leading comment block (line 8, added)** — added the
   third published-image bullet so the comment matches the
   matrix:

   ```diff
      #   - ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud
      #   - ghcr.io/jgruberf5/roksbnkctl-tools-iperf3
   +  #   - ghcr.io/jgruberf5/roksbnkctl-tools-mdbook
      #
   ```

2. **`matrix.image` list (line 34)** — added `mdbook` as the
   third matrix entry alongside `ibmcloud` and `iperf3`:

   ```diff
   -        image: [ibmcloud, iperf3]
   +        image: [ibmcloud, iperf3, mdbook]
   ```

3. **Inline broader-context comment (lines 50–55)** —
   extended the "iperf3 ignores the broader context"
   reassurance to cover `mdbook` as well:

   ```diff
      # Context is the REPO ROOT (not tools/docker/<image>/) because
      # the ibmcloud image's Stage 1 Go build needs ./cmd, ./internal,
   -  # go.mod/go.sum, embedded.go, and ./terraform/. The iperf3
   -  # Dockerfile doesn't reference repo-root paths and ignores the
   -  # broader context. The build-args below are ibmcloud-specific
   -  # and harmlessly ignored by iperf3.
   +  # go.mod/go.sum, embedded.go, and ./terraform/. The iperf3 and
   +  # mdbook Dockerfiles don't reference repo-root paths and ignore
   +  # the broader context. The build-args below are ibmcloud-specific
   +  # and harmlessly ignored by iperf3 and mdbook.
   ```

No other edits anywhere. The three existing `Build and push
(tag)` / `Build and push (main → :dev)` / `Build and push
(workflow_dispatch → :dev)` steps already use
`${{ matrix.image }}` for both the `file:` path and the
`tags:` value, so the matrix expansion automatically wires up
mdbook for all three trigger paths with zero step-body edits.

### Dockerfile inspection — confirms broader-context claim

Inspected `tools/docker/mdbook/Dockerfile` line-by-line.
**Zero references to repo-root paths.** Specifically:

- No `COPY ./cmd`, `COPY ./internal`, `COPY ./terraform`,
  `COPY ./go.mod`, `COPY ./go.sum`, `COPY ./embedded.go`,
  or any other path rooted at the repo top.
- Only three `COPY` directives, all sibling-of-Dockerfile or
  in-image:
  - `COPY puppeteer-config.json /opt/puppeteer-config.json`
    (line 103) — sibling file under `tools/docker/mdbook/`.
  - `COPY render-mermaid.lua /opt/render-mermaid.lua`
    (line 115) — sibling file under `tools/docker/mdbook/`.
  - `COPY --from=builder /out/bin/mdbook …` (lines 107–109)
    — multi-stage copy from the Rust builder stage, in-image
    paths only.
- Two `RUN` directives are remote/registry-pull operations
  (`apt-get install`, `cargo install --root /out`,
  `npm install -g`) — these don't consult the build context
  at all.
- `WORKDIR /book` (line 117) sets an in-container path; the
  `/book` volume is bind-mounted at run time via
  `Makefile:57` (`docker run --rm -v $(CURDIR)/book:/book
  $(BOOK_IMAGE)`), not baked into the image.

Conclusion: the broader repo-root context is harmless for
the mdbook image — Docker's build context tar will include
the full repo tree, but the mdbook Dockerfile never references
anything outside its own sibling files, so the extra context
is silently discarded. Identical situation to `iperf3` per
the pre-existing workflow comment. The build-args
`ROKSBNKCTL_VERSION` and `ROKSBNKCTL_COMMIT` are also
harmlessly ignored (no `ARG` directive consumes them in the
mdbook Dockerfile).

Image name match confirmed: matrix expansion will produce
`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` on
main-push / workflow_dispatch and
`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:<tagname>` +
`:latest` on tag-push. `Makefile:54`'s
`BOOK_IMAGE ?= ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`
matches the main-push tag byte-identically; no Makefile
edit needed.

### Integrator's post-merge verification step

After the integrator merges this change to `main`, the
matrix expansion needs one manual `workflow_dispatch` run to
verify before relying on it for the next main-push or
tag-push. Per `prompts/sprint22/validator.md` Task 4 + the
sprint's "do not trigger anything yourself" constraint, the
validator does NOT trigger this; the integrator runs it
post-merge:

```
gh workflow run tools-images.yml
```

(Run from a clone of the repo with `gh` authenticated as a
user with workflow-dispatch permission on
`jgruberf5/roksbnkctl`. Optionally pin to `--ref main`
explicitly — the workflow's only `workflow_dispatch:` trigger
has no `inputs:` block, so no `-f key=value` args needed.)

### Expected outcome of the manual dispatch

Three matrix jobs spin up under the `Build tools images`
workflow run, one per matrix entry — `build (ibmcloud)`,
`build (iperf3)`, `build (mdbook)` — all running in parallel
(no `fail-fast`, per the existing `fail-fast: false` strategy
setting). Each job:

1. Checks out the repo (`actions/checkout@v4`).
2. Sets up Buildx (`docker/setup-buildx-action@v3`).
3. Logs into GHCR with the workflow's `GITHUB_TOKEN`
   (`docker/login-action@v3`).
4. Runs the `Build and push (workflow_dispatch → :dev)` step
   (the only step whose `if:` condition matches
   `github.event_name == 'workflow_dispatch'` — the other
   two steps are skipped). Builds with `context: .` +
   `file: ./tools/docker/<image>/Dockerfile` +
   `ROKSBNKCTL_VERSION=dev` + `ROKSBNKCTL_COMMIT=<sha>`
   build-args, then pushes to
   `ghcr.io/jgruberf5/roksbnkctl-tools-<image>:dev`.

All three jobs complete green; three `:dev` tags land in
GHCR:

- `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev`
- `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:dev`
- `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` ← **new**

The mdbook job is the slowest (Stage 1 cargo build of
mdbook / mdbook-mermaid / mdbook-pandoc + Stage 2 ~1.2 GB
runtime image with pandoc + texlive + Chromium per the
Dockerfile's docstring) but should still complete on
`ubuntu-latest` well within GitHub Actions' 6-hour job
ceiling — `cargo install --locked` of three small Rust CLIs
plus an `apt-get install` of the texlive packages and an
`npm install -g @mermaid-js/mermaid-cli` typically lands in
the 15–25 minute range on a cold runner.

Once the dispatch completes green, the matrix expansion is
verified and the integrator's tech-writer scope (per
`prompts/sprint22/README.md` per-role table row 4) can
spot-check the manifest digest against the workflow log to
confirm the `:dev` push was the one the workflow actually
built. Subsequent main-push events (and the eventual
Sprint-23-gated `v1.7.1` tag-push) will then auto-publish the
mdbook image alongside the other two without further manual
intervention.

### Acceptance criteria check

1. `.github/workflows/tools-images.yml`'s `matrix.image`
   includes `mdbook` alongside `ibmcloud` and `iperf3`. **PASS**
   (line 34: `image: [ibmcloud, iperf3, mdbook]`).
2. The workflow's leading comment + the broader-context
   inline comment both name `mdbook` accurately. **PASS**
   (line 8 adds the mdbook published-image bullet in the
   leading block; lines 52–55 add `mdbook` alongside `iperf3`
   in both phrasings of the broader-context comment).
3. `tools/docker/mdbook/Dockerfile` does NOT reference
   repo-root paths. **PASS** (inspection above — three `COPY`
   directives total, all sibling-of-Dockerfile or in-image
   multi-stage; no `./cmd`, `./internal`, `./terraform`,
   `./go.mod`, `./go.sum`, `./embedded.go`, or other
   repo-root references).
4. No other file edits. **PASS** (only the workflow file and
   this closure file touched).
5. `gh workflow run tools-images.yml` documented in closure
   as the integrator's post-merge verification step. **PASS**
   (section above).

All five acceptance criteria GREEN. Validator scope closed.
The release-tag work remains gated on Sprint 23 per
`prompts/sprint22/README.md` decision 3 — this closure does
NOT propose any tag-push or release activity.
