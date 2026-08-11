# flp_vsi — mode: vsi FLP backend. Provisions a headless Ubuntu VSI in the cluster
# VPC running the f5-license-proxy stack as a podman pod (no Kubernetes). Terraform
# generates the CA and injects it; the box (cloud-init) signs the leaves and brings
# up the pod. Egress to FAR + F5 licensing goes out a public gateway; the VSI keeps
# a private VPC IP for the CWC to dial.

locals {
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"
  enabled        = var.deploy_flp_vsi
  zone           = var.flp_vsi_zone != "" ? var.flp_vsi_zone : "${var.ibmcloud_cluster_region}-1"
  # The CWC/CNEInstance endpoint is ALWAYS the private VPC IP — the consuming
  # cluster reaches the proxy privately (same VPC or over a Transit Gateway).
  reach_ip = local.enabled ? ibm_is_instance.flp[0].primary_network_interface[0].primary_ipv4_address : ""
  # Optional operator floating IP — a MANAGEMENT path (remote `roksbnkctl flp
  # status` + the :80 web UI from another machine), NOT the CWC endpoint. Reserved
  # zone-only (no target) so its address is known before the instance and can be
  # baked into the leaf-cert SAN; bound to the NIC after the instance exists.
  # Reserving with target=NIC instead would form an instance→cloud-init→fip→instance
  # cycle.
  floating_ip_addr = local.enabled && var.flp_vsi_floating_ip ? try(ibm_is_floating_ip.flp[0].address, "") : ""
  # FLP chart/image tag: pinned, else resolved from the BNK manifest.
  flp_tag = var.flp_chart_version != "" ? var.flp_chart_version : try(data.external.flp_version[0].result.v, "")
}

# ── resource group + newest Ubuntu 24.04 stock image ─────────────────────────
data "ibm_resource_groups" "all" {
  count = local.enabled ? 1 : 0
}
data "ibm_resource_group" "rg" {
  count = local.enabled ? 1 : 0
  name = var.ibmcloud_resource_group != "" ? var.ibmcloud_resource_group : [
    for rg in data.ibm_resource_groups.all[0].resource_groups : rg.name if rg.is_default == true
  ][0]
}
data "ibm_is_images" "ubuntu" {
  count      = local.enabled ? 1 : 0
  status     = "available"
  visibility = "public"
}
locals {
  ubuntu_names = local.enabled ? sort([
    for im in data.ibm_is_images.ubuntu[0].images : im.name
    if length(regexall("^ibm-ubuntu-24-04-[0-9]+-minimal-amd64-[0-9]+$", im.name)) > 0
  ]) : []
  ubuntu_image_id = local.enabled ? one([
    for im in data.ibm_is_images.ubuntu[0].images : im.id
    if im.name == local.ubuntu_names[length(local.ubuntu_names) - 1]
  ]) : ""
}

# ── network: reuse the cluster VPC, add a subnet with egress + an SG ──────────
# ── The VPC: adopted, or created here (#60) ──────────────────────────────────
# The proxy is the one component that needs egress to F5, which makes it a natural
# FIRST deployment in an air-gapped estate. It could not be, because the module had
# no create path — so it had to land in a VPC something else had already made, in
# practice the registry's, coupling licensing to a registry that has nothing to do
# with it.
#
# create_vpc owns the whole network: VPC, its address prefix, and the public gateway
# (there is no other tenant to inherit one from). The adopt path is unchanged.
resource "ibm_is_vpc" "flp" {
  count                     = local.enabled && var.flp_vsi_create_vpc ? 1 : 0
  name                      = var.flp_vsi_vpc_name != "" ? var.flp_vsi_vpc_name : "flp-vsi-vpc"
  resource_group            = data.ibm_resource_group.rg[0].id
  address_prefix_management = "manual"
}

resource "ibm_is_vpc_address_prefix" "flp" {
  count = local.enabled && var.flp_vsi_create_vpc ? 1 : 0
  name  = "flp-vsi-prefix"
  vpc   = ibm_is_vpc.flp[0].id
  zone  = local.zone
  cidr  = var.flp_vsi_subnet_cidr
}

data "ibm_is_vpc" "cluster" {
  count      = local.enabled && !var.flp_vsi_create_vpc ? 1 : 0
  identifier = var.existing_cluster_vpc_id

  lifecycle {
    precondition {
      condition     = var.existing_cluster_vpc_id != ""
      error_message = "the F5 License Proxy needs a network: set bnk.flp.vsi.vpc to adopt one, or bnk.flp.vsi.create_vpc = true to have it build its own."
    }
  }
}

locals {
  # Everything below uses this, never the resource or the data source directly.
  flp_vpc_id = var.flp_vsi_create_vpc ? try(ibm_is_vpc.flp[0].id, "") : try(data.ibm_is_vpc.cluster[0].id, "")
}
# IBM Cloud allows exactly ONE public gateway per zone per VPC. The cluster phase
# already attached a gateway to every zone of the cluster VPC, so creating a
# second one in the FLP VSI's zone fails ("over quota. Quota: 1"). Look the VPC's
# gateways up and REUSE the one already in this zone; create ours only if absent.
data "ibm_is_public_gateways" "vpc" {
  count = local.enabled ? 1 : 0
}
locals {
  existing_pgw_id = local.enabled && !var.flp_vsi_create_vpc ? lookup({
    for g in try(data.ibm_is_public_gateways.vpc[0].public_gateways, []) :
    g.zone => g.id if try(g.vpc, "") == local.flp_vpc_id
  }, local.zone, "") : ""
  create_pgw = local.enabled && local.existing_pgw_id == ""
  # The gateway the subnet attaches to: the one already in the zone, else ours.
  pgw_id = local.existing_pgw_id != "" ? local.existing_pgw_id : try(ibm_is_public_gateway.egress[0].id, "")
}
resource "ibm_is_public_gateway" "egress" {
  count          = local.create_pgw ? 1 : 0
  name           = "flp-vsi-egress"
  vpc            = local.flp_vpc_id
  zone           = local.zone
  resource_group = data.ibm_resource_group.rg[0].id
}
resource "ibm_is_subnet" "flp" {
  count                    = local.enabled ? 1 : 0
  depends_on               = [ibm_is_vpc_address_prefix.flp]
  name                     = "flp-vsi-subnet"
  vpc                      = local.flp_vpc_id
  zone                     = local.zone
  total_ipv4_address_count = 16
  public_gateway           = local.pgw_id
  resource_group           = data.ibm_resource_group.rg[0].id
}
resource "ibm_is_security_group" "flp" {
  count          = local.enabled ? 1 : 0
  name           = "flp-vsi-sg"
  vpc            = local.flp_vpc_id
  resource_group = data.ibm_resource_group.rg[0].id
}
locals {
  # Ingress source CIDRs, split by plane so each defaults to a sane posture on the
  # (now default-on) floating IP:
  #  - MANAGEMENT (:80 flp-status) is a read-only status page → defaults OPEN.
  #  - LICENSING (:8443 proxy) + SSH (:22) are trusted-access → default to the
  #    RFC-1918 private ranges (the cluster reaches the proxy privately over the
  #    VPC / Transit Gateway; it never needs a public source).
  # The legacy single flp_vsi_allowed_cidrs, when set, seeds BOTH planes so existing
  # configs keep working (it takes precedence over the per-plane defaults, not over
  # an explicitly-set per-plane list).
  flp_rfc1918 = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
  flp_mgmt_cidrs = length(var.flp_vsi_management_allowed_cidrs) > 0 ? var.flp_vsi_management_allowed_cidrs : (
    length(var.flp_vsi_allowed_cidrs) > 0 ? var.flp_vsi_allowed_cidrs : ["0.0.0.0/0"]
  )
  flp_licensing_cidrs = length(var.flp_vsi_licensing_allowed_cidrs) > 0 ? var.flp_vsi_licensing_allowed_cidrs : (
    length(var.flp_vsi_allowed_cidrs) > 0 ? var.flp_vsi_allowed_cidrs : local.flp_rfc1918
  )
}
# ingress to 8443 — the licensing proxy. Private by default (licensing plane).
resource "ibm_is_security_group_rule" "flp_in" {
  for_each  = local.enabled ? toset(local.flp_licensing_cidrs) : toset([])
  group     = ibm_is_security_group.flp[0].id
  direction = "inbound"
  remote    = each.value
  # Top-level protocol/port_min/port_max — the nested `tcp {}` block form is
  # deprecated ("tcp is deprecated, use 'protocol', 'code', and 'type' instead").
  # Matches the flat form already used in modules/flp and modules/testing.
  protocol = "tcp"
  port_min = 8443
  port_max = 8443
}
# ingress to 22 (SSH) — ONLY when an operator SSH key is attached. Trusted access,
# so it follows the licensing (private) plane, NOT the open management plane.
resource "ibm_is_security_group_rule" "flp_ssh" {
  for_each  = local.enabled && var.flp_vsi_ssh_key != "" ? toset(local.flp_licensing_cidrs) : toset([])
  group     = ibm_is_security_group.flp[0].id
  direction = "inbound"
  remote    = each.value
  protocol  = "tcp"
  port_min  = 22
  port_max  = 22
}
# ingress to 80 (flp-status web UI) — ONLY when the status UI is enabled. Read-only
# status, so it uses the management plane (default open).
resource "ibm_is_security_group_rule" "flp_status" {
  for_each  = local.enabled && var.flp_status_image != "" ? toset(local.flp_mgmt_cidrs) : toset([])
  group     = ibm_is_security_group.flp[0].id
  direction = "inbound"
  remote    = each.value
  protocol  = "tcp"
  port_min  = 80
  port_max  = 80
}
resource "ibm_is_security_group_rule" "egress" {
  count     = local.enabled ? 1 : 0
  group     = ibm_is_security_group.flp[0].id
  direction = "outbound"
  remote    = "0.0.0.0/0"
}

# ── operator floating IP (management/status access; default on) ──────────────
# Reserved unbound (zone only) so local.floating_ip_addr resolves BEFORE the
# instance — its value goes into the cert SAN so `:8443` and the `:80` web UI are
# valid over the floating IP. Bound to the VSI's NIC after the instance is up.
# Reachability is still gated by the security-group rules (allowed_cidrs): the
# floating IP provides the path, allowed_cidrs authorizes the source — scope
# allowed_cidrs to the operator's public IP to reach it from outside the VPC.
resource "ibm_is_floating_ip" "flp" {
  count          = local.enabled && var.flp_vsi_floating_ip ? 1 : 0
  name           = "flp-vsi-fip"
  zone           = local.zone
  resource_group = data.ibm_resource_group.rg[0].id
}
resource "ibm_is_instance_network_interface_floating_ip" "flp" {
  count             = local.enabled && var.flp_vsi_floating_ip ? 1 : 0
  instance          = ibm_is_instance.flp[0].id
  network_interface = ibm_is_instance.flp[0].primary_network_interface[0].id
  floating_ip       = ibm_is_floating_ip.flp[0].id
}

# ── CA (terraform-owned; injected into the box, output as flp_root_ca) ────────
resource "tls_private_key" "ca" {
  count       = local.enabled ? 1 : 0
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}
resource "tls_self_signed_cert" "ca" {
  count             = local.enabled ? 1 : 0
  private_key_pem   = tls_private_key.ca[0].private_key_pem
  is_ca_certificate = true
  subject {
    common_name  = "flp.ca"
    organization = "F5"
  }
  validity_period_hours = 87600
  allowed_uses          = ["cert_signing", "crl_signing"]
}

# ── COS: FAR auth tarball → cne_pull_64 SA + subscription JWT ─────────────────
data "ibm_resource_instance" "cos" {
  count             = local.enabled && var.use_cos_bucket ? 1 : 0
  name              = var.ibmcloud_cos_instance_name
  resource_group_id = data.ibm_resource_group.rg[0].id
  service           = "cloud-object-storage"
}
data "http" "iam_token" {
  count  = local.enabled && var.use_cos_bucket ? 1 : 0
  url    = "https://iam.cloud.ibm.com/identity/token"
  method = "POST"
  request_headers = {
    "Content-Type" = "application/x-www-form-urlencoded"
  }
  request_body = "grant_type=urn%3Aibm%3Aparams%3Aoauth%3Agrant-type%3Aapikey&apikey=${var.ibmcloud_api_key}"
}
resource "null_resource" "far_download" {
  count = local.enabled && var.use_cos_bucket ? 1 : 0
  triggers = {
    bucket = var.ibmcloud_resources_cos_bucket
    file   = var.f5_cne_far_auth_file
    region = var.ibmcloud_cos_bucket_region
    dir    = var.scratch_dir
  }
  # 1. cos-get: download the FAR auth tarball (binary → a file, via the COS SDK).
  # No interpreter → cmd.exe execs roksbnkctl.exe on Windows; key via env.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx cos-get --instance-crn ${data.ibm_resource_instance.cos[0].crn} --bucket ${var.ibmcloud_resources_cos_bucket} --key ${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/${var.f5_cne_far_auth_file} --region ${var.ibmcloud_cos_bucket_region}"
    environment = {
      IBMCLOUD_API_KEY = var.ibmcloud_api_key
    }
  }
  # 2. far-extract: write the single _json_key_base64 service-account JSON (Go
  # tar-extract, no host tar/grep).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx far-extract --tarball ${var.scratch_dir}/${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/far-sa.json"
  }
}
# Disconnected / local supply-chain path (use_cos_bucket=false): the root already
# extracted the FAR service-account (bnk.far_auth_local_file) into
# far_service_account_b64, so write it straight to far-sa.json instead of pulling the
# tarball from COS. Same file the COS path (far_download → far-extract) produces, so
# every downstream consumer (repo.f5.com pulls, cloud-init far_sa_b64) is unchanged.
resource "local_file" "far_sa_local" {
  count    = local.enabled && !var.use_cos_bucket ? 1 : 0
  content  = var.far_service_account_b64
  filename = "${var.scratch_dir}/far-sa.json"
}
data "local_file" "far_sa" {
  count      = local.enabled ? 1 : 0
  filename   = "${var.scratch_dir}/far-sa.json"
  depends_on = [null_resource.far_download, local_file.far_sa_local]
}
data "http" "jwt" {
  count  = local.enabled && var.use_cos_bucket ? 1 : 0
  url    = "https://s3.${var.ibmcloud_cos_bucket_region}.cloud-object-storage.appdomain.cloud/${var.ibmcloud_resources_cos_bucket}/${var.f5_cne_subscription_jwt_file}"
  method = "GET"
  request_headers = {
    "Authorization"           = "Bearer ${jsondecode(data.http.iam_token[0].response_body).access_token}"
    "ibm-service-instance-id" = data.ibm_resource_instance.cos[0].guid
  }
}

# ── resolve the FLP chart/image tag from the BNK manifest (mirrors the helm path) ──
resource "null_resource" "resolve_flp_version" {
  count = local.enabled && var.flp_chart_version == "" ? 1 : 0
  triggers = {
    manifest = var.f5_bigip_k8s_manifest_version
    dir      = var.scratch_dir
  }
  # helm-value chart-version: pull the BNK manifest chart and read the
  # f5-license-proxy sub-chart version out of it (helm binary for the OCI pull, Go
  # for the extract — no host tar/grep/awk). --file is the basename; the verb walks
  # the untarred tree to find it. No interpreter → cmd.exe execs roksbnkctl.exe.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx helm-value chart-version --chart oci://repo.f5.com/release/f5-bigip-k8s-manifest --version ${var.f5_bigip_k8s_manifest_version} --subchart charts/f5-license-proxy --file bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml --registry-login repo.f5.com --username _json_key_base64 --password-env HELM_REGISTRY_PW --out ${var.scratch_dir}/flp-version.txt"
    environment = {
      HELM_REGISTRY_PW = trimspace(data.local_file.far_sa[0].content)
    }
  }
}
data "external" "flp_version" {
  count      = local.enabled && var.flp_chart_version == "" ? 1 : 0
  program    = [local.roksbnkctl_bin, "tfx", "read-json", "--file", "${var.scratch_dir}/flp-version.txt", "--key", "v"]
  depends_on = [null_resource.resolve_flp_version]
}

# ── prod_jwks (F5's public signature-verification keyset) — extracted from the
# f5-license-proxy chart, mirroring the helm module's chart pull. Skipped when the
# phase supplies flp_prod_jwks_b64 directly.
resource "null_resource" "extract_prod_jwks" {
  lifecycle {
    precondition {
      condition     = local.flp_tag != ""
      error_message = "The f5-license-proxy chart version (flp_tag) could not be resolved, so the prod-jwks pull would run with an empty --version. An empty value does not fail cleanly: the flag swallows the next argument and everything after it shifts, surfacing as an 'unknown command' naming repo.f5.com. Same shape as issue #50. Pin bnk.flp.chart_version, or check that resolve_flp_version wrote its output under the workspace scratch dir."
    }
  }

  count = local.enabled && var.flp_prod_jwks_b64 == "" ? 1 : 0
  triggers = {
    tag = local.flp_tag
    dir = var.scratch_dir
  }
  # helm-value prod-jwks: pull the FLP chart and extract + base64-decode the
  # bundled prod_jwks keyset (Go scan of the template YAMLs — no host tar/grep/awk).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx helm-value prod-jwks --chart oci://repo.f5.com/charts/f5-license-proxy --version ${local.flp_tag} --registry-login repo.f5.com --username _json_key_base64 --password-env HELM_REGISTRY_PW --out ${var.scratch_dir}/prod_jwks.txt"
    environment = {
      HELM_REGISTRY_PW = trimspace(data.local_file.far_sa[0].content)
    }
  }
}
data "local_file" "prod_jwks" {
  count      = local.enabled && var.flp_prod_jwks_b64 == "" ? 1 : 0
  filename   = "${var.scratch_dir}/prod_jwks.txt"
  depends_on = [null_resource.extract_prod_jwks]
}

# ── cloud-init + VSI ─────────────────────────────────────────────────────────
locals {
  cloud_init = local.enabled ? templatefile("${path.module}/cloudinit.yaml.tftpl", {
    far_sa_b64            = trimspace(data.local_file.far_sa[0].content)
    prod_jwks_b64         = var.flp_prod_jwks_b64 != "" ? var.flp_prod_jwks_b64 : base64encode(join("", data.local_file.prod_jwks[*].content))
    ca_cert_b64           = base64encode(tls_self_signed_cert.ca[0].cert_pem)
    ca_key_b64            = base64encode(tls_private_key.ca[0].private_key_pem)
    pod_up_b64            = base64encode(file("${path.module}/flp-pod-up.sh"))
    jwt_token             = var.use_cos_bucket ? try(trimspace(data.http.jwt[0].response_body), "") : trimspace(var.f5_cne_subscription_jwt)
    external_ip           = local.floating_ip_addr # operator floating IP → added to the leaf-cert SAN (empty when disabled)
    reg                   = var.flp_image_registry
    tag                   = local.flp_tag
    vault_tag             = var.flp_vault_image_tag
    f5_cert_url           = var.f5_cert_url
    f5_entitlement_url    = var.f5_entitlement_url
    f5_initial_config_url = var.f5_initial_config_url
    mode_of_operation     = var.mode_of_operation
    proxy_enabled         = var.flp_forward_proxy_host != "" ? "true" : "false"
    proxy_host            = var.flp_forward_proxy_host
    proxy_port            = var.flp_forward_proxy_port > 0 ? tostring(var.flp_forward_proxy_port) : ""
    proxy_protocol        = var.flp_forward_proxy_protocol
    # Optional flp-status web UI (a container in the pod, served on :80).
    flp_status_image    = var.flp_status_image
    flp_registry_host   = var.flp_status_registry_host
    flp_registry_ca_b64 = var.flp_status_registry_ca_b64
  }) : ""
}

variable "flp_status_image" {
  description = "Optional flp-status web UI image (mirror or public ref). Empty = no status UI. When set, the FLP pod publishes :80 and runs the flp-status container."
  type        = string
  default     = ""
}

variable "flp_status_registry_host" {
  description = "Registry host:port whose CA to trust so podman can pull flp_status_image (e.g. Harbor's <ip>). Empty = no extra trust (public image)."
  type        = string
  default     = ""
}

variable "flp_status_registry_ca_b64" {
  description = "Base64 CA cert for flp_status_registry_host, dropped into the VSI's /etc/containers/certs.d."
  type        = string
  default     = ""
}

data "ibm_is_ssh_key" "flp" {
  count = local.enabled && var.flp_vsi_ssh_key != "" ? 1 : 0
  name  = var.flp_vsi_ssh_key
}

resource "ibm_is_instance" "flp" {
  count          = local.enabled ? 1 : 0
  name           = "flp-vsi"
  vpc            = local.flp_vpc_id
  zone           = local.zone
  profile        = var.flp_vsi_profile
  image          = local.ubuntu_image_id
  resource_group = data.ibm_resource_group.rg[0].id
  keys           = var.flp_vsi_ssh_key != "" ? [data.ibm_is_ssh_key.flp[0].id] : []

  primary_network_interface {
    subnet          = ibm_is_subnet.flp[0].id
    security_groups = [ibm_is_security_group.flp[0].id]
  }
  boot_volume {
    name = "flp-vsi-boot"
    size = var.flp_vsi_boot_size_gb
  }
  user_data = local.cloud_init
}
