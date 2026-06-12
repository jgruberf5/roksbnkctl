# Shakeout against workspace `test-004` — failures found + fixed

> Filed while running `./scripts/full-shakeout.sh test-004` (the new workspace-mode
> shakeout). Iteration 1 surfaced 4 failures across two root causes; both are real,
> fixable bugs (not environmental dead-ends). Tracked here per the "create issues for
> failures" convention.

`Status`: **resolved 2026-06-12** — all 4 fixed; see commits on `sprint29-registry-mirror`.

---

## Issue 1 — DRY_RUN driver preflights hard-require IBMCLOUD_API_KEY (3 failures)

**Symptom.** Tier-1 dry-runs `dry:e2e-test-full`, `dry:e2e-test`, `dry:e2e-test-backends`
all failed (rc 1/3), aborting in `preflight` with:

```
IBMCLOUD_API_KEY is unset and not found in $TFVARS
```

**Root cause.** In workspace mode the shakeout derives `TFVARS` from the workspace's
own *rendered* tfvars (`~/.roksbnkctl/test-004/state/terraform.tfvars`), which
deliberately **omits `ibmcloud_api_key`** (the key is injected at apply time, never
persisted to state). Those three drivers' preflights grep the key out of `$TFVARS`
and `exit 3` when it is absent — and that block ran **unconditionally, even under
`DRY_RUN=1`**. This contradicts the shakeout's contract ("the DRY_RUN drivers redact
the key and make no cloud calls… never needs IBMCLOUD_API_KEY") and is inconsistent
with `e2e-three-phase.sh`, which already gates its key check on `DRY_RUN`.

It was previously masked only because the legacy tfvars scan landed on
`~/bnkfun/terraform.tfvars`, which *does* carry the key.

**Fix.** Gate the key-requirement block on `DRY_RUN != 1` in all three drivers
(`scripts/e2e-test-full.sh`, `e2e-test.sh`, `e2e-test-backends.sh`). A DRY_RUN
walkthrough makes no cloud calls, so it needs no key. Non-DRY_RUN behavior is
unchanged.

## Issue 2 — live-cluster integration tests fail (not skip) when the cluster is down (1 failure)

**Symptom.** `make test-integration` failed; three tests in `internal/cli`:
`TestIntegration_KubectlPassthrough_ReachesCluster`, `_GetNodes`,
`TestIntegration_OpsInstall_ShowsRBACAndPod` — all with:

```
dial tcp 161.156.187.226:31381: connect: connection refused
```

**Root cause.** These opportunistic integration tests resolve the ambient
kubeconfig and assert a live API server answers. The host's kubeconfig points at a
**torn-down ROKS cluster** (`c113-e.eu-de…:31381`). The passthrough demonstrably
reached the *real* server address (NOT the `localhost:8080` no-config fallback that
the regression guard targets) — so the wiring is fine; the cluster is simply gone.
The tests already skip when a *prerequisite* (kubectl/workspace/API key) is absent,
but "a reachable cluster" was not among the guarded preconditions, so a dead cluster
hard-failed instead of skipping.

**Fix.** Add a shared `clusterUnreachableSkip(out)` helper
(`internal/cli/lifecycle_e2e_integration_test.go`) that recognises connection-level
failures against a **real** (non-localhost) server and skips. Wired into all three
tests. It matches both error dialects: the Go/client-go form the `ops` path emits
(`connection refused`, `i/o timeout`, `no route to host`, `network is unreachable`,
`no such host`, TLS/reset) **and** kubectl's human phrasing the passthrough emits
(`… was refused …`, `Unable to connect to the server`). The `localhost:8080`
regression assertion is deliberately preserved — that symptom is excluded from the
skip and still fails loudly.

---

## Verification

- `bash -n` on all three drivers; `go vet -tags integration ./internal/cli/` clean.
- Re-ran `./scripts/full-shakeout.sh test-004` to a clean summary (see
  `.shakeout/full-shakeout-test-004.log`).

---

## Follow-up — TIER L (opt-in live lifecycle)

Added a gated live tier to `full-shakeout.sh` so a fresh workspace can be taken from
zero to a full cloud test run and back (the gap analysis answer):

```
./scripts/full-shakeout.sh --live <ws>          # plan → up → gateway → probes → reuse → down
IBMCLOUD_API_KEY=… …  --live --keep <ws>        # … but hold the cluster
```

- **Gated:** requires a workspace + `IBMCLOUD_API_KEY`, and runs ONLY after Tier 0 +
  Tier 1 are green (refuses to spend on a broken tree). Off by default — the script's
  zero-cost behavior is unchanged.
- **Steps (each in the SUMMARY):** `live:plan` (apply-readiness gate) → `live:up`
  (Cluster+BNK+Testing) → `live:gateway` → `live:test-connectivity` / `live:test-dns`
  (workspace-scoped, uses the workspace kubeconfig) → `live:perf-matrix` (SKIP unless
  `PERF_MATRIX_CMD` is set — `test throughput` is a v1.x stub) → `live:reuse-bnk-native`
  / `live:airgap-mirror` (reuse drivers vs the standing cluster) → `live:down`.
- **Teardown safety:** `up` arms an EXIT trap; a mid-run failure or Ctrl-C still tears
  the workspace down (unless `--keep`), so a partial apply never leaks billable infra.
- **Verified without spend:** `run_step -t/return-rc/stdin=/dev/null` unit-tested;
  `run_live_tier` structurally tested with a stub binary across success / up-failure /
  `--keep`; non-live `test-004` run still `PASS 16 FAIL 0`. The real live run is the
  user's to launch (gated, billable).
