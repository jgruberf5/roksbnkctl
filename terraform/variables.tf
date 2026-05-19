# ============================================================
# Root Terraform Variables
# F5 BIG-IP Next for Kubernetes 2.3
#
# Module execution order:
#   roks_cluster    — ROKS cluster + Transit Gateway
#   cert_manager    — cert-manager Helm install
#   flo             — F5 Lifecycle Operator
#   cne_instance    — CNEInstance custom resource
#   license         — License custom resource
#   testing         — Jumphost infrastructure
#
# Cross-module wiring (handled automatically by Terraform):
#   roks_cluster_name_or_id         ← roks_cluster output: roks_cluster_name
#   testing_transit_gateway_name    ← roks_cluster output: transit_gateway_name
#   flo_namespace                   ← flo output: flo_namespace
#   flo_trusted_profile_id          ← flo output: flo_trusted_profile_id
#   flo_cluster_issuer_name         ← flo output: flo_cluster_issuer_name
#   cneinstance_network_attachments ← flo output: cneinstance_network_attachments
# ============================================================


# ============================================================
# IBM Cloud — Common (all modules)
# ============================================================

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region for all cluster resources"
  type        = string
  default     = "ca-tor"
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud resource group name"
  type        = string
  default     = "default"
}


# ============================================================
# roks_cluster
# ============================================================

variable "create_roks_cluster" {
  description = "Create a new ROKS cluster. When false, supply roks_cluster_id_or_name instead."
  type        = bool
  default     = true
}

variable "roks_cluster_id_or_name" {
  description = "ID or name of an existing ROKS cluster — used when create_roks_cluster = false"
  type        = string
  default     = ""
}

variable "create_roks_transit_gateway" {
  description = "Create Transit Gateway and VPC connections"
  type        = bool
  default     = true
}

variable "create_roks_registry_cos_instance" {
  description = "Create Cloud Object Storage instance for the OpenShift image registry"
  type        = bool
  default     = true
}

variable "roks_cluster_vpc_name" {
  description = "Name of the cluster VPC"
  type        = string
  default     = "tf-cluster-vpc"
}

variable "openshift_cluster_name" {
  description = "Name of the OpenShift cluster"
  type        = string
  default     = "tf-openshift-cluster"
}

variable "openshift_cluster_version" {
  description = "OpenShift cluster version (e.g. 4.18). Leave empty to use the latest available."
  type        = string
  default     = "4.18"
}

variable "roks_workers_per_zone" {
  description = "Number of worker nodes per availability zone"
  type        = number
  default     = 1
}

variable "roks_min_worker_vcpu_count" {
  description = "Minimum vCPU count when auto-selecting the worker node flavor"
  type        = number
  default     = 16
}

variable "roks_min_worker_memory_gb" {
  description = "Minimum memory in GB when auto-selecting the worker node flavor"
  type        = number
  default     = 64
}

variable "roks_cos_instance_name" {
  description = "Name of the COS instance for the OpenShift image registry"
  type        = string
  default     = "tf-openshift-cos-instance"
}

variable "roks_transit_gateway_name" {
  description = "Name of the Transit Gateway. Must reference an existing TGW when create_roks_transit_gateway = false and testing_create_tgw_jumphost = true."
  type        = string
  default     = "tf-tgw"
}

# Existing-cluster-VPC reuse (phase-handoff). When the bnk/testing phase
# runs against a workspace whose cluster phase already created the cluster
# VPC, roksbnkctl renders these (use_existing_cluster_vpc = true +
# existing_cluster_vpc_id = <cluster-outputs.json vpc_id>) so the cluster
# submodule looks the VPC up via data.ibm_is_vpc.existing_cluster_vpc
# instead of re-creating ibm_is_vpc.cluster_vpc[0] (which IBM Cloud
# rejects as a duplicate name). Default false keeps the FIRST/cluster
# phase byte-identical (create). See issues/issue_sprint16_validator.md
# Issue 2.
variable "use_existing_cluster_vpc" {
  description = "Reuse an existing cluster VPC instead of creating one. roksbnkctl sets this true in the second (bnk/testing) phase when cluster-outputs.json exists; the cluster phase leaves it false (create)."
  type        = bool
  default     = false
}

variable "existing_cluster_vpc_id" {
  description = "ID of the existing cluster VPC (used only when use_existing_cluster_vpc = true) — sourced from cluster-outputs.json vpc_id."
  type        = string
  default     = ""
}


# ============================================================
# cert_manager
# ============================================================

variable "install_cert_manager" {
  description = "Install cert-manager. When false, cert_manager_namespace is passed directly to flo."
  type        = bool
  default     = true
}

variable "cert_manager_namespace" {
  description = "Kubernetes namespace for cert-manager"
  type        = string
  default     = "cert-manager"
}

variable "cert_manager_version" {
  description = "cert-manager Helm chart version"
  type        = string
  default     = "v1.17.3"
}


# ============================================================
# COS Bucket — shared by flo and license
# ============================================================

variable "ibmcloud_cos_bucket_region" {
  description = "IBM Cloud region where the COS bucket is located"
  type        = string
  default     = "us-south"
}

variable "ibmcloud_cos_instance_name" {
  description = "IBM Cloud COS instance name"
  type        = string
  default     = "bnk-orchestration"
}

variable "ibmcloud_resources_cos_bucket" {
  description = "IBM Cloud COS bucket containing FAR auth key and JWT files"
  type        = string
  default     = "bnk-schematics-resources"
}


# ============================================================
# flo / cne_instance / license
# ============================================================

variable "deploy_bnk" {
  description = "Deploy BIG-IP Next for Kubernetes — creates flo, cne_instance, and license. When false all three modules are skipped."
  type        = bool
  default     = true
}


# ============================================================
# flo — F5 Lifecycle Operator
# ============================================================

variable "far_repo_url" {
  description = "FAR repository URL for Docker and Helm images"
  type        = string
  default     = "repo.f5.com"
}

variable "f5_bigip_k8s_manifest_version" {
  description = "Version of the f5-bigip-k8s-manifest chart (FLO and CIS versions are extracted from this)"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

variable "f5_cne_far_auth_file" {
  description = "FAR auth key filename in the COS bucket (.tgz)"
  type        = string
  default     = "f5-far-auth-key.tgz"
}

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT filename in the COS bucket — used by flo and license"
  type        = string
  default     = "trial.jwt"
}

variable "flo_namespace" {
  description = "Kubernetes namespace for the F5 Lifecycle Operator"
  type        = string
  default     = "f5-bnk"
}

variable "flo_utils_namespace" {
  description = "Kubernetes namespace for F5 utility components — used by flo, cne_instance, and license"
  type        = string
  default     = "f5-utils"
}

variable "bigip_username" {
  description = "BIG-IP username for the CIS controller"
  type        = string
  default     = "admin"
}

variable "bigip_password" {
  description = "BIG-IP password for the CIS controller"
  type        = string
  default     = "admin"
  sensitive   = true
}

variable "bigip_url" {
  description = "BIG-IP URL for the CIS controller"
  type        = string
  default     = "192.168.1.245"
}


# ============================================================
# flo output fallbacks (flo → cne_instance)
#
# Terraform wires these automatically from flo module outputs.
# Set manually only when flo was applied in a prior state but
# is not included in the current module configuration.
# ============================================================

variable "flo_trusted_profile_id" {
  description = "IBM Cloud Trusted Profile ID created by flo — wired automatically from flo output; set here to override"
  type        = string
  default     = ""
}

variable "flo_cluster_issuer_name" {
  description = "Kubernetes ClusterIssuer name created by flo — wired automatically from flo output; set here to override"
  type        = string
  default     = ""
}

variable "cneinstance_network_attachments" {
  description = "Network attachment names for cne_instance — wired automatically from flo output; set here to override"
  type        = list(string)
  default     = ["ens3-ipvlan-l2", "macvlan-conf"]
}


# ============================================================
# cne_instance
# ============================================================

variable "cneinstance_deployment_size" {
  description = "Deployment size for CNEInstance (Small, Medium, Large)"
  type        = string
  default     = "Small"
}

variable "cneinstance_gslb_datacenter_name" {
  description = "GSLB datacenter name for CNEInstance (optional)"
  type        = string
  default     = ""
}


# ============================================================
# license
# ============================================================

variable "license_mode" {
  description = "License operation mode (connected or disconnected)"
  type        = string
  default     = "connected"
}


# ============================================================
# testing
# ============================================================

variable "testing_create_tgw_jumphost" {
  description = "Create a jumphost in a client VPC connected to the cluster via the Transit Gateway"
  type        = bool
  default     = true
}

variable "testing_create_cluster_jumphosts" {
  description = "Create one jumphost per availability zone directly inside the cluster VPC"
  type        = bool
  default     = false
}

variable "testing_ssh_key_name" {
  description = "Name of the IBM Cloud SSH key to inject into all jumphosts"
  type        = string
  default     = ""
}

variable "testing_jumphost_profile" {
  description = "Instance profile for all jumphosts (leave empty to auto-select based on min_vcpu_count and min_memory_gb)"
  type        = string
  default     = ""
}

variable "testing_min_vcpu_count" {
  description = "Minimum vCPU count when auto-selecting the jumphost instance profile"
  type        = number
  default     = 4
}

variable "testing_min_memory_gb" {
  description = "Minimum memory in GB when auto-selecting the jumphost instance profile"
  type        = number
  default     = 8
}

variable "testing_create_client_vpc" {
  description = "Create a new client VPC for the TGW jumphost. When false, testing_client_vpc_name must reference an existing VPC."
  type        = bool
  default     = false
}

variable "testing_client_vpc_name" {
  description = "Name of the client VPC — created when testing_create_client_vpc = true, or looked up when false"
  type        = string
  default     = "tf-testing-vpc"
}

variable "testing_client_vpc_region" {
  description = "IBM Cloud region for the client VPC and TGW jumphost"
  type        = string
  default     = "ca-tor"
}

variable "testing_tgw_jumphost_name" {
  description = "Name of the TGW-connected jumphost instance"
  type        = string
  default     = "tf-testing-jumphost-tgw"
}

variable "testing_cluster_jumphost_name_prefix" {
  description = "Name prefix for cluster jumphosts — zone name is appended (<prefix>-<zone>)"
  type        = string
  default     = "tf-testing-jumphost-cluster"
}

# ============================================================
# Kubeconfig scratch directory
# ============================================================

# Threaded through to each of the four submodules
# (cert_manager / cne_instance / flo / license) where the IBM provider's
# ibm_container_cluster_config data source writes its admin kubeconfig.
# Each module appends its own name as a subdir, so the four downloads
# don't collide.
#
# Default targets the bnk runner image's bind-mount layout (/work is
# the host cwd inside the container). Consumers running terraform
# directly on a host (e.g., roksbnkctl) should override this to a writable
# path, e.g., ~/.roksbnkctl/<workspace>/state/kubeconfig.
#
# The path must already exist (the IBM provider does NOT MkdirAll) and
# be writable by the user running terraform.
variable "kubeconfig_dir" {
  description = "Parent directory where ibm_container_cluster_config writes admin kubeconfigs. Each submodule appends its name as a subdir. Default is the bnk runner image's /work mount; override for direct-on-host runs."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig"
}

# ============================================================
# Scratch directory for FAR / manifest cross-apply artifacts
# ============================================================

# Threaded into the flo module which uses it for:
#   - FAR auth tarball download + extraction
#   - f5-bigip-k8s-manifest helm chart extraction
#
# The flo module derives manifest_download_dir as ${scratch_dir}/f5-manifest
# automatically; users only need to override this single root variable.
#
# Default targets the bnk runner image's /work bind-mount; override for
# direct-on-host runs (e.g., roksbnkctl).
variable "scratch_dir" {
  description = "Persistent scratch directory for FLO's FAR/manifest cross-apply artifacts. Default is the bnk runner image's /work mount; override for direct-on-host runs."
  type        = string
  default     = "/work/.bnk/scratch"
}
