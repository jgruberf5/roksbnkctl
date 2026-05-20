# Sprint 18

**Theme:** ship `roksbnkctl cos bucket get` (recursive COS-bucket download) and fix the mermaid-PDF text-missing rendering bug — first regular work sprint post-`v1.6.2`, driven by two scope items captured from the former GitHub backlog into local issue ledgers.

_Drafted by the integrator 2026-05-20 after Sprint 17 (backlog-grooming attempt) was abandoned and the two GitHub issues (`#1 cos bucket get`, `#2 mermaid PDF`) were deleted, with their content moved verbatim into the Sprint 18 role ledgers below. Source of truth for Sprint 18 work = `issues/issue_sprint18_*.md`, not GitHub Issues._

Integrator decisions baked in (decided — do not relitigate):

1. **Two scope items, partitioned by role.** Staff Issue 1 = the `cos bucket get` feature (Go: `internal/cos/`, `internal/cli/`); Architect Issue 1 = the mermaid PDF text-missing bug (book pipeline / docker image / Lua filter / mermaid-cli). Validator and tech-writer issues are role-standard (tests + live verify; doc review).
2. **Regular work-sprint shape**, not the abandoned Sprint 17 backlog-grooming shape. Four roles dispatch in parallel (architect + staff + validator), tech-writer after integration. Agents **commit nothing** and do not run `gh issue create`; the integrator commits and ships per `prompts/README.md` §"Kicking off Sprint N".
3. **`live-verify-high-issues` discipline applies** to the staff feature (a `cos bucket` round-trip needs a live IBM Cloud bucket to truly verify; agents draft the hermetic + the opt-in live driver, integrator runs the live verify before closure).
4. **No `gh issue create` in this sprint.** The two GitHub issues were moved into local ledgers and deleted from GitHub by integrator decision (the local ledgers are the work-of-record; GitHub remains a destination for external-user reports filed via the templates, not a parallel source of truth for in-flight work).
5. **Version is integrator-owned at cut.** Likely shape: a single combined `v1.6.3` patch (the bug fix is user-facing; the new feature is additive). Integrator decides at gate close.

Four-agent dispatch (work-sprint tier):

- **Staff (full)** — implements `cos bucket get` per `issues/issue_sprint18_staff.md` Issue 1 (`internal/cos/bucket.go` recursive download + the cobra command in `internal/cli/cos.go`; additive unit tests under `internal/cos/`).
- **Architect (full)** — diagnoses + fixes the mermaid PDF text-missing bug per `issues/issue_sprint18_architect.md` Issue 1; owns the `book/book.toml` Lua filter / docker image / mermaid-cli interaction. May need a `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook` image rebuild via `tools-images.yml` workflow if the fix is image-side.
- **Validator (full)** — additive hermetic tests covering the staff feature (sha256 round-trip on a fake COS client); opt-in live-verify driver (`scripts/e2e-cos-bucket-get.sh` mirroring the gated-live-verify discipline); regression check that the architect's mermaid fix actually renders text in the resulting PDF.
- **Tech-writer (light, read-only, runs after)** — drift sweep: CHANGELOG / book / `--help` text consistency for both changes; pin the new `cos bucket get` example into Chapter 19 (COS) and the mermaid fix into a §Diagnostics paragraph in the relevant book chapter.

The integrator commits the integrated three-way work in one commit, then dispatches tech-writer over the integrated tree, then runs the live verify (per `live-verify-high-issues`), then tags `v1.6.3` (or `v1.7.0` if integrator judges minor-worthy at cut).
