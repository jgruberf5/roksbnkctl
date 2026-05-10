# Sprint 4 — architect issues

## Issue 1: Chapter 17 §K8s pod-spec details are forward-statements; staff may diverge
**Severity**: low
**Status**: open
**Description**: Chapter 17's `k8s backend` deep-dive describes the long-lived ops pod and one-shot Job specs (image references, namespaces, securityContext fields, ttlSecondsAfterFinished, envFrom: secretRef, ownerReferences-based GC). At drafting time `internal/exec/k8s.go` and `internal/exec/k8s_install.yaml` were not yet committed (staff agent has them in flight). The chapter aligns with PRD 03 §"K8s" and PRD 04 §"In-cluster pod" verbatim. Integrator should diff the chapter's spec details against staff's landed YAML once both are in the integration branch.

Specific points to spot-check:

- Long-lived pod name: chapter says `ops` (in `roksbnkctl-ops` namespace).
- Long-lived pod's `command: ["sleep", "infinity"]` shape — staff may pick a different idle process (e.g., `tail -f /dev/null`); chapter prose can be tweaked if so.
- One-shot Job's `ttlSecondsAfterFinished: 60` — value chosen from PRD 03 §"K8s" "Auto-delete on completion (`ttlSecondsAfterFinished: 60`)"; staff free to pick differently.
- One-shot Job's `backoffLimit: 0` — author's choice, not PRD-mandated; staff may set this differently.
- Secret name `roksbnkctl-ibm-creds` and key `IBMCLOUD_API_KEY` — both consistent with PRD 04; should match staff's `internal/exec/k8s_install.yaml`.
- The `roksbnkctl-test` (one-shot) vs `roksbnkctl-ops` (long-lived) namespace split — consistent with PRD 03 §"K8s" "long-lived ops pod" and "iperf3 client one-shot Job".

**Files affected**: `book/src/17-execution-backends.md`, `book/src/19-in-cluster-ops-pod.md`
**Proposed fix**: integrator runs `diff -u` between the chapter's YAML excerpts and staff's `internal/exec/k8s_install.yaml` + the Pod/Job builder code; small-prose tweaks land as a follow-up commit on the integration branch.

## Issue 2: Chapter 18 per-tool default-backend table assumes staff lands `iperf3 → k8s` as the explicit default this sprint
**Severity**: low
**Status**: open
**Description**: Chapter 18 documents `iperf3` default = `k8s`, citing the `resolveBackendSpecWith` map. The PLAN.md Sprint 4 row 6 ("iperf3 backend selection: default `k8s`, supports `local`/`ssh`") and PRD 03 §"iperf3" both say k8s is the iperf3 default. Sprint 3's chapter 17 (intro) hedged with "Sprint 4 (Sprint 4) / `local` (today)". Chapter 18 commits to the post-Sprint-4 state. If staff defers the default-flip for any reason, chapter 18's table will be inaccurate.

**Files affected**: `book/src/18-choosing-backend.md` §"Per-tool default backends" table
**Proposed fix**: integrator confirms the iperf3 default in `internal/cli/test.go::resolveBackendSpecWith` (or wherever staff wires the per-tool default map) is `k8s`. If for any reason it's still `local` post-merge, chapter 18 needs a one-line edit.

## Issue 3: Chapter 18 references DNS probe as Sprint 5
**Severity**: low
**Status**: open
**Description**: Chapter 18 §"I'm doing GSLB DNS validation" forward-links to Chapter 21 ("DNS testing for GSLB") with the note "The DNS probe lands in Sprint 5". Per PLAN.md Sprint 5 documentation deliverables this is correct. The supported-backend matrix also lists "DNS probe (Sprint 5)" explicitly. If the DNS-probe schedule slips, that prose should track.

**Files affected**: `book/src/18-choosing-backend.md`
**Proposed fix**: standing review item; no action needed unless Sprint 5 scope changes.

## Issue 4: Chapter 19 mentions `roksbnkctl init --rotate-key` which may not exist
**Severity**: low
**Status**: open
**Description**: Chapter 19's rotation example uses `roksbnkctl init --rotate-key` as an illustrative way to update the resolver chain's API key value. This flag is not specified in any PRD that I read; it may be a reasonable feature name but isn't necessarily implemented. The intent of the prose is "update the resolver-chain source"; if the actual UX is `roksbnkctl ws set-key` or `roksbnkctl init` (re-prompt) or just `keyring set roksbnkctl <ws>/ibmcloud_api_key`, the example needs the right command.

**Files affected**: `book/src/19-in-cluster-ops-pod.md` §"Rotation: rotating the API key"
**Proposed fix**: integrator should confirm the canonical "update the API key" UX and adjust the example if needed. As-is the prose still reads correctly because it follows up with `roksbnkctl ops install` (the part that actually rotates the cluster Secret) — the `init --rotate-key` is just one possible first step.
