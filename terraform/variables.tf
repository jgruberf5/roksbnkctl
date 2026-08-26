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

variable "cluster_public_gateway" {
  description = "Attach a public gateway to each cluster subnet for worker Internet egress. true (default) keeps current behavior; false builds a private, disconnected cluster with no egress (operator must supply private connectivity — VPEs / private service endpoints — for image pulls and IBM Cloud services)."
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

# Sprint 23: bnk-phase-override.tfvars sets this false when cluster-outputs.json
# exists. Cluster phase manages cert_manager (helm release + namespace lifecycle);
# trial phase MUST NOT re-manage it, or `roksbnkctl bnk down` would execute the
# inner module's destroy provisioner — `kubectl delete namespace cert-manager` —
# and wipe cert_manager + every cert it issued.
variable "deploy_cert_manager" {
  description = "When true, the cert_manager module's helm/null_resource bring-up runs. Forced false by writeBnkPhaseOverrideAt when cluster-outputs.json exists (cluster phase already deployed cert_manager; trial phase consumes it via outputs that resolve to null on the bnk-phase apply, and downstream gates fall back to \"direct-apply\")."
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
  description = <<-EOT
    OpenShift cluster version as a BARE major.minor prefix (e.g. "4.20"). Empty
    uses the latest available.

    Do NOT include the "_openshift" suffix — this module appends it. A value that
    carries it matches nothing, and an unmatched value used to build the newest
    OCP silently (#178).
  EOT
  type        = string
  default     = "4.21"

  validation {
    # The suffix is appended by modules/roks_cluster/modules/cluster; supplying
    # it is always wrong, and catching it here costs a second where the
    # alternative was a 45-minute install failure on an unintended OCP version.
    condition     = !can(regex("_openshift", var.openshift_cluster_version))
    error_message = "openshift_cluster_version must be a bare version like \"4.20\" — the \"_openshift\" suffix is appended automatically."
  }
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
  default     = "bnk-supply-chain"
}

variable "ibmcloud_resources_cos_bucket" {
  description = "IBM Cloud COS bucket containing FAR auth key and JWT files"
  type        = string
  default     = "bnk-artifacts"
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

# Sprint 29 air-gap registry mirror. When a populated mirror exists, the
# single FAR host splits into a chart host (the registry route, reachable
# from the helm provider running on the host) and an image host (the
# in-cluster registry service, reachable from pods). Both default to "" and
# fall back to far_repo_url in the modules' locals, so behavior is
# BYTE-IDENTICAL when no mirror is configured.
variable "far_chart_repo_url" {
  description = "Chart-pull host for the air-gap mirror (helm_release repository + manifest pull). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "far_image_repo_url" {
  description = "Image-pull host for the air-gap mirror (image.repository + CNEInstance spec.registry.uri). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
}

variable "registry_mirror_username" {
  description = "Basic-auth username for an EXTERNAL registry mirror (e.g. a Harbor robot/admin). Empty → the mirror is the in-cluster/ICR registry, which authenticates via the kube token / IAM key instead."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password/token for an external registry mirror. When set (with use_registry_mirror), chart and image pulls authenticate to the mirror with these credentials instead of the in-cluster kube token."
  type        = string
  sensitive   = true
  default     = ""
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
  default     = "subscription.jwt"
}

# ---- Local-file supply chain (no COS) --------------------------------------
# When use_cos_bucket = false, the FAR service account + subscription JWT are
# injected directly (roksbnkctl reads local files and passes them here), so the
# BNK phase needs no orchestration COS instance/bucket. Empty defaults keep the
# COS path (use_cos_bucket = true) byte-identical.
variable "use_cos_bucket" {
  description = "Download the FAR auth tarball + subscription JWT from the orchestration COS. false = use the injected far_service_account_b64 / f5_cne_subscription_jwt content instead (local files)."
  type        = bool
  default     = true
}

variable "far_service_account_b64" {
  description = "FAR _json_key_base64 service account (base64 of the .json), injected when use_cos_bucket = false. Empty on the COS path."
  type        = string
  default     = ""
  sensitive   = true
}

variable "f5_cne_subscription_jwt" {
  description = "Subscription/license JWT token content, injected when use_cos_bucket = false. Empty on the COS path (downloaded from COS instead)."
  type        = string
  default     = ""
  sensitive   = true
}

# The BNK release line the rest of this tree gates on.
#
# WHY A VARIABLE AND NOT AN OVERLAY. Per-line HCL could live in terraform/lines/
# (see lines/README.md), but 2.4's differences are overwhelmingly ADDITIVE — new
# CR kinds alongside the old ones — and `count` expresses that without forking a
# file. An overlay is a copy, and two copies of a file drift.
#
# DERIVED, NOT CONFIGURED. roksbnkctl renders this from bnk.manifest_version, so
# it cannot disagree with the release actually being installed. It is declared
# with a default so a hand-run `terraform apply` still plans something coherent,
# and validated so a typo fails at plan time rather than by quietly building the
# wrong line.
variable "bnk_line" {
  description = "BNK release line driving per-release resource gating (2.3 or 2.4). Derived by roksbnkctl from bnk.manifest_version — set it by hand only for a standalone terraform run."
  type        = string
  default     = "2.3"

  validation {
    condition     = contains(["2.3", "2.4"], var.bnk_line)
    error_message = "bnk_line must be 2.3 or 2.4."
  }
}

# SET BOTH TO THE SAME VALUE FOR ONE NAMESPACE (#66). Supported and verified —
# the utils-side namespace and its duplicate secrets are then not created.
#
# Validated as RFC 1123 labels because the reference documentation has claimed
# they were for some time and nothing enforced it. An invalid namespace name is
# not caught until the apply, where it surfaces as a Kubernetes admission error
# partway through creating things.
variable "flo_namespace" {
  description = "Kubernetes namespace for the F5 Lifecycle Operator"
  type        = string
  default     = "f5-bnk"

  validation {
    condition     = can(regex("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", var.flo_namespace)) && length(var.flo_namespace) <= 63
    error_message = "flo_namespace must be a valid RFC 1123 label: lowercase alphanumerics and '-', starting and ending alphanumeric, at most 63 characters."
  }
}

variable "flo_utils_namespace" {
  description = "Kubernetes namespace for F5 utility components — used by flo, cne_instance, and license. Set equal to flo_namespace to install into ONE namespace."
  type        = string
  default     = "f5-utils"

  validation {
    condition     = can(regex("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", var.flo_utils_namespace)) && length(var.flo_utils_namespace) <= 63
    error_message = "flo_utils_namespace must be a valid RFC 1123 label: lowercase alphanumerics and '-', starting and ending alphanumeric, at most 63 characters."
  }
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

variable "flo_trusted_profile_sa_name" {
  description = <<-EOT
    Kubernetes service account name the CNE controller's IBM Cloud Trusted Profile is
    linked to — i.e. which service account may ASSUME the profile and act on the VPC.

    Must match the service account the CNE controller pod actually runs as. It is
    created by the FLO Helm chart, not by this terraform, so a value that does not
    match produces a profile nobody can assume: the pod starts, and its IBM Cloud
    calls fail later with an authorization error that says nothing about this setting.

    The same value also drives the privileged-SCC ClusterRoleBinding for that account
    (see modules/cne_instance), because a profile the pod may assume and an SCC the
    pod may use have to name the same account or one of them is inert.

    NOT part of the profile's uniqueness. See the note on the trusted profile resource:
    uniqueness comes from the profile name (which carries the cluster name) and from
    the link's cluster CRN, so every cluster in an account can safely use this same
    service account name.
  EOT
  type        = string
  default     = ""
}

variable "flo_trusted_profile_roles" {
  description = <<-EOT
    IAM roles granted to the CNE controller's Trusted Profile, scoped to the cluster's
    OWN VPC (serviceName=is, vpcId=<cluster vpc>).

    Editor is required for the controller to manage the VPC network attachments it
    creates for TMM. Narrow this only with a policy you have actually tested — an
    under-privileged profile fails at attachment time, on a running cluster, not at
    apply.
  EOT
  type        = list(string)
  default     = ["Viewer", "Editor"]

  validation {
    condition     = length(var.flo_trusted_profile_roles) > 0
    error_message = "flo_trusted_profile_roles cannot be empty; the CNE controller needs at least one role on the cluster VPC."
  }
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
  description = "Deployment size for CNEInstance (Tiny, Small, Medium, Large). EMPTY takes the line default: Small on 2.3, Tiny on 2.4 (what the 2.4 install guide and F5's reference cluster use). Tiny is what the BNK 2.4 install guide uses; it is passed through unvalidated, so a size a given manifest does not define is rejected by the operator, not here."
  type        = string
  default     = ""
}




variable "cneinstance_gslb_datacenter_name" {
  description = "GSLB datacenter name for CNEInstance (optional)"
  type        = string
  default     = ""
}

# Per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap +
# external/internal F5SPKVlan CRs (BNK install-guide "Configuration"). Empty
# (default) → the cne_instance module's install-guide defaults apply. roksbnkctl
# renders this from the optional config.yaml bnk.network.zones block.
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs + TMM self-IPs (empty = use install-guide defaults)"
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  default = []
}

variable "cneinstance_vlan_prefixlen_external" {
  description = <<-EOT
    External VLAN self-IP prefix length. 0 (default) → cneinstance_vlan_prefixlen,
    so a deployment that does not care keeps one knob and one value.

    Exists because the external and internal VLANs are not always the same size:
    an estate can front TMM on a /23 while keeping the internal side a /26, and a
    single shared scalar cannot express that. Setting it does NOT imply the
    subnets differ — the mask is deliberately independent of the CIDRs, so that a
    smaller or larger directly-connected block can be forced and the remainder
    steered with static routes.
  EOT
  type        = number
  default     = 0

  validation {
    condition     = var.cneinstance_vlan_prefixlen_external == 0 || (var.cneinstance_vlan_prefixlen_external >= 1 && var.cneinstance_vlan_prefixlen_external <= 32)
    error_message = "cneinstance_vlan_prefixlen_external must be 0 (inherit) or a valid IPv4 prefix length 1-32."
  }
}

variable "cneinstance_vlan_prefixlen_internal" {
  description = "Internal VLAN self-IP prefix length. 0 (default) → cneinstance_vlan_prefixlen. See the external variable for why the two can differ."
  type        = number
  default     = 0

  validation {
    condition     = var.cneinstance_vlan_prefixlen_internal == 0 || (var.cneinstance_vlan_prefixlen_internal >= 1 && var.cneinstance_vlan_prefixlen_internal <= 32)
    error_message = "cneinstance_vlan_prefixlen_internal must be 0 (inherit) or a valid IPv4 prefix length 1-32."
  }
}

variable "cneinstance_vlan_prefixlen" {
  description = "TMM self-IP prefix length (spec.prefixlen_v4) for the external/internal F5SPKVlan CRs"
  type        = number
  default     = 24
}

variable "cneinstance_tmm_k8s_routes" {
  description = "Pod CIDR TMM routes to (advanced.tmm.env TMM_K8S_ROUTES). Default is the ROKS default pod subnet."
  type        = string
  default     = "172.17.0.0/18"
}


# ============================================================
# license
# ============================================================

variable "license_mode" {
  description = "License operation mode (connected, disconnected, or f5licenseproxy)"
  type        = string
  default     = "connected"
}

variable "flp_license_server_url" {
  description = "Base URL of the in-cluster F5 License Proxy service (FLP mode only; e.g. https://f5-license-proxy.<ns>.svc.cluster.local:8443)"
  type        = string
  default     = ""
}

variable "license_server_root_ca" {
  description = "PEM of the FLP root CA, written into the licenseserver-rootca Secret so CWC trusts the proxy (FLP mode only)"
  type        = string
  default     = ""
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


# ============================================================
# gateway — data-plane ingress/egress config (optional phase)
# ============================================================

variable "deploy_gateway" {
  description = "Master toggle for the Gateway phase. Off in every other phase's override; on only for `gateway up`."
  type        = bool
  default     = false
}

variable "deploy_flp" {
  description = "Master toggle for the F5 License Proxy phase. Off in every other phase's override; on only for `flp up`."
  type        = bool
  default     = false
}

variable "flp_namespace" {
  description = "Namespace the F5 License Proxy is installed into (FLP phase)."
  type        = string
  default     = "f5-license-proxy"
}

variable "flp_chart_version" {
  description = "Pin for the f5-license-proxy chart version (empty → registry latest)."
  type        = string
  default     = ""
}

variable "flp_storage_class" {
  description = "Dynamic StorageClass for the FLP's PVCs (FLP phase). Default = ROKS VPC block."
  type        = string
  default     = "ibmc-vpc-block-metro-10iops-tier"
}

variable "gateway_app_namespace" {
  description = "Application namespace the Gateway + HTTPRoute serve (created by the gateway module)"
  type        = string
  default     = "f5-app"
}

variable "gateway_class_name" {
  description = "GatewayClass name. Set it to run more than one BNK GatewayClass in a cluster — GatewayClass is cluster-scoped, so two installs sharing the name collide."
  type        = string
  default     = "gateway-class"
}

variable "gateway_route_examples" {
  description = "Extra route kinds to create working examples of, alongside the default HTTPRoute. Valid on BNK 2.3: GRPCRoute, L4Route. Empty (default) leaves an existing deployment byte-identical. L4Route also adds a TCP listener to the Gateway, because an L4Route cannot attach to an HTTP one."
  type        = list(string)
  default     = []
  # Validated HERE as well as in the module so a bad value fails at the point the
  # user set it, with the root variable's name in the error, rather than surfacing
  # from inside a module the user did not write. The module keeps its own copy so
  # it stays correct when applied directly.
  validation {
    condition = alltrue([
      for k in var.gateway_route_examples : contains(["GRPCRoute", "L4Route"], k)
    ])
    error_message = "gateway_route_examples accepts only GRPCRoute and L4Route on BNK 2.3 (Gateway API 1.4.1 standard installs no TCPRoute/TLSRoute/UDPRoute; BNK provides L4Route for TCP)."
  }
}

variable "gateway_l4_listener_port" {
  description = "Port for the TCP listener added when gateway_route_examples includes L4Route"
  type        = number
  default     = 8080
}

variable "gateway_controller_name" {
  description = "GatewayClass controllerName. Empty → derived as f5.com/<flo_namespace>-f5-cne-controller, which is the value the CNE controller answers to. Set it only to point the GatewayClass at a controller this deployment did not install; a wrong value fails silently (the GatewayClass is never Accepted and the apply still succeeds)."
  type        = string
  default     = ""
}

variable "gateway_backend_service" {
  description = "HTTPRoute backend Service name in the app namespace"
  type        = string
  default     = "nginx-service"
}

variable "gateway_backend_port" {
  description = "HTTPRoute backend Service port"
  type        = number
  default     = 80
}

variable "gateway_egress_mode" {
  description = "Egress SNAT strategy: snatpool (default), automap, or both"
  type        = string
  default     = "snatpool"
}

variable "gateway_client_subnet_local" {
  description = "Local-VSI client subnet CIDRs the static routes reach (cluster-VPC clients; one route per entry × zone). Empty = no local client routes. `gateway up` auto-derives these from the cluster jumphost subnets when unset (PRD 12)."
  type        = list(string)
  default     = []
}

variable "gateway_client_subnet_remote" {
  description = "Remote-VSI client subnet CIDRs the static routes reach (client-VPC clients over the TGW; one route per entry × zone). Empty = no remote client routes."
  type        = list(string)
  default     = []
}

variable "gateway_vxlan_port" {
  description = "Egress VXLAN UDP port (also opened on the cluster security group)"
  type        = number
  default     = 6789
}

variable "flp_node_port_access" {
  description = "Expose the F5 License Proxy outside its own cluster (NodePort + worker-node-IP cert SANs), so a BNK install in a different cluster can license through it."
  type        = bool
  default     = false
}

variable "flp_node_port_source_cidrs" {
  description = "With flp_node_port_access: open the proxy's NodePort on the worker security group to these CIDRs. A LIST — a multi-zone VPC has one address prefix per zone, and a consuming pod in an unlisted zone is silently dropped."
  type        = list(string)
  default     = []
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary. helm invokes it as the f5-license-proxy chart's post-renderer (`roksbnkctl flp postrender`), so the FLP install needs no interpreter on the host. roksbnkctl sets this automatically via TF_VAR_roksbnkctl_binary; empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

# ── FLP as a VSI (bnk.flp.mode: vsi) ──────────────────────────────────────────
variable "deploy_flp_vsi" {
  description = "Deploy the F5 License Proxy as a standalone VSI (podman pod, no k8s) instead of the helm chart. Set true only by the FLP phase in mode: vsi."
  type        = bool
  default     = false
}
variable "flp_status_image" {
  description = "Optional flp-status web UI image for the standalone FLP VSI (mirror/public ref). Empty = no status UI."
  type        = string
  default     = ""
}

variable "flp_status_registry_host" {
  description = "Registry host:port whose CA to trust so the FLP VSI can pull flp_status_image (e.g. Harbor's <ip>)."
  type        = string
  default     = ""
}

variable "flp_status_registry_ca_b64" {
  description = "Base64 CA cert for flp_status_registry_host."
  type        = string
  default     = ""
}

variable "flp_vsi_ssh_key" {
  description = "Existing IBM Cloud VPC SSH key name to attach to the standalone FLP VSI (operator access). Empty = no key."
  type        = string
  default     = ""
}

variable "flp_vsi_profile" {
  description = "VSI instance profile for the FLP (>= 4 vCPU / 8 GB)."
  type        = string
  default     = "bx2-4x16"
}
variable "flp_vsi_zone" {
  description = "Zone for the FLP VSI. Empty → <region>-1."
  type        = string
  default     = ""
}
variable "flp_vsi_boot_size_gb" {
  description = "Boot volume size (GB) for the FLP VSI (>= 80)."
  type        = number
  default     = 100
}
variable "flp_vsi_floating_ip" {
  description = "Attach an operator floating IP to the FLP VSI for remote management (flp status + web UI + 8443 from another machine). Not the CWC endpoint. Reachability still gated by flp_vsi_allowed_cidrs. Default true."
  type        = bool
  default     = true
}
variable "flp_vsi_management_allowed_cidrs" {
  description = "Source CIDRs for the FLP VSI's :80 flp-status web UI (read-only). Empty → 0.0.0.0/0 (open)."
  type        = list(string)
  default     = []
}
variable "flp_vsi_licensing_allowed_cidrs" {
  description = "Source CIDRs for the FLP VSI's :8443 proxy (+ :22 SSH). Empty → RFC-1918 private ranges."
  type        = list(string)
  default     = []
}
variable "flp_vsi_allowed_cidrs" {
  description = "DEPRECATED — legacy single list; seeds both management + licensing when set. Prefer the two per-plane variables."
  type        = list(string)
  default     = []
}
variable "flp_prod_jwks_b64" {
  description = "Optional override: base64 of F5's public prod_jwks.txt. Empty → the flp_vsi module extracts it from the f5-license-proxy chart."
  type        = string
  default     = ""
}
variable "flp_forward_proxy_host" {
  type    = string
  default = ""
}
variable "flp_forward_proxy_port" {
  type    = number
  default = 0
}
variable "flp_forward_proxy_protocol" {
  type    = string
  default = "http"
}

# ── Transit Gateway connection phase (roksbnkctl tgw connect) ─────────────────

variable "deploy_tgw_connection" {
  description = "Attach the cluster's VPC to an existing Transit Gateway. On only for the tgw phase; a no-op everywhere else."
  type        = bool
  default     = false
}

variable "tgw_connection_target" {
  description = "Existing Transit Gateway to attach the cluster VPC to, by NAME or ID. Multiple clusters passing the same value share one gateway."
  type        = string
  default     = ""
}

variable "tgw_connection_name" {
  description = "Name for this cluster's connection on the gateway (unique per gateway; prefix-derived so shared-gateway clusters don't collide)."
  type        = string
  default     = ""
}

variable "cluster_absent" {
  description = "True only in the standalone FLP-VSI phase: no ROKS cluster exists or will be adopted, so all cluster data-source lookups + kube providers across modules are skipped (count=0)."
  type        = bool
  default     = false
}

variable "cluster_vpc_cidr" {
  description = <<-EOT
    CIDR block the cluster VPC's per-zone address prefixes are carved from
    (e.g. "10.241.0.0/16" → 10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18).

    Empty (the default) leaves IBM's "auto" address prefix management in place,
    which gives EVERY VPC in a region the same prefixes — so two roksbnkctl-created
    clusters cannot share a Transit Gateway without overlapping. Set a distinct block
    per cluster when they must. Ignored when reusing an existing VPC. See issue #46.
  EOT
  type        = string
  default     = ""

  validation {
    condition     = var.cluster_vpc_cidr == "" || can(cidrhost(var.cluster_vpc_cidr, 0))
    error_message = "cluster_vpc_cidr must be empty or a valid CIDR block, e.g. 10.241.0.0/16."
  }
  validation {
    condition     = var.cluster_vpc_cidr == "" || try(tonumber(split("/", var.cluster_vpc_cidr)[1]) <= 18, false)
    error_message = "cluster_vpc_cidr needs /18 or larger. It is split into three per-zone prefixes (/n+2) and each cluster subnet is the first /n+8 of its zone: /16 gives 256-address subnets (today's size), /17 gives 128, /18 gives 64."
  }
}

# ── BYO cluster subnets (#61) ────────────────────────────────────────────────
# Adopting the VPC alone is not enough for an estate that allocates address space
# centrally: the subnets carry the ACLs and routing, and a cluster placed in
# freshly-created subnets sits outside all of it.
variable "use_existing_cluster_subnets" {
  description = "Place the cluster in subnets that already exist instead of creating them. Requires use_existing_cluster_vpc — a subnet cannot be adopted independently of its VPC."
  type        = bool
  default     = false
}

variable "existing_cluster_subnet_ids" {
  description = "Subnet ids to place the cluster in, one per zone, in zone order. Used only when use_existing_cluster_subnets = true. Their zones are read from the subnets themselves."
  type        = list(string)
  default     = []
}

# ── The proxy's own network (#60) ────────────────────────────────────────────
# Forwarded to modules/flp_vsi. Default false keeps every existing workspace on
# the adopt path, byte-identical.
variable "flp_vsi_create_vpc" {
  description = "Build the F5 License Proxy its own VPC instead of placing it in an existing one."
  type        = bool
  default     = false
}

variable "flp_vsi_name_prefix" {
  description = "Prefix for the standalone FLP VSI's resource names (e.g. bnk-ci → bnk-ci-flp-vsi). Empty (the default) keeps the legacy unprefixed names so an existing proxy is NOT replaced on upgrade. Set it to run more than one FLP in an account, and to bring the resources into `roksbnkctl cleanup`'s <prefix>-* sweep."
  type        = string
  default     = ""
}

variable "flp_vsi_vpc_name" {
  description = "Name for the VPC created when flp_vsi_create_vpc = true."
  type        = string
  default     = ""
}

variable "flp_vsi_subnet_cidr" {
  description = "Address prefix for the VPC created when flp_vsi_create_vpc = true."
  type        = string
  default     = "10.250.0.0/24"

  validation {
    condition     = var.flp_vsi_subnet_cidr == "" || can(cidrhost(var.flp_vsi_subnet_cidr, 0))
    error_message = "flp_vsi_subnet_cidr must be a valid IPv4 CIDR, e.g. 10.250.0.0/24."
  }
}

# ── Worker network attachment mode ───────────────────────────────────────────
# single-nic is today's behaviour and the default. multi-nic changes both the IBM
# cluster creation semantics and what the BNK phase must attach to, which is why
# it is fixed at creation and never converted in place.
# Declared and validated here before any module consumes it. That is deliberate:
# the value decides how a cluster is BUILT, so an unknown one must be rejected at
# plan time by the same configuration that will later act on it — not only by the
# CLI, which a `terraform apply` run by hand would bypass. The multi-nic HCL
# arrives as a per-line overlay (see lines/README.md) once the IBM module that
# expresses it ships.
variable "cluster_network_mode" {
  description = "How the cluster's worker nodes are attached: single-nic (default) or multi-nic."
  type        = string
  default     = "single-nic"

  validation {
    condition     = contains(["single-nic", "multi-nic"], var.cluster_network_mode)
    error_message = "cluster_network_mode must be single-nic or multi-nic."
  }
}

variable "testing_jumphost_allowed_cidrs" {
  description = "Source CIDRs allowed to SSH (:22) to the testing jumphosts. Empty → 0.0.0.0/0 (open; access is key-only). Narrow to the operator's public /32 on a shared account."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.testing_jumphost_allowed_cidrs : can(cidrhost(c, 0))])
    error_message = "testing_jumphost_allowed_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}
variable "testing_client_vpc_inbound_cidrs" {
  description = "Source CIDRs allowed inbound to the testing client VPC's default security group. Empty → the RFC-1918 private ranges (in-fabric test traffic arrives over the Transit Gateway)."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.testing_client_vpc_inbound_cidrs : can(cidrhost(c, 0))])
    error_message = "testing_client_vpc_inbound_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}
variable "cluster_http_allowed_cidrs" {
  description = "Source CIDRs allowed to reach :80 on the cluster security group. Empty → 0.0.0.0/0 (the ingress/ALB path is meant to be publicly reachable)."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.cluster_http_allowed_cidrs : can(cidrhost(c, 0))])
    error_message = "cluster_http_allowed_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}
variable "cluster_vpc_default_sg_inbound_cidrs" {
  description = "Source CIDRs allowed inbound (all protocols/ports) to the cluster VPC's default security group. Empty → 0.0.0.0/0, the historical behaviour. Narrow to your private ranges unless a workload in this VPC needs a public source."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.cluster_vpc_default_sg_inbound_cidrs : can(cidrhost(c, 0))])
    error_message = "cluster_vpc_default_sg_inbound_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}

variable "cneinstance_tmm_resources" {
  description = <<-EOT
    Overrides the TMM pod's resource requests/limits, rendered as the
    CNEInstance's advanced.tmm.resources (#203).

    The controller derives TMM's resources from deploymentSize, and every size
    above Tiny asks for hugepages -- Small requests 4Gi of hugepages-2Mi. A
    stock ROKS worker reports zero and there is no supported way to allocate
    them: the Node Tuning Operator route needs MachineConfigPools that ROKS
    does not have, and ROKS deletes a user-created Tuned CR outright.

    F5's own 2.3.1 sizing guide describes the reference-tested Small profile as
    "TMM: 1 thread, 1 vCPU / 1.5 GiB, hugepages disabled", so running without
    them is the reference configuration rather than a workaround. Setting this
    replaces the derived block, which is what drops the hugepages request:

      cneinstance_tmm_resources = {
        requests = { cpu = "1000m", memory = "1536Mi" }
        limits   = { cpu = "1000m", memory = "1536Mi" }
      }

    Empty renders no resources key at all, leaving the controller's derived
    values untouched, so a workspace that does not set this plans exactly as
    it did before.
  EOT
  type        = map(map(string))
  default     = {}
}

variable "cneinstance_advanced_env" {
  description = <<-EOT
    Per-component environment passthrough for the BNK 2.4 CNEInstance's
    advanced.<component>.env[] lists (#175).

    A map of component name to a map of env name/value, e.g.

      cneinstance_advanced_env = {
        tmm           = { TMM_DEFAULT_MTU = "9000" }
        cneController = { USE_GATEWAY_SETTINGS = "true" }
      }

    Empty renders no advanced block at all, so a workspace that sets none of
    these plans exactly as it did before. A map rather than a typed object
    because the component set belongs to the product — F5 adds components between
    releases, and a typed schema would make each addition a code change here
    before anyone could use it.
  EOT
  type        = map(map(string))
  default     = {}
}

# ── BNK 2.4 conformance with F5's reference CNEInstance ──────────────────────
#
# Everything below is emitted on 2.4 ONLY. The 2.3 spec is asserted
# byte-identical to the previous release, so none of these may appear there.
#
# The defaults are F5's reference values, taken from the live 2.4 cluster
# capture in /mnt/d/roksbnkctl-gap-2-3-to-2-4 — not invented. A field whose
# default differs from the reference is a field that needs a reason.

variable "cneinstance_tmm_replicas" {
  description = "Number of f5-tmm data-plane replicas (2.4). Reference: 3."
  type        = number
  default     = 3
}

variable "cneinstance_watch_namespaces" {
  description = "Namespaces the CNE controller watches (2.4). Reference: [\"All\"]."
  type        = list(string)
  default     = ["All"]
}

variable "cneinstance_tmm_anti_affinity" {
  description = <<-EOT
    Require f5-tmm pods onto DIFFERENT NODES (2.4).

    On 2.4 this replaces the node-labeler: 2.4 removed the labeler (#171) and
    `placement` is the mechanism that took over. Without it nothing stops two
    TMMs landing on one node — which happened to work in verification only
    because the scheduler spread them, not because anything required it.
  EOT
  type        = bool
  default     = true
}

variable "cneinstance_tmm_zone_spread" {
  description = "Spread f5-tmm pods across zones with maxSkew 1, DoNotSchedule (2.4). Reference: on."
  type        = bool
  default     = true
}

variable "cneinstance_tmm_rolling_update" {
  description = <<-EOT
    Pin TMM's rolling update to maxSurge 0 / maxUnavailable 1 (2.4).

    Same shape as the cwc Multi-Attach deadlock: an unconstrained rolling update
    on a workload holding a single-attach resource can wedge. Reference sets it.
  EOT
  type        = bool
  default     = true
}

variable "cneinstance_external_bigip" {
  description = "Enable the external BIG-IP controller (2.4). Reference: true."
  type        = bool
  default     = false
}

variable "cneinstance_external_bigip_login_secret" {
  description = "Secret holding external BIG-IP credentials (2.4). Reference: f5-bigip-ctlr-login."
  type        = string
  default     = "f5-bigip-ctlr-login"
}

variable "cneinstance_cluster_identifier" {
  description = "CLUSTER_IDENTIFIER passed to the external BIG-IP controller (2.4). Empty derives from the cluster name."
  type        = string
  default     = ""
}

variable "cneinstance_gateway_api_version" {
  description = <<-EOT
    GATEWAY_API_VERSION for the CNE controller (2.4). Reference: 1.5.0.

    roksbnkctl previously set this nowhere, so the controller ran on whatever
    the operator defaulted to — v1.4.1 on the verified cluster. The 2.4 EA guide
    requires the 1.5 bundle for mTLS.
  EOT
  type        = string
  default     = "1.5.0"
}

variable "gateway_api_bundle_url" {
  description = <<-EOT
    Where the Gateway API standard-install bundle is FETCHED from when the
    workspace has no registry mirror recorded (2.4 + gateway_api_mtls). Empty
    derives the upstream release asset for cneinstance_gateway_api_version.

    Terraform installs nothing from this. roksbnkctl fetches the bundle, checks
    it against the sha256 it pins for that version, and server-side-applies it
    inside the window where the gateway-api admission-policy sweep is running —
    which is the only window in which the apply survives the OpenShift ingress
    operator recreating its policy.

    It is declared here so terraform REJECTS a malformed URL at plan time.
    Without the declaration, roksbnkctl rendering the value into terraform.tfvars
    would fail the apply outright ("value for undeclared variable"); with it but
    without the validation, a typo would instead surface as a fetch failure once
    the apply was already under way.
  EOT
  type        = string
  default     = ""

  validation {
    condition     = var.gateway_api_bundle_url == "" || can(regex("^https://[^[:space:]]+\\.(yaml|yml)$", var.gateway_api_bundle_url))
    error_message = "gateway_api_bundle_url must be empty (derive the upstream release) or an https:// URL ending in .yaml/.yml. Plain http is refused: the bundle is applied to the cluster with --force-conflicts, and its sha256 pin authenticates the CONTENT, not the peer that served it."
  }
}

variable "cneinstance_demo_mode" {
  description = <<-EOT
    advanced.demoMode.enabled.

    Empty string means "the line default": true on 2.3 (what has always
    shipped) and FALSE on 2.4, matching F5's reference. Demo mode was being set
    true on every install, which is not something a customer deployment should
    carry. Set "true"/"false" to pin it explicitly.
  EOT
  type        = string
  default     = ""
}

variable "cneinstance_tmm_pod_label" {
  description = "Value of the `app` label the placement rules select f5-tmm pods by (2.4). Reference: f5-tmm."
  type        = string
  default     = "f5-tmm"
}

variable "cneinstance_tmm_anti_affinity_topology_key" {
  description = <<-EOT
    Node label the TMM anti-affinity rule spreads across (2.4).

    The IBM ROKS per-node label. Surfaced rather than hard-coded so a cluster
    that labels its topology differently is still configurable — the assumption
    the node-labeler used to bake in.
  EOT
  type        = string
  default     = "kubernetes.io/hostname"
}

variable "cneinstance_tmm_zone_topology_key" {
  description = "Node label the TMM zone spread uses (2.4). The IBM ROKS zone label. Reference: topology.kubernetes.io/zone."
  type        = string
  default     = "topology.kubernetes.io/zone"
}

variable "cneinstance_tmm_zone_max_skew" {
  description = "maxSkew for the TMM zone topology-spread constraint (2.4). Reference: 1."
  type        = number
  default     = 1
}

variable "cneinstance_tmm_zone_when_unsatisfiable" {
  description = "whenUnsatisfiable for the TMM zone spread (2.4): DoNotSchedule or ScheduleAnyway. Reference: DoNotSchedule."
  type        = string
  default     = "DoNotSchedule"
  validation {
    condition     = contains(["DoNotSchedule", "ScheduleAnyway"], var.cneinstance_tmm_zone_when_unsatisfiable)
    error_message = "cneinstance_tmm_zone_when_unsatisfiable must be DoNotSchedule or ScheduleAnyway."
  }
}

variable "cneinstance_tcp_settings" {
  description = <<-EOT
    F5BigTcpSetting field overrides, as a flat map of field name to value.

    The CR has 54 fields across bool, int and string. Surfacing each as its own
    config field would be 54 fields nobody reads; a map keeps the whole surface
    reachable and lets F5 add fields between releases without a code change here.

    Values are strings and coerced on the way out: "1500" renders as a number,
    "true" as a bool, anything else as a string. That is because a config file
    and an environment variable can only carry text, while the CR is typed.

    Empty renders NO CR at all. That is deliberate — the product creates its own
    default TCP profile, and emitting an empty one would fight it.
  EOT
  type        = map(string)
  default     = {}
}

variable "cneinstance_tcp_settings_name" {
  description = "Name of the F5BigTcpSetting CR to write. Reference: sys-default-tcp."
  type        = string
  default     = "sys-default-tcp"
}

variable "cneinstance_whole_cluster_override" {
  description = <<-EOT
    spec.wholeCluster, as a tri-state.

    Empty means the LINE default: true on 2.3, which is what has always shipped,
    and FALSE on 2.4, matching F5's reference. "true"/"false" pins it.

    This has to move with watchNamespaces. The two are validated together by the
    product — `wholeCluster: true` with `watchNamespaces: ["All"]` is rejected as
    "Invalid product configuration, please check WholeCluster, WatchNamespaces
    and GatewayAPI settings", because saying "watch everything" twice in two
    different ways is a contradiction rather than emphasis.
  EOT
  type        = string
  default     = ""
}

variable "roks_worker_flavor" {
  description = <<-EOT
    Exact worker-node flavor, e.g. "cx3d.8x20". Empty auto-selects.

    The auto-select only considers the bx2 family — its filter is
    `^bx2-[0-9]+x[0-9]+$` — so any other profile family is unreachable without
    naming it here. F5's approved reference cluster runs cx3d.8x20, which the
    auto-select can never produce at any minimum.

    The inner cluster module has always honoured this; nothing surfaced it, so no
    config.yaml or environment override could reach it.
  EOT
  type        = string
  default     = ""
}

# ── hugepages on the worker pool ─────────────────────────────────────────────

variable "cneinstance_hugepages" {
  description = <<-EOT
    Allocate hugepages on the worker pool via the OpenShift Node Tuning Operator.

    OFF by default. Turning it on sets a bootloader kernel argument, which makes
    the Machine Config Operator DRAIN AND REBOOT every matching worker, one at a
    time. That is a maintenance event, not a configuration change, and is not
    something a default should decide.
  EOT
  type        = bool
  default     = false
}

variable "cneinstance_hugepages_size" {
  description = "Hugepage size, e.g. 2M or 1G. TMM requests hugepages-2Mi, so 2M is the matching size."
  type        = string
  default     = "2M"
}

variable "cneinstance_hugepages_count" {
  description = "Hugepages PER NODE. 2048 x 2M = 4Gi, which is what deploymentSize Small was observed to request."
  type        = number
  default     = 2048
}

variable "cneinstance_hugepages_node_role" {
  description = "machineconfiguration.openshift.io/role the Tuned profile applies to."
  type        = string
  default     = "worker"
}

variable "cneinstance_hugepages_profile_name" {
  description = "Name of the Tuned profile and CR."
  type        = string
  default     = "bnk-hugepages"
}

variable "cneinstance_storage_class_name" {
  description = <<-EOT
    StorageClass for the CNEInstance's persistent volumes, TMM's included. Empty
    leaves the CR's own default, which resolves to the cluster default class.

    This matters because TMM's replicas are pinned to separate nodes across
    separate zones by the placement F5's reference prescribes, while their volume
    is shared. The stock ROKS default (ibmc-vpc-block-*, ReadWriteOnce, zonal)
    can therefore bind only one of them and the rest stay Pending. A
    ReadWriteMany class from the vpc-file-csi-driver addon serves all three —
    ibmc-vpc-file-regional additionally spans zones.
  EOT
  type        = string
  default     = ""
}
