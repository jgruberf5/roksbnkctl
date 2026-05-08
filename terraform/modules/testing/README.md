# BIG-IP Next for Kubernetes on IBM Cloud — Testing Jumphosts Workspace 2.3

## About This Workspace

This Schematics-ready Terraform workspace deploys testing jumphosts for validating BIG-IP Next for Kubernetes deployments in IBM Cloud. Two independent jumphost types can be enabled in any combination:

| Feature Flag | Jumphost Type | Placement |
|---|---|---|
| `testing_create_tgw_jumphost` | Single jumphost in a client VPC | Client VPC in any region, optionally connected to the cluster via Transit Gateway |
| `testing_create_cluster_jumphosts` | One jumphost per availability zone | Directly inside the cluster VPC, in every zone of `ibmcloud_cluster_region` |

Both types run Ubuntu 22.04, share the same SSH key name, and are provisioned with the same user_data boot script.

## Installed Software

Every jumphost is provisioned at boot with the following tools via `user_data`:

| Tool | Purpose |
|------|---------|
| IBM Cloud CLI + plugins | `container-service`, `openshift`, `vpc-infrastructure` |
| Docker CE | Container image pulls and local builds |
| Helm 3 | Helm chart inspection and manual installs |
| kubectl | Kubernetes API access |
| OpenShift CLI (`oc`) | OpenShift-specific cluster operations |
| `curl` | HTTP/HTTPS endpoint testing |
| `iperf3` | Network throughput measurement between jumphosts or to cluster nodes |
| `dig` (dnsutils) | DNS resolution testing and troubleshooting |
| `nc` (netcat) | TCP/UDP port reachability checks |
| `netstat` (net-tools) | Active connection and routing table inspection |

## Shared SSH Keypair

Terraform generates an RSA 4096 keypair (`tls_private_key.jumphost_shared_key`) once per workspace apply. The public key is written to `/home/ubuntu/.ssh/authorized_keys` and `/root/.ssh/authorized_keys` on every jumphost at the top of the boot script — before any long-running apt-get steps — so that Terraform remote-exec provisioners (and manual SSH sessions) can connect as soon as the SSH daemon is reachable. The private and public key files are also written to `/home/ubuntu/.ssh/id_rsa` and `/root/.ssh/id_rsa` so any jumphost can SSH to any other jumphost without a password.

Use `terraform output testing_jumphost_shared_private_key` to retrieve the private key for local SSH access.

## Kubeconfig

The boot script calls `ibmcloud ks cluster config --cluster <roks_cluster_name_or_id> --admin` and copies the resulting kubeconfig to `/root/.kube/config` and `/home/ubuntu/.kube/config`. The call is wrapped in `|| true` so a transient failure (cluster not yet ready, network issue) does not abort the rest of the boot script. Re-run manually if needed:

```bash
ibmcloud login --apikey <key> -r <ibmcloud_cluster_region>
ibmcloud ks cluster config --cluster <roks_cluster_name_or_id> --admin
```

## /etc/hosts — Inter-Jumphost Name Resolution

After all floating IPs are assigned, Terraform connects to each jumphost via the shared SSH key and writes a fenced block of `<ip>  <hostname>` entries to `/etc/hosts`:

| Jumphost | Hostname |
|----------|----------|
| Cluster jumphost in zone `us-south-1` | `cluster-us-south-1` |
| Cluster jumphost in zone `us-south-2` | `cluster-us-south-2` |
| TGW jumphost | `tgw-jumphost` |

The block is idempotent — re-applying removes the previous block before writing a fresh one. This requires SSH access from the Terraform runner to each floating IP on port 22. For IBM Schematics, run `terraform apply` locally or through a VPN-connected runner.

## TGW Jumphost

A single jumphost is created in a client VPC in `testing_client_vpc_region`. The client VPC is optionally connected to an existing IBM Cloud Transit Gateway (when `testing_transit_gateway_name` is set), which bridges it to the cluster VPC across regions.

**VPC resolution when `testing_create_tgw_jumphost = true`:**

| Mode | Configuration | Behaviour |
|------|---------------|-----------|
| Create new VPC | `testing_create_client_vpc = true` | New VPC named `testing_client_vpc_name` is created in `testing_client_vpc_region` |
| Use existing VPC | `testing_create_client_vpc = false` | Existing VPC named `testing_client_vpc_name` is looked up in `testing_client_vpc_region` |

The jumphost is placed in the first available zone of `testing_client_vpc_region`. A dedicated security group permits inbound SSH from `0.0.0.0/0` and all outbound traffic. When `testing_create_client_vpc = true`, the VPC default security group is also opened to all inbound traffic to simplify test access.

## Cluster Jumphosts

One jumphost is created per availability zone in the cluster VPC. The workspace looks up all zones in `ibmcloud_cluster_region` and creates the following per zone:

- A `/24` subnet
- An attachment to the zone's existing cluster VPC public gateway (IBM Cloud quota: one public gateway per zone per VPC; the cluster VPC already has one per zone for its worker nodes)
- A VSI instance
- A floating IP for external SSH access

All cluster jumphosts share a single security group (inbound SSH, all outbound) and the same SSH key.

The SSH key must exist in `ibmcloud_cluster_region` for cluster jumphosts and in `testing_client_vpc_region` for the TGW jumphost. If both types are enabled with different regions, the key must be present in both regions under the same name.

## Deploying with IBM Schematics

### IBM Provider and IAM Variables

| Variable | Description | Required | Example |
|----------|-------------|----------|---------|
| `ibmcloud_api_key` | API key used to authorize all deployment resources | REQUIRED | `0q7N3CzUn6oKxEsr7fLc1mxkukBeAEcsjNRQOg1kdDSY` (not a real key) |
| `ibmcloud_cluster_region` | IBM Cloud region where the referenced cluster resides | REQUIRED with default | `ca-tor` (default) |
| `ibmcloud_resource_group` | IBM Cloud resource group name (leave empty for account default) | Optional | `default` |

### Referenced Cluster

The workspace always looks up the referenced ROKS cluster to derive its VPC and zone topology.

| Variable | Description | Required | Example |
|----------|-------------|----------|---------|
| `roks_cluster_name_or_id` | Name or ID of the existing OpenShift ROKS cluster | REQUIRED | `my-openshift-cluster` |

### Feature Flags

| Variable | Description | Default |
|----------|-------------|---------|
| `testing_create_tgw_jumphost` | Create a jumphost in a client VPC connected via Transit Gateway | `true` |
| `testing_create_cluster_jumphosts` | Create one jumphost per availability zone in the cluster VPC | `false` |

### Shared Jumphost Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `testing_ssh_key_name` | Name of an existing IBM Cloud SSH key to inject into all jumphosts (must exist in each relevant region) | `""` |
| `testing_jumphost_profile` | VPC instance profile for all jumphosts (leave empty to auto-select) | `""` |
| `testing_min_vcpu_count` | Minimum vCPU count when auto-selecting the instance profile | `4` |
| `testing_min_memory_gb` | Minimum memory in GB when auto-selecting the instance profile | `8` |

When `testing_jumphost_profile` is empty, the workspace queries available VPC instance profiles in the relevant region and picks the first profile meeting the `testing_min_vcpu_count` and `testing_min_memory_gb` thresholds. Falls back to `bx2-4x16` if no match is found.

### TGW Jumphost Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `testing_create_client_vpc` | Create a new client VPC (`true`) or look up an existing one (`false`) | `false` |
| `testing_client_vpc_name` | Name of the client VPC to create or look up | `"tf-testing-vpc"` |
| `testing_client_vpc_region` | IBM Cloud region for the client VPC and TGW jumphost | `"ca-tor"` |
| `testing_transit_gateway_name` | Name of an existing Transit Gateway to connect the client VPC to (leave empty to skip) | `""` |
| `testing_tgw_jumphost_name` | Name prefix for the TGW jumphost instance, subnet, gateway, security group, and floating IP | `"tf-testing-jumphost-tgw"` |

### Cluster Jumphosts Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `testing_cluster_jumphost_name_prefix` | Name prefix for cluster jumphosts — zone is appended as `<prefix>-<zone>` | `"tf-testing-jumphost-cluster"` |

## Project Directory Structure

```
ibmcloud_schematics_bigip_next_for_kubernetes_2_3_testing/
├── main.tf                    # TGW jumphost and cluster jumphost resources
├── variables.tf               # All input variable declarations
├── outputs.tf                 # Cluster, TGW jumphost, and cluster jumphost outputs
├── providers.tf               # IBM provider configuration (default + vpc_region alias)
├── data.tf                    # Data sources for cluster, VPCs, images, profiles, SSH keys, TGW
└── terraform.tfvars.example   # Example variable values
```

## Configuration

### Required Variables (terraform.tfvars)

**TGW jumphost only — new client VPC in a different region, connected via Transit Gateway:**
```hcl
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"

roks_cluster_name_or_id = "my-openshift-cluster"

testing_ssh_key_name = "my-ssh-key"   # must exist in eu-gb

testing_create_tgw_jumphost      = true
testing_create_cluster_jumphosts = false

testing_create_client_vpc    = true
testing_client_vpc_name      = "tf-testing-vpc"
testing_client_vpc_region    = "eu-gb"
testing_transit_gateway_name = "tf-tgw"
testing_tgw_jumphost_name    = "tf-testing-jumphost-tgw"
```

**Cluster jumphosts only — one jumphost per zone in the cluster VPC:**
```hcl
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"

roks_cluster_name_or_id = "my-openshift-cluster"

testing_ssh_key_name = "my-ssh-key"   # must exist in ca-tor

testing_create_tgw_jumphost      = false
testing_create_cluster_jumphosts = true

testing_cluster_jumphost_name_prefix = "tf-testing-jumphost-cluster"
```

**Both types enabled:**
```hcl
ibmcloud_api_key        = "YOUR_API_KEY"
ibmcloud_cluster_region = "ca-tor"

roks_cluster_name_or_id = "my-openshift-cluster"

# Key must exist in both ca-tor and eu-gb
testing_ssh_key_name = "my-ssh-key"

testing_create_tgw_jumphost      = true
testing_create_cluster_jumphosts = true

# TGW jumphost — separate VPC in eu-gb
testing_create_client_vpc    = true
testing_client_vpc_name      = "tf-testing-vpc"
testing_client_vpc_region    = "eu-gb"
testing_transit_gateway_name = "tf-tgw"
testing_tgw_jumphost_name    = "tf-testing-jumphost-tgw"

# Cluster jumphosts — one per zone in ca-tor cluster VPC
testing_cluster_jumphost_name_prefix = "tf-testing-jumphost-cluster"
```

## Deployment

### Prerequisites
1. Copy `terraform.tfvars.example` to `terraform.tfvars` and fill in your API key and cluster name
2. Run `terraform init` to download provider plugins
3. Ensure the referenced cluster exists and is reachable with the provided API key
4. Ensure the SSH key exists in the required region(s)

### Deploy
```bash
terraform plan
terraform apply -auto-approve
```

### Cleanup
```bash
terraform destroy -auto-approve
```

## Outputs

```bash
terraform output                                       # All outputs
terraform output testing_tgw_jumphost_ssh_command      # TGW jumphost SSH command
terraform output testing_cluster_jumphost_ssh_commands # Map of zone -> SSH command
terraform output -raw testing_jumphost_shared_private_key > ~/.ssh/jumphost.pem && chmod 600 ~/.ssh/jumphost.pem
```

### Shared SSH Key Outputs

| Output | Description |
|--------|-------------|
| `testing_jumphost_shared_public_key` | Public key installed on all jumphosts |
| `testing_jumphost_shared_private_key` | Private key (sensitive) — write to a local file for SSH access |

### TGW Jumphost Outputs

| Output | Description |
|--------|-------------|
| `testing_tgw_jumphost_vpc_id` | ID of the VPC containing the TGW jumphost |
| `testing_tgw_jumphost_vpc_name` | Name of the VPC containing the TGW jumphost |
| `testing_tgw_jumphost_id` | Instance ID of the TGW jumphost |
| `testing_tgw_jumphost_private_ip` | Private IP address of the TGW jumphost |
| `testing_tgw_jumphost_public_ip` | Floating (public) IP address of the TGW jumphost |
| `testing_tgw_jumphost_ssh_command` | Ready-to-use SSH command for the TGW jumphost |
| `testing_tgw_jumphost_zone` | Availability zone where the TGW jumphost was placed |
| `testing_tgw_jumphost_profile_used` | Instance profile selected for the TGW jumphost |
| `testing_transit_gateway_connection_id` | ID of the Transit Gateway VPC connection |

### Cluster Jumphost Outputs (maps keyed by zone)

| Output | Description |
|--------|-------------|
| `testing_cluster_jumphost_ids` | Map of zone to instance ID |
| `testing_cluster_jumphost_private_ips` | Map of zone to private IP address |
| `testing_cluster_jumphost_public_ips` | Map of zone to floating IP address |
| `testing_cluster_jumphost_ssh_commands` | Map of zone to ready-to-use SSH command |

Example cluster jumphost output for a three-zone cluster:
```
testing_cluster_jumphost_ssh_commands = {
  "us-south-1" = "ssh -i my-ssh-key ubuntu@169.55.12.10"
  "us-south-2" = "ssh -i my-ssh-key ubuntu@169.55.12.11"
  "us-south-3" = "ssh -i my-ssh-key ubuntu@169.55.12.12"
}
```

### Cluster Reference Outputs

| Output | Description |
|--------|-------------|
| `roks_cluster_id` | ID of the referenced OpenShift cluster |
| `roks_cluster_name` | Name of the referenced OpenShift cluster |

## Debugging and Troubleshooting

**Plan specific resources:**
```bash
terraform plan -target=ibm_is_instance.tgw_jumphost
terraform plan -target=ibm_is_instance.cluster_jumphost
terraform plan -target=ibm_tg_connection.tgw_vpc_connection
```

**List all managed resources:**
```bash
terraform state list
terraform state list 'ibm_is_instance.cluster_jumphost'
```

**Validate configuration:**
```bash
terraform validate
```

**Common issues:**

| Issue | Solution |
|-------|----------|
| `roks_cluster_name_or_id` not found | Verify with `ibmcloud ks clusters --provider vpc-gen2` in `ibmcloud_cluster_region` |
| SSH key not found in cluster region | Verify with `ibmcloud is keys --region <ibmcloud_cluster_region>` |
| SSH key not found in client VPC region | Verify with `ibmcloud is keys --region <testing_client_vpc_region>` |
| No eligible instance profile found | Lower `testing_min_vcpu_count` or `testing_min_memory_gb`, or set `testing_jumphost_profile` explicitly |
| TGW jumphost unreachable via SSH | Confirm the floating IP is assigned (`terraform output testing_tgw_jumphost_public_ip`) |
| Cluster jumphost unreachable via SSH | Confirm floating IPs are assigned (`terraform output testing_cluster_jumphost_public_ips`) |
| Transit Gateway connection fails | Confirm the TGW exists and the API key has permission to create connections |
| `testing_create_tgw_jumphost = true` but no VPC specified | Set `testing_create_client_vpc = true` or provide `testing_client_vpc_name` for an existing VPC |
| `/etc/hosts` provisioner times out | Ensure port 22 is reachable from the Terraform runner to each floating IP |
