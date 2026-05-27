# Sprint 24 — architect issue (test hosts CLI documentation)

> **Sprint 24 frame.** UX-only sprint adding the `roksbnkctl test
> hosts {list,add,remove,clear}` CLI surface for managing
> `test.connectivity.extra_hosts` without hand-editing
> `~/.roksbnkctl/<workspace>/config.yaml`. Staff ships the cobra
> wiring + the updated error message at `internal/cli/test.go:803`;
> validator pins the four subcommands hermetically; this closure
> covers the book chapter the operator hits when they read about
> the new surface.

**Status**: resolved

---

## Issue 1 — Documenting `roksbnkctl test hosts` in the book

**Severity**: low (documentation; no behavior change — staff owns
the binary surface).
**Status**: resolved

### Motivation

`prompts/sprint24/README.md` §"Per-role scope" Architect Issue 1
specifies a single subsection covering the four new subcommands
with a worked example (operator runs `roksbnkctl test hosts add
https://docs.f5.com`, `roksbnkctl test hosts list`, then
`roksbnkctl test connectivity` in sequence), plus cross-links from
the existing `test connectivity` and `test dns` chapter sections
so an operator hitting the "no hosts configured" error finds the
CLI path immediately. Tone matches the `targets` chapter — the
ergonomic precedent the new subcommands mirror byte-for-byte.

`prompts/sprint24/architect.md` Task 3 directed verifying
`book/src/SUMMARY.md` lists chapter 12 — the SUMMARY does **not**
list sub-bullets (only chapter titles), so no SUMMARY edit needed.
The relevant chapter is `book/src/20-connectivity-testing.md`
(verified via SUMMARY.md line 41), not `12-running-tests.md` (the
prompt was speculative about the file name).

### Closure — architect, 2026-05-27

**Files edited**: `book/src/20-connectivity-testing.md` and
`book/src/21-dns-testing-gslb.md` only. No edits to `SUMMARY.md`
(no depth-2 entries listed there for any chapter), no edits to
`internal/cli/test.go:803`'s error message (staff-territory per
the sprint README), no edits to `12-workspace-config.md` (the
chapter-12 §"test:" reference is unchanged — the new CLI is a
read/write surface over an existing slice, not a new field), no
edits to `27-command-reference.md` or `28-configuration-reference.md`
(tech-writer's drift sweep will pick those up against the built
binary; the architect surface is the chapter-20 prose).

### Edit 1 — new subsection in chapter 20

**Site**: `book/src/20-connectivity-testing.md`, **lines 54–122**,
new H2 §"Managing test hosts via the CLI" placed directly after
the existing §"Configuring `extra_hosts`" (lines 21–52) and
before §"The `--insecure` flag" (now line 123). 69 lines added.

Subsection structure (4 sub-blocks, terse tone matching the
`targets` chapter):

1. **Lead paragraph + command listing** — names the four
   subcommands in a code block matching the
   `roksbnkctl targets list/add/remove/show` shape in chapter 15.
   Cites the persistence path ("same workspace marshaller `targets
   add` uses") so operators who already know the YAML field
   `test.connectivity.extra_hosts` see the backing-field
   connection. Names the Sprint 21 argv strictness contract
   (`cobra.NoArgs` on `list`/`clear`, `cobra.MinimumNArgs(1)` on
   `add`/`remove`).
2. **Worked example** — bash session showing the exact sequence
   the prompt called for: empty `list` (zero bytes + exit 0), `add
   https://docs.f5.com`, repeated `add` showing "already present:"
   idempotence, populated `list`, then `roksbnkctl test
   connectivity` producing a PASS. Output is explicitly marked as
   **illustrative** with a blockquote note that tech-writer's
   drift sweep will re-capture byte-for-byte against the built
   binary before the `v1.7.1` cut — pins the shape (empty-list
   exit 0 contract, "already present:" log on idempotent add) so
   wording deltas don't invalidate the prose.
3. **`--output json`** — shape note for the JSON form (empty list
   `[]`, populated list preserves insertion order). One-line
   bash + JSON example.
4. **`clear`** — the confirmation prompt defaults to No with
   `--auto` to skip; explicitly names the pattern parity with
   `roksbnkctl down` / `roksbnkctl cluster down` (not `targets
   remove`, which has no prompt). Two bash sessions: an
   interactive confirm and the `--auto` form.
5. **Scope of the CLI** — names what's **not** managed by `test
   hosts` (the `test.dns.*` and `test.throughput.*` config fields,
   which have flag-driven equivalents) per the sprint README's
   tight-scope decision. Flags a broader `test config` surface as
   a future-sprint candidate. Names the YAML-comment preservation
   caveat ("best-effort and depends on the workspace round-trip")
   so operators with hand-written comments in `config.yaml` know
   the trade-off without it blocking the sprint.

The subsection's tone is practical and terse — names the
recovery (the operator's path out of the "no hosts configured"
error) without restating the design rationale (which lives in
`issues/issue_sprint24_staff.md`).

### Edit 2 — cross-link from §"Configuring `extra_hosts`"

**Site**: `book/src/20-connectivity-testing.md`, line 38 (the
existing §"Configuring `extra_hosts`" prose, immediately after
the YAML-schema paragraph). One-sentence pointer added:

> You don't have to hand-edit the YAML to populate this list —
> `roksbnkctl test hosts {list,add,remove,clear}` reads and
> writes the same slice. See [§ Managing test hosts via the
> CLI](#managing-test-hosts-via-the-cli) below.

Catches the top-down reader who lands on §"Configuring
`extra_hosts`" first and sends them to the CLI subsection without
making them scroll through the rest of the chapter to find it.

### Edit 3 — cross-link from chapter 20 §"Cross-references"

**Site**: `book/src/20-connectivity-testing.md`, line 293 (new
bullet in the existing §"Cross-references" block). Added:

> [Chapter 15 — SSH targets](./15-ssh-targets.md#roksbnkctl-targets--full-reference) — the `roksbnkctl targets` ergonomic precedent that `roksbnkctl test hosts` mirrors.

Names the precedent so a reader curious about why the four
subcommands look the way they do can read the chapter 15
reference for the full `targets` surface.

### Edit 4 — cross-link from chapter 21 §"Integration with `extra_hosts`"

**Site**: `book/src/21-dns-testing-gslb.md`, line 466 (new
paragraph in the existing §"Integration with `extra_hosts`"
block, between the `extra_hosts` fallback paragraph and the
"Chapter 20 covers `connectivity.extra_hosts` in full" sentence).
Added:

> If the no-flag invocation exits with `no hosts configured to
> probe; add via roksbnkctl test hosts add <url>`, the slice is
> empty for this workspace. Populate it with `roksbnkctl test
> hosts add <url>` (see [Chapter 20 §"Managing test hosts via
> the CLI"](./20-connectivity-testing.md#managing-test-hosts-via-the-cli))
> instead of hand-editing `config.yaml`.

Quotes the literal error message string (`no hosts configured to
probe; add via roksbnkctl test hosts add <url>`) — the post-Sprint-24
version of the `internal/cli/test.go:803` error per the sprint
README integrator decision 4. An operator who hits the error
from `roksbnkctl test dns` (no-flag form) and searches the book
for the error string finds this paragraph and the cross-link.

### Edit 5 — cross-link from chapter 21 §"Cross-references"

**Site**: `book/src/21-dns-testing-gslb.md`, line 556 (new bullet
in the existing §"Cross-references" block, directly below the
existing Chapter 20 entry). Added:

> [Chapter 20 §"Managing test hosts via the CLI"](./20-connectivity-testing.md#managing-test-hosts-via-the-cli) — `roksbnkctl test hosts {list,add,remove,clear}`, the CLI path for populating the `extra_hosts` slice the no-flag `test dns` invocation walks.

A second cross-reference path for readers who hit the chapter 21
references first.

### Discipline checks

- Only `book/src/20-connectivity-testing.md` and
  `book/src/21-dns-testing-gslb.md` touched.
- No edits to `book/src/SUMMARY.md` (it lists only chapter
  titles, no sub-bullets — Task 3 of the architect prompt
  verified the case where no edit is needed).
- No edits to `internal/cli/test.go` (staff-territory — including
  the `:803` error-message update; tech-writer's drift sweep
  verifies the binary's stdout/stderr matches the chapter post-edit).
- No edits to `internal/`, `cmd/`, `.github/workflows/`,
  `Makefile`, `CONTRIBUTING.md`, `docs/PRD/`, `terraform/` — all
  out of scope per the architect prompt's "Critical constraints".
- No edits to `book/src/12-workspace-config.md` — the chapter-12
  §"test:" schema reference is unchanged; `test.connectivity.extra_hosts`
  is the same `[]string` field, just with a new CLI on top.
- No edits to `book/src/27-command-reference.md` or
  `book/src/28-configuration-reference.md` — those will be
  swept by tech-writer against the built binary; the architect
  subsection in chapter 20 is the human-readable prose.
- No edits to `book/src/23-e2e-test-plan.md` — the existing
  Phase L-Connectivity prose at line 127 still names
  `test.connectivity.extra_hosts` correctly; the new CLI is an
  alternate path to the same slice, not a behavior change.
- Worked-example output is **illustrative** and explicitly marked
  as such — tech-writer captures byte-for-byte output against
  the built binary in the GREEN-verdict re-capture before the
  `v1.7.1` cut.
- No commits; no `gh issue create`; no tag proposal. Sprint 24
  release-cut is integrator-owned post-tech-writer's GREEN.

### Follow-up candidates flagged for the integrator

1. **Tech-writer worked-example re-capture.** The example
   bash session in the new §"Managing test hosts via the CLI"
   subsection is illustrative — the shape is pinned (empty-list
   exit 0; "already present:" idempotent log) but the exact
   log-line wording staff lands may differ minorly. Tech-writer's
   sweep against the built binary (per
   `prompts/sprint24/README.md` per-role-scope table item 1) is
   the authoritative GREEN-verdict re-capture. Two paths: (a) if
   wording matches, leave the prose; (b) if minor deltas, swap
   the verbatim output in place and drop the "illustrative"
   blockquote note.
2. **`27-command-reference.md` + `28-configuration-reference.md`.**
   Both auto-generated chapters need the new `test hosts` command
   group entries when the binary lands. Tech-writer's surface
   per the sprint README's per-role-scope table; not architect's.
   Architect verified neither needs hand-editing today — the
   chapter-20 subsection is the operator-facing prose, the
   command-reference chapters are reflected from the binary.
3. **Future-sprint candidate: `test config` surface.** The
   sprint README integrator decision 1 confines this sprint to
   `test.connectivity.extra_hosts` only. A broader `roksbnkctl
   test config {get,set}` surface covering `test.dns.{default_target,resolvers}`
   and `test.throughput.*` would close the asymmetry for the
   other config fields that today have only flag-driven
   equivalents. Architect flags this as a low-priority candidate
   for a future UX sprint — not a Sprint 24 issue.
4. **Future-sprint candidate: comment-preserving YAML
   marshaller.** The subsection's "Scope of the CLI" block names
   the comment-preservation caveat (best-effort, marshaller-dependent).
   The validator's hermetic test suite per the sprint README
   makes comment-preservation optional (decision 6 in the
   tight-scope set: "if the existing marshaller doesn't preserve
   YAML comments, document the limitation in your closure rather
   than blocking the sprint"). A future sprint could audit the
   workspace marshaller's round-trip fidelity and either fix it
   in `internal/config` or document the trade-off more
   thoroughly — neither is required for `v1.7.1`.
5. **Demo.sh follow-up.** Per
   `issues/issue_sprint24_staff.md` §"Related" closing bullet,
   the demo script's `test dns` / `test connectivity` lines
   currently exit with the "no hosts configured" error until the
   operator hand-edits YAML. Post-Sprint-24, the demo can
   include `roksbnkctl test hosts add ...` lines in its setup
   block, removing one manual prep step. Out-of-scope for the
   architect closure here but worth flagging for the integrator
   to schedule as a small demo.sh-cleanup follow-up after the
   `v1.7.1` cut (e.g. as part of the next demo re-verify cycle).

---

## Related

- `prompts/sprint24/architect.md` — this issue's source prompt.
- `prompts/sprint24/README.md` — integrator decisions
  (tight-scope on `test.connectivity.extra_hosts`; mirror the
  `targets` ergonomic; staff updates `:803` error message; the
  tech-writer covers Sprint 23 round-2 + Sprint 24 in ONE pass
  and unblocks `v1.7.1`).
- `issues/issue_sprint24_staff.md` — full CLI surface design
  the architect prose documents.
- `book/src/15-ssh-targets.md` §"`roksbnkctl targets` — full
  reference" (lines 190–263) — the ergonomic precedent the new
  chapter-20 subsection mirrors.
- `book/src/20-connectivity-testing.md` §"Configuring
  `extra_hosts`" (lines 21–52) — the existing YAML-schema prose
  the new CLI subsection sits adjacent to.
- `book/src/21-dns-testing-gslb.md` §"Integration with
  `extra_hosts`" (lines 454–468 pre-edit) — the no-flag `test
  dns` fallback path that hits the same "no hosts configured"
  error and now cross-links to the CLI.
