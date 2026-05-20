---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: book — seven intra-chapter anchor cross-refs resolve to nothing (silent dead links)'
labels: []
assignees: ''
---

## Symptom

Seven `[...](./NN-chapter.md#anchor)` cross-references inside `book/src/*.md` point at anchors that do not exist on the target chapter. The links render as plain underlined text in the published HTML and PDF — clicking them scrolls to the top of the target chapter instead of the named subsection, so the reader silently lands in the wrong place. `mdbook build` does not flag intra-chapter anchors (only file existence), so `.github/workflows/book.yml` has never caught any of these.

Concretely (paths relative to `book/src/`, line numbers from the current tip of `main`):

| file:line | href | target chapter has anchor? |
|---|---|---|
| `08-cluster-phase.md:5` | `./10-deploying-bnk-trials.md#the-bnk-up--bnk-down-command-group` | no — heading slugifies to `the-bnk-up-bnk-down-command-group` (single dash) |
| `08-cluster-phase.md:243` | `./10-deploying-bnk-trials.md#worked-example--iterating-on-a-bnk-trial` | no — heading slugifies to `worked-example-iterating-on-a-bnk-trial` |
| `11-tearing-down.md:3` | `./10-deploying-bnk-trials.md#the-bnk-up--bnk-down-command-group` | no — same dup-dash slug bug as row 1 |
| `25-cos-supply-chain.md:242` | `./12-workspace-config.md#cos--cos-supply-chain-optional` | no — heading slugifies to `cos-cos-supply-chain-optional` |
| `30-glossary.md:8` | `./14-credentials-resolver.md#source-3--workspace-api_key_b64` | no — heading slugifies to `source-3-workspace-api_key_b64` |
| `30-glossary.md:43` | `./22-throughput-testing.md#--mode-east-west` | no — heading slugifies to `mode-east-west` (mdBook strips leading punctuation) |
| `30-glossary.md:114` | `./22-throughput-testing.md#--mode-north-south` | no — heading slugifies to `mode-north-south` |

All seven are the same shape: the author typed the link target by hand and matched the heading's em-dash with a double `--`, but mdBook's GitHub-style slugifier collapses adjacent dashes (and trims leading punctuation like `--mode-…`). The reproduction below builds the book, opens chapter 30 § glossary entry for `--mode east-west`, clicks the `mode east-west` link, and lands at the top of chapter 22 rather than the §"`--mode east-west`" subsection.

## Reproduction

```
# 1. build the HTML book on the same toolchain CI uses
mdbook build book/

# 2. open the rendered glossary in a browser
xdg-open book/book/html/30-glossary.html
# (or curl + grep — see below)

# 3. inspect the href the renderer produced for the "east-west" link
curl -fsS file://$PWD/book/book/html/30-glossary.html \
  | grep -o 'href="22-throughput-testing.html#[^"]*"' \
  | head -1
# expected (post-fix): href="22-throughput-testing.html#mode-east-west"
# actual:               href="22-throughput-testing.html#--mode-east-west"

# 4. confirm the anchor that href points at does not exist in chapter 22
curl -fsS file://$PWD/book/book/html/22-throughput-testing.html \
  | grep -E 'id="(--mode-east-west|mode-east-west)"'
# only the second form is in the HTML — the link is dead.

# 5. one-shot grep across the whole src tree to enumerate all seven
python3 -c '
import re, os, glob
def slug(s):
    s = re.sub(r"[^\w\s-]", "", s.strip().lower())
    return re.sub(r"\s+", "-", s).strip("-")
anchors = {}
for f in glob.glob("book/src/*.md"):
    a = set()
    for line in open(f):
        m = re.match(r"^#{1,6}\s+(.+?)\s*$", line)
        if m: a.add(slug(m.group(1)))
    anchors[os.path.basename(f)] = a
rx = re.compile(r"\[[^\]]+\]\(\.\/([^)#]+\.md)#([^)]+)\)")
bad = []
for f in glob.glob("book/src/*.md"):
    for ln, line in enumerate(open(f), 1):
        for m in rx.finditer(line):
            if m.group(2) not in anchors.get(m.group(1), set()):
                bad.append((os.path.basename(f), ln, m.group(1), m.group(2)))
for b in bad: print(b)
print("count:", len(bad))
'
# count: 7 on the current tip; the fix flips count to 0.
```

## Expected behavior

All seven cross-refs resolve to the intended subsection in the published HTML and PDF (browser scrolls to the named heading). The Python probe in step 5 of the reproduction reports `count: 0`. A new CI gate makes a regression of this class fail `book.yml` (see Issue ND on intra-MD anchor validation — out of scope here; this issue is purely the seven existing dead links).

## Actual behavior

Each of the seven links renders with the wrong `href`. Clicking scrolls to the top of the target chapter; the reader has to skim the chapter to find the subsection they were sent to (in glossary cases — entries for terms like `--mode east-west` — that is the entire glossary entry's payload). The PDF build has the same defect: `mdbook-pandoc` consumes the same Markdown source, and `xelatex` follows the same slug rules.

## Environment

- `roksbnkctl version`: (n/a — book defect)
- OS / arch: (n/a — reproduces on every host that can run `mdbook build`)
- IBM Cloud region: (n/a)
- Backend: (n/a)
- mdBook version: latest (matches `.github/workflows/book.yml`'s `peaceiris/actions-mdbook@v2 → mdbook-version: 'latest'`)

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** authors typed the slugs by hand and double-encoded em-dashes from the source headings as `--`. mdBook's GitHub-style slugifier collapses runs of dashes to one and strips leading punctuation, so every `--`-in-anchor form is born broken. Found by enumerating `[...](./*.md#anchor)` refs and comparing against each file's slugified headings (see step 5).
2. Less likely: a slugifier rules change between mdBook versions. The collapse-runs-of-dashes rule is stable across recent mdBook majors per `CommonMark` + GitHub conventions — these are author-side typos, not toolchain drift.

## Acceptance criteria

1. The Python probe in step 5 of the reproduction prints `count: 0` on the fixed tree (run via `make book-test` or `python3 -c ...` against `book/src/`).
2. Each of the seven `href`s in the rendered HTML — `08-cluster-phase.html`, `11-tearing-down.html`, `25-cos-supply-chain.html`, `30-glossary.html` — points at an anchor that exists on the target page (verified with the `curl + grep id=` recipe from steps 3–4).
3. `mdbook build book/` exits 0 with no new warnings vs the pre-fix baseline (the fix is markdown-only — chapters' headings are not renamed; only the link text's anchor fragment is corrected).
4. Regression check: a `make book-anchor-check` target (or the Python probe inlined into a `book.yml` step) is added in this issue's PR or in the companion CI-gate issue, so the next dead anchor fails the build rather than ships silently. If the companion CI-gate issue lands first, this issue's PR adds no new make/CI surface — only the markdown fixes.

## Out of scope (deliberately)

- Adding the intra-MD anchor validator to `book.yml`. Tracked as the companion CI-gate feature issue; landing it here couples a content fix with a workflow change for no reason.
- Renaming any chapter heading. The fix is to correct the *link's anchor fragment*, not the headings — every author-facing slug change risks breaking external bookmarks / search-engine indices.
- Auditing absolute external `https://` links in the book (different defect class; `markdown-link-check` territory).

## Notes

- The probe in step 5 is the same one the architect used to find these seven; keep it as the canonical dead-anchor enumerator until/unless `book.yml` grows a first-class checker.
- The two `--mode-...` glossary entries in `30-glossary.md` are the highest-visibility offender — the glossary is the single chapter most likely to be reached by search, and `--mode east-west` / `--mode north-south` are user-facing flag names readers will look up first when configuring throughput tests.
