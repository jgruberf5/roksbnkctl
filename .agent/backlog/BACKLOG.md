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

## Done

```yaml
# No completed items
```
