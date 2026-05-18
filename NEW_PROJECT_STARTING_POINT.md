# Starting a new project — PRFAQ → parallel market + engineering tracks → multi-agent sprints

A repeatable starting playbook for new projects that follow the same pattern this repo uses: an Amazon-style **PRFAQ** as the ignition document, fanning out into a **market-research track** and an **engineering track** that run in parallel, converge at a go/no-go gate, and then feed the existing four-agent sprint workflow described in [`agents/README.md`](./agents/README.md).

This doc is the *meta-template*. It tells you which artifacts to create, in which order, and which agents own each artifact. The artifact contents are project-specific; the shape is not.

> **Read this with:**
> - [`agents/README.md`](./agents/README.md) — the engineering four-agent pattern (architect / staff / validator / tech-writer).
> - [`docs/PRD.md`](./docs/PRD.md) and [`docs/prd/`](./docs/prd/) — example PRD shape used in this repo.
> - [`docs/PLAN.md`](./docs/PLAN.md) — example phased plan that sequences PRDs into sprints.
> - [`prompts/README.md`](./prompts/README.md) — how per-sprint prompts are written and dispatched.

## The shape at a glance

```
┌──────────────────────────────────────────────────────────────────────┐
│ Phase 0  —  PRFAQ.md (single author, ~3-5 pages)                     │
│            The product as the customer will eventually see it,       │
│            written as a press release with a FAQ. The ignition doc.  │
└──────────────────────────────────────────────────────────────────────┘
                                │
                                │ fan out into two parallel tracks
                                ▼
┌──────────────────────────────┐  ┌─────────────────────────────────┐
│ Phase 1a — MARKET track       │  │ Phase 1b — ENGINEERING track   │
│ Four parallel market agents:  │  │ Four parallel engineering      │
│ - market-analyst (CAGR/TAM)   │  │ pre-sprint agents (Sprint -1): │
│ - competitive-analyst         │  │ - architect → PRDs, PLAN       │
│ - pricing-strategist          │  │ - staff → tech spikes, ADRs    │
│ - gtm-reviewer (read-only)    │  │ - validator → risk + test plan │
│                               │  │ - tech-writer (read-only)      │
│ Output: docs/market/*.md +    │  │                                 │
│ MARKET_BRIEF.md (synthesis)   │  │ Output: docs/prd/*.md, PRD.md, │
│                               │  │ docs/PLAN.md, ADR set          │
└──────────────────────────────┘  └─────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Phase 2 — Convergence: GO / NO-GO / RESHAPE                          │
│   Integrator reconciles MARKET_BRIEF + engineering PRDs/PLAN:        │
│   - Market sizing supports the engineering investment?               │
│   - Pricing model aligns with feature tiers in the PRDs?             │
│   - Competitive gaps reorder PLAN.md priority?                       │
│   Output: PRFAQ.md v2 (locked), GO_NO_GO.md decision memo            │
└──────────────────────────────────────────────────────────────────────┘
                                │
                                ▼
┌──────────────────────────────────────────────────────────────────────┐
│ Phase 3 — Sprint 0 onward (the existing four-agent pattern kicks in) │
│   See agents/README.md and prompts/README.md                         │
└──────────────────────────────────────────────────────────────────────┘
```

The two-track fanout is the same parallel-fanout pattern used per sprint, applied at the project-genesis level. Market and engineering agents work against disjoint file surfaces (`docs/market/` vs. `docs/prd/`), file issues into their respective issue files, and an integrator folds the aggregate at convergence.

---

## Phase 0 — the PRFAQ

A **PRFAQ** is a single document written by the product owner (a human, sometimes paired with one LLM) that combines:

1. **Press Release** (1-2 pages, future-dated to the day the product launches): headline, sub-headline, customer quote, the problem, the solution, a second customer quote, a "how to get started" line, a spokesperson quote.
2. **Internal FAQ** (2-3 pages): the questions a skeptical exec, a customer, and an engineer would each ask — answered honestly.
3. **External FAQ** (1 page): the questions the customer-facing docs will eventually answer.

The PRFAQ is *not* a spec. It deliberately commits to **outcome statements** ("a customer can do X in under 5 minutes") and leaves the implementation open. It's the source-of-truth that the market and engineering tracks both ground against.

### File

`PRFAQ.md` at repo root. Frontmatter:

```yaml
---
status: draft | locked
version: v0.1
author: <owner>
launch-date: <target>
---
```

### Skeleton

```markdown
# PRFAQ — <product name>

## Press Release (future-dated <YYYY-MM-DD>)

### Headline
<One sentence the press would print.>

### Sub-headline
<Audience + outcome in one sentence.>

### Body
<150-300 words. The problem in the user's voice, the solution in the
customer's voice, no engineering jargon.>

> "<Customer quote that names the pain and the relief.>"
> — <Name>, <Title>, <Company>

### How to get started
<One paragraph. URL, command, signup, etc.>

> "<Spokesperson quote that names the strategic reason this exists.>"
> — <Name>, <Title>, <Our org>

## Internal FAQ

**Why now?** ...
**What's the smallest possible v1?** ...
**Who's the buyer vs. the user?** ...
**What does success look like at 6 months / 18 months?** ...
**What kills this project?** ...
**What if a competitor ships first?** ...
**What's the tech risk?** ...
**What's the ops cost?** ...

## External FAQ

**Q: How is this different from <competitor>?** ...
**Q: How do I get started?** ...
**Q: What does it cost?** ...
**Q: What integrates with it?** ...
**Q: Is my data safe?** ...
```

### Done criteria for Phase 0

- Press Release reads like a real press release, not a feature list.
- A skeptical exec can read the Internal FAQ and predict your answer to the next question they'd ask.
- The pricing question is **not** answered yet — that's what the market track decides.
- The architecture question is **not** answered yet — that's what the engineering track decides.

---

## Phase 1a — Market research track

The market track has the same four-role shape as an engineering sprint: three parallel authoring agents against disjoint surfaces, one read-only reviewer at the end, an integrator at the close.

### The four market roles

| Role | Surface | Output artifact(s) |
|---|---|---|
| [`market-analyst`](#suggested-agentmarket-analystmd) | Market sizing, CAGR, TAM / SAM / SOM, adoption curves, macro trends | `docs/market/01-MARKET-SIZE-AND-CAGR.md` |
| [`competitive-analyst`](#suggested-agentcompetitive-analystmd) | Competitive landscape, feature matrix, win/loss patterns, differentiation map | `docs/market/02-COMPETITIVE-LANDSCAPE.md` |
| [`pricing-strategist`](#suggested-agentpricing-strategistmd) | Pricing model, packaging tiers, willingness-to-pay, comparison to alternatives | `docs/market/03-PRICING-AND-PACKAGING.md` |
| [`gtm-reviewer`](#suggested-agentgtm-reviewermd) | Read-only synthesis: drift check across the three, persona + JTBD alignment to PRFAQ, go/no-go verdict | `docs/market/MARKET_BRIEF.md` + `issues/issue_market_gtm-reviewer.md` |

The three authoring agents (analyst / competitive / pricing) run **in parallel** against disjoint files. The gtm-reviewer runs **after** the three finish, synthesizes their outputs against the PRFAQ, and writes the consolidated brief.

### Surface boundaries

- **market-analyst** owns `docs/market/01-*.md` only. Off-limits: `02-*.md`, `03-*.md`, PRFAQ, anything under `docs/prd/`.
- **competitive-analyst** owns `docs/market/02-*.md` only. Off-limits: `01-*.md`, `03-*.md`, PRFAQ, anything under `docs/prd/`.
- **pricing-strategist** owns `docs/market/03-*.md` only. Off-limits: `01-*.md`, `02-*.md`, PRFAQ, anything under `docs/prd/`.
- **gtm-reviewer** is **read-only** except for `MARKET_BRIEF.md` and its own issue file.

Boundaries are hard. If an analyst wants to challenge a sibling's assumption, file an issue — don't edit their file.

### Suggested `agents/market-analyst.md`

A new agent role file in `agents/` modeled after the existing `architect.md`. Skeleton:

```markdown
# market-analyst — role definition

You size the market: TAM / SAM / SOM, CAGR over a 3-5 year horizon,
adoption curve stage, macro trends shaping demand. You ground every
number to a citable source. You write `docs/market/01-MARKET-SIZE-AND-CAGR.md`.

## When to use
- During Phase 1a of a new project, in parallel with competitive-analyst,
  pricing-strategist, gtm-reviewer.
- During PRFAQ refreshes where market shape may have moved.

## Inputs you'll receive
- The PRFAQ at repo root.
- A read-first list (analyst reports, public filings, industry datasets).
- The prior market brief if this is a refresh.

## What you produce
- `docs/market/01-MARKET-SIZE-AND-CAGR.md` (~300-600 lines):
  - Definitions: what counts as "the market" for this product
  - TAM / SAM / SOM with numeric estimates and confidence intervals
  - CAGR 3yr / 5yr with source citations
  - Adoption-curve stage (innovator / early-adopter / early-majority / etc.)
  - Macro trends: regulatory, technological, economic forces
  - Key uncertainties — what would invalidate the sizing
- `issues/issue_market_market-analyst.md` — one entry per finding
  the integrator should know (data gaps, conflicting sources, assumption flags)

## How you ground
- Every percentage / dollar figure cites a source (URL, report name, page).
- Where two reputable sources disagree, present both with the gap explained.
- Where you have to estimate, label it `[estimate, rationale: ...]`.
- Never invent numbers to round out a table — leave a `[gap: need source for X]` marker.

## Verification before reporting done
- Every claim has a citation or `[estimate]` tag.
- TAM ≥ SAM ≥ SOM numerically.
- CAGR signs (positive/negative) match the adoption-curve narrative.
- No claim contradicts the PRFAQ's stated problem.

## Final report (under 200 words)
Lines written, citations included, gaps flagged, integrator-attention items.

Do NOT commit. Do NOT edit files outside your scope.
```

### Suggested `agents/competitive-analyst.md`

```markdown
# competitive-analyst — role definition

You map the competitive landscape: who else solves this problem, how, at what
price, with what gaps. You write `docs/market/02-COMPETITIVE-LANDSCAPE.md`.

## What you produce
- Competitor list: direct + indirect + adjacent
- Per-competitor profile: product, pricing model, target buyer, strengths, weaknesses, recent moves
- Feature-coverage matrix: rows = features from the PRFAQ, columns = competitors, cells = present / partial / absent / unknown
- Differentiation map: where this product wins, ties, loses today
- Watch list: competitors most likely to leapfrog the proposed v1

## How you ground
- Public sources only unless the read-first list authorizes proprietary data
- Distinguish "ships today" from "announced / roadmap" — never conflate
- Label each cell in the feature matrix with a citation (URL or doc reference)

## Verification before reporting done
- Every competitor has at least one source cited
- Feature matrix has no `?` cells without an explanation in the watch list
- "Where we lose today" section is not empty (if it is, you didn't try hard enough)
```

### Suggested `agents/pricing-strategist.md`

```markdown
# pricing-strategist — role definition

You design the pricing and packaging model: tiers, metering dimensions,
willingness-to-pay, comparison to alternatives. You write
`docs/market/03-PRICING-AND-PACKAGING.md`.

## What you produce
- Pricing model: subscription / usage / hybrid / perpetual / freemium, with rationale
- Tier definitions: what's in each tier (Free / Team / Enterprise or analogous)
- Metering dimensions: per-seat, per-call, per-GB, per-cluster, etc.
- Anchor pricing: numbers, with the customer-value math that justifies them
- Comparison: how the proposed price compares to the competitor pricing competitive-analyst found
- Pricing-elasticity hypotheses: what happens at -25% / +25% / +100%
- Land/expand path: how a customer grows their spend over 12-24 months

## How you ground
- Anchor every dollar figure to either a competitor data point or a
  value-math calculation (e.g., "cost-of-incumbent − cost-of-us = customer ROI")
- Mark every figure as `[anchored to <competitor>]`, `[value-math]`, or `[estimate]`
- If a tier feature depends on the PRD set, name the dependency

## Verification before reporting done
- Every tier has a clear "who buys this" persona statement
- The Free / entry tier doesn't cost more to serve than its acquisition
  value (or you flag that it does and why it's worth it)
- Anchor pricing reconciles with the competitive landscape document
- Pricing tiers align to features the PRFAQ promised (no tier sells
  vapor; no tier hides a feature the PRFAQ said was core)
```

### Suggested `agents/gtm-reviewer.md`

```markdown
# gtm-reviewer — role definition

You are the end-of-phase read-only reviewer for the market track. You
synthesize the three authored documents into `MARKET_BRIEF.md`, surface
drift between them, and emit a go / no-go / reshape verdict.

## Inputs
- PRFAQ.md (the source of truth for what's being sized)
- docs/market/01-MARKET-SIZE-AND-CAGR.md (market-analyst's output)
- docs/market/02-COMPETITIVE-LANDSCAPE.md (competitive-analyst's output)
- docs/market/03-PRICING-AND-PACKAGING.md (pricing-strategist's output)
- The three sibling issue files

## What you produce
- `docs/market/MARKET_BRIEF.md` — the single document a product owner reads
  to brief themselves on the market in under 15 minutes. Sections:
  - One-paragraph TL;DR
  - Sizing snapshot (TAM/SAM/SOM, CAGR)
  - Competitive snapshot (top-3 threats, top differentiator)
  - Pricing snapshot (proposed model + anchor + tier names)
  - Top 5 risks (with the source document for each)
  - Verdict: go / no-go / reshape — with the reshape ask if applicable
- `issues/issue_market_gtm-reviewer.md` — one issue per finding the
  integrator must reconcile (severity: blocker / high / medium / low)

## Drift checks
- Does the competitive matrix's "we win on X" align with the pricing model's
  premium tier rationale?
- Does the TAM number support the pricing × customer-count math implied by
  the pricing tiers?
- Does any document contradict a claim in the PRFAQ?

## Verdict criteria
- **go** — sizing supports investment, competitive position is defensible,
  pricing model is internally consistent
- **reshape** — one or more of the above is off, but a clear fix exists
  (e.g., "narrow ICP to <segment>, sizing then supports", "drop tier X,
  margins don't work")
- **no-go** — sizing collapses below an investment threshold, or the
  competitive position is structurally weak

You do NOT edit `01-*.md`, `02-*.md`, `03-*.md`, or PRFAQ.md. Read-only
except for MARKET_BRIEF.md and your own issue file.
```

### Issue tracking for the market track

Files at `issues/issue_market_<role>.md`. Same severity ladder as the engineering pattern:

- **blocker** — track cannot complete, e.g., no defensible TAM source
- **high** — material finding the integrator must reconcile
- **medium** — improvement that would strengthen the brief
- **low** — note for next refresh
- **roadmap** — out of scope but worth remembering

---

## Phase 1b — Engineering pre-sprint track ("Sprint -1")

The engineering track produces the spec artifacts that make Sprint 0 dispatchable. It uses the **same four-role pattern as a regular sprint** but with retargeted scope: no implementation yet, just the spec scaffolding.

### The four engineering pre-sprint roles

| Role | Output artifact(s) | Notes |
|---|---|---|
| `architect` | `docs/PRD.md` (top-level), `docs/prd/00-OVERVIEW.md`, per-phase `docs/prd/0N-*.md`, `docs/PLAN.md` skeleton | The PRD set is the engineering analog of the market brief |
| `staff` | `docs/spikes/` — short technical-feasibility writeups; `docs/adr/` — architecture decision records for the load-bearing choices | One spike per "we don't know if X is feasible" question raised by the PRFAQ |
| `validator` | `docs/RISK_AND_TEST_PLAN.md` — what could go wrong, how the test surface will be shaped, what regressions a future sprint must guard | Shapes the test surface before code exists |
| `tech-writer` | `issues/issue_sprint-minus-1_tech-writer.md` — read-only review of the above three | Same end-of-phase pattern as in a normal sprint |

### Surface boundaries

- **architect** owns `docs/PRD.md`, `docs/prd/*.md`, `docs/PLAN.md`. Off-limits: `docs/spikes/`, `docs/adr/`, `docs/RISK_AND_TEST_PLAN.md`.
- **staff** owns `docs/spikes/*.md`, `docs/adr/*.md`. Off-limits: PRD files, PLAN, risk plan.
- **validator** owns `docs/RISK_AND_TEST_PLAN.md`. Off-limits: PRDs, PLAN, spikes, ADRs.
- **tech-writer** is read-only; edits only its own issue file.

### The PRD set (architect's output)

Mirror the existing repo's shape:

- `docs/PRD.md` — single top-level "what we're building and why" document (~400-800 lines). Sections: TL;DR, Problem, Goals, Non-goals, Target users, UX (the happy path written in concrete commands or screens), Functional requirements, Open questions.
- `docs/prd/00-OVERVIEW.md` — roadmap index: what phases exist, what they trade off, dependency graph.
- `docs/prd/01-*.md` through `0N-*.md` — one PRD per phase. Each is self-contained: motivation, scope, design, acceptance criteria, out-of-scope.

See [`docs/PRD.md`](./docs/PRD.md) and [`docs/prd/00-OVERVIEW.md`](./docs/prd/00-OVERVIEW.md) for examples of the level of detail expected.

### The PLAN.md (architect's output)

Sequences the PRDs into sprints. See [`docs/PLAN.md`](./docs/PLAN.md). Minimum sections:

- Milestone table (which tag delivers what)
- Phase-sequencing ASCII diagram (which sprint depends on which)
- Per-sprint section: goal, scope, off-limits, deliverables, parallel-agent dispatch summary, exit criteria

### The spikes (staff's output)

A *spike* is a 50-150 line technical-feasibility writeup that answers a single question with high confidence:

- "Can we use library X for Y, given constraint Z?"
- "What's the cold-start latency of this approach?"
- "Does this library support the OS matrix we promised?"

Spikes are written before implementation. They sometimes include throwaway code, executed locally, with results pasted in. If a spike says *no*, the PRD owning that question gets reshaped.

File at `docs/spikes/<NN>-<slug>.md`.

### The ADRs (staff's output)

One **Architecture Decision Record** per load-bearing choice: language, primary framework, persistence layer, distribution model, etc. Each ADR is short (~50-100 lines):

```markdown
# ADR-NNN: <Title>

**Status**: proposed | accepted | superseded by ADR-MMM
**Date**: <YYYY-MM-DD>
**Deciders**: <names>

## Context
<What problem motivates this decision; what constraints apply.>

## Decision
<The choice. One paragraph.>

## Consequences
- Positive: <...>
- Negative: <...>
- Neutral: <...>

## Alternatives considered
- <Alt 1>: why rejected
- <Alt 2>: why rejected
```

File at `docs/adr/NNNN-<slug>.md`.

### The risk and test plan (validator's output)

`docs/RISK_AND_TEST_PLAN.md`. Sections:

- **Risk register** — risks ranked by likelihood × impact, with owner and mitigation
- **Test-surface plan** — what unit, integration, and e2e tests will look like; what fixtures need to exist; what CI matrix to run on
- **Regression-gate criteria** — what a "green sprint" looks like at the gate level (build clean, test clean, lint clean, e2e DRY_RUN clean)
- **Failure-mode catalogue** — for each PRD phase, what's the worst-case failure and how is it detected

---

## Phase 2 — Convergence (the go/no-go gate)

After Phase 1a and 1b finish, an **integrator** (human, optionally aided by an LLM) does the cross-track reconciliation. Output:

- **`PRFAQ.md` updated to v2** — reflects any market-track learnings (pricing, positioning, ICP narrowing) and engineering-track constraints (scope realism, dependency sequencing). Mark `status: locked`.
- **`GO_NO_GO.md`** — single-page decision memo. Sections:
  - Verdict (go / reshape / no-go) and rationale
  - Market preconditions met (cite MARKET_BRIEF)
  - Engineering preconditions met (cite PRD + PLAN)
  - Constraints inherited by Sprint 0 (e.g., "v1 ships before competitor X is GA in <month>")
  - Sign-offs

### Reconciliation questions the integrator answers

| Question | Source documents |
|---|---|
| Does the market sizing support the engineering investment? | MARKET_BRIEF + PLAN.md (sprint count → effort estimate) |
| Does the pricing model align with the proposed tier features? | Pricing doc + PRD set |
| Do competitive gaps reorder PLAN.md priority? | Competitive doc + PLAN.md |
| Are technical feasibility spikes consistent with PRFAQ promises? | Spikes + PRFAQ |
| Are risks identified by validator already mitigated in PLAN? | RISK_AND_TEST_PLAN + PLAN.md |
| Is anything in the PRFAQ now known to be infeasible / too expensive / too late? | All of the above |

If any answer is "no," the integrator either rewrites the PRFAQ (and a delta sprint of the affected track runs again) or declares no-go.

### Done criteria for Phase 2

- PRFAQ v2 is locked (no edits without a new convergence pass).
- GO_NO_GO.md is signed.
- Sprint 0's task brief can be written without any "TBD" left in the PRD set.

---

## Phase 3 — Sprint 0 and onward

Once the gate is green, the existing four-agent engineering sprint workflow takes over. See:

- [`agents/README.md`](./agents/README.md) — the four engineering roles in detail
- [`prompts/README.md`](./prompts/README.md) — how to write per-sprint task briefs
- [`docs/PLAN.md`](./docs/PLAN.md) — example sprint sequencing

The market track typically does **not** run every sprint. Schedule market-track refreshes at milestone boundaries (after a release tag) or when a triggering event happens — competitor announcement, regulatory change, large customer ask that smells like an ICP shift.

When a refresh runs, it's the same four market roles against the same surface, plus a fresh `GO_NO_GO_Mn.md` if anything material changed.

---

## Tiering the sprint process by change size

The four-agent sprint workflow (architect / staff / validator / tech-writer, each producing a cross-referenced issue ledger, plus end-of-cycle drift sweeps and re-review passes) is **fixed-cost per sprint**. That cost is well-spent on a feature/PRD cycle. It is *not* well-spent on a two-bug patch — and if you run full ceremony on every cycle regardless of size, the overhead becomes the dominant cost as the codebase matures and changes get smaller. The observed failure mode (real, from this repo's own history): a patch cycle that shipped a ~50-line helper generated ~2,000 lines of four-agent ledger and spanned four calendar days, while early greenfield feature cycles shipped far more in a single day. Sprints feel "slower and slower" — but the engineering didn't slow; the ceremony-to-change ratio inverted.

**Rule: ceremony must be proportionate to change size. Pick the tier at sprint kickoff, in the `prompts/sprint-N/README.md` dispatch.**

| Tier | When | Agents that run | Ledger depth | Gate |
|---|---|---|---|---|
| **Patch** | 1–2 bugs, no design surface, no book change | staff (fix) + validator (regression sweep only) | one issue each, no drift-sweep tables, no re-review pass | regression sweep green; bug reproduces pre-fix |
| **Minor / feature** | new PRD-backed capability | all four | full ledgers + one end-of-cycle drift sweep | the standard PRD gate |
| **Consolidation** | refactor / debt-paydown / test-infra, **no** behavior change | staff + validator, *light* architect/tech-writer (no PRD, no book) | staff + validator full; architect/tech-writer one-line "no surface" record | behavior-parity (pre-existing suite green **unchanged**) |
| **Greenfield** | Sprint 0, or a new top-level subsystem | all four | full | as PLAN.md defines |

Two structural cautions that the tiering does not by itself fix — fold these into the architect's pre-sprint PRDs so they don't accrue:

- **Root-cause recurring bug *classes*, don't patch instances per sprint.** If the same defect shape recurs across cycles (e.g. "a value correct in one context is consumed in another"), a patch-tier sprint that fixes the third instance is the wrong call — escalate to a consolidation-tier sprint that installs one chokepoint and retires the class. Recurring patch sprints on one defect family is the signal.
- **Keep an end-to-end / integration test that exercises the real cross-component path.** If high-severity defects are first found by a human running the real workflow rather than by the validator gate, the gate has an integration blind spot; close it in a consolidation sprint. Human live-testing is the slowest feedback loop there is.

A worked example of a consolidation-tier sprint (root-causing a recurring bug class + closing an integration blind spot + a phased god-package split, run at the consolidation tier) is `docs/PLAN.md` §"Sprint 14" in this repo — use it as the shape for the equivalent cycle in the new project.

---

## Suggested repo layout

For a new project starting from scratch, the directory shape that supports the above:

```
<project>/
├── PRFAQ.md                              # Phase 0
├── GO_NO_GO.md                           # Phase 2 (and per-milestone refreshes)
├── README.md                             # written by the integrator after Phase 2
├── agents/                               # role definitions
│   ├── market-analyst.md                 # market-track roles
│   ├── competitive-analyst.md
│   ├── pricing-strategist.md
│   ├── gtm-reviewer.md
│   ├── architect.md                      # engineering roles
│   ├── staff.md
│   ├── validator.md
│   └── tech-writer.md
├── docs/
│   ├── market/                           # Phase 1a output
│   │   ├── 00-OVERVIEW.md
│   │   ├── 01-MARKET-SIZE-AND-CAGR.md
│   │   ├── 02-COMPETITIVE-LANDSCAPE.md
│   │   ├── 03-PRICING-AND-PACKAGING.md
│   │   ├── 04-PERSONAS-AND-JTBD.md       # optional, often subsumed by 01 or 02
│   │   └── MARKET_BRIEF.md               # gtm-reviewer synthesis
│   ├── PRD.md                            # Phase 1b — top-level
│   ├── prd/                              # Phase 1b — per-phase PRDs
│   │   ├── 00-OVERVIEW.md
│   │   └── 0N-<phase>.md
│   ├── PLAN.md                           # Phase 1b — sprint sequencing
│   ├── spikes/                           # Phase 1b — technical spikes
│   │   └── 0N-<question>.md
│   ├── adr/                              # Phase 1b — architecture decisions
│   │   └── NNNN-<slug>.md
│   └── RISK_AND_TEST_PLAN.md             # Phase 1b — validator output
├── prompts/                              # per-sprint task briefs (from Sprint 0 onward)
│   ├── market-phase-1/                   # the dispatch briefs that ran the market agents
│   │   ├── market-analyst.md
│   │   ├── competitive-analyst.md
│   │   ├── pricing-strategist.md
│   │   └── gtm-reviewer.md
│   ├── sprint-minus-1/                   # the dispatch briefs for the engineering pre-sprint
│   │   ├── architect.md
│   │   ├── staff.md
│   │   ├── validator.md
│   │   └── tech-writer.md
│   └── sprint-N/                         # ongoing sprints
├── issues/                               # per-dispatch issue files (audit trail)
│   ├── issue_market_market-analyst.md
│   ├── issue_market_competitive-analyst.md
│   ├── issue_market_pricing-strategist.md
│   ├── issue_market_gtm-reviewer.md
│   ├── issue_sprint-minus-1_architect.md
│   └── ...
└── CLAUDE.md / AGENTS.md                 # project conventions (written by integrator after Phase 2)
```

The structure deliberately mirrors this repo's existing shape so the engineering pattern in `agents/` + `prompts/` + `issues/` extends naturally upward into the project-genesis phases.

---

## Artifact summary — what to produce, in what order

| # | Artifact | Owner | Phase | Reads | Feeds |
|---|---|---|---|---|---|
| 1 | `PRFAQ.md` | Product owner (human + LLM pair) | 0 | external research, customer interviews | Phases 1a + 1b |
| 2 | `docs/market/01-MARKET-SIZE-AND-CAGR.md` | market-analyst | 1a | PRFAQ, analyst reports | MARKET_BRIEF |
| 3 | `docs/market/02-COMPETITIVE-LANDSCAPE.md` | competitive-analyst | 1a | PRFAQ, competitor public surface | MARKET_BRIEF |
| 4 | `docs/market/03-PRICING-AND-PACKAGING.md` | pricing-strategist | 1a | PRFAQ, doc 03, willingness-to-pay data | MARKET_BRIEF |
| 5 | `docs/market/MARKET_BRIEF.md` | gtm-reviewer | 1a | docs 02-04, PRFAQ | Phase 2 |
| 6 | `docs/PRD.md` + `docs/prd/0N-*.md` | architect (pre-sprint) | 1b | PRFAQ | PLAN, Phase 2 |
| 7 | `docs/PLAN.md` | architect (pre-sprint) | 1b | PRD set | Sprint 0 dispatch |
| 8 | `docs/spikes/0N-*.md` | staff (pre-sprint) | 1b | PRFAQ, ADRs | PRDs (may reshape them) |
| 9 | `docs/adr/NNNN-*.md` | staff (pre-sprint) | 1b | spikes, PRDs | every later sprint |
| 10 | `docs/RISK_AND_TEST_PLAN.md` | validator (pre-sprint) | 1b | PRDs, ADRs | PLAN, every later sprint |
| 11 | `issues/issue_sprint-minus-1_tech-writer.md` | tech-writer (pre-sprint) | 1b | all of 6-10 | Phase 2 |
| 12 | `PRFAQ.md` v2 (locked) | integrator | 2 | 1, 5, 6-11 | Sprint 0 |
| 13 | `GO_NO_GO.md` | integrator | 2 | 1, 5, 6-11 | release decision |

---

## Dispatching the market agents — example invocation

Mirroring the per-sprint dispatch pattern, here's the kickoff prompt for the market track (file as `prompts/market-phase-1/README.md`):

```markdown
# Market Phase 1 — dispatch

**Theme:** Initial market research for <product>

Carry-overs: none (first run).

Four-agent dispatch:

- **market-analyst** — `docs/market/01-MARKET-SIZE-AND-CAGR.md`. Size the
  global market for <category>, CAGR 3yr / 5yr, adoption-curve stage.
- **competitive-analyst** — `docs/market/02-COMPETITIVE-LANDSCAPE.md`.
  Identify and profile the top 8-12 competitors / substitutes.
- **pricing-strategist** — `docs/market/03-PRICING-AND-PACKAGING.md`.
  Propose pricing model + tiers + anchor numbers.
- **gtm-reviewer** — runs after the three above. Produces
  `docs/market/MARKET_BRIEF.md` and the read-only review issue.

The integrator folds the four outputs into the Phase 2 GO_NO_GO memo.
```

Per-agent task briefs live at `prompts/market-phase-1/<role>.md` and follow the same template shape as the engineering per-sprint briefs documented in [`agents/README.md`](./agents/README.md#writing-a-sprint-task-brief).

---

## When to deviate from this template

- **Solo prototype / hobby project** — skip Phase 1a entirely; the market is "you," the pricing is free, and the PRFAQ collapses to a README.
- **Internal tool** — keep Phase 1a but replace pricing-strategist with an `internal-economics` role that does cost-to-serve and team-time-saved math instead of revenue modeling.
- **Research / academic project** — replace the market track with a literature-survey track (same four-role shape: surveyor / gap-analyst / methodology-reviewer / synthesis-reviewer).
- **Open-source library** — drop pricing-strategist; keep the other three. The "buyer" is the adopting maintainer, and the "competitive landscape" is the alternative-library survey that adoption decisions hinge on.

The pattern that generalizes is:

- **One role per disjoint surface** the phase has to touch
- **One read-only end-of-phase role** to synthesize and gate-check
- **Issue files as the hand-off contract** between roles and between phases
- **A separate integrator step** that locks the upstream artifact (PRFAQ, GO_NO_GO) before the next phase dispatches

---

## Cross-reference

This document is the *project genesis* extension of the per-sprint pattern in [`agents/README.md`](./agents/README.md). Read both together when starting a new project. Once Sprint 0 kicks off, `agents/README.md` is the live reference; this doc retires until the next milestone-boundary refresh.
