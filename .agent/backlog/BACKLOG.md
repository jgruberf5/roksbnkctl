# Backlog

Agents pick highest priority `ready` item and work until complete.

## Current

```yaml
current: null
```

## Queue

```yaml
- id: slice-13-followup-verify-order-test
  title: "Add recording-fake pin test for scenario Verify call order"
  source: "slice-13 reviewer-r1 finding F-1"
  why: "Verify order (control-plane → ResyncHTTPRoutes → curl) is load-bearing per project_pool_member_sync_root_cause; today only the code shape enforces it. A future refactor could silently reorder. Add a recording fake that asserts the sequence."
  scope: "internal/scenarios/httproutee2e/scenario_test.go + small refactor extracting Verify steps into named methods for mock-friendly call order assertion."
  size: small
  status: ready

- id: slice-14-port-second-scenario
  title: "Port a second scenario (tcp-l4-lb or grpc-route) from kindbnkctl"
  source: "slice-13 PRD §'Follow-up slices'"
  why: "Wire up `awsbnkctl scenarios run --all` topo-sort (currently a stub returning 'not implemented' for >1 scenario). Second scenario also exercises the framework's manifest templating + cleanup paths on different CRD shapes."
  scope: "internal/scenarios/<name>/ — port from /tmp/kindbnkctl-scenarios upstream + adapt to AWS host-device data path."
  size: medium
  status: ready

- id: preflight-cluster-yaml-validation
  title: "Expand phase 00 preflight to catch all common cluster.yaml mistakes before any AWS resource is created"
  source: "user feedback 2026-05-24 — once users author their own cluster.yaml, slow-fail surprises waste 25 min of provisioning before the bad setting surfaces"
  why: "Slice-13 added one preflight check (host-device ENI capacity, m5.xlarge minimum). Same pattern applies to other settings: BNK credential file paths, AZ availability, instance type availability in the chosen AZ, EBS gp3 quota, EIP/NAT/VPC service quotas, EKS supported Kubernetes versions, mgmtSubnetIndex validity, JWT readability. Each check is cheap (file stat or one API call) and prevents a costly fail-deep-in-up."
  scope: |
    Extend internal/aws/phases/phase00_preflight.go with composable checks:
      - checkBNKCredentialFiles — os.Stat both farArchive and jwt paths; report missing files.
      - checkAZAvailability — DescribeAvailabilityZones; assert every az listed in cluster.network.azs and network.dataPath.*.az is "available" in the account.
      - checkInstanceTypeAvailability — DescribeInstanceTypeOfferings (LocationType=availability-zone, Filters=instance-type) for nodeGroups[*].instanceType in nodeGroups[*].subnet's AZ. Some types are unavailable in some AZs.
      - checkKubernetesVersionSupported — DescribeAddonVersions or hardcoded supported-version set; refuse versions that EKS no longer creates.
      - checkServiceQuotas (best-effort, warn-only) — vpc count, eip count, nat-gateway count, m5.xlarge vCPU quota.
      - checkJumphostMgmtSubnetIndex — when testing.jumphost.enabled, mgmtSubnetIndex must be a valid index into network.subnets.public.
    Each check fails fast with the specific cluster.yaml line/field to fix.
    Add a `--skip-preflight=name1,name2` flag for advanced operators who know what they're doing (rare; default behavior must be safe).
  size: medium
  status: ready
```

- id: egress-vxlan-route-scoping-guard
  title: "Stop egress-snat (F5SPKEgress vxlan.create) from hijacking the whole pod network + breaking ingress"
  source: "cycle-5 live finding 2026-05-26 (docs/audits/2026-05-26-prefix-delegation-cycle/SUMMARY.md; memory project_egress_vxlan_blocker)"
  why: "Applying the egress-snat scenario makes TMM install a broad 10.0.0.0/17 dev vxlan100 route; the node-side vxlan100 VTEP never comes up (CSRC 'Link not found'), so ALL TMM->in-cluster-pod traffic is black-holed — which broke http-routing-e2e in `scenarios run --all`. egress-snat is currently destructive."
  scope: |
    - Exclude egress-snat from `scenarios run --all` until its data path works (or gate it behind an explicit opt-in), so it can't break the other scenarios.
    - Investigate scoping the egress route to the egress namespace's pod(s) instead of the whole pod CIDR (clouddocs 2.3 pseudoCNIConfig.namespaces semantics — do NOT guess).
    - Distinct from the node-side VTEP root-cause (that's the egress-vxlan-snat task itself, now blocked).
  size: small
  status: ready

- id: retire-secondary-eni-coldstart-heals
  title: "Retire the secondary-ENI-era cold-start heals (Phase 24c + Phase 24 + Phase 25 kick) now that prefix delegation fixes the shared root cause"
  source: "cycle-5 finding 2026-05-26; reframes memory project_pod_manager_image_regression + see project_cni_prefix_delegation_fix"
  why: "The secondary-ENI black-hole was ONE root cause wearing three masks, each band-aided separately: (24c) pod-manager 'self-heals in HOURS' reaching the API ClusterIP; (24) CWC 'DNS-warmup' crash loop (likely couldn't reach coredns/rabbitmq ClusterIP, mislabeled); (25-kick) CNEInstance Available sub-conditions staying stale (a component was unreachable). Prefix delegation (PR #44) fixes the root → all three are now redundant. Phase 24c's UNCONDITIONAL 7-min poll is the only one with real cost (~7 min/up); the 25-kick is cheap + conditional (didn't fire in cycle-5)."
  scope: |
    Validate over 1-2 more clean prefix-delegation cycles that each heal never fires, then:
    - Phase 24c (HIGHEST VALUE): early-exit on first Ready or remove — kills the ~7 min/up overhead. Fix its doc comment (the severe wedge was the secondary-ENI black-hole, NOT a kube-proxy timing race).
    - Phase 24 CWC heal: slim or remove (its 'DNS-warmup' trigger was likely the same black-hole).
    - Phase 25 annotation-kick: lowest priority (cheap + conditional); remove only once confident, else keep as insurance.
    KEEP (different root causes, still needed): Phase 24b (DSSM --tls --insecure cert-trust), pkg/bnk.ResyncHTTPRoutes (pool-member reconcile gap), phase17 src/dest-check disable (required for TMM).
  size: small
  status: ready

- id: validate-iface-discovery-live
  title: "Live-validate node-side MAC iface/PCI discovery (PR #45) on the next up"
  source: "PR #45 (feat/host-device-pcibusid-discovery) — built + unit-tested but NOT yet live-validated"
  why: "pciBusID NADs are validated (cycle 5), but Phase17cIfaceDiscovery (the discovery pod + state population + render-from-state) only validates against a real node. Hold PR #45 (draft) until an up confirms: discovery pod Succeeds, MACs match, NAD/CNEInstance render the discovered values, and TMM binds + ingress green."
  scope: |
    Rebuild the binary with PR #45, run an up, confirm the 4 above, then mark PR #45 ready. Also validate forge e2e end-to-end with the new creds (PR #43 + AWSBNKCTL_FORGE_PASSWORD=admin123 / current forge password).
  size: small
  status: done   # cycle 6 (2026-05-26): all 4 confirmed live, PR #45 merged to main. See docs/audits/2026-05-26-cycle6/SUMMARY.md. (forge e2e split out → forge-allocate-project-credentials)

- id: forge-unattended-sso-refresh-token
  title: "Capture an SSO refresh token for forge (and awsbnkctl's own long-run SDK creds) so up --auto / down stay non-interactive"
  source: "cycle-6 forge RETURN-handoff 2026-05-26 (.agent/handoff/2026-05-26-forge-RETURN-to-awsbnkctl.md) — supersedes the old forge-allocate-project-credentials item"
  why: |
    Forge-side connectivity is now FIXED (forge agent: credential binding + ARN→bare-name EKS token; test_cluster_connectivity cluster 24 → success v1.30). The remaining piece is ONE awsbnkctl/IdC action: the is_default credential template has `can_refresh:false` (NO refresh token), and its exchanged AWS keys expire (~12h). After expiry forge can't re-mint non-interactively and EKS 401s until an interactive SSO re-auth. SAME root cause bit the cycle-6 teardown: a long `awsbnkctl down` stranded at phase 18 because the Go SDK couldn't refresh the expiring SSO token (the AWS CLI could, the SDK could not). One fix addresses both.
  scope: |
    - One-time SSO bootstrap must capture a REFRESH token: request `offline_access` scope / ensure the IdC permission set issues refresh tokens, AND target the credential TEMPLATE (POST /api/credential-templates/{id}/authenticate-sso + poll), NOT the project record (template-backed projects authenticate via the template's keys; forge's lazy refresh operates on the template).
    - If the IdC permission set does NOT issue refresh tokens for this account/role, unattended is impossible — `up --auto` must prompt for SSO re-auth or pre-flight a fresh token each run.
    - For awsbnkctl's OWN SDK creds (long up/down): make the SSO credential provider use the refresh-token flow, or pre-flight a fresh/longer session, so a multi-phase down doesn't strand resources mid-run (D-005 sentinel catches expired-at-entry, not expires-mid-run / SDK-can't-refresh).
    - Optional (forge now tolerates both, NOT required): forge register could pass credential_template_id to create_project; create_cluster could store the bare cluster name as context. Issue 2 (response envelope) is ALREADY handled by PR #47's dual-shape client — no change needed.
  size: medium
  status: ready

# RESOLVED 2026-05-27 — verified ALREADY DONE: internal/doctor no longer gates terraform
# (doctor.go only checks kubectl + helm, both internalised; comment literally says "Terraform is gone")
# and `grep hashicorp/terraform internal/` = 0 hits. The functional fix landed in a prior PR; the
# cosmetic comment/brand tail merged in PR #62 (staging 55f7bd2). No further action.
- id: doctor-exec-terraform-residue
  title: "Remove Terraform from internal/doctor (required-binary gate) + internal/exec (image pins) — cleanup PR7"
  source: "found during cleanup PR4 2026-05-26 (status rewrite); not in the original audit chunk plan"
  why: "After the TF removal (PRs #50/#51/#52), internal/doctor STILL gates `terraform` as a REQUIRED host binary — so `awsbnkctl doctor` would flag missing terraform that's no longer needed (a real post-cleanup bug). internal/exec carries hashicorp/terraform:1.5.7 docker image pins that are now dead."
  scope: |
    - internal/doctor: drop `terraform` (and re-evaluate `helm`) from the required-host-binary gate; fix doctor help text.
    - internal/exec: remove the hashicorp/terraform image pins (~48 refs) + any TF-specific backend wiring.
    - Verify `awsbnkctl doctor` is green on a box without terraform installed.
  size: medium
  status: ready

- id: book-retarget-from-ibm-roks
  title: "Retarget book/src/ user guide from IBM-ROKS/Terraform to AWS/EKS (M5 sprint)"
  source: "2026-05-27 doc truth-up session — book/src/ is still the un-ported roksbnkctl book"
  why: "CLI help + README point operators at https://JLCode-tech.github.io/awsbnkctl/book/, but book/src/ is still the upstream IBM-Cloud guide: chapters 02-why-roks, 03-what-roksbnkctl-does, 13-terraform-variables, 29-terraform-variable-reference, 32-extending-roksbnkctl; 'terraform' in ~12 files, 'roksbnkctl' in ~10, IBM/ROKS in ~8. A fresh AWS operator following the published book gets IBM-Cloud/Terraform instructions."
  scope: |
    Rewrite book/src/ chapter-by-chapter for AWS/EKS: rename roksbnkctl->awsbnkctl, ROKS->EKS, drop/replace the Terraform chapters (13, 29) with the Go-SDK phased model, rewrite 02-why-roks, retarget cluster/credentials/backends chapters, update SUMMARY.md. Verify the gh-pages publish target (.github/workflows/book.yml) lands at JLCode-tech.github.io. Matches the never-completed M5 sprint in docs/PLAN.md.
  size: large
  status: ready

## Done

```yaml
# No completed items
```
