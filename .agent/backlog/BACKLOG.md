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

- id: forge-allocate-project-credentials
  title: "Make forge registration credentialed (e2e) so forge can operate the cluster, not just record it"
  source: "cycle-6 live finding 2026-05-26 + user direction; docs/audits handoff .agent/handoff/2026-05-26-forge-agent-prompt.md"
  why: "Phase09/`forge register` creates the project + cluster (validated, PR #47) but passing credential_template_id to create_project does NOT populate the project's AWS credential record — get_project_aws_credentials stays configured=false, so forge 401s on every EKS call (test_cluster_connectivity). The cluster is only RECORDED, not OPERABLE. forge's model is SSO-based (aws_sso_initiate→poll→aws_set_project_credentials, or an SSO credential_template)."
  scope: |
    Gated by the existing forge.enabled flag (cluster.yaml; no new flag needed — confirm whether a top-level `up` CLI flag is also wanted):
    - After create_project/create_cluster, allocate working AWS creds to the project so connectivity succeeds. Options:
      (a) call aws_set_project_credentials with the operator SSO identity (account_id, role_name=Users, region, SSO access token), or
      (b) create/allocate an SSO credential_template (aws_sso_enabled, aws_sso_account_id, aws_sso_role_name) and bind it to the project.
    - The SSO device flow (aws_sso_initiate→poll) is INTERACTIVE (browser approval) — `up --auto` is non-interactive, so design how creds are obtained unattended (reuse operator SSO cache token? aws_assume_role with forge's base creds? pre-flighted token?). This is the key design question.
    - Coordinate the forge-side half with the forge agent (handoff prompt): create_project+template should populate project creds, or connectivity should fall back to the default template.
    - syd-tracer is CONFIG_MAP auth mode; role Users (operator SSO) already has implicit admin, so no aws-auth change needed for that identity.
  size: medium
  status: ready

## Done

```yaml
# No completed items
```
