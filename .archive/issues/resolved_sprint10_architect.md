# Sprint 10 — architect issues (resolved)

All architect-surface deliverables landed across three remediation passes. 18 resolved, 3 wontfix, 0 open at sprint close.

## Pass 1 — primary sprint deliverables

Issues 1-10: chapter 19 partial-closure admonition removal, smoke-test un-guarding, chapter 24 per-shape status samples, four Sprint-9-deferred polish items, CHANGELOG `v1.3.0` entry under `## Unreleased (v1.x)`. Issue 6 (chapter 14 §"What's new in v1.2" section position) marked `wontfix` per Sprint 10 scope. Issue 10 (PRD 04/06/PLAN.md refinement) closed `no changes needed` — no design gap surfaced from staff or validator during pass 1.

## Pass 2 — tech-writer remediation

Issues 11-18: chapter 24 TF source drift fixed (`embedded@v1.3.0` → real binary outputs), chapter 19 retry-failure stderr text quoted verbatim, CHANGELOG integration-test execution gate bullet added under `### Changed`, plus four post-tag polish items (chapter 24 cross-links to chapter 10/11, CHANGELOG four-vs-five framing, chapter 24 dual `Cluster:` callout, chapter 24 intro framing). Issue 17 (PRD 04 §"Resolved in Sprint 10" companion section) and Issue 18 (chapter 19 §"5" YAML expansion) marked `wontfix` — deferred to v1.4 chapter-polish pass.

## Pass 3 — validator remediation

Issues 19-21: chapter 19 `ops show` profile NAME → Profile-uuid shape, chapter 24 LegacySingle `(<age> ago)` suffix, `--trusted-profile-id` flag references swept from chapter 19 + PLAN.md + CHANGELOG `### Changed` bullet (replaced with `--cr-token` + `--profile` shape per validator's blocker Issue 1).

## Verdict

`mdbook build book/` clean across all three passes. No new findings against staff or validator surface during any pass. Validator's live re-verify against `canada-roks` sandbox returned GREEN on first attempt — CHANGELOG `### Changed` "in-pod wrap is trusted-profile-aware" claim substantiated.
