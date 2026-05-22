# Slice-09 — aws-gpu-setup audit

**Author:** Lead (post-failed-live-retest 2026-05-22)
**Trigger:** Slice-08 live retest reached Phase 25 activation poll, timed out at [40/40] with `cne="" lic=""`. Root cause traced to Multus error `failed to find host device: Link not found` on `ens7/ens8`, which exposed an unaudited AMI-type decision.
**Process commitment:** per `[[feedback-systematic-aws-gpu-setup-audit]]`, every port-from-aws-gpu-setup slice now starts with a line-by-line audit doc instead of an ad-hoc translation.

---

## 1 · Scope

Audit the slice of `aws-gpu-setup` that produces the **worker node OS environment** (AMI choice + kernel-level networking knobs) that our **Phase 10 nodegroup + Phase 17 secondary-ENI + Phase 19 cloud-network-mapping + Phase 20 NADs** rely on.

The activation chain doesn't care what the host OS is — but `Multus host-device CNI` does, because it looks up Linux interface names. AL2 names secondary ENIs `eth1..ethN`. AL2023 (predictable naming) names them `ens5..ensN`. Our entire downstream stack (Phase 17 attaches at device-index 2/3 expecting `ens7/ens8`; Phase 19 hard-codes `ens7/ens8` in the cloud-network-mapping; Phase 20 NADs reference `ens7/ens8`) is built around the AL2023 naming. **Phase 10 was creating AL2 nodes** — exclusively because we ported the API calls without auditing the AMI constraint.

---

## 2 · Two-column line-by-line mapping

### 2.1 `aws-gpu-setup/vars.env` — constants the up flow assumes

| aws-gpu-setup line | Our equivalent | Status |
|---|---|---|
| `vars.env:92` `EXTERNAL_IFNAME="ens8"  # BNK_EXT kernel ifname on TMM host (AL2023, device-index 3)` | `internal/aws/phases/phase19_cloud_network_mapping.go` hard-codes `EXTERNAL_IFNAME=ens8`; cluster.yaml has no field for it | **✓ matched** — depends on AL2023 (see §2.3 GAP-1) |
| `vars.env:93` `INTERNAL_IFNAME="ens7"  # BNK_INT kernel ifname on TMM host (AL2023, device-index 2)` | `internal/aws/phases/phase19_cloud_network_mapping.go` hard-codes `INTERNAL_IFNAME=ens7` | **✓ matched** — depends on AL2023 (see §2.3 GAP-1) |
| `vars.env:94-95` `EXTERNAL_PCI="0000:00:08.0"` / `INTERNAL_PCI="0000:00:07.0"` | Phase 19 state keys `EXTERNAL_PCI/INTERNAL_PCI` set to same values | **✓ matched** — also AL2023-predictable BDF mapping |
| `vars.env:105 (template)` comment: `AL2023 predictable naming: device-index 0,1,2,3 → ens5,ens6,ens7,ens8` | (no comment in our code surfaces this — buried in Phase 17 doc string) | **△ trace** — comment should land in Phase 10 next to AmiType |
| `vars.env:109` `BNK_WORKER_INSTANCE_TYPE="m6i.4xlarge"  # MINIMUM for BNK 2.3 Small` | cluster.yaml `cluster.nodeGroups[0].instanceType` | **✓ matched** |
| `vars.env:110` `BNK_WORKER_COUNT="3"  # ≥3 for dSSM quorum (§9 F9)` | cluster.yaml `desiredSize: 1` (live-test value) | **✗ DEFERRED** — see §3 future-slice |

### 2.2 `aws-gpu-setup/up.sh` Phase 11 — Launch Template + create-nodegroup

| aws-gpu-setup line | Our equivalent | Status |
|---|---|---|
| `up.sh:388` `banner "[11/18] Launch template + nodegroup..."` | `internal/aws/phases/phase10_nodegroup.go:Phase10NodeGroup` | **✓ matched** |
| `up.sh:389-403` Launch Template idempotent describe-by-name | `phase10_nodegroup.go:ensureLaunchTemplate` (Describe → reuse on hit) | **✓ matched** |
| `up.sh:405-411` LT userdata comment: `Secondary ENIs attached in phase 14 (BNK_INT→ens7, BNK_EXT→ens8) come up DOWN on AL2023 and need to be UP on the host BEFORE the TMM pod starts` | `phase10_nodegroup.go:bnkLaunchTemplateUserData` block lines 22-46 (port-comment matches) | **✓ matched** |
| `up.sh:413-433` LT userdata: sysctl `fs.inotify.max_user_watches=1048576` + udev rule for ENA driver + boot-time bring-up loop | `phase10_nodegroup.go:bnkLaunchTemplateUserData` constant (same script) | **✓ matched** |
| `up.sh:434-438` Comment: `MetadataOptions.HttpPutResponseHopLimit=2: EKS-optimized AMIs default to IMDSv2 with hop=1, which blocks pods from reaching 169.254.169.254 (containers are 2 hops away via the pod network). Without hop=2 the EBS CSI controller (and any other workload using IMDS-based credentials) fails with "no EC2 IMDS role found".` | `phase10_nodegroup.go:ensureLaunchTemplate` (search for `HttpPutResponseHopLimit`) | **✓ matched** — value 2 set on LT |
| `up.sh:439-442` `aws ec2 create-launch-template ... --launch-template-data '{...,"MetadataOptions":{"HttpTokens":"required","HttpPutResponseHopLimit":2,"HttpEndpoint":"enabled"}}'` | `phase10_nodegroup.go:ensureLaunchTemplate` Go SDK call with same MetadataOptions | **✓ matched** |
| `up.sh:454-461` `aws eks create-nodegroup --cluster-name ... --launch-template "id=$LT_ID,version=\$Latest" --labels role=bnk` — **no `--ami-type` argument** | `phase10_nodegroup.go:260` `AmiType: ekstypes.AMITypesAl2X8664` — **explicitly forces AL2** | **✗ GAP-1 (slice-09 target)** |

**Why the gap was missed earlier.** When the aws-gpu-setup `create-nodegroup` call omits `--ami-type`, AWS picks the EKS-default AMI for the cluster version. On EKS 1.30 with a launch-template binding that has no `ImageId`, the empirically-observed default for aws-gpu-setup deploys was AL2023 (see `aws-gpu-setup/SESSION_FINDINGS_2026_05_19_part3.md:23`, `..._part4.md:19`, `..._part5.md:40` — all explicitly note "AL2023, m6i.4xlarge × 3"). Our Go port translated the EKS Go SDK call from the documented examples — those examples explicitly set `AmiType: AMITypesAl2X8664` — and we kept that value without checking the aws-gpu-setup downstream constraint (`ens*` names hard-coded in Phase 19).

### 2.3 GAP-1 details

**Symptom in live retest.** After Phase 10 produced AL2 nodes, kubelet named secondary ENIs `eth1, eth2, eth3`. Phase 17 attached at device-index 2 and 3 (correct semantics) but the device names AL2 assigned were `eth2` and `eth3`, not `ens7/ens8`. Phase 19's cloud-network-mapping ConfigMap declared `EXTERNAL_IFNAME=ens8` / `INTERNAL_IFNAME=ens7`. Phase 20 NADs referenced those names. Multus could not resolve `ens7` or `ens8` to a host device → `failed to find host device: Link not found` → `f5-tmm` Init container stuck → CNEInstance never reached `Ready` → Phase 25 timed out at [40/40].

**Cascading symptom.** Several supporting pods (`f5-observer-0`, `f5-observer-operator`, `f5-observer-receiver`, `f5-spk-cwc`, `f5-dssm-db-0`, `f5-dssm-sentinel-0`) stayed `Pending` with `Insufficient cpu` on the single m6i.4xlarge node. This is the **secondary** issue and may resolve when AL2023 nodes come up with their slightly different system-pod CPU footprint, OR it may need the BNK_WORKER_COUNT=3 fix from §2.1 row 6. To be re-evaluated AFTER GAP-1 is fixed.

**Fix.** `internal/aws/phases/phase10_nodegroup.go:260` change `AmiType: ekstypes.AMITypesAl2X8664` → `AmiType: ekstypes.AMITypesAl2023X8664Standard`. Add an inline comment citing this audit doc + the aws-gpu-setup vars.env constraint chain (`EXTERNAL_IFNAME=ens8 (AL2023 device-index 3)`). Update the Phase 10 unit test that asserts AmiType.

---

## 3 · Out-of-scope for this slice — captured for future slices

| Item | aws-gpu-setup source | Constraint | Defer to |
|---|---|---|---|
| `BNK_WORKER_COUNT=3` for dSSM quorum | `vars.env:110` | "≥3 for dSSM quorum (§9 F9)" | slice-09.1 (after AL2023 live-validated) |
| SelfIP assignment on each secondary ENI (`ec2:AssignPrivateIpAddresses`) | `up.sh:577-590` (and F5 Multi-AZ PDF p.9) | "AWS won't route SelfIPs to the ENI unless they're also listed as secondary IPs on the ENI" | slice-10 (data-plane plumbing) |
| `F5SPKVlan` + `GatewayClass` + `test-gateway` + `test-nginx` manifests | `aws-gpu-setup/manifests/{f5spkvlan,gatewayclass,test-gateway,test-nginx}.yaml` | Needed for `awsbnkctl test` traffic to have a target | slice-10 |
| Forge `credential_template_id` wired through `restCreateProject` | (no aws-gpu-setup equivalent; forge feature) | Forge k8s UI 500s without it | side-quest already tracked |

---

## 4 · Acceptance for slice-09 PR

- [x] Audit doc captures GAP-1 with line-level citations.
- [ ] `phase10_nodegroup.go:260` AmiType changed to `AMITypesAl2023X8664Standard`.
- [ ] Inline comment at the AmiType line cites `docs/audits/slice-09-aws-gpu-setup-audit.md §2.3`.
- [ ] Phase 10 unit test asserts the new AmiType value.
- [ ] CI green.
- [ ] Live retest validates: secondary ENIs named `ens7/ens8`, Multus resolves them, `f5-tmm` Init container proceeds. (If Pending pods still show `Insufficient cpu`, surface §3 row 1 for a follow-up.)
