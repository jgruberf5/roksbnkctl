# Sprint 21 — architect issues (argv strictness docs)

> **Sprint 21 frame.** First regular work sprint post-`v1.6.4`.
> Architect ships the one-paragraph addition to the
> first-run / command-line chapter, regens the auto-generated
> CLI reference if any `Args:` change surfaces, and sweeps the
> existing book for stuck-together-shorthand examples that the
> new strictness contract would break.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Doc the parser strictness contract; sweep stale stuck-together examples

**Severity**: medium (the contract is a small change but
operator-facing; a stale example in the book would confuse
any operator who tried to follow it)
**Status**: resolved (pending integrator gate)

### Motivation

Staff Issue 1 adds parser strictness — short flags accept
`-f value` or `-f=value`; the stuck-together `-fvalue` form
is rejected. The book must (a) name the rule once in the
first-run chapter so operators know about it, (b) include the
verbatim error text the binary produces so the error feels
familiar when an operator hits it in the wild, and (c) survive
a cross-chapter sweep that catches any existing example that
demonstrates the now-rejected form.

### Tasks

1. **Find the first-run / command-line chapter** in
   `book/src/SUMMARY.md` (likely `book/src/03-quick-tour.md`;
   verify the actual structure).
2. **Add one short paragraph** that:
   - Names the strictness contract: short flags accept
     `-f value` (space) or `-f=value` (equals); the
     stuck-together form is rejected.
   - Cross-links the long-flag forms: `--workspace value` or
     `--workspace=value`.
   - Quotes the verbatim error the binary produces for the
     typo `-ws canada-roks`. Run the binary to capture this
     exactly; do NOT paraphrase from the staff issue spec.
   - Names the recovery succinctly.
3. **Regen `book/src/27-command-reference.md`** if staff's
   `Args: cobra.NoArgs` audit changes any per-command Usage
   text. Invocation:
   `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`.
4. **Cross-chapter sweep** — grep the existing book for any
   short-flag-value usage in stuck-together form:
   `grep -rn '\-[a-zA-Z][a-zA-Z]' book/src/` (rough — refine
   per false-positive shape). Each hit must be rewritten to
   space-separated form. If no hits, document "none found" in
   the closure.

### Acceptance criteria

1. The first-run chapter carries the new paragraph; the error
   text inside it matches the binary's verbatim output (NOT
   paraphrased from the spec).
2. `book/src/27-command-reference.md` is regen'd against the
   post-staff cobra tree.
3. No stuck-together short-flag-value example survives in the
   book (or the sweep result is documented as "none found").
4. No `docs/PRD/` touched; no `CHANGELOG.md` touched
   (integrator-owned at cut).

### Out of scope

- `internal/`, `cmd/`. That's staff's surface.
- Restructuring the first-run chapter beyond the new
  paragraph. Drift-sweep / additive only.

### Files affected

- `book/src/03-quick-tour.md` (or the actual first-run chapter
  per `SUMMARY.md`) — one new paragraph.
- `book/src/27-command-reference.md` — auto-regen.
- Possibly other `book/src/*.md` per the cross-chapter sweep
  (if any stuck-together example is found).

### Related

- Sprint 21 staff Issue 1 — the code-side contract this docs
  surface introduces.
- `argv-strictness-prevents-resource-damage` memory — the
  user-priority context that motivates surfacing this in the
  book (so operators understand the rule, not just hit the
  error).

### Closure — architect, 2026-05-21

**First-run chapter located**: `book/src/07-quick-start.md`
(per `book/src/SUMMARY.md` — "Quick start: from API key to
deployed BNK", the chapter where the operator first runs the
binary). No standalone first-run / command-line-basics chapter
exists in the SUMMARY structure; Step 2 of the quick-start
(the `roksbnkctl init` walkthrough that already mentions
`-w <name>`) is the natural anchor for the strictness rule.

**Paragraph inserted**: `book/src/07-quick-start.md:59–71`
(13 lines, blockquote-formatted callout immediately after the
Step 2 "Initialises a workspace under `~/.roksbnkctl/default/`…"
intro paragraph and before the `roksbnkctl init` code fence).
Names the contract for short flags (`-w value` / `-w=value`),
cross-links the long-flag forms (`--workspace value` /
`--workspace=value`), enumerates the value-requiring shorts
the rule applies to (`-w`, `-f`, `-n`, `-c`, `-l`, `-o`),
quotes the verbatim binary stderr for the `-ws canada-roks`
typo inside a fenced code block, and closes with the recovery
in concrete terms ("re-run with `--workspace canada-roks` (or
`-w canada-roks`)"). Tone matches the rest of the book —
terse, practical, names the recovery.

**Verbatim binary error text** (captured 2026-05-21):

Command run (from repo root, stdin redirected from
`/dev/null` to suppress any TTY prompt):

```
go run ./cmd/roksbnkctl init -ws canada-roks --var-file ./terraform.tfvars </dev/null
```

Stderr captured:

```
roksbnkctl: "-ws" is not a recognised flag (looks like a stuck-together short-flag-value, which this binary does not accept).
Use one of these forms instead:
  -w s              (short, space)
  -w=s              (short, equals)
  --workspace s     (long, space)
  --workspace=s     (long, equals)
Did you mean '--workspace s'?
```

Process exit: `exit status 2`. The text quoted in the
paragraph at `07-quick-start.md:62–68` matches this verbatim
(line-for-line, including the punctuation, the two-space
indent on the four shape lines, and the `'--workspace s'`
quoting around the "did you mean" suggestion). Staff's
preflight derives the "did you mean" from the typo's tail
characters against the binary's registered long-flag set, so
`-ws canada-roks` yields `'--workspace s'` (the suffix `s`
became the suggested value); when the operator hits this in
the wild against their real argv the suggestion will reflect
their actual typo, not necessarily `s`.

**`book/src/27-command-reference.md` regen**: yes (re-run).

Invocation (from repo root):

```
go run ./tools/refgen/cobra-md > book/src/27-command-reference.md
```

`diff` against the pre-regen copy: **zero lines changed**
(byte-identical; same `md5sum` `bee798f5857c0b5d9fd46287d942c2fa`,
1317 lines). Staff's `Args: cobra.NoArgs` audit does not
affect refgen output — the tool renders cobra `Use` /
`Short` / `Long` / flag-table content, not the `Args:`
constraint shape, so 32 newly-pinned `NoArgs` commands +
14 preserved positional constraints produce the same markdown
they did pre-staff. Regen was run anyway to satisfy the
acceptance-criterion "regen'd against the post-staff cobra
tree"; the resulting file is on-disk and current. No
follow-up tech-writer action needed on chapter 27.

**Cross-chapter sweep result**: **none found**.

Sweep methodology:

1. Enumerated the binary's actual short flags via `grep -nE
   'BoolVarP|StringVarP|IntVarP|DurationVarP|StringSliceVarP|
   Float64VarP' internal/cli/*.go cmd/**/*.go` — universe is
   `{A, c, f, i, l, n, o, q, t, v, w}`.
2. Filtered to value-requiring (non-bool) shorts only —
   `{c, f, l, n, o, w}`. `-A`, `-i`, `-q`, `-t`, `-v` are
   booleans; bool-stacking (e.g. `-it`, `-vvv`) and bare
   bool shorthand are explicitly preserved by staff's
   preflight (`NoOptDefVal != ""` is the skip criterion) and
   therefore remain canonical in the book.
3. Greppped `book/src/` for any `roksbnkctl` invocation
   carrying a `-X<suffix>` form where `X ∈ {c, f, l, n, o, w}`
   and `<suffix>[0]` is `[a-zA-Z]` (not `=`, not a space, not
   `-`, not `/`).

Hit triage:

| Match | Verdict |
|-------|---------|
| `book/src/24-day-2-ops.md:319` `roksbnkctl k exec flo-controller-abc123 -n f5-bnk -- ls -la /` | False positive — `-n` carries `f5-bnk` in canonical space form; the regex matched `-la` which is `ls`'s bool-stack, not roksbnkctl flags |
| `book/src/27-command-reference.md:704` `roksbnkctl k exec my-pod -it -- bash` | False positive — `-i` and `-t` are bools in `k exec` (verified at `internal/cli/k_exec.go:47–49` — `BoolVarP(&kExecStdin, "stdin", "i", …)` and `BoolVarP(&kExecTTY, "tty", "t", …)`). `-it` is legal bool-stacking |
| `book/src/04-installation.md:140` and `book/src/31-building-from-source.md:55` | False positives — both are `go build -ldflags`, a Go toolchain flag, not a roksbnkctl flag |
| All other matches (`~/.roksbnkctl/<dirname>-...` paths, pod names, hyphenated cluster names) | False positives — paths/identifiers, not flags |

No `roksbnkctl` invocation in the book uses a stuck-together
value-flag form (`-w<name>`, `-f<path>`, `-n<ns>`, `-c<container>`,
`-l<level>`, `-o<output>`). The canonical space and equals
forms are used uniformly. No book edits beyond the
quick-start paragraph were required.

**Files touched this closure**:

- `book/src/07-quick-start.md` — +13 lines (the new
  blockquote paragraph at lines 59–71). No other content
  altered.
- `book/src/27-command-reference.md` — regen'd, byte-identical
  output (zero net diff).

**Discipline checks**:

- Touched only `book/src/*` (specifically `07-quick-start.md`
  and the regen of `27-command-reference.md`).
- No edits to `internal/`, `cmd/`, `docs/PRD/`, or
  `CHANGELOG.md`.
- No commits, no `git push`, no `gh` invocations.
- The paragraph's quoted error text matches the binary's
  actual stderr verbatim (captured by running the binary
  against the typo argv; not paraphrased from the staff spec).
- Cross-chapter sweep produced zero hits; no other book
  files were modified.
