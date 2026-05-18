You are the tech writer agent for Sprint 4 of the roksbnkctl project. Read-only review of all documentation produced this sprint, plus example correctness and PRD/PLAN drift.

Project location: `/mnt/d/project/roksbnkctl/`. Your scope is **review + issue filing only** — do not edit any files except `issues/issue_sprint4_tech-writer.md`.

## Context — what the other agents produced this sprint

- **Architect** filled in chapter 17's `*Coming in Sprint 4.*` deep-dive sections (k8s + ssh backends) and replaced 2 chapter stubs with real prose: chapter 18 (Choosing a backend per tool — decision tree) and chapter 19 (The in-cluster ops pod — RBAC + lifecycle).
- **Staff engineer** implemented PRD 03 second half: `internal/exec/k8s.go` (long-lived ops pod path + one-shot Job path), `internal/exec/ssh.go` (file materialization, env propagation with SetEnv + wrapper-script fallback, Ubuntu apt-bootstrap behind `--bootstrap` opt-in), `internal/cli/ops.go` (`roksbnkctl ops install/show/uninstall`), `internal/exec/k8s_install.yaml` (embedded RBAC manifests), iperf3 SCC fix in `internal/test/throughput.go`, iperf3 + ibmcloud backend-selection wiring (per-tool default map: iperf3=k8s, ibmcloud=local, terraform=local), doctor extensions for `--backend k8s/ssh`, and four Sprint 3 polish carry-overs: legacy `internal/config.ResolveAPIKey` migration, `:dev` tag pinning fix in `internal/exec/docker.go::toolImages`, the 126 vs 127 backend-failure semantics split, and the per-tool default map.
- **Validator** added k8s + ssh argv-builder unit tests, k8s + ssh cred-leak audit extensions, kind-based integration tests + CI workflow updates, a new `scripts/e2e-test-backends.sh` covering PRD 05 Phases K + L + M, the cspell `SSC→SCC` fix + Sprint 4 vocabulary additions, README highlight bullet for Sprint 4, and CONTRIBUTING.md updates for kind testing + e2e-test-backends.sh usage.

Their issue files are at `issues/issue_sprint4_<role>.md` with corresponding `resolved_sprint4_<role>.md`. Read them — your job is to find what they missed.

## Tasks

### 1. New chapter quality — chapters 17 (full), 18, 19

For each chapter:
- **Tone consistency** with each other and Sprint 1+2+3 chapters (clipped technical voice, lower-case prose, code-block-heavy)
- **Audience alignment**: chapter 17 is the architecture reference; chapter 18 is the decision-tree user-facing chapter; chapter 19 is the ops-pod operational reference
- **Code examples runnable**: every `roksbnkctl ...` snippet should be a real command. Verify against `cmd/roksbnkctl --help` for the new subcommand `roksbnkctl ops` and any new flags (`--bootstrap`)
- **Cross-references resolve**: relative links work; PRD links use GitHub-canonical URLs (per Sprint 1 Issue 9 fix)
- **No unfilled placeholders**: zero "Coming in Sprint 4" should remain (the only acceptable forward-references are to Sprint 5+ for terraform-via-k8s/ssh and DNS probe, both of which are explicitly future-tense in PLAN.md)

### 2. Chapter 17 example correctness — k8s + ssh deep-dive sections

Chapter 17's k8s + ssh sections are net-new this sprint. Verify:
- The Pod + Job spec details match the staff agent's actual implementation in `internal/exec/k8s.go` — namespace names, Secret name, image references, securityContext fields
- The ops-pod cred-propagation description (env-from-secretRef on `roksbnkctl-ibm-creds`) matches `internal/exec/k8s_install.yaml`
- The SSH backend's `--bootstrap` opt-in description matches the staff agent's actual flag plumbing in `internal/cli/root.go`
- The SSH backend's wrapper-script env fallback description matches the implementation (the `set +x` redaction discipline is documented; the env-file path is correct)
- The 126 vs 127 split documented in chapter 17 matches what staff implemented across all four backends

### 3. Chapter 18 example correctness — per-tool defaults

Chapter 18 describes the per-tool default backend table (iperf3=k8s, ibmcloud=local, terraform=local). Verify:
- The values match the staff agent's actual `perToolDefaults` map (or whatever it ended up named) in `internal/cli/cluster.go` or wherever the resolution sits
- The "supported backends" matrix matches: iperf3 supports k8s/local/ssh (no docker); ibmcloud supports all four; terraform supports local/docker today (k8s/ssh deferred to Sprint 5+)
- The "when not to use a backend" section's foot-guns match real implementation behaviour (e.g., `--backend docker` for DNS probe rejected — that error message must match what the binary actually produces, even though the DNS probe itself is Sprint 5 and the error path may not be wired yet; if not, file an issue)

### 4. Chapter 19 example correctness — ops pod RBAC

Chapter 19's RBAC description must match the embedded manifests. Verify:
- ClusterRole rules in chapter 19 match `internal/exec/k8s_install.yaml`'s actual rule set
- Namespace names (`roksbnkctl-ops`, `roksbnkctl-test`) match
- Secret name (`roksbnkctl-ibm-creds`) matches
- The "rotation story" subsection matches what `roksbnkctl ops install` actually does on re-run (Secret update + pod rollout)
- The "negative RBAC" examples (e.g., `kubectl auth can-i delete pods …` returns `no`) match the validator's actual integration test assertions

### 5. PRD-to-chapter coverage check

PRD 03 §"K8s" and §"SSH" specify the design. Chapters 17 + 18 + 19 are the user-facing version. Verify:
- Every user-visible decision in PRD 03 §"K8s" appears in chapters 17 + 19
- Every user-visible decision in PRD 03 §"SSH" appears in chapter 17 §SSH
- The PRD 04 cross-backend cred-propagation rules (especially §"K8s" and §"SSH") appear in the relevant chapter sections
- The chapters don't claim functionality the staff agent didn't build (e.g., terraform via k8s — that's deferred to Sprint 5+; should be marked future-tense if mentioned)

### 6. Cross-document drift check

Spot-check:
- `docs/PLAN.md` (does PLAN.md still accurately describe Sprint 4's outcomes? Are the M3-prelim gate criteria met?)
- `docs/prd/03-EXECUTION-BACKENDS.md` (any details now obsolete given the staff agent's implementation choices? Open questions resolved this sprint should be reflected in PRD 03 — e.g., the `--bootstrap` opt-in decision)
- `docs/prd/04-CREDENTIALS.md` (any cred-propagation decisions changed?)
- `docs/prd/05-E2E-TEST-PLAN.md` (Phases K, L, M now implemented in `scripts/e2e-test-backends.sh` — does PRD 05 still match the implementation?)
- `book/src/SUMMARY.md` (chapter titles match h1?)
- The Go version in chapter 4 + README — Sprint 4's deps may have bumped go.mod; chapter 4 + README should follow

### 7. Tool image Dockerfile correctness

Read `tools/docker/ibmcloud/Dockerfile` and `tools/docker/iperf3/Dockerfile` again — Sprint 3 tech-writer Issue 7 + 8 noted the Sprint 3 → Sprint 4 transition would land specific changes (iperf3 wired up in Sprint 4; the `:dev` tag publish fix). Flag if:
- The iperf3 Dockerfile comment says "wired up in Sprint 4" but the actual wiring landed differently
- The `:dev` tag resolution behaviour described in chapter 17 doesn't match what `internal/exec/docker.go::toolImages` actually does after the polish carry-over fix
- iperf3's Dockerfile lacks `USER 1000` (or equivalent non-root user) given the SCC fix's `runAsNonRoot: true` requirement

### 8. SSH backend documentation specifics

Chapter 17's SSH section is the most failure-mode-rich. Verify:
- All four documented bootstrap failure modes (sudo password, non-Ubuntu, network unreachable, missing-tool-without-bootstrap) match the staff agent's actual error messages and exit codes
- The wrapper-script fallback path is documented for the `AcceptEnv` silent-drop case — readers shouldn't be surprised by this fallback existing
- The cleanup-on-EXIT trap behaviour is documented; what happens on ctx-cancel mid-run is documented

### 9. Test code readability

Read `internal/exec/k8s_test.go`, `ssh_test.go`, `audit_test.go` extensions, `internal/cli/ops_*_test.go`. Flag if:
- A test name is unclear (e.g., `TestK8sBackend_Run` is too generic — better: `TestK8sBackend_RunOnOpsPod_PropagatesCredsViaSecret`)
- A test lacks a comment explaining the behaviour it pins down (especially security-relevant ones like cred audit)
- Magic constants without explanation
- The audit tests miss inspection surfaces (e.g., they check stdout but not the Job's spec, or they check the Secret but not metadata.annotations)

### 10. Highlight bullet + README consistency

Sprint 4 should add a `--backend k8s + --backend ssh` highlight bullet to README. Verify:
- It's present (validator owns this; if missing, file as medium per Sprint 1+3 precedent)
- The bullet is consistent in tone + structure with Sprint 1's `--on jumphost`, Sprint 2's k-commands, and Sprint 3's `--backend docker` bullets
- It links to chapter 17 (the deep-dive), and ideally chapter 18 (decision tree) for "which one to use"

## Issue file format

`/mnt/d/project/roksbnkctl/issues/issue_sprint4_tech-writer.md`. Same format as Sprints 0/1/2/3. If genuinely clean, file with `*No issues filed.*`. Don't manufacture issues.

## Verification before reporting done

- All 3 chapter files (17 + 18 + 19) contain real prose; chapter 17's `*Coming in Sprint 4.*` markers are gone
- All cross-references in the new + edited chapters resolve
- All `roksbnkctl ...` commands appear in the actual binary's help output (especially the new `roksbnkctl ops` subcommand and the `--bootstrap` flag)

## Final report (under 200 words)

- Files reviewed (counts)
- Issues filed (counts by severity)
- Top 3 noteworthy observations not filed as issues
- Whether you spotted any drift between PRD 03 / PRD 04 / PRD 05 / PLAN.md and delivered surface
- Whether the Sprint 4 gate criteria (M3-prelim per PLAN.md) are met by the delivered surface

Do NOT edit any files (except your issue file). Do NOT commit anything.
