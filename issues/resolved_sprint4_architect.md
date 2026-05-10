# Sprint 4 — architect issues, resolution notes

Four low-severity issues filed; all four resolved or accepted in this
integration pass. No book-prose churn — chapters 17 / 18 / 19 landed
clean against staff's actual implementation.

## Issue 1 (chapter 17 §K8s pod-spec details forward-statements vs staff's landed YAML) — resolved by integrator (spot-check passed)

Spot-checked the chapter 17 §K8s deep-dive against the landed
`internal/exec/k8s_install.yaml` + `internal/exec/k8s.go::buildJobSpec`:

- Long-lived pod name `ops` in `roksbnkctl-ops` namespace — matches
  `K8sOpsPodName = "roksbnkctl-ops"` (the chapter's `ops` short-name
  matches the namespace-scoped resolution `roksbnkctl-ops/roksbnkctl-ops`)
- One-shot Job's `ttlSecondsAfterFinished: 60` — matches
  `internal/exec/k8s.go::buildJobSpec` (chapter 17 quote is verbatim)
- Secret name `roksbnkctl-ibm-creds` and key `IBMCLOUD_API_KEY` —
  match the const block at `internal/exec/k8s.go:28-32` (`K8sOpsSecretName`)
- Two-namespace split (`roksbnkctl-ops` for long-lived, `roksbnkctl-test`
  for ephemeral Jobs) — matches `K8sOpsNamespace` + `K8sTestNamespace`
- `ownerReferences`-based GC for the per-Job Files Secret — matches
  `setSecretOwnerRef`

No prose tweaks needed.

**Status**: ✅ resolved (chapter 17 ↔ `internal/exec/k8s.go` consistent)

## Issue 2 (chapter 18 per-tool default-backend table assumes `iperf3 → k8s`) — resolved by integrator (staff landed the flip)

Staff's report and `internal/cli/cluster.go::perToolDefaultBackend`
confirm `iperf3` resolves to `k8s` post-Sprint-4. Chapter 18's table is
accurate.

**Status**: ✅ resolved

## Issue 3 (chapter 18 forward-references DNS probe to Sprint 5) — accepted (standing review item)

PLAN.md Sprint 5 lands the DNS probe + chapter 21. Chapter 18's
forward-link is correct; no action needed unless Sprint 5 scope changes.

**Status**: ✅ accepted (no action required)

## Issue 4 (chapter 19 references `roksbnkctl init --rotate-key` flag that may not exist) — resolved by integrator

Confirmed via `grep -rn "rotate-key" cmd/ internal/cli/` that the
`--rotate-key` flag is not implemented. Rewrote chapter 19's "Rotation:
rotating the API key" example to walk the user through the three
canonical resolver-chain update paths (env, keychain, config-b64) per
chapter 14, instead of pointing at a non-existent flag. Step 2
(`roksbnkctl ops install`) is unchanged — that's the load-bearing part
of the rotation flow.

**Status**: ✅ resolved (chapter 19 ↔ `cmd/roksbnkctl` flag set
consistent)

## Summary

4 issues filed, 4 resolved (1 spot-check, 1 self-resolved by staff
landing the per-tool default map, 1 accepted standing item, 1 prose
fix). Build, vet, gofmt, and full test suite green.
