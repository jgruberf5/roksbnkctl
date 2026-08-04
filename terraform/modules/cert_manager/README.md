# Certificate Manager Install

## About This Workspace

This Terraform workspace deploys cert-manager onto an **existing** IBM Cloud ROKS (OpenShift) cluster. It does not create cluster infrastructure — provide the name or ID of a running ROKS cluster and the workspace installs cert-manager in the correct dependency order.

## Project Directory Structure

```
cert_manager/
├── main.tf                    # Root module configuration
├── variables.tf               # Root module variables
├── outputs.tf                 # Root module outputs
├── providers.tf               # Provider configuration
├── data.tf                    # Data sources (resource group, cluster)
├── terraform.tfvars.example   # Example variable values
├── modules/
│   └── cert-manager/          # Cert-manager module
│       ├── main.tf            # Cert-manager helm release and namespace
│       ├── variables.tf       # Cert-manager variables
│       └── outputs.tf         # Cert-manager outputs
```

## Module Dependency Chain

```
┌──────────────────────────────────┐
│  DATA SOURCES                    │
│  (Existing IBM Cloud Resources)  │
│                                  │
│  - Existing ROKS Cluster         │
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

### Deploy Cert-Manager (2–3 min)
```bash
terraform plan
terraform apply -auto-approve
```

### Cleanup (Reverse Order)

```bash
terraform destroy -auto-approve
```

## Configuration

### Module-Level Variables

#### Cert-Manager Module
- `enabled`: Enable/disable cert-manager deployment (hardcoded `true` at root)
- `namespace`: Kubernetes namespace for cert-manager (default: `cert-manager`)
- `chart_version`: Helm chart version (default: `v1.17.3`)
- `chart_repository`: Helm repository URL (default: `https://charts.jetstack.io`)
- `wait_for_deployment`: Wait for deployment to be ready (default: `true`)
- `post_deployment_delay`: Time to wait after deployment for CRD registration (default: `30s`)

### Required Variables (terraform.tfvars)

```hcl
# IBM Cloud Credentials
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"
ibmcloud_resource_group = ""

# Target Cluster (required)
roks_cluster_name_or_id = "my-openshift-cluster"

# Namespace Configuration
cert_manager_namespace = "cert-manager"
cert_manager_version   = "v1.17.3"
```

## Outputs

View all outputs:
```bash
terraform output                          # All outputs
terraform output cert_manager_namespace   # Specific output
```

| Output | Description |
| ------ | ----------- |
| `cert_manager_namespace` | Namespace where cert-manager is deployed |
| `cert_manager_version` | Installed cert-manager Helm chart version |

## Debugging & Troubleshooting

```bash
terraform plan
```

**View module-specific changes:**
```bash
terraform plan -target=module.cert_manager
```

**List resources by module:**
```bash
terraform state list module.cert_manager
```

**Validate configuration:**
```bash
terraform validate
terraform state list
```
