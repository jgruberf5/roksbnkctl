# Sprint 16 — tech-writer issues (consolidation phase-1b, post-v1.6.0)

> **Sprint 16 frame.** Light, read-only. Dispatched after
> staff/architect/validator. Internal-only refactor → no user-visible /
> doc / book surface; the job is a drift sweep (CHANGELOG ↔ as-landed
> code ↔ `docs/PLAN.md` §"Sprint 16") + a GREEN/RED launch verdict.
> Only write surface: this ledger. See `prompts/sprint16/tech-writer.md`.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — drift sweep + launch verdict (light read-only cycle)

`Status: resolved` — recorded by the integrator (light read-only role, consolidation tier; no separate tech-writer agent dispatched, per `prompts/sprint16/README.md`).

**Drift sweep — clean.** Internal-only refactor → no user-visible / doc / book / PRD surface. The three surfaces that exist agree:

- **CHANGELOG `## Unreleased` `### Changed` ↔ as-landed code:** "no user-visible behavior change" is true and **test-backed** — validator Issue 1 (integrator-run) recorded zero pre-existing test-file diffs vs `v1.6.0`, full hermetic `go test -race ./...` green across all 14 packages, Sprint 14 `--on` + Sprint 15 chokepoint guards green & byte-unedited, `orchestration`↛`cli` boundary clean. The bullet's specifics (which symbols moved, the function-field DI) match `issues/issue_sprint16_staff.md` §Closure and the as-landed `internal/orchestration/{lifecycle,cluster}.go`.
- **CHANGELOG ↔ `docs/PLAN.md` §"Sprint 16":** consistent (phase-1b = the deferred Sprint-15 bulk move; remaining ~27 files = tracked phase-2).
- **No book/PRD** (correct for a decomposition) — nothing to drift; `mdbook` a no-op by construction.

**Dogfooding:** N/A — zero user-facing change by design; the "no behavior change" claim is the gate itself and it passed.

**Launch verdict: GREEN** for the integrator-owned tag (`v1.6.1` strict-SemVer or `v1.7.0`). All four Sprint 16 ledgers terminal (staff resolved, validator resolved, architect resolved, tech-writer resolved). Tag/version designation is integrator-owned at cut.

---

## Issue 2 follow-up — doc/example review

> Light, read-only doc-drift sweep of the integrated Issue 2 phase-handoff
> fix (HEAD `27f7a02`, diff `6839b65..27f7a02`). Scope: CHANGELOG `### Fixed`,
> `docs/PLAN.md` follow-up note, `docs/E2E_TEST.md` §"Phase-handoff
> regression (Issue 2)", `scripts/e2e-phase-handoff.sh` comments, and the
> Go doc-comments — for overclaim, terminology/version consistency,
> key-leak/safety drift, and cross-link integrity. Read-only; this ledger
> is the only write surface; no commit.

### Finding 1 — CHANGELOG/PLAN cross-link uses a section label, not a resolvable anchor

**Severity**: low (cosmetic / pre-existing house convention)
**Status: accepted** — matches the established CHANGELOG linking convention across all prior entries; not drift. Closed without doc change per `resolved_sprint16_tech-writer.md`.
**Description.** The new `v1.6.2` CHANGELOG block and the PLAN follow-up
note both link "PLAN.md §"Sprint 16"" / Issue 2 as bare file links
(`docs/PLAN.md`, `issues/issue_sprint16_validator.md`,
`../issues/issue_sprint16_validator.md`) with a `§"Sprint 16"` text label
rather than a GitHub heading-slug fragment (`#sprint-16--consolidation-…`).
All targets resolve as files (verified: `docs/PLAN.md`,
`issues/issue_sprint16_validator.md` exist; PLAN→issues uses the correct
`../issues/` relative prefix; PLAN anchor `## Sprint 16 …` exists at
PLAN.md:1074; the E2E_TEST section `## Phase-handoff regression (Issue 2)`
exists). The reader lands on the right file but not the exact section.
**Suggested fix.** None required for launch — this is the established
CHANGELOG convention on every prior entry (`§"Sprint 15"`, `§"Sprint 13"`,
etc. all use the same bare-file + label form), so it is consistent, not
drift. Optionally a future polish pass could add real `#…` fragments
repo-wide; out of scope here.

### Finding 2 — `create_roks_transit_gateway` reuse toggle is emitted into tfvars but its second-phase effect is via module.testing's data lookup, not a root→roks_cluster reuse passthrough (doc could mislead a code reader)

**Severity**: low (doc-precision; the code behavior is correct and the
Go doc-comment already explains it)
**Status: accepted** — the authoritative Go doc-comment (`tf.RenderTFVarsWithClusterOutputs`, retained at `0db0ad6`) and the Issue 2 closure precisely explain the TG handling; CHANGELOG intentionally user-facing (no module-internal detail). Closed without doc change per `resolved_sprint16_tech-writer.md`.
**Description.** The CHANGELOG bullet says the second phase "reuses the
already-created cluster VPC, transit gateway, and client VPC" and lists
`use_existing_cluster_vpc` + `existing_cluster_vpc_id` +
`testing_create_client_vpc=false` as the wired toggles (TG omitted from
the bullet's explicit toggle list — accurate, not overclaimed). The
`RenderTFVarsWithClusterOutputs` doc-comment correctly and explicitly
documents the asymmetry: the cluster submodule has *no* existing-TG data
lookup, so the second phase sets `create_roks_transit_gateway = false`
and `module.testing` finds the gateway by name via
`data.ibm_tg_gateway.transit_gateway` (confirmed present at
`terraform/modules/testing/main.tf:421`). This is internally consistent
and not an overclaim. The only residual: unlike
`use_existing_cluster_vpc`/`existing_cluster_vpc_id` (newly threaded
root→roks_cluster→cluster, verified in `terraform/main.tf` +
`modules/roks_cluster/{main,variables}.tf`), `create_roks_transit_gateway`
already existed as a root var and is *not* a new passthrough — a reader
skimming only the CHANGELOG might assume symmetric new wiring.
**Suggested fix.** No code/doc change required for launch; the
authoritative Go doc-comment and Issue 2 closure section both spell out
the TG-via-name-lookup mechanism correctly. If a future doc pass wants
extra precision, the CHANGELOG could add a half-sentence that TG reuse is
"second phase does not manage the gateway; module.testing looks it up by
name" — nice-to-have, not a launch blocker.

### Key-leak / safety review — clean

No drift. Verified across the whole integrated change set:
- No doc, script comment, or example echoes, hardcodes, or scrapes the
  IBM Cloud API key. `scripts/e2e-phase-handoff.sh` requires
  `IBMCLOUD_API_KEY` from the env, has a `redact()` belt-and-braces
  filter, explicitly refuses to scrape the key from `$TFVARS`, and never
  prints `./terraform.tfvars` contents (header comment + preflight +
  `loadReuseClusterOutputs` boundary all state "structure only / never
  printed").
- The hermetic test `internal/tf/secondphase_handoff_test.go` asserts
  `api_key` does **not** leak into the rendered second-phase tfvars.
- `docs/E2E_TEST.md` §"Phase-handoff regression" explicitly marks the
  driver real-spend (≈$5-8, ≈70+ min), opt-in, self-tearing-down, and
  **NOT a CI job** (no `.github/workflows`, no `workflow_dispatch`) —
  matching README decision 2.
- `.gitignore` additions (`/terraform.tfvars` already present;
  `.terraform/`, `*.tfstate*` added) reduce, not increase, leak risk.

### Overclaim review — clean

The CHANGELOG `### Fixed` wording, the PLAN follow-up note, and the
E2E_TEST section all describe what the code/driver actually does and
**do not imply Issue 2 is verified or resolved**. The CHANGELOG/PLAN
explicitly frame closure (and the `v1.6.2` tag) as gated on the live `!`
run per `live-verify-high-issues`. The E2E_TEST section ends with "A red
A2/A3/A4 means the phase handoff is still broken — Issue 2 stays open."
Terminology ("phase"/"second (bnk/testing) phase"/"cluster-outputs.json"/
"handoff"/"duplicate-name") is consistent with Issue 2, the staff/
validator closure sections, and PLAN. Version framing is consistent:
`v1.6.2` patch, `### Fixed` (not `### Changed`), placed above `v1.6.1`,
integrator-owned/live-gated.

### GREEN / RED launch verdict

**GREEN** — the documentation of the Issue 2 phase-handoff fix is
consistent, correctly cross-linked, key-leak-clean, and non-overclaiming;
the two findings are low-severity cosmetic/precision notes, neither a
launch blocker. GREEN here means *the docs are sound* — it does **not**
mean Issue 2 is closed; that remains the integrator/operator's live `!`
verify call per README decision 3 and `live-verify-high-issues`.
