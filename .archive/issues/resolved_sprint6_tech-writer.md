# Sprint 6 — tech writer issues, resolution notes

Twelve issues filed: 1 blocker (chapter 23 non-existent flag), 6 medium, 4 low, 1 roadmap. **All 11 actionable issues resolved this pass**; Issue 12 (PRD 05 step-number refresh) deferred to Sprint 7 polish per the tech-writer's own roadmap recommendation. The v1.0 release-narrative blocker is cleared.

## Issue 1 (BLOCKER — chapter 23 `--use-existing-cluster` flag) — resolved by integrator

The flag doesn't exist in `scripts/e2e-test-backends.sh`. Per the tech-writer's option 1 ("drop the flag from chapter 23, smaller surface for v1.0 polish"), removed three references:

- `book/src/23-e2e-test-plan.md` line 49 — replaced the documented invocation with a bare `./scripts/e2e-test-backends.sh` + a one-line note explaining the implicit "live cluster required" contract.
- Line 77 — same fix on the resuming-a-partial-run example.
- Line 236 — same fix on the §"Re-runnability" example.

Verified post-edit: `grep -n "use-existing-cluster" book/src/23-e2e-test-plan.md` returns zero hits.

**Status**: ✅ resolved (v1.0 release-narrative unblocked)

## Issue 2 (MEDIUM — chapter 23 per-phase log path) — resolved by integrator

Rewrote chapter 23 §"Per-phase logs" → §"Run logs". The actual driver writes a single combined log per run (`/tmp/roksbnkctl-e2e-backends/run-<ts>.log` and the equivalent for the baseline driver), not per-phase split files. Documented the actual emission shape; noted per-phase splitting as a v1.x consideration. Dropped the "success deletes everything but the last summary line" claim — logs are preserved on both success and failure today.

**Status**: ✅ resolved (chapter 23 ↔ `scripts/e2e-test-backends.sh::log_file` consistent)

## Issue 3 (MEDIUM — chapter 23 Phase J row in backends tier) — resolved by integrator

Phase J is manual (requires `sudo mv kubectl $KUBECTL_PATH.hidden`) — not run by the automated driver. Three-part fix:

1. Top-of-chapter prose: "14 phases organised into two tiers" → "14 automated phases organised into two tiers, plus Phase J as a manual integrator step".
2. Tier table: added a third **Manual** tier row for J; backends tier row now lists `I, K, L, L-DNS, M, N` (no J).
3. At-a-glance phase table: Phase J row's "Tier" column flipped from `backends` to **`manual`** with a "requires `sudo mv`" annotation.
4. Replaced the "Phases I-N share the cluster brought up by Phase D" sentence (which described the PRD-envisioned cluster-sharing design that v1.0 doesn't implement) with a Phase J integrator-step explainer pointing at `docs/E2E_TEST.md`.

**Status**: ✅ resolved (chapter 23 ↔ shipped driver consistent)

## Issue 4 (MEDIUM — chapter 26 non-existent `--refresh-kubeconfig` flags) — resolved by integrator

Two non-existent flags removed; one stale hedge cleared:

- Line 49: `roksbnkctl init --refresh-kubeconfig` and the "once that command lands" hedge → `roksbnkctl kubeconfig --download -w <workspace>` (the command exists today per chapter 27).
- Line 221: `roksbnkctl cluster register <name> --refresh-kubeconfig` → `roksbnkctl kubeconfig --download --cluster <name>` (the `--cluster` flag on `kubeconfig` exists and handles exactly this case).

**Status**: ✅ resolved (chapter 26 ↔ shipped CLI surface consistent)

## Issue 5 (MEDIUM — chapter 13 + MIGRATING.md wrong `terraform.tfvars.user` path) — resolved by integrator

Bulk find-and-replace `state/terraform.tfvars.user` → `terraform.tfvars.user` across chapter 13 (4 references) + MIGRATING.md (1 reference). MIGRATING.md's workspace-layout diagram (lines 110-122) also corrected — `terraform.tfvars.user` now appears as a sibling of `config.yaml` (workspace root), not under `state/`. Chapter 28 stays as the source-of-truth that matches `internal/tf/terraform.go::UserTFVarsPath`.

**Status**: ✅ resolved (chapter 13 + MIGRATING.md ↔ chapter 28 ↔ `internal/tf/terraform.go::UserTFVarsPath` consistent)

## Issue 6 (LOW-medium — chapter 23 chapter-16 cross-link) — resolved by integrator

Updated the link to include the correct anchor: `[Chapter 16 §"Auto-discovery from \`roksbnkctl up\`"](./16-on-flag-ssh-jumphosts.md#auto-discovery-from-roksbnkctl-up)`. Anchor slug matches mdbook's GFM derivation from the actual header text.

**Status**: ✅ resolved

## Issue 7 (MEDIUM — chapter 27 auto-generator omits `Aliases`) — resolved by integrator

Extended `tools/refgen/cobra-md/main.go::renderCommand` to emit a `**Aliases**: \`<name>\`` callout under the synopsis line when `cmd.Aliases` is non-empty. Re-ran `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`; the regenerated chapter now shows alias callouts for `workspaces` (`ws`) and any other aliased commands. Verified via `grep -c '^\*\*Aliases\*\*:' book/src/27-command-reference.md` (2 entries).

The generator's smoke test (`tools/refgen/cobra-md/main_test.go`) still passes — the assertion is on the output's well-formedness, not on which commands have aliases.

**Status**: ✅ resolved (generator + regenerated chapter 27 both committed)

## Issue 8 (LOW — chapter 23 combined-runner narrative) — resolved by integrator

Rewrote chapter 23's top-of-section paragraph to match the actual `scripts/e2e-test-full.sh` design: the baseline driver runs A-H to completion (including Phase H's destroy), then the backends driver provisions a fresh cluster via Phase N's mixed-mode-lifecycle step. Wall-time estimate bumped from ~4-6h to ~5-7h to account for the second cluster apply. Cluster-sharing across drivers documented as a v1.x queued item with a PRD 05 cross-link.

**Status**: ✅ resolved (chapter 23 ↔ `scripts/e2e-test-full.sh` consistent)

## Issue 9 (LOW — chapter 23 stale validator-agent reference) — resolved by integrator

Replaced the `See the validator agent's e2e CI workflow file (landed in Sprint 6) for the concrete YAML` placeholder with a direct link to [`.github/workflows/e2e-full.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/e2e-full.yml), including the dispatch-input names (`cluster_region`, `teardown_on_success`) and the release-branch auto-trigger note.

**Status**: ✅ resolved

## Issue 10 (LOW — chapter 32 misnamed `internal/exec/registry.go` + `Register` signature + `toolPackages` type) — resolved by integrator

Three corrections:

- `internal/exec/registry.go` → `internal/exec/backend.go` (the actual file housing `ResolveBackend`).
- `Register(name string, factory func() Backend)` → `Register(name string, b Backend)` (the actual production signature; backend is passed pre-constructed, not as a factory).
- `toolPackages` example revised from `map[string][]string` to the actual `map[string]toolPackage` struct shape with an explanatory comment ("see internal/exec/ssh.go for the full struct shape").

**Status**: ✅ resolved (chapter 32 ↔ `internal/exec/{backend,ssh}.go` consistent)

## Issue 11 (LOW — MIGRATING.md introduces v0.10 label nothing else uses) — resolved by integrator

Per the tech-writer's option 1, dropped the "v0.10" label. MIGRATING.md §"From roksbnkctl v0.7 / v0.8 → v0.9 → v0.10" relabelled to "→ v1.0"; subsection `### v0.10 (current — Sprint 6)` relabelled to `### Sprint 6 (v1.0 prep — pre-tag)`. Body content stays. CHANGELOG.md and PLAN.md framing (v0.9 → Sprint 6 work → v1.0 tag at end of Sprint 7) is now consistent across all artefacts.

**Status**: ✅ resolved (MIGRATING.md ↔ CHANGELOG.md ↔ PLAN.md consistent on version labels)

## Issue 12 (ROADMAP — PRD 05 §I + §N step-number drift) — accepted (deferred to Sprint 7 polish)

PRD 05 §"Phase I" defines I0-I7 (8 steps); shipped driver implements I0-I11 (12). PRD 05 §"Phase N" defines N0-N10 (11 steps); shipped driver implements N1-N6 (6 steps, restructured). Per chapter 32 §"The PRD process" ("when implementation diverges from PRD, update the PRD"), Sprint 7 should refresh PRD 05 §I + §N to match the shipped step matrix. Adding the explicit task to Sprint 7's polish list — captured below.

**Status**: ⏸ accepted (queued for Sprint 7 PRD-refresh polish task)

## Sprint 7 polish carry-over

One item rolls forward to Sprint 7 from this resolved pass: **refresh PRD 05 §"Phase I" and §"Phase N" step matrices to match the shipped `scripts/e2e-test-backends.sh` implementation** (Issue 12 above). All other Sprint 6 polish items closed this pass.

## Integrator additions

- Re-verified `go build ./...`, `go vet ./...`, `gofmt -d -l .`, `go test ./...` all green post-fixes (incl. the regenerated chapter 27, the extended cobra-md generator, and its smoke test).
- Re-ran `DRY_RUN=1 ./scripts/e2e-test-backends.sh` and `DRY_RUN=1 ./scripts/e2e-test-full.sh` post-chapter-23 edits — both still green; the chapter edits didn't introduce any script changes that the dry-runs could regress.

## Summary

12 issues filed; 11 resolved this pass (blocker + 6 medium + 4 low + 1 generator extension); 1 deferred to Sprint 7's polish window (PRD 05 §I/§N step-number refresh). Build, vet, gofmt, full test suite (incl. cobra-md generator smoke test) all green. Both DRY_RUN walkthroughs of the e2e drivers still emit cleanly.

**Sprint 6 gate criteria from PLAN.md §"Gate to Sprint 7" — verdict**: **all four criteria met.** The Issue 1 blocker that the tech-writer flagged is cleared in this resolved pass; the other items were polish concerns Sprint 7 doesn't need to revisit.

**The codebase is in clean v1.0-prep shape entering Sprint 7.**
