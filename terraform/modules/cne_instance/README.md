# BIG-IP Next for Kubernetes on IBM ROKS — BNK Deployment build 2.3.0-ehf-2-3.2598.3-0.0.17

## About This Workspace

This Schematics-ready Terraform workspace deploys CNEInstance Gateway API Class onto an **existing** IBM Cloud ROKS (OpenShift) cluster. It does not create cluster infrastructure — provide the name or ID of a running ROKS cluster and the workspace installs the CNEInstance component. cert-manager and FLO (F5 Lifecycle Operator) must already be deployed on the cluster before applying this workspace.

### Target Cluster Variables

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `roks_cluster_name_or_id` | Name or ID of the existing OpenShift ROKS cluster | REQUIRED | `my-openshift-cluster` |

Get your existing cluster name or ID:
```bash
ibmcloud ks clusters --provider vpc-gen2
```

### Deployment Variables for BIG-IP Next for Kubernetes

#### F5 Lifecycle Operator (FLO) Output Variables

These variables pass FLO deployment outputs into the CNEInstance configuration.

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `flo_namespace` | Namespace where FLO is deployed | Optional | `f5-bnk` (default) |
| `flo_utils_namespace` | Namespace for F5 utility components | Optional | `f5-utils` (default) |
| `flo_f5_bigip_k8s_manifest_version` | Version of f5-bigip-k8s-manifest chart | Optional | `2.3.0-3.2598.3-0.0.170` (default) |
| `flo_trusted_profile_id` | IBM IAM Trusted Profile ID for provisioning VPC routes | Optional | |
| `flo_cluster_issuer_name` | mTLS certificate issuer name | Optional | `sample-issuer` |
| `flo_far_repo_url` | FAR Repository URL for Docker and Helm registry | Optional | `repo.f5.com` (default) |

#### Deploy CNE Instance as a Gateway Provider

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `cneinstance_deployment_size` | Deployment size for CNEInstance | Optional | `Small` (default) |
| `cneinstance_gslb_datacenter_name` | GSLB datacenter name for CNEInstance | Optional | |
| `cneinstance_network_attachments` | Multus Network Attachment Definitions for TMM deployments | Optional | `["ens3-ipvlan-l2", "macvlan-conf"]` (default) |

## Project Directory Structure

```
ibmcloud_schematics_bigip_next_for_kubernetes_2_3_cneinstance/
├── main.tf                    # Root module configuration
├── variables.tf               # Root module variables
├── outputs.tf                 # Root module outputs
├── providers.tf               # Provider configuration
├── data.tf                    # Data sources (cluster, VPC)
├── terraform.tfvars.example   # Example variable values
├── modules/
│   └── cneinstance/           # CNEInstance deployment module
│       ├── main.tf            # CNEInstance custom resource
│       ├── variables.tf       # CNEInstance variables
│       ├── outputs.tf         # CNEInstance outputs
│       └── terraform.tf       # CNEInstance provider requirements
```

## Module Dependency Chain

```
┌──────────────────────────────────┐
│  DATA SOURCES                    │
│  (Existing IBM Cloud Resources)  │
│                                  │
│  - Existing ROKS Cluster         │
│  - Cluster VPC (auto-discovered) │
└─────────────┬────────────────────┘
              │ (provides kubeconfig + VPC info)
              ▼
┌──────────────────────────────────┐
│  PRE-REQUISITES (external)       │
│  Must be deployed before apply   │
│                                  │
│  - cert-manager                  │
│  - FLO (F5 Lifecycle Operator)   │
└─────────────┬────────────────────┘
              │ (FLO deployed, CNEInstance CRD ready)
              ▼
┌──────────────────────────────────┐
│  CNEINSTANCE                     │
│  (CNEInstance Deployment)        │
│                                  │
│  - CNEInstance Custom Resource   │
│  - privileged SCC bindings       │
│  - Pod Health Validation         │
└──────────────────────────────────┘
```

## Local Host Installation & Deployment

### Prerequisites

1. An existing IBM Cloud ROKS (OpenShift) cluster with cert-manager and FLO already deployed
2. Copy `terraform.tfvars.example` to `terraform.tfvars` and fill in your values
3. Run `terraform init` to initialize all modules

Get your cluster name or ID:
```bash
ibmcloud ks clusters --provider vpc-gen2
```

### Deployment

```bash
terraform init
terraform plan
terraform apply -auto-approve
```

### Cleanup

```bash
terraform destroy -auto-approve
```

## Configuration

### Module-Level Variables

#### CNEInstance Module
- `enabled`: Enable/disable module (default: `true`)
- `cneinstance_deployment_size`: Deployment size — `Small`, `Medium`, or `Large` (default: `Small`)
- `cneinstance_gslb_datacenter_name`: GSLB datacenter name (optional)
- `cneinstance_network_attachments`: Multus Network Attachment Definitions for TMM (default: `["ens3-ipvlan-l2", "macvlan-conf"]`)
- `cneinstance_vpc_name`: VPC name — auto-discovered from cluster data source
- `cneinstance_cloud_region`: Cloud region — sourced from `ibmcloud_cluster_region`

### Required Variables (terraform.tfvars)

```hcl
# IBM Cloud Credentials
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"
ibmcloud_resource_group = ""

# Target Cluster (required)
roks_cluster_name_or_id = "my-openshift-cluster"

# FLO Output Variables
flo_namespace                     = "f5-bnk"
flo_utils_namespace               = "f5-utils"
flo_f5_bigip_k8s_manifest_version = "2.3.0-3.2598.3-0.0.170"
flo_trusted_profile_id            = ""
flo_cluster_issuer_name           = ""
flo_far_repo_url                  = "repo.f5.com"

# CNEInstance
cneinstance_deployment_size      = "Small"
cneinstance_gslb_datacenter_name = ""
cneinstance_network_attachments  = ["ens3-ipvlan-l2", "macvlan-conf"]
```

## Outputs

View all outputs:
```bash
terraform output                              # All outputs
terraform output cneinstance_id              # Specific output
```

| Output | Description |
| ------ | ----------- |
| `cneinstance_id` | Name of the CNEInstance resource |
| `cneinstance_namespace` | Namespace where CNEInstance is deployed |
| `cneinstance_pod_deployment_status` | Pod deployment status after CNEInstance readiness validation |

## Debugging & Troubleshooting

**Plan and apply:**
```bash
terraform plan
terraform apply -auto-approve
```

**List resources:**
```bash
terraform state list
terraform state list module.cneinstance
```

**Validate configuration:**
```bash
terraform validate
```

**Common issues:**

| Issue | Solution |
|-------|----------|
| "no matches for kind CNEInstance" during plan | FLO must be deployed first — CNEInstance CRD is registered by FLO. |
| "field manager conflict" on CNEInstance | The `force_conflicts = true` field_manager is already set. If it persists, check for manual edits to the CR. |
| "clusterrolebinding already exists" for SCC | The SCC binding may have been created by another workspace. Remove the duplicate from `scc_policy_assignments`. |
| Cluster not found | Verify `roks_cluster_name_or_id` matches output of `ibmcloud ks clusters --provider vpc-gen2`. Ensure `ibmcloud_cluster_region` matches the cluster's region. |
| VPC lookup fails | The cluster VPC is auto-discovered from the cluster. Ensure the cluster is in `Running` state before applying. |
