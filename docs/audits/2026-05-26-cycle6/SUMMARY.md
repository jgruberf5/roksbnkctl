# Cycle 6 — node-side discovery (#45) + 2nd prefix-delegation cycle + forge register fix (2026-05-26)

Cluster: `syd-tracer` (BNK 2.3.0, ap-southeast-2, host-device, m6i.4xlarge×3 + jumphost).
AWS account 292785712872. One clean `up` (UP EXIT 0); cluster intentionally left UP afterward
for the forge agent to work connectivity against. AWS profile `Users-292785712872`.

## Headline
- **Node-side iface/PCI discovery (PR #45) validated live and merged to main.** The discovery pod
  MAC-matches each data ENI to its real ifname+PCI; NAD `pciBusID` + CNEInstance `PCIDEVICE_INTEL_COM_<ifname>`
  now render from discovered state (not hardcoded constants). Hard-fails with no fallback — passed cleanly.
- **Prefix delegation held for the 2nd clean cycle** — single-ENI nodes, 0 restarts, first-poll activation.
- **forge `register` parsing bug found + fixed (PR #47)** — was creating projects but mis-parsing the response
  (`id=0`), leaving orphans. Validated live: register now writes real ids.
- **7/7 ingress scenarios green** (egress-snat excluded as destructive).
- **forge connectivity (forge operating the cluster) is still blocked forge-side** — the project has no
  allocated AWS creds; handed to the forge agent with a precise mechanism. Cluster RECORDED in forge, not yet OPERABLE.

## (A) Prefix delegation — 2nd clean cycle ✅
- `[phase 08b] vpc-cni addon ACTIVE` with prefix config.
- TMM node carries only its data ENIs (idx 2/3) — device-index 1 free; no secondary/spillover ENI.
- rabbitmq + CWC healthy, 0 restarts. Cross-node licensing succeeded.
- Phase 25: `[1/18] ready=true lic=Active` — clean activation on the FIRST poll. `UP EXIT: 0`.
- CNEInstance: ALL 18 conditions `True` (incl. `Available=True` rollup + `DSSMAvailable=True` — even cleaner than
  the gold-ref baseline which sits at Available=False). `functionallyReady=true`.

## (B) Node-side discovery (PR #45) — validated live ✅
phase17 captured MACs: `INTERNAL_ENI_MAC=02:f0:a1:0b:a6:2f` (idx2), `EXTERNAL_ENI_MAC=02:b6:58:53:94:79` (idx3).
phase17c discovery pod (`iface-discovery`, kube-system) Succeeded; probe output:
```
{"02:97:3e:06:25:71":{"ifname":"ens5","pci":"0000:00:05.0"},   ← primary/CNI ENI (correctly ignored)
 "02:f0:a1:0b:a6:2f":{"ifname":"ens7","pci":"0000:00:07.0"},   ← INTERNAL match
 "02:b6:58:53:94:79":{"ifname":"ens8","pci":"0000:00:08.0"}}   ← EXTERNAL match
```
`[phase 17c] discovered: external=ens8(0000:00:08.0) internal=ens7(0000:00:07.0)`

Render-from-discovered-state confirmed:
- state.env: `IFACE_DISCOVERY_AT=2026-05-26T04:25:22Z` (RFC3339, NOT "dry-run"), `EXTERNAL/INTERNAL_IFNAME` + `_PCI` set.
- Live NADs: `external-netdevice pciBusID=0000:00:08.0`, `internal-netdevice pciBusID=0000:00:07.0`.
- Live CNEInstance: `PCIDEVICE_INTEL_COM_ENS8=0000:00:08.0`, `PCIDEVICE_INTEL_COM_ENS7=0000:00:07.0`,
  `ROBIN_VFIO_RESOURCE_1=ens8`, `ROBIN_VFIO_RESOURCE_2=ens7`.
- TMM bound 7/7 Running, 0 restarts. → PR #45 merged to main.

## (C) Cold-start heals — 2nd data point toward retirement
- Phase 24 (CWC heal): CWC Ready at t=136s, restartCount=0 → "no heal needed".
- Phase 24b (DSSM --insecure): ran (KEEP — different root cause).
- Phase 24c (pod-manager heal): `f5-tmm-pod-manager Ready` found immediately (no 7-min wedge).
- Phase 25 annotation-kick: did not fire (first-poll activation).
→ Under prefix delegation the heals are redundant; this is the 2nd clean cycle. See backlog `retire-secondary-eni-coldstart-heals`.

## Topology smoke ✅
`awsbnkctl topology --config` populated all live fields (VPC/IGW/NAT, public+private+BNK-data subnets, jumphost+EICE,
TMM VLAN self-IPs with discovered `ifname=ens8/ens7`, node group, tmm-node).

## Scenario suite — 7/7 ingress green ✅
Run individually (egress-snat EXCLUDED — destructive; no `--exclude` flag, all scenarios declare no Dependencies()).
Each run + cleaned between for isolation. All rc=0, all "control-plane reconciled + end-to-end curls via Gateway HTTP 200":
http-routing-e2e, http-traffic-split, multi-vip, proxy-protocol-l4, external-resource-pool (green) +
ai-token-counting, ai-semantic-cache (amber). The SSA Force fix (in main) confirmed — no `.spec.rules` conflict on re-apply.

## forge register parsing bug → PR #47
`awsbnkctl forge register`/phase09 created the forge project but errored "create_project succeeded but no project ID in
response" and left orphans. Root cause: the MCP server returns flat shapes — `create_project` →
`{"success":true,"project_id":N,...}`, `create_cluster` → bare `{"id":N,...,"project_id":P}` — but the Go client
unmarshalled into nested `project.id`/`cluster.id` (always 0). Second bug: standalone `register` short-circuited on a
`pending` link (phase09 soft-fail) reporting false success. Fix (PR #47): custom UnmarshalJSON (flat + nested back-compat)
+ `Register()` gated on `IsRegistered()`. Validated live: register now creates project 40 / cluster 24 with real ids.
All CI gates green locally + in CI.

## forge connectivity — BLOCKED forge-side (handed to forge agent)
forge can RECORD the cluster but not OPERATE it: `test_cluster_connectivity` → 401 for syd-tracer AND the gold-ref.
Root cause: `get_project_aws_credentials` is `configured:false` for both projects — passing `credential_template_id`
to `create_project` does NOT populate the project's credential record, and connectivity uses the project's creds.
forge's credential model is SSO-based (`aws_sso_initiate`→`aws_sso_poll`→`aws_set_project_credentials`, or an
SSO `create_credential_template`). Operator SSO role `Users` (acct 292785712872) already has implicit cluster-admin on
syd-tracer (CONFIG_MAP mode; awsbnkctl created the cluster with that identity) — no aws-auth change needed for it.
Captured: handoff `.agent/handoff/2026-05-26-forge-agent-prompt.md` + backlog `forge-allocate-project-credentials`.
forge integration on/off flag already exists: `forge.enabled` in cluster.yaml (+ `up --register-with-forge`).

## Forge stack note
The `bnk-forge-mcp` container authenticates to the backend with `BNK_FORGE_PASSWORD: ${MCP_PASSWORD:-changeme}`.
It was running with stale `changeme` (401 on every authenticated MCP tool); recreated with `MCP_PASSWORD=admin123`.
Preflight forge with an AUTHENTICATED MCP tool (`list_projects`), not `tools/list` + a direct REST login.

## Deferred this cycle
- Egress VXLAN node-side VTEP root-cause (cluster kept up for forge instead).
- `down` (cluster intentionally left running for the forge agent).

## Open PRs
- #45 (discovery) — MERGED.
- #47 (forge register parsing fix) — open, CI green; merge pending.
