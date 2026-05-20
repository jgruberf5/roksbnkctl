# Sprint 18 — tech-writer issues (cos bucket get + post-v1.6.2 work cycle)

> **Sprint 18 frame.** First regular work sprint post-`v1.6.2`.
> Tech-writer runs **after** the three-way integration of staff
> (Issue 1: `cos bucket get`) + architect (Issue 1: mermaid PDF
> rendering) + validator (Issues 1 + 2: hermetic + live tests). The
> tech-writer's job is a drift/consistency sweep over the integrated
> tree and an optional ≤2 Part-B cross-cutting documentation issues.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift / consistency review for the integrated `cos bucket get` + mermaid PDF fix

**Severity**: low
**Status**: open

**Description.** Once the three-way integration commit for staff +
architect + validator lands on `main`, the tech-writer reviews it
for:

- `--help` text on the new `cos bucket get` matches sibling
  `cos bucket {create,list,delete}` style (flag shapes, positional
  args, required-vs-optional surfacing).
- CHANGELOG bullets for both changes are user-facing — no internal
  jargon (`internal/...`, Lua filter, mermaid-cli args), each
  cross-links the canonical chapter.
- The COS chapter pins a short subsection for the new verb where
  the existing `cos object get` example lives.
- The mermaid fix is mentioned in a §Diagnostics paragraph in the
  relevant book chapter, NOT presented as a feature.
- If the architect required a docker-image rebuild, `book.toml`'s
  image tag is current.
- `book/src/SUMMARY.md` is unchanged (this is a defect fix +
  additive feature, not a TOC change).

**Acceptance criteria**:

1. Every finding in this issue's Closure section names a specific
   file path + line number so the integrator can act precisely.
2. Findings are tagged with severity (low / medium / high); each
   high finding blocks the release, each medium gets an integrator
   judgement-call, each low is FYI.
3. A final GREEN / RED launch verdict line ends the closure —
   GREEN = integrator can ship `v1.6.3` (or `v1.7.0` if so
   judged), RED = address findings first.

**Out of scope**:

- Restyling the mermaid diagrams themselves (the fix is
  pipeline-side, not source-side).
- Wholesale chapter rewrites — drift sweep, not redesign.
- New documentation chapters — that's tracked separately.

**Files affected**: `issues/issue_sprint18_tech-writer.md` (this
file's Closure section). Read-only on the integrated tree.

**Related**: staff Issue 1, architect Issue 1, validator Issues
1 + 2 — all reviewed for drift, no edits suggested to those
ledgers.

## Closure (tech-writer, 2026-05-20)

`Status: resolved`

Reviewed the integrated tree (`HEAD = 420eac9`, end of Sprint 18
round-6) — three-way integration commit `4da221a` plus rounds 2/4/6
that closed the two manual-testing defects (Issue 2 cos perf, Issue
3 cos cross-region 404) live-GREEN against a real account. Drift
sweep checked: `cos bucket get --help` shape vs sibling `cos
bucket {create,list,delete}` + `cos object get`; the COS book
chapter (`book/src/25-cos-supply-chain.md`); the auto-generated
command reference (`book/src/27-command-reference.md`); `book.toml`
image-tag reference; mermaid fix surface vs PDF-build prose;
docstring/comment references to CHANGELOG versions.

### High — `book/src/27-command-reference.md` is stale; missing `cos bucket get` subsection

`book/src/27-command-reference.md` line 3 states the file is
**auto-generated** by `go run ./tools/refgen/cobra-md >
book/src/27-command-reference.md` and must be **"re-run on every CLI
surface change"**. Sprint 18 staff Issue 1 added a new cobra verb
(`internal/cli/cos.go` lines 100-121, `cosBucketGetCmd`) — but the
reference file was never regenerated. Concretely, the `### roksbnkctl
cos bucket` section runs from line 161 to line 204 and contains
subsections for `create` (line 173), `delete` (line 190), and
`list` (line 200) — **no `#### roksbnkctl cos bucket get`
subsection**. The `--no-clobber` flag (added at `internal/cli/cos.go`
line 169) is therefore also undocumented in the reference, and the
two-positional-arg signature `<bucket> <local-dir>` is missing.

**Fix.** Integrator re-runs `go run ./tools/refgen/cobra-md >
book/src/27-command-reference.md` from the repo root before tagging
`v1.6.3`. The regeneration is mechanical; no human editing.

**Why high.** This is the canonical CLI surface the book exposes
to users; shipping a release with the new flagship verb absent
from its own reference file is a user-visible doc regression
exactly of the kind the auto-gen header was written to prevent.

### Medium — `book/src/25-cos-supply-chain.md` does not pin the new `cos bucket get` verb alongside `cos object get`

`book/src/25-cos-supply-chain.md` is the COS chapter; the prompt's
Part A explicitly calls this one out (*"if the new verb's example
isn't pinned there alongside `cos object get`, it's a finding"*).
Three drift points in the same file:

- **Line 29** — the "three command levels" code block shows
  `roksbnkctl cos bucket   {create|delete|list} --instance
  <name-or-CRN>`. Should be `{create|delete|list|get}`.
- **Lines 64-86** — the `### cos bucket` subsection has examples
  for `create`, `list`, `delete` but no `get` example and no row
  in the flags table for `--no-clobber`. The natural place to pin
  the new verb is between the `delete` example (line 78) and the
  flags table at line 81, mirroring the shape `cos object get`
  takes at lines 97-100.
- **Lines 207-238** — the *"Worked example: rotating COS supply-
  chain assets"* section is the canonical end-to-end walkthrough.
  A `cos bucket get` recipe ("snapshot the supply-chain bucket
  before a rotation, so you can roll back to a known-good set of
  artefacts") is a one-paragraph natural fit.

**Fix.** Integrator adds the pinned example in the `### cos bucket`
subsection (per the prompt's *"pin a short subsection"* shape) and,
optionally, a snapshot-before-rotate paragraph in the worked
example. Updating line 29's three-command-levels listing to
`{create|delete|list|get}` is one character.

**Why medium and not high.** The auto-generated reference (finding
1) is the contractual doc surface; the chapter is the narrative
companion. A v1.6.3 that ships with a regenerated chapter 27 and a
chapter 25 still missing the pinned example is degraded but not
broken — the integrator can judgement-call carrying this one finding
into a v1.6.4 doc-polish if release-cut timing is tight.

### Low — Stale "SVG" comments across the PDF-build pipeline files (mermaid fix landed PNG-side, but the prose around the pipeline still says SVG)

The architect's fix in `tools/docker/mdbook/render-mermaid.lua`
pivoted output from SVG to PNG (file's own comment block, lines
11-21, now correctly documents the foreignObject / librsvg
interaction). Five sibling comments in **other** files still say
SVG and now contradict the as-landed pipeline:

- `book/book.toml` line 35: *"The Lua filter pre-renders Mermaid
  code blocks to SVG via mermaid-cli so the PDF embeds real
  diagrams …"* — should read **PNG via mermaid-cli's Puppeteer +
  Chromium path** (the librsvg-can't-rasterise-foreignObject
  explanation belongs in the Lua filter itself, where the architect
  already put it; book.toml just needs the one-word swap).
- `Makefile` line 78: *"the Lua filter at /opt/render-mermaid.lua
  pre-renders Mermaid blocks to SVG via mmdc"* — same swap.
- `tools/docker/mdbook/Dockerfile` line 8: *"(pre-renders Mermaid
  blocks to SVG so the PDF has real diagrams rather than literal
  `sequenceDiagram ...` code-block fallback text)"* — same swap.
- `tools/docker/mdbook/Dockerfile` line 69: *"nodejs + chromium
  are for @mermaid-js/mermaid-cli's puppeteer-based rendering of
  Mermaid diagrams to SVG."* — same swap.
- `tools/docker/mdbook/Dockerfile` lines 110-113: *"Pandoc Lua
  filter that pre-renders Mermaid code blocks to SVG via mmdc
  during the PDF build. Configured via book.toml's
  [output.pandoc.profile.pdf]::lua-filter setting …"* — two bugs:
  (a) SVG → PNG; (b) the actual TOML key is `filters` (plural, an
  array — see `book.toml` line 48: `filters = ["/opt/render-mermaid.lua"]`),
  not `lua-filter`.

`librsvg2-bin` at Dockerfile line 85 stays in the apt install list
because mdbook-pandoc / pandoc still depend on it transitively for
non-mermaid SVG content (HTML book builds, the `mermaid.min.js`
HTML path) — that's intentional, not a finding.

**Fix.** Six in-place comment edits, all one-line. Mechanical;
no behavioral impact (comments, not code).

**Why low.** None of these are user-visible; they're contributor
prose. A future contributor reading any of these files to debug a
PDF render will be sent on a wrong-path SVG investigation, but
the as-built behavior is correct. FYI grade.

### CHANGELOG / PLAN check (integrator-owned, not a finding)

`CHANGELOG.md` and `docs/PLAN.md` carry **no Sprint 18 entry yet**
(top entry is `v1.6.2 — 2026-05-19`). Per the dispatch frame, the
integrator drafts both after the tech-writer pass — so this is
expected, not drift. One forward-looking note for the integrator:
the `### Fixed` bullets for the mermaid bug and the cos perf bug
should follow the user-facing / no-internal-jargon convention
already established in the v1.6.2 entry (name the symptom — "PDF
mermaid diagrams shipped without label text"; "cos commands ran
~50× slower than ibmcloud cos" — not the internal mechanism —
"Lua filter SVG→PNG pivot"; "Resource Controller v2 server-side
service-id filter"). The Sprint 18 round-6 commit message
(`420eac9`) and the staff/architect Closure sections are the
canonical PR-description / engineering-rationale sources; the
CHANGELOG is the user-facing summary.

No docstring or in-code comment in the integrated tree references
"see CHANGELOG vX.Y.Z" with a mismatched version. (`internal/cos/
client.go` line 350's `"v1.6.x"` reference is to the IBM COS
regional endpoint list, not a roksbnkctl version, and is correct.)

### Part B — no cross-cutting documentation issues filed

The three findings above are all sweep-grade drift cleanups; each
fits cleanly inside Issue 1's closure. No cross-cutting
documentation gap surfaced that warrants a new numbered issue
(the prompt's quality-over-volume bar). Part B count: **0** new
issues; cap was ≤2.

### Findings summary

- **1 high** (chapter 27 stale — `cos bucket get` missing from the
  auto-generated command reference)
- **1 medium** (chapter 25 stale — new verb not pinned alongside
  `cos object get`)
- **1 low** (five SVG → PNG comment drifts across `book.toml`,
  `Makefile`, and the mdbook `Dockerfile`; plus a stale
  `lua-filter` → `filters` TOML-key reference in the Dockerfile)

### Launch verdict

**RED** — the high finding (chapter 27 auto-gen drift) blocks
`v1.6.3` release. The fix is one mechanical command
(`go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`);
re-run, eyeball the resulting `### roksbnkctl cos bucket` section
to confirm the new `#### roksbnkctl cos bucket get` subsection
appears with the `--no-clobber` flag row, commit. The medium and
low findings are integrator-judgement: chapter 25 can fold into
the same release-prep commit (recommended; low cost) or land in a
v1.6.4 doc polish if release-cut timing is tight; the SVG → PNG
comment drift is FYI and can ride either commit.

### Did not commit

Per the prompt's *"Do **not** commit. Do **not** run `gh issue
create`."* constraint. This ledger is the only modified file; the
integrated tree is read-only as dispatched.

---

## Integrator pass — 2026-05-20

All three findings addressed in the integration commit:

- **High** — `book/src/27-command-reference.md` regenerated via
  `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`.
  `#### roksbnkctl cos bucket get` subsection now present at line 267;
  positional args + `--no-clobber` flag both documented.
- **Medium** — `book/src/25-cos-supply-chain.md` pinned: line 29's
  three-command-levels block updated to `{create|delete|list|get}`;
  a `cos bucket get` example added between `delete` and the flags
  table (the snapshot-before-rotate framing the tech-writer
  suggested); `--no-clobber` row appended to the flags table.
- **Low** — five SVG→PNG comment drifts fixed across `book/book.toml`,
  `Makefile`, `tools/docker/mdbook/Dockerfile` (three locations);
  the stale `lua-filter` TOML-key reference at Dockerfile:112 also
  corrected to `filters` (the actual array key per
  `book.toml`'s `[output.pandoc.profile.pdf]` block). `librsvg2-bin`
  apt install left in place per the tech-writer note (transitively
  needed for non-mermaid SVG content).

**Verdict flipped GREEN.** Ready for v1.6.3 CHANGELOG + tag.
