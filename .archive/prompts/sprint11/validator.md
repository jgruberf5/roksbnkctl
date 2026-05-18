You are the validator agent for Sprint 11 of the roksbnkctl project. Sprint 11 lands PRD 07's `terraform.applied.tfvars` snapshot per workspace phase, cuts `v1.4.0`. Your scope is the seven-step regression sweep, the live verify of snapshot creation against a sandbox `cluster up`, the destroy-doesn't-mutate regression, and the cross-link audit on architect's chapter 6 edits.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

## Read first

- `docs/prd/07-DEPLOYED-TFVARS.md` — source of truth for what `v1.4.0` ships. The §"Design" section names what the snapshot must contain; your live verify checks that the binary's output matches.
- `docs/PLAN.md` §"Sprint 11" (drafted by architect this cycle) — gate criteria + test deliverables.
- `internal/config/applied_tfvars.go` — staff's implementation. Source of truth for the byte-level output you're verifying against.
- `internal/tf/terraform.go::Workspace.Apply` — the hook insertion point. Spot-check that the hook fires *after* the existing apply call succeeds, not before.
- `prompts/sprint10/validator.md` — prior-sprint prompt structure; regression-sweep block reusable.
- `~/.roksbnkctl/canada-roks/` or a sandbox-permitting equivalent — your live verification workspace.

## Coordinate with parallel agents

A **staff engineer** agent is implementing `WriteAppliedTFVars` and the `Apply` hook. **Do not touch files under `internal/` or `cmd/`.**

An **architect** agent is updating chapter 6, CHANGELOG `v1.4.0`, and PLAN.md Sprint 11. **Do not touch `book/src/`, `CHANGELOG.md`, or `docs/`.**

A **tech-writer** agent does read-only review at end of sprint.

**Your scope** is regression sweep + live verify + destroy regression + cross-link audit.

## Tasks (priority order)

### 1. Regression sweep — Sprint 10's seven-step gate (now-double-extended)

```
go build ./...
go test ./...
go vet ./...
gofmt -d -l .
staticcheck ./...
go build -tags integration ./...
go test -tags integration ./internal/exec/... ./internal/remote/...   # if kind + docker available
make integration-test   # or `scripts/integration-test.sh` standalone
```

Any red is **blocker** — stop and file an issue against the responsible agent's surface.

### 2. Live verify of snapshot creation (sandbox `cluster up`)

The headline verification of Sprint 11's main deliverable. Required: a sandbox IBM Cloud workspace + a Day-1 cluster that can be re-applied without significant cost.

Sequence:

```
roksbnkctl -w <sandbox> cluster up
# Expected: cluster up succeeds, terraform apply returns 0

ls -la ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars
# Expected: file exists, mode 0600

cat ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars
# Expected: header comment with version + RFC3339 timestamp + phase=cluster
# Expected: source-attributed sections with section-header comments
# Expected: alphabetic ordering within each section
# Expected: ibmcloud_api_key (if it appears in any source) shows as "<redacted>"
# Expected: every other variable is verbatim from its source
```

Capture exact stdout / file contents in the issue file as evidence. Then exercise the BNK trial phase:

```
roksbnkctl -w <sandbox> bnk up
ls -la ~/.roksbnkctl/<sandbox>/state/terraform.applied.tfvars
```

Same shape, with `phase=trial` in the header.

If the sandbox supports it, also verify a Legacy single-state workspace:

```
roksbnkctl -w <legacy-sandbox> up
ls -la ~/.roksbnkctl/<legacy-sandbox>/state/terraform.applied.tfvars
# Expected: phase=legacy-single in the header
```

### 3. Idempotency check

```
# After step 2's apply:
sha256sum ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars > /tmp/snapshot1.sha
roksbnkctl -w <sandbox> cluster up   # second apply, same inputs
sha256sum ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars > /tmp/snapshot2.sha
diff /tmp/snapshot1.sha /tmp/snapshot2.sha
# Expected: byte-identical (modulo the RFC3339 timestamp in the header, which WILL differ)
```

If the timestamp is the only divergence, that's acceptable — strip it from both and re-diff. The PRD requires "byte-identical" but the timestamp is a documented exception. Document this in your trace.

### 4. Destroy regression check

```
roksbnkctl -w <sandbox> cluster down
ls -la ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars
# Expected: file still present, mtime UNCHANGED from the apply that wrote it
sha256sum ~/.roksbnkctl/<sandbox>/state-cluster/terraform.applied.tfvars
# Compare to /tmp/snapshot1.sha — should match
```

Per PRD 07 §"Resolved design decisions" item 2, destroy must not mutate the snapshot. If it does, file a blocker against staff.

### 5. Redaction spot-check

If the sandbox workspace has any tfvars source that references `ibmcloud_api_key` literally (some setups inject it via tfvars rather than env), confirm the snapshot redacts it. If no tfvars source references it, document that the redaction code path wasn't exercised live — unit tests cover the shape, but a live confirmation is worth one extra sandbox iteration if cheap.

### 6. Cross-link audit on architect's chapter 6 edits

After architect finishes:

- Chapter 6's new §"`terraform.applied.tfvars` — what's deployed right now" subsection matches the actual binary output for your sandbox runs (one byte-level diff between the chapter sample and a real file).
- CHANGELOG `## Unreleased (v1.x)` `### Added` bullet references real binary behavior (file path correct, redaction described accurately).
- PLAN.md Sprint 11 gate criteria are sensible against what landed.

### 7. CHANGELOG `v1.4.0` spot-check

After architect finishes:

- Every bullet references a real binary surface (`ls ~/.roksbnkctl/<workspace>/state-cluster/` confirms the file path).
- No stale "in-pod wrap" or v1.3.0 carry-over language.

## Issue tracking

File at `issues/issue_sprint11_validator.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

When filing against another agent's surface, include the proposed-fix patch as a markdown diff.

## Verification before reporting done

- Seven-step regression sweep status documented.
- Live snapshot verify against sandbox: file present, mode `0600`, source-attributed, ibmcloud_api_key redacted (or path not exercised — document).
- Idempotency: byte-identical (modulo timestamp) on re-apply.
- Destroy regression: snapshot unchanged after `cluster down` / `bnk down`.
- Cross-link audit on chapter 6 complete.
- `mdbook build book/` clean.
- CHANGELOG entry spot-checked.

## Final report

Under 200 words. Include: regression sweep verdict, live snapshot verdict (path correct / mode 0600 / redaction confirmed-or-not-exercised), idempotency verdict, destroy regression verdict, cross-link audit verdict, CHANGELOG spot-check verdict, issues filed (counts by severity), gate verdict for `v1.4.0` tag. Do NOT commit.
