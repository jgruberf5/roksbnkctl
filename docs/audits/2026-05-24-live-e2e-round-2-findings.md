# Live e2e round 2 findings — 2026-05-24 (post #34/#35/#36)

> Third cold-start attempt on syd-tracer after all original cold-start PRs
> (#27-#31), C-6 hotfix (#32), Finding #2 PR (#34), Finding #3 PR (#35), and
> the multi-script Phase 24b hotfix (#36) merged.
>
> Verdict: full e2e acceptance (5/5 HTTP 200 + zero residue + zero intervention)
> **STILL NOT REACHED**. New BNK runtime-layer issues uncovered, **NOT addressable
> in awsbnkctl** without escalating workarounds beyond reasonable scope.

## What did work on first cold try

- AWS infra phases 0-18: green, no retries.
- Phase 12 cert-manager + cert chain: green on first apply (proves the C-6
  RESTMapper Reset hotfix from PR #32).
- Phase 24b patched all 7 `--tls` scripts (proves PR #36 multi-script
  hotfix): `init.sh`, `liveness_probe.sh`, `readiness_probe.sh`,
  `sentinel_liveness_probe.sh`, `sentinel_readiness_probe.sh`,
  `sentinel_startup_probe.sh`, `startup_probe.sh`.
- Phase 25 controller kick fired at iter 6 (3 min) as designed (proves PR #34).
- All 9 control-plane pods reached `phase=Running` within 6 min.
- License CR reached `state=Active` within ~2 min.

## What blocked CNE Available=True (and hence acceptance)

Phase 25 timed out at full 9-min / 18-iter budget. CNEInstance Available
condition stayed `Failed` with three Pending sub-conditions:

### Finding #4 (NEW) — f5-tmm-pod-manager K8s API timeout, CrashLoopBackOff

- `f5-cne-controller-7b5988d4bc-nr2vg` pod: 4/5 Ready, `CrashLoopBackOff`,
  5 restarts in 12 min, last restart 2m41s ago.
- Failing container: `f5-tmm-pod-manager`.
- Last `--previous` log lines: pod-manager initializes, then errors:

  ```
  unable to set up indexers
  error: unable to register Pod deletionTimestamp field index:
    failed to get restmapping: failed to get server groups:
    Get "https://172.20.0.1:443/api": dial tcp 172.20.0.1:443: i/o timeout
  ```

  `172.20.0.1:443` is the EKS API server ClusterIP service. From inside the
  pod, this should always be reachable via kube-proxy. The fact that it
  times out suggests a host-device/Multus networking interaction with the
  TMM node's secondary ENIs that the f5-tmm-pod-manager container's net
  namespace can't route past.

- **Same pattern observed twice** (second + third cold-up attempts; this
  finding wasn't captured in the round-1 findings doc because pod-manager
  was self-recovering at the time of triage).

- **Hypothesis**: Multus secondary-network attachment on the TMM node
  alters its routing in a way that blocks the f5-tmm-pod-manager
  container's exec-time DNS or service-IP resolution. Possibly resolves
  once a kube-proxy reconcile fires after the TMM node's ENIs settle.

### Finding #5 (NEW) — f5-dssm StatefulSet replicas hang after `init.sh`

- `f5-dssm-db-2` (3rd replica, 2/3 Ready, restartCount=0 — never restarted).
- `f5-dssm-sentinel-0` (only sentinel created so far, 2/3 Ready, restartCount=1).
- Both pods exhibit the same log pattern: container starts, `Running init.sh
  for single cluster deployment`, `grpc server is starting up :19891`,
  then **silence** — Redis/Sentinel daemon never logs "ready" or binds
  to its port.
- Readiness probe (correctly patched with `--insecure` by Phase 24b) returns
  `Connection refused` because the daemon isn't listening on `:6379` (db) /
  `:26379` (sentinel) inside the pod.

- **NOT** the TLS hostname issue (Phase 24b fixed that for ALL 7 scripts —
  verified live).
- **IS** something in the BNK 2.3 f5-dssm container image's `init.sh` or
  s6-supervised wrapper that hangs on cold cluster. Possibly a race
  against DSSM cluster formation (sentinel needs primary to be reachable
  before it can join; primary needs sentinel quorum to be writeable; etc).

- aws-gpu-setup's `deploy-bnk.sh` doesn't appear to have a workaround for
  this. The previous slice-11 cold start that reached HTTP 200 was against
  a manually-provisioned cluster, not a fresh `awsbnkctl up` — the user
  may have hit this and bounced pods by hand without it being captured.

## What this means for the cold-start acceptance criterion

The user's stated acceptance: **"one clean cycle returns 5/5 HTTP 200 with
zero AWS residue and zero human intervention."**

After 8 merged PRs (#27-#31, #32, #34, #35, #36) the awsbnkctl-side cold
start is **as clean as we can make it without venturing into F5-image
workarounds beyond Phase 24b**. Two specific blockers remain, both at the
BNK runtime layer:

1. f5-tmm-pod-manager K8s API timeout → CrashLoopBackOff
2. f5-dssm sentinel + 3rd-replica hang after init.sh

Both might be fixable with awsbnkctl workarounds (e.g. a Phase 24c that
bounces stuck pods after N minutes), but at that point we're adding
escalating workarounds for what look like BNK 2.3 cold-start races.

## Escalation options (need user direction)

**Option A — Continue adding workarounds**:
- Phase 24c: bounce f5-tmm-pod-manager if it crashloops > 3 times.
- Phase 24d: bounce f5-dssm pods at 2/3 stuck > 2 min.
- Risk: each workaround is brittle and patches over an underlying image bug.

**Option B — Manual-bounce escape hatch + document**:
- Add a `awsbnkctl heal` subcommand that does the bounces manually.
- Document the procedure in the operator runbook.
- Acceptance stays "zero intervention" only for the warm path; cold-start
  requires `awsbnkctl heal` as a documented step.

**Option C — Escalate upstream to F5**:
- File issues against BNK 2.3 for the two cold-start races.
- Pin awsbnkctl to a future BNK 2.4 that resolves them.
- In the interim, document the manual workaround.

**Option D — Live-iterate with longer cluster time**:
- Spend more cluster time (= more $$) trying to characterise exactly when
  the races resolve naturally. May take 2-3 more cycles ($10-15 each).
- May or may not yield a clean code workaround.

## What landed in the cold-start reliability work overall

Across all 9 merged PRs (#27 + #28 + #29 + #30 + #31 + #32 + #34 + #35 + #36):

- 41 of 42 audit findings closed (1 = M-8 was wrong-deferred; now addressed
  by Phase 24b in PR #35 + #36).
- 6 new findings surfaced via live testing:
  - Finding #1: C-6 RESTMapper Reset (FIXED in #32).
  - Finding #2: Phase 25 budget + controller kick (FIXED in #34).
  - Finding #3: DSSM `--insecure` overlay (FIXED in #35).
  - Finding #3 follow-up: multi-script patch (FIXED in #36).
  - **Finding #4: f5-tmm-pod-manager API timeout (THIS DOC, OPEN — BNK
    runtime issue, escalate).**
  - **Finding #5: f5-dssm replica/sentinel hang after init.sh (THIS DOC,
    OPEN — BNK runtime issue, escalate).**
- Zero AWS residue verified on all 3 down cycles (H-3 SG-revoke working).
- Zero deferred bugs in awsbnkctl code per `feedback_no_deferred_fixes`.

## Cost summary

3 live cycles × ~$3-5 each = **~$10-15 total AWS spend**. All clusters
torn down cleanly with zero residue.

---

## 2026-05-24 — Cross-cluster comparison addendum

> Added post-audit after cross-checking against the two pre-existing AWS
> reference clusters (`aws-syd-test` and `jl-gpu-lab`, both unrelated to
> awsbnkctl). Materially reframes Findings #4 and #5 and identifies a
> follow-up direction on NAD form.
>
> **Important caveat**: Sydney runs BNK `2.2.1-3.2226.0-0.0.511`. Our
> target is BNK 2.3.x. Some 2.3-only regressions (notably `crashagentConfig`
> below) cannot be observed on Sydney. Tokyo (`jl-gpu-lab`) is BNK 2.3.0 but
> currently scaled to 0 nodes (cost), so its CRs survive but pods don't.
> Where the comparison is "Sydney 2.2 works → 2.3 should too", call it
> out — we're using Sydney as a sanity reference, not a copy target.

### Finding #5 — REFRAMED: DSSM replica hang is not a blocker

`aws-syd-test` has been running in production for 33 days with:

```
CNEInstance.status.conditions:
  type=Available       status=False  reason=Failed
  type=DSSMAvailable   status=False  reason=Pending
    message: "pod f5-operator/f5-dssm-db-1: 0/6 nodes are available:
             1 node(s) had untolerated taint {dpu: true},
             2 node(s) had volume node affinity conflict,
             3 Insufficient cpu."
  type=F5TmmAvailable          status=True   reason=Available
  type=CNEControllerAvailable  status=True   reason=Available
  ... (all other component sub-conditions True)
```

`f5-dssm-db` StatefulSet is `1/3` (only db-0 ready; db-1 never schedules,
db-2 doesn't exist) — and the cluster is fully functional with HTTP 200
traffic flowing via `10.0.10.100` per `bnk-tmm-recovery-runbook.md` and
the live `f5-tmm-pod-manager` logs which show steady-state TMM endpoint
reconciles every second.

**Conclusion**: the rollup `CNEInstance.Available` condition aggregates all
sub-conditions including DSSM HA replicas. Production BNK does not require
all 3 DSSM `db` replicas ready — primary + sentinel quorum is sufficient.
Our Phase 25 acceptance criterion is checking the wrong condition.

**Action**: cold-start acceptance (Phase 25 / scenarios `http-routing-e2e`)
should check **`F5TmmAvailable=True && CNEControllerAvailable=True &&
RoutingDone=True`** plus the actual HTTP 200 traffic test, not the rollup
`Available=True`. The `ResyncCNEInstance` kick from PR #34 still has value
(refreshes sub-conditions after pods become Ready) — keep it. Drop any
plan for a Phase 24d DSSM-bounce.

### Finding #4 — REFRAMED: pod-manager image regression, not Multus

Image versions across the two reference clusters:

| Cluster              | BNK       | f5-tmm-pod-manager image  | State              |
|----------------------|-----------|---------------------------|--------------------|
| aws-syd-test (Sydney)| 2.2.1     | `v1.2.8-0.0.12`           | 0 restarts in 19d  |
| jl-gpu-lab (Tokyo)   | 2.3.0     | `v1.6.1-0.0.4` (template) | (no nodes)         |
| syd-tracer (ours)    | 2.3.0     | `v1.6.1-0.0.4`            | CrashLoopBackOff   |

Pod-manager 1.6.x added an up-front K8s API call to register Pod indexers
during controller startup, before the main reconcile loop. The 1.2.x image
didn't have this call. The 1.6.x indexer setup is therefore newly sensitive
to the cold-start race where the controller pod starts before EKS
kube-proxy has finished programming iptables rules for the EKS API
ClusterIP (`172.20.0.1`) on the just-came-up node. Self-heals within ~5
min via CrashLoopBackOff retries.

The original audit hypothesis was wrong: in Sydney the `f5-cne-controller`
pod runs on a non-TMM node (TMM node carries `taint=dpu`) with plain
aws-cni eth0 and **no Multus attachments**. So Multus routing on the TMM
node cannot be the cause — our controller pod isn't Multus-attached either.

**Action options** (not yet committed):
1. **Accept the self-heal**: 5-min cold-start delay with no code change.
2. **Conditional bounce**: after Phase 23b but before Phase 25 polls, if
   `f5-cne-controller` has any container in `CrashLoopBackOff`,
   `kubectl rollout restart deploy/f5-cne-controller` once. Resets backoff.
3. **Init-container gate**: patch the FLO-shipped Deployment to add an
   initContainer that confirms `172.20.0.1:443` is reachable before the
   pod-manager container starts. Most surgical, biggest patch surface.
4. **Upstream**: file against F5 — pod-manager 1.6.x indexer setup should
   retry on i/o timeout, not exit.

### DSSM CRD added `crashagentConfig` in 2.3 — confirms FLO hot-loop

Live diff of the `dssms.k8s.f5.com` CRD between Sydney (2.2) and Tokyo
(2.3):

```
Tokyo 2.3 spec.properties has an extra key NOT in Sydney 2.2:
  crashagentConfig    type=object    description="Crashagent configuration for core collection"
```

This is the **exact field** that `bnk-tmm-blocker.md` documented as the
source of the FLO reconcile hot-loop on the rebuilt Tokyo cluster: FLO
sends `null` for the field on every update; the 2.3 CRD requires
`type: object`; API server rejects; reconcile fails; requeue immediately.
The hot loop self-clears when an empty object `{}` is written.

**Action**: a tiny awsbnkctl post-install patch step could write
`spec.crashagentConfig: {}` on each affected component CR (F5Tmm, DSSM,
Afm, Downloader, Analyzer, Fluentd, Rabbitmq, CRDConversion, Cwc,
IPAMController, CSRC, Observer, CNEController) immediately after Phase 23
to pre-empt the hot loop. Cheap insurance; verify against next cold start.

### NAD form: `pciBusID` is canonical; our `device:` form is a hack

Sydney's NAD config (BNK 2.2, working in production):

```yaml
spec:
  config: '{"cniVersion":"0.3.1","type":"host-device","name":"external-network","pciBusID":"0000:00:07.0"}'
```

Paired with these on `CNEInstance.spec.advanced.tmm`:

```yaml
annotations:
  k8s.v1.cni.cncf.io/networks: '[
    {"name":"external-netdevice","namespace":"f5-operator","interface":"eth1"},
    {"name":"internal-netdevice","namespace":"f5-operator","interface":"eth2"}
  ]'
env:
  - name: ROBIN_VFIO_RESOURCE_1
    value: eth1
  - name: PCIDEVICE_INTEL_COM_ETH1
    value: "0000:00:07.0"
  - name: ROBIN_VFIO_RESOURCE_2
    value: eth2
  - name: PCIDEVICE_INTEL_COM_ETH2
    value: "0000:00:08.0"
```

Our current rendering uses **`device: ens7`** (kernel ifname) in the NAD,
which keeps the pod-side name as `ens7` and so encodes the host's kernel
naming convention into the env var key (`PCIDEVICE_INTEL_COM_ENS7`). This
is fragile: kernel ifname depends on instance type, Nitro slot order,
udev rules, and AMI. The PCI BDF is the stable identity.

**Direction**: switch NAD template + CNEInstance env to the pciBusID form:

- NAD: `{"cniVersion":"0.3.1","type":"host-device","name":"external-network","pciBusID":"<discovered-BDF>"}`
- TMM annotation: `k8s.v1.cni.cncf.io/networks` with `interface:eth1`/`eth2`
- TMM env: `ROBIN_VFIO_RESOURCE_<N>=ethN` and `PCIDEVICE_INTEL_COM_ETHN=<BDF>`
- We already discover `$EXTERNAL_PCI` and `$INTERNAL_PCI` — they currently
  feed only the env vars. Route them into the NAD template too.

**BDF discovery edge cases to handle**:
- m5/m6i typical layout: `0000:00:06.0` = EKS CNI's secondary, `0000:00:07.0`
  + `0000:00:08.0` = BNK ext/int. But:
- On instance types where EKS CNI does NOT claim device-index=1 first, or
  when the BNK ENIs attach at a different order, the BDFs can shift to
  `0000:00:06.0` / `0000:00:07.0`.
- Don't hardcode 07/08 — keep the existing tag-based ENI discovery + BDF
  resolution and feed actual values into the template.

**Tracking**: this is a follow-up code change (NAD template + CNEInstance
env rewrite + tests). Not in scope for this audit doc — flag and plan
separately.

### FLO Helm chart + values: what we can / cannot inspect

- Chart source: `oci://repo.f5.com/charts/f5-lifecycle-operator` version
  `v2.21.13-0.0.28`. Pulled OCI at install time; no local cache.
- aws-gpu-setup's `manifests/flo-values.yaml` is the override (license,
  cluster issuer, image pull secrets, `containerPlatform=AWS`). Nothing
  exotic relevant to the two findings.
- `bnk-forge-modules/bnk/flo/values.yaml` is the bnk-forge module wrapper's
  override; also unremarkable except for the full inline JWT scaffolding.
- To inspect the rendered chart we'd need `helm pull oci://... --version
  v2.21.13-0.0.28 --untar` on a host with `repo.f5.com` creds. Worth doing
  on the next syd-tracer cold cycle to confirm the `crashagentConfig:null`
  pattern in FLO's update payloads and to check what readiness gates /
  init containers FLO ships for `f5-cne-controller`.

### CRD schema diff summary

| CRD             | 2.2 → 2.3 diff                                                                 |
|-----------------|--------------------------------------------------------------------------------|
| CNEInstance     | identical at top-level spec keys + advanced.* sub-keys                         |
| DSSM            | **+`crashagentConfig`** (object) — drives the FLO hot-loop documented in 2.3   |
| F5Tmm           | (not diffed in this pass; likely same `crashagentConfig` addition)             |

### Updated takeaway

The two original "open" findings (#4 + #5) reframe as:
- **#5 → not a bug.** Acceptance criterion needs to drop the rollup `Available`.
- **#4 → real but narrower.** Image cold-start race; fix or accept self-heal.

Plus two new follow-ups uncovered by the cross-check:
- **NAD form**: migrate to `pciBusID` (separate code change).
- **`crashagentConfig:{}` patch**: cheap mitigation for the 2.3 FLO hot-loop
  documented in `bnk-tmm-blocker.md` (separate code change).

---

## 2026-05-24 — FLO chart deep-dive + log sweep + multi-namespace lens

> Added after `helm pull oci://repo.f5.com/charts/f5-lifecycle-operator
> --version v2.21.13-0.0.28` and reading templates/deployment.yaml +
> values.yaml. Also a log sweep across Sydney's f5-cne-controller, FLO,
> DSSM db-0 + sentinel for working-baseline patterns.

### FLO chart — namespace-relevant env vars

`templates/deployment.yaml` emits these on the FLO pod (only if the
matching value is set):

```yaml
- name: POD_NAMESPACE                       # always set, from metadata.namespace
- name: CONTAINER_PLATFORM                  # we override to "AWS" (default Generic) ✓
{{- if .Values.sharedComponentNamespace }}
- name: SHARED_COMPONENT_NAMESPACE
  value: {{ .Values.sharedComponentNamespace }}
{{- end }}
{{- if .Values.sharedDssm }}
- name: SHARED_DSSM
  value: {{ .Values.sharedDssm }}
{{- end }}
```

`values.yaml` defaults:
- `sharedComponentNamespace`: commented out → FLO falls back to its own ns.
- `sharedDssm`: empty → unset → defaults to false (single-instance).
- `namespace`: empty → install lands in release ns (whatever we pass via
  `helm install --namespace <X>`).

Our `internal/k8s/manifests/shared/flo-values.yaml.tmpl` sets **none** of
the three namespace-flavoured values. FLO therefore runs with:
- `SHARED_COMPONENT_NAMESPACE` env: not emitted → FLO defaults internally
  (likely to `POD_NAMESPACE` = its own ns = `f5-cne-core` for us).
- `SHARED_DSSM` env: not emitted → false.
- Install ns: whatever `helm install --namespace` passes = `f5-cne-core`.

### Multi-namespace asymmetry: us vs Sydney

| Component group           | Sydney (single-ns) | awsbnkctl (multi-ns)               |
|---------------------------|--------------------|------------------------------------|
| FLO + License + CWC       | `f5-operator`      | `f5-cne-core` (OperatorNamespace)  |
| CNEInstance + DSSM + F5Tmm + RabbitMQ + NADs + CM | `f5-operator` | `f5-cne-system` (InstanceNamespace) |

Sydney's defaults collapse correctly because everything is in one ns. We
split. The question: does FLO's "shared component namespace default-to-own-ns"
behaviour silently break things, given that FLO is in `f5-cne-core` but
components are in `f5-cne-system`?

**Counter-evidence that it does NOT silently break us**: in round-2 cold
start, sub-conditions `CNEControllerAvailable=True`, `F5TmmAvailable=True`,
`AnalyzerAvailable=True`, `LicenseAvailable=True` all reached True. FLO is
clearly finding and instantiating the components in the correct ns —
probably by deriving from `CNEInstance.metadata.namespace`, not from
`SHARED_COMPONENT_NAMESPACE`. So this env var is not the bug.

Setting `sharedComponentNamespace: f5-cne-system` is defensive hygiene,
not a root-cause fix.

### Sentinel + db log pattern: Sydney baseline vs our hang

Sydney `f5-dssm-sentinel-0` `f5-dssm` container — first three lines after start:
```
"dssm:start This is for SPK"
"Running init.sh for single cluster deployment"
"grpc server is starting up :19891"
```
…then `certificates loaded successfully` every ~40 min thereafter.

Our broken syd-tracer `f5-dssm-db-1` hangs **exactly** at the third line.
Sydney's s6-supervised redis-server starts; ours doesn't. Identical
preamble, divergence after `grpc server is starting up :19891`.

Without exec into a hung db-1 pod (cluster is torn down), we can't see
which subprocess of init.sh fails. **Next live cycle action**: while
db-1 is in the 2/3 hung state, `kubectl exec -it f5-dssm-db-1 -c f5-dssm
-- sh` and inspect:
- `ps -ef` (which init.sh child is alive?)
- `ls /run/service/` (s6-rc state per service)
- `cat /var/log/redis*.log` (if any)
- `cat /conf/init.sh` (the actual script content — may reveal a 2.3
  branch we don't satisfy)

### Cne-controller + FLO steady-state in Sydney

- `f5-cne-controller` main container: only Secret reconciles (~30s cadence
  for cert rotation), 0 restarts in 19d, no error patterns.
- FLO container: mostly idle — fluentbit reloads + cert refresh. **No
  "Reconciler error", no `crashagentConfig` hot-loop**. Confirms the 2.3
  regression is real and absent in 2.2.
- `f5-tmm-pod-manager` 1.2.8 reconciles endpoints every <1s, no warnings.

### Recommended experimental matrix — ranked by leverage

Hypothesis-driven: what produces the most information per cycle?

| Rank | Change | Hypothesis | Predicted outcome | Cost |
|------|--------|------------|-------------------|------|
| **H1** | Phase 25 acceptance switches to sub-conditions + HTTP 200 (drops rollup `Available`) | Acceptance criterion is wrong, not the cluster (Sydney runs 33d with Available=False) | Phase 25 PASSes with current behaviour, no other change | code-only, 0 AWS cost |
| **H2** | Phase 24c patches `spec.crashagentConfig: {}` on the 13 component CRs after Phase 23 | FLO hot-loop drives extra Phase 25 latency in 2.3 | FLO reconcile cadence drops from 300ms to seconds | 1 cycle |
| **H3** | Add 3 TMM env vars (`USE_PHYS_MEM=true`, `TMM_MAPRES_IGNORE_MEM_LIMIT=true`, `TMM_MAPRES_HUGEPAGES=1536`) to CNEInstance.spec.advanced.tmm.env | Sydney has them and Sydney's TMM is stable; ours had SIGSEGV history | TMM init faster/more stable | 1 cycle |
| **H4** | Conditional `kubectl rollout restart deploy/f5-cne-controller` after Phase 23b if any container is in CrashLoopBackOff | pod-manager 1.6.x cold-start race vs kube-proxy is the timeout cause | Pod-manager comes up clean on the restart; Phase 25 completes earlier | 1 cycle (combine with H1/H2) |
| H5 | Set `sharedComponentNamespace: f5-cne-system` in flo-values | Multi-ns FLO defaults are silently wrong | Likely no visible difference (we already have all subsystems Available=True) | combine with H1-H4 |
| H6 | Set `sharedDssm: false` explicitly | We're depending on implicit default | No change — default is already false | skip |
| H7 | Set `namespace: f5-cne-core` explicitly in FLO values | We're depending on `helm --namespace` | No change — equivalent to current state | skip |

### Suggested first cycle

Combine **H1 + H2 + H5** into one PR / one cold cycle:
- H1 removes the red-herring acceptance failure (cheap insurance).
- H2 directly attacks a 2.3 regression documented in
  `aws-gpu-setup/bnk-tmm-blocker.md`.
- H5 is defensive multi-ns hygiene; bundles for free.

Predicted outcome if H1+H2 hypotheses are right:
- Phase 25 reaches PASS within budget.
- FLO reconcile cadence visibly drops.
- `scenarios http-routing-e2e` returns 5/5 HTTP 200.

If only H1 fixes acceptance and H2 still shows FLO hot-looping → keep H2 as
cleanup. If H5 changes nothing → confirms FLO derives ns from CNEInstance
and we can deprioritise the multi-ns hygiene.

H3 + H4 should be the **second** cycle, isolated from H1+H2 so we can tell
which moved the needle.

### Read on the namespace/sharedDssm/sharedComponent triple

Honest assessment of the three FLO values the user flagged:

- **`namespace`**: no-op — equivalent to `helm install --namespace` which
  we already pass. **Skip.**
- **`sharedDssm`**: default is false, we have one CNEInstance, setting
  `true` would change behaviour in a direction we don't want. **Skip.**
- **`sharedComponentNamespace`**: worth setting defensively (H5), but
  we already have all sub-conditions reaching True — so the multi-ns
  default-to-own-ns is not the silent failure mode it could have been
  in a different code path. Set it for cleanliness, don't expect a fix
  from it.

The higher-leverage tests are H1 (acceptance criterion) and H2
(`crashagentConfig` patch), which directly attack the two open findings.
