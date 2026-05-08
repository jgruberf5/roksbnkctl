# BIG-IP Next for Kubernetes on IBM ROKS — License Workspace 2.3.0-ehf-2-3.2598.3-0.0.17

## About This Workspace

This Schematics-ready Terraform workspace deploys the F5 BNK **License custom resource** onto an **existing** IBM Cloud ROKS (OpenShift) cluster. It assumes BNK components (FLO, CNEInstance) are already deployed and their CRDs are registered. Provide the name or ID of a running ROKS cluster, your IBM Cloud credentials, and a COS bucket containing the license JWT token.

## Deploying with IBM Schematics

### Required IBM Provider and IAM Variables

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `ibmcloud_api_key` | API Key used to authorize all deployment resources | REQUIRED | _(sensitive)_ |
| `ibmcloud_cluster_region` | IBM Cloud region where the target cluster resides | REQUIRED with default | `ca-tor` (default) |
| `ibmcloud_resource_group` | IBM Cloud resource group name (leave empty to use account default) | Optional | `default` |

### Target Cluster Variables

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `roks_cluster_name_or_id` | Name or ID of the existing OpenShift ROKS cluster | REQUIRED | `my-openshift-cluster` |

Get your existing cluster name or ID:
```bash
ibmcloud ks clusters --provider vpc-gen2
```

### License Configuration Variables

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `license_mode` | License operation mode (`connected` or `disconnected`) | REQUIRED with default | `connected` (default) |
| `flo_utils_namespace` | Namespace where the License CR is deployed | REQUIRED with default | `f5-utils` (default) |
| `license_f5_cne_subscription_jwt_file` | JWT filename in the COS bucket | REQUIRED with default | `trial.jwt` (default) |

### IBM COS Variables

The JWT license token is fetched from an IBM Cloud Object Storage (COS) bucket. Download the JWT from myf5.com and upload it to a COS bucket before deploying.

| Variable | Description | Required | Example |
| -------- | ----------- | -------- | ------- |
| `ibmcloud_cos_bucket_region` | IBM Cloud region where the COS bucket is located | REQUIRED with default | `us-south` (default) |
| `ibmcloud_cos_instance_name` | IBM Cloud COS instance name | REQUIRED with default | `bnk-orchestration` (default) |
| `ibmcloud_resources_cos_bucket` | IBM Cloud COS bucket containing the JWT file | REQUIRED with default | `bnk-schematics-resources` (default) |

As an example using the variable defaults:

1. Create an IBM COS instance named `bnk-orchestration`
2. With a bucket named `bnk-schematics-resources`
3. Upload the license JWT token file `trial.jwt`

```
bnk-orchestrator              # IBM COS Instance
└── bnk-schematics-resources  # IBM COS Bucket
    └── trial.jwt             # IBM COS Resource (key)
```

```bash
# Create the COS instance
ibmcloud resource service-instance-create bnk-orchestration cloud-object-storage standard global

# Create the COS bucket (replace RESOURCE_INSTANCE_ID with the CRN from the previous command)
ibmcloud cos bucket-create \
  --bucket bnk-schematics-resources \
  --ibm-service-instance-id RESOURCE_INSTANCE_ID \
  --region us-south

# Upload the license JWT token
ibmcloud cos object-put \
  --bucket bnk-schematics-resources \
  --key trial.jwt \
  --body ./trial.jwt \
  --region us-south
```

## Project Directory Structure

```
ibmcloud_schematics_bigip_next_for_kubernetes_2_3_license/
├── main.tf                    # Root module — invokes license module
├── variables.tf               # Root module variables
├── outputs.tf                 # Root module outputs
├── providers.tf               # IBM, Kubernetes, and HTTP provider configuration
├── data.tf                    # Data sources (resource group, cluster)
├── terraform.tfvars.example   # Example variable values
└── modules/
    └── license/               # License CR module
        ├── main.tf            # License CR and COS JWT download resources
        ├── variables.tf       # License module variables
        ├── outputs.tf         # License module outputs
        └── terraform.tf       # License provider requirements
```

## Installation & Deployment

### Prerequisites

1. An existing IBM Cloud ROKS cluster with BNK (FLO + CNEInstance) already deployed
2. A COS bucket containing the license JWT token
3. Copy `terraform.tfvars.example` to `terraform.tfvars` and fill in your values
4. Run `terraform init` to initialize providers and the license module

### Deploy

```bash
terraform init
terraform plan
terraform apply -auto-approve
```

### Cleanup

```bash
terraform destroy -auto-approve
```

## Required Variables (terraform.tfvars)

```hcl
# IBM Cloud Credentials
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"
ibmcloud_resource_group = ""

# Target Cluster (required)
roks_cluster_name_or_id = "my-openshift-cluster"

# COS Bucket — JWT fetched from IBM COS
ibmcloud_cos_bucket_region    = "us-south"
ibmcloud_cos_instance_name    = "bnk-orchestration"
ibmcloud_resources_cos_bucket = "bnk-schematics-resources"

# License
license_f5_cne_subscription_jwt_file = "trial.jwt"
license_mode                         = "connected"
flo_utils_namespace                  = "f5-utils"
```

## Outputs

```bash
terraform output
```

| Output | Description |
| ------ | ----------- |
| `license_id` | Name of the License custom resource |
| `license_namespace` | Namespace where the License CR is deployed |

## Debugging & Troubleshooting

**Validate configuration:**
```bash
terraform validate
terraform state list
```

**Common issues:**

| Issue | Solution |
|-------|----------|
| "no matches for kind License" during plan | The License CRD is not yet registered. Ensure CNEInstance is fully deployed before applying this workspace. |
| License CR stuck in "Registering" state | Verify the JWT token is valid and the cluster has internet access for `connected` mode. |
| Cluster not found | Verify `roks_cluster_name_or_id` matches output of `ibmcloud ks clusters --provider vpc-gen2`. Ensure `ibmcloud_cluster_region` matches the cluster's region. |
| COS download fails | Verify `ibmcloud_api_key` has Reader access to the COS bucket, and that the JWT filename matches `license_f5_cne_subscription_jwt_file`. |
