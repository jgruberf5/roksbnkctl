# BIG-IP Next for Kubernetes on IBM ROKS — BNK Deployment build 2.3.0-ehf-2-3.2598.3-0.0.17

## About This Workspace

This Terraform workspace deploys F5 Lifecycle Operator onto an **existing** IBM Cloud ROKS (OpenShift) cluster. It does not create cluster infrastructure — provide the name or ID of a running ROKS cluster and the workspace installs all BNK components in the correct dependency order.

## OCP Security Context Constraints Bindings Detail

BIG-IP Next for Kubernetes required bindings grant `system:openshift:scc:privileged` for the following resources:

| Module | Namespace | Service Accounts |
|--------|-----------|------------------|
| FLO | `f5-bnk` | `flo-f5-lifecycle-operator`, `f5-bigip-ctlr-serviceaccount`, `default` (CIS) |

## Project Directory Structure

```
roks_single_nic/
├── main.tf                    # Root module configuration
├── variables.tf               # Root module variables
├── outputs.tf                 # Root module outputs
├── providers.tf               # Provider configuration
├── data.tf                    # Data sources (cluster, VPC, transit gateway)
├── terraform.tfvars.example   # Example variable values
├── modules/
│   ├── flo/                   # FLO (F5 Lifecycle Operator) module
│   │   ├── main.tf            # FLO deployment resources (includes CIS helm chart)
│   │   ├── variables.tf       # FLO module variables
│   │   ├── outputs.tf         # FLO module outputs
│   │   └── versions.tf        # FLO provider requirements
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
              │ (provides kubeconfig)
              ▼
┌──────────────────────────────────┐
│  1. CERT-MANAGER                 │
│  (Certificate Management CRDs)   │
│                                  │
│  - Namespace                     │
│  - Helm Release                  │
│  - CRD Registration              │
└─────────────┬────────────────────┘
              │ (cert-manager.io CRDs: ClusterIssuer, Certificate)
              ▼
┌──────────────────────────────────┐
│  2. FLO                          │
│  (F5 Lifecycle Operator)         │
│                                  │
│  - Cert-Manager ClusterIssuer    │
│  - Certificates                  │
│  - NAD (Network Attachments)     │
│  - Node Labels                   │
│  - F5 Lifecycle Operator Helm    │
│  - F5 BNK CIS Helm               │
│  - BIG-IP Login Secret           │
│  - IBM IAM Trusted Profile       │
│  - privileged SCC (3 bindings)   │
└──────────────────────────────────┘
```

## Local Host Installation & Deployment

### Prerequisites

1. An existing IBM Cloud ROKS (OpenShift) cluster
2. Copy `terraform.tfvars.example` to `terraform.tfvars` and fill in your values
3. Run `terraform init` to initialize all modules

Get your cluster name or ID:
```bash
ibmcloud ks clusters --provider vpc-gen2
```

### Recommended Deployment Order

#### Deploy FLO — F5 Lifecycle Operator (5–10 min)
```bash
terraform plan
terraform apply -auto-approve
```
### Cleanup

```bash
terraform destroy -auto-approve
```

## Configuration

### Module-Level Variables

#### FLO (F5 Lifecycle Operator) Module
- `enabled`: Enable/disable module (controlled by `deploy_flo`)
- `cert_manager_crd_ready`: **CRITICAL** — Dependency trigger from cert-manager module (ensures CRDs exist before plan validates manifests)
- `bigip_username`: BIG-IP username for CIS controller login (default: admin)
- `bigip_password`: BIG-IP password for CIS controller login (sensitive)
- `bigip_url`: BIG-IP URL for CIS controller login (`https://` prefix is stripped automatically)
- `nad_cni_type`: CNI type for Network Attachment Definition — `ipvlan` or `host-device` (default: `ipvlan`)
- `nad_interface_name`: Network interface name for NAD (default: `ens3`)
- `nad_ipvlan_mode`: IPVLAN mode — `l2` or `l3` (default: `l2`)

**IBM IAM Trusted Profile** (created by FLO module, passed to CNEInstance):
- `openshift_cluster_name`: Name of the OpenShift cluster — used to make the trusted profile name unique per cluster (sourced from cluster data source)
- `openshift_cluster_crn`: CRN of the OpenShift cluster — used to link the trusted profile to a ROKS service account in `flo_namespace` (sourced from cluster data source). The account is `var.trusted_profile_sa_name`, default `f5-cne-controller`. **Releases before v1.44.0 hardcoded `f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount`** — an existing deployment that leaves the variable unset therefore retargets both the IAM link and the privileged-SCC binding; `roksbnkctl` warns before the apply that does it
- `cluster_vpc_id`: ID of the cluster VPC — grants the trusted profile Viewer and Editor IAM roles on this VPC (sourced from cluster data source)

**COS Bucket Integration** (FAR auth key and JWT fetched from IBM Cloud Object Storage):
- `ibmcloud_cos_bucket_region`: IBM Cloud region where the COS bucket is located (default: `us-south`)
- `ibmcloud_cos_instance_name`: IBM Cloud COS instance name (default: `bnk-supply-chain`)
- `ibmcloud_resources_cos_bucket`: COS bucket name containing FAR auth key and JWT files (default: `bnk-artifacts`)
- `f5_cne_far_auth_file`: FAR auth key filename in COS bucket, must be `.tgz` (default: `f5-far-auth-key.tgz`)
- `f5_cne_subscription_jwt_file`: Subscription JWT filename in COS bucket (default: `subscription.jwt`)

> The FLO module uses the IBM Cloud API key to exchange for an IAM token, then downloads the FAR auth key archive and JWT from the COS bucket via the S3 REST API. The `.tgz` archive is automatically extracted and the JSON key file inside is auto-detected. The JWT fetched from COS is passed to the License module.

### Required Variables (terraform.tfvars)

```hcl
# IBM Cloud Credentials
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"
ibmcloud_resource_group = ""

# Target Cluster (required)
roks_cluster_name_or_id = "my-openshift-cluster"

# FAR Registry
far_repo_url                  = "repo.f5.com"
f5_bigip_k8s_manifest_version = "2.3.0-3.2598.3-0.0.170"

# COS Bucket — FAR auth key and JWT fetched from IBM COS
ibmcloud_cos_bucket_region    = "us-south"
ibmcloud_cos_instance_name    = "bnk-supply-chain"
ibmcloud_resources_cos_bucket = "bnk-artifacts"
f5_cne_far_auth_file          = "f5-far-auth-key.tgz"
f5_cne_subscription_jwt_file  = "subscription.jwt"

# Namespace Configuration
flo_namespace          = "f5-bnk"
utils_namespace        = "f5-utils"

# BIG-IP CIS (optional)
bigip_username = "admin"
bigip_password = "YOUR_BIGIP_PASSWORD"
bigip_url      = "https://your-bigip-url"

```

## Outputs

View all outputs:
```bash
terraform output                    # All outputs
terraform output cluster_id         # Specific output
```

| Output | Description |
| ------ | ----------- |
| `cluster_id` | ID of the target OpenShift cluster |
| `cluster_name` | Name of the target OpenShift cluster |
| `cluster_crn` | CRN of the target OpenShift cluster |
| `flo_release_name` | Name of the f5-lifecycle-operator Helm release |
| `flo_namespace` | Namespace where f5-lifecycle-operator is installed |
| `flo_version` | Installed f5-lifecycle-operator version |
| `trusted_profile_id` | IBM IAM Trusted Profile ID created for the CNE controller service account |
| `flo_pod_deployment_status` | FLO pod deployment status |

## Debugging & Troubleshooting

```bash
terraform plan
```

**View module-specific changes:**
```bash
terraform plan -target=module.flo
```

**List resources by module:**
```bash
terraform state list module.flo
```

**Validate configuration:**
```bash
terraform validate
terraform state list
```