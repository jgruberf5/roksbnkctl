You are the tech writer agent for Sprint 5 of the roksbnkctl project. Read-only review of all documentation produced this sprint, plus example correctness, PRD/PLAN drift, and **v0.9 release-readiness verification**.

Sprint 5 is the **v0.9 release gate sprint** — your job includes confirming that documentation is release-quality and the integrator can comfortably tag `v0.9` once you've signed off.

Project location: `/mnt/d/project/roksbnkctl/`. Your scope is **review + issue filing only** — do not edit any files except `issues/issue_sprint5_tech-writer.md`.

## Context — what the other agents produced this sprint

- **Architect** replaced 3 chapter stubs with real prose: chapter 20 (Connectivity testing), chapter 21 (DNS testing for GSLB — the flagship testing chapter), chapter 22 (Throughput testing). Also appended a terraform docker-backend subsection to chapter 17.
- **Staff engineer** implemented the DNS probe (replaced `net.Resolver` with miekg-based `Probe` in `internal/test/dns.go`; extended `internal/cli/test.go::dnsCmd` with `--target`/`--type`/`--server`/`--iterations`/`--gslb-compare`/`--require-divergence` flags; added `dns-probe` Job mode in `internal/exec/k8s.go`; added `test.dns.resolvers` + `test.dns.default_target` to workspace config), the terraform docker backend (`internal/exec/docker.go` + `internal/cli/lifecycle.go` wiring for `--backend docker` on `up/plan/apply/destroy`), doctor extensions for DNS-probe + ops-pod health + cred-rotation-freshness, and three Sprint 4 polish carry-overs (k8s argv-entrypoint fix per validator Issue 7; SSH wrapper-script test seam per validator Issue 3; optional `:dev`-on-main coordination).
- **Validator** added miekg-based DNS probe unit tests (in-process mock server), integration tier against `8.8.8.8` + local stub, `--gslb-compare` multi-vantage tests, terraform-via-docker tests, Phase L-DNS in `scripts/e2e-test-backends.sh`, k8s test-name + comment polish (tech-writer Issue 14 carry-over), SSH wrapper-script + bootstrap-failure matrix tests (validator Issue 3 carry-over), cspell + CONTRIBUTING updates, the v0.9 release-checklist section in `docs/E2E_TEST.md`, and possibly the `:dev`-on-main publish in `tools-images.yml`.

Their issue files are at `issues/issue_sprint5_<role>.md` with corresponding `resolved_sprint5_<role>.md`. Read them — your job is to find what they missed.

## Tasks

### 1. New chapter quality — chapters 20, 21, 22

For each chapter:
- **Tone consistency** with Sprint 1-4 chapters (clipped technical voice, lower-case prose, code-block-heavy)
- **Audience alignment**: chapter 20 is a workflow reference (extra_hosts + insecure-tls); chapter 21 is the flagship diagnostic chapter — readers come here when their GSLB is broken; chapter 22 is operational with cross-references to chapter 17 §K8s backend
- **Code examples runnable**: every `roksbnkctl ...` snippet should be a real command. Verify against `cmd/roksbnkctl test dns --help`, `test connectivity --help`, `test throughput --help`, and `--backend docker` on `up/plan/apply/destroy`.
- **Cross-references resolve**: relative links work; PRD links use GitHub-canonical URLs (per Sprint 1 Issue 9 fix)
- **No unfilled placeholders**: zero "Coming in Sprint 5" should remain. Sprint 6+ forward-references (E2E plan in chapter 23, the migration story, etc.) should be explicitly future-tense.

### 2. Chapter 21 (flagship) example correctness — DNS probe

Chapter 21 is the most code-example-heavy chapter this sprint. Verify:

- The `--target`/`--type`/`--server`/`--iterations`/`--gslb-compare`/`--require-divergence` flag set matches the staff agent's actual flag definitions in `internal/cli/test.go::dnsCmd`
- The supported record-type list (A/AAAA/CNAME/MX/NS/TXT/SRV/SOA/PTR/CAA/DS/DNSKEY/ANY) matches what `dns.StringToType` accepts in the staff's implementation
- The server-resolution values (`<ip>`, `<ip>:<port>`, `system`, `cluster`, named-from-config) all work as documented (verify `cluster` errors clearly on the local backend; verify named-from-config reads `test.dns.resolvers` correctly)
- The JSON output examples match the `roksbnkctl.dns.v1` schema produced by `internal/test/dns.go::ProbeResult.MarshalJSON` (or however staff structured the marshalling)
- The `--gslb-compare` workflow's `gslb_divergence` boolean matches what the staff actually computes (set-equality across vantage answer sets, or something more nuanced — verify against `internal/cli/test.go::runGSLBCompare`)
- Sample F5 BIG-IP Next GSLB scenarios — these may be aspirational/illustrative rather than tested; flag if any specific example claims behaviour that the binary can't actually demonstrate (e.g., "the probe detects health-check flapping" — does the binary's iterations=10 actually expose this, or is it just RTT variance the user has to interpret?)

### 3. Chapter 20 example correctness — connectivity testing

Verify:
- The `extra_hosts:` schema in chapter 20 matches `internal/config/workspace.go::Workspace.ExtraHosts` byte-for-byte
- The `--insecure-tls` flag matches the actual flag in `internal/cli/test.go::connectivityCmd`
- Pass/fail criteria (2xx-3xx = pass; anything else = fail) matches the actual check in `internal/test/connectivity.go`
- The JSON output schema matches what the binary actually emits

### 4. Chapter 22 example correctness — throughput testing

Verify:
- `--mode east-west` vs `--mode north-south` matches the staff Sprint 4 wiring + Sprint 5 backend selection
- The bundled iperf3 image reference matches `internal/exec/docker.go::toolImages["iperf3"]` (post-Sprint-4 it's version-pinned via `toolImageTag()`)
- The "if your throughput pod fails to start with an SCC error, see chapter 17" cross-link still works
- Sample iperf3 `-J` JSON excerpts match what `roksbnkctl test throughput` actually surfaces

### 5. Chapter 17 update — terraform docker-backend section

Sprint 5 appends a terraform docker-backend subsection. Verify:
- The bind-mount path matches the staff's actual implementation (`~/.roksbnkctl/<ws>/state/` → some container path)
- The `--user $(id -u):$(id -g)` UID/GID story matches what the docker backend actually passes
- The image reference (`hashicorp/terraform:<v>`) matches `toolImages["terraform"]` — should be a literal pin, not version-resolved
- The "deferred to v1.x" framing for k8s + ssh terraform backends is correct (PRD 03 §"State concerns")

### 6. PRD-to-chapter coverage check

PRD 03 §"DNS probe (GSLB-aware)" is the authoritative spec. Chapter 21 is the user-facing version. Verify:
- Every user-visible decision in PRD 03 §"DNS probe" appears in chapter 21 (or is intentionally deferred to Sprint 6+ — e.g., trusted-profile auto-provisioning isn't a Sprint 5 deliverable)
- PRD 03 §"terraform" §"State concerns" is reflected in chapter 17's terraform docker-backend section
- The chapters don't claim functionality the staff agent didn't build (e.g., if `--gslb-compare` doesn't actually run against `ssh:<target>` vantages this sprint because SSH e2e is Sprint 6, the chapter should say so)

### 7. Cross-document drift check

Spot-check:
- `docs/PLAN.md` Sprint 5 — does PLAN.md still accurately describe Sprint 5's outcomes? Are the gate criteria met? Specifically check whether the M3 / v0.9 gate items in PLAN.md are met.
- `docs/prd/03-EXECUTION-BACKENDS.md` (any details now obsolete given the staff's implementation choices? Open questions resolved this sprint should be reflected — e.g., the `--gslb-compare` flag set landed; the "Open questions" section may need updating again)
- `docs/prd/05-E2E-TEST-PLAN.md` (Phase L-DNS now implemented in `scripts/e2e-test-backends.sh` — does PRD 05 still match the implementation?)
- `book/src/SUMMARY.md` (chapter titles match h1?)

### 8. v0.9 release-readiness check (CRITICAL FOR THIS SPRINT)

Sprint 5 gates the `v0.9` tag. Verify the release surface is clean:

- The README "Highlights" section reflects the Sprint 5 additions (DNS probe + terraform docker backend) — there should be a v0.9 highlight bullet. If missing, file as **medium** severity.
- The CHANGELOG (or release-notes equivalent) covers Sprints 3-5 (the v0.9 cumulative set: cred abstraction, four backends, DNS probe, terraform docker, ops pod). If the project doesn't have a CHANGELOG yet, flag as a v0.9-blocker (low-effort to create; integrator can fill in from PLAN.md Sprint 3+4+5 sections).
- The PLAN.md milestone table (Sprint 5 → `v0.9` gate criteria) should still match what shipped. Flag any discrepancy.
- `docs/E2E_TEST.md`'s v0.9 release checklist (validator owns this) should be clear enough that a future contributor following it can re-cut a release patch.
- All `*Coming in Sprint 5.*` markers are gone from the book.
- The book TOC (`book/src/SUMMARY.md`) renders cleanly — chapters 20, 21, 22 are all listed; chapter 17's expansion shows in the rendered TOC if you can preview the book locally.
- `cmd/roksbnkctl --version` reports a non-`dev` value when built with the release ldflags (or the build setup is documented so the integrator can do this at tag time).

### 9. Test code readability

Read `internal/test/dns_test.go`, `internal/test/dns_integration_test.go`, the `internal/cli/test_dns_compare_test.go`, the SSH wrapper-script tests, and the split k8s tests. Flag if:
- The DNS test cases don't cover all the record types they document
- The mock DNS server setup is hard to understand (a top-of-file comment explaining the `dns.Server` shape is worth its weight)
- Test names are unclear (Sprint 4 tech-writer's Issue 14 set the bar; verify the split + docstrings landed)
- Security-relevant tests (cred-leak audit, SSH wrapper-script content) carry PRD-citation docstrings

### 10. Highlight bullet + README + book consistency

Sprint 5's highlight bullet should land in README. The bullet should be:

- Consistent in tone + structure with Sprint 1's `--on jumphost`, Sprint 2's k-verbs, Sprint 3's `--backend docker`, Sprint 4's `--backend k8s + ssh`
- Cross-link to chapters 21 (DNS) + 22 (throughput) + 17 (terraform docker backend)
- Mark `v0.9` so the release-version cadence is consistent

## Issue file format

`/mnt/d/project/roksbnkctl/issues/issue_sprint5_tech-writer.md`. Same format as Sprints 0/1/2/3/4. If genuinely clean, file with `*No issues filed.*`. Don't manufacture issues.

## Verification before reporting done

- All 3 chapter files (20, 21, 22) contain real prose; no `Coming in Sprint 5` markers anywhere
- Chapter 17 has a terraform docker-backend subsection
- All cross-references in the new + edited chapters resolve
- All `roksbnkctl ...` commands appear in the actual binary's help output (`roksbnkctl test dns --help`, `roksbnkctl up --backend docker --help`, etc.)
- README has a v0.9 / Sprint 5 highlight bullet
- The book builds cleanly (or, if mdbook unavailable locally, book CI is the fallback)

## Final report (under 200 words)

- Files reviewed (counts)
- Issues filed (counts by severity)
- Top 3 noteworthy observations not filed as issues
- Whether you spotted any drift between PRD 03 / PRD 05 / PLAN.md and delivered surface
- **Whether the v0.9 release gate criteria (PLAN.md §"Sprint 5 — Gate to Sprint 6") are met by the delivered surface** — this is the v0.9 ship gate; if any item is missing, flag as **blocker** in the issue file.

Do NOT edit any files (except your issue file). Do NOT commit anything.
