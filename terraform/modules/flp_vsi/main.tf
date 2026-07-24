# flp_vsi — mode: vsi FLP backend. Provisions a headless Ubuntu VSI in the cluster
# VPC running the f5-license-proxy stack as a podman pod (no Kubernetes). Terraform
# generates the CA and injects it; the box (cloud-init) signs the leaves and brings
# up the pod. Egress to FAR + F5 licensing goes out a public gateway; the VSI keeps
# a private VPC IP for the CWC to dial.

locals {
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"
  enabled        = var.deploy_flp_vsi
  zone           = var.flp_vsi_zone != "" ? var.flp_vsi_zone : "${var.ibmcloud_cluster_region}-1"
  reach_ip       = local.enabled ? ibm_is_instance.flp[0].primary_network_interface[0].primary_ipv4_address : ""
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
data "ibm_is_vpc" "cluster" {
  count      = local.enabled ? 1 : 0
  identifier = var.existing_cluster_vpc_id
}
# IBM Cloud allows exactly ONE public gateway per zone per VPC. The cluster phase
# already attached a gateway to every zone of the cluster VPC, so creating a
# second one in the FLP VSI's zone fails ("over quota. Quota: 1"). Look the VPC's
# gateways up and REUSE the one already in this zone; create ours only if absent.
data "ibm_is_public_gateways" "vpc" {
  count = local.enabled ? 1 : 0
}
locals {
  existing_pgw_id = local.enabled ? lookup({
    for g in try(data.ibm_is_public_gateways.vpc[0].public_gateways, []) :
    g.zone => g.id if try(g.vpc, "") == data.ibm_is_vpc.cluster[0].id
  }, local.zone, "") : ""
  create_pgw = local.enabled && local.existing_pgw_id == ""
  # The gateway the subnet attaches to: the one already in the zone, else ours.
  pgw_id = local.existing_pgw_id != "" ? local.existing_pgw_id : try(ibm_is_public_gateway.egress[0].id, "")
}
resource "ibm_is_public_gateway" "egress" {
  count          = local.create_pgw ? 1 : 0
  name           = "flp-vsi-egress"
  vpc            = data.ibm_is_vpc.cluster[0].id
  zone           = local.zone
  resource_group = data.ibm_resource_group.rg[0].id
}
resource "ibm_is_subnet" "flp" {
  count                    = local.enabled ? 1 : 0
  name                     = "flp-vsi-subnet"
  vpc                      = data.ibm_is_vpc.cluster[0].id
  zone                     = local.zone
  total_ipv4_address_count = 16
  public_gateway           = local.pgw_id
  resource_group           = data.ibm_resource_group.rg[0].id
}
resource "ibm_is_security_group" "flp" {
  count          = local.enabled ? 1 : 0
  name           = "flp-vsi-sg"
  vpc            = data.ibm_is_vpc.cluster[0].id
  resource_group = data.ibm_resource_group.rg[0].id
}
# ingress to 8443 from the consuming cluster's subnets (or the whole VPC if unset)
resource "ibm_is_security_group_rule" "flp_in" {
  for_each  = local.enabled ? toset(length(var.flp_vsi_allowed_cidrs) > 0 ? var.flp_vsi_allowed_cidrs : ["0.0.0.0/0"]) : toset([])
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
resource "ibm_is_security_group_rule" "egress" {
  count     = local.enabled ? 1 : 0
  group     = ibm_is_security_group.flp[0].id
  direction = "outbound"
  remote    = "0.0.0.0/0"
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
  count             = local.enabled ? 1 : 0
  name              = var.ibmcloud_cos_instance_name
  resource_group_id = data.ibm_resource_group.rg[0].id
  service           = "cloud-object-storage"
}
data "http" "iam_token" {
  count  = local.enabled ? 1 : 0
  url    = "https://iam.cloud.ibm.com/identity/token"
  method = "POST"
  request_headers = {
    "Content-Type" = "application/x-www-form-urlencoded"
  }
  request_body = "grant_type=urn%3Aibm%3Aparams%3Aoauth%3Agrant-type%3Aapikey&apikey=${var.ibmcloud_api_key}"
}
resource "null_resource" "far_download" {
  count = local.enabled ? 1 : 0
  triggers = {
    bucket = var.ibmcloud_resources_cos_bucket
    file   = var.f5_cne_far_auth_file
    region = var.ibmcloud_cos_bucket_region
    dir    = var.scratch_dir
  }
  # 1. cos-get: download the FAR auth tarball (binary → a file, via the COS SDK).
  # No interpreter → cmd.exe execs roksbnkctl.exe on Windows; key via env.
  provisioner "local-exec" {
    command = "\"${local.roksbnkctl_bin}\" tfx cos-get --instance-crn ${data.ibm_resource_instance.cos[0].crn} --bucket ${var.ibmcloud_resources_cos_bucket} --key ${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/${var.f5_cne_far_auth_file} --region ${var.ibmcloud_cos_bucket_region}"
    environment = {
      IBMCLOUD_API_KEY = var.ibmcloud_api_key
    }
  }
  # 2. far-extract: write the single _json_key_base64 service-account JSON (Go
  # tar-extract, no host tar/grep).
  provisioner "local-exec" {
    command = "\"${local.roksbnkctl_bin}\" tfx far-extract --tarball ${var.scratch_dir}/${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/far-sa.json"
  }
}
data "local_file" "far_sa" {
  count      = local.enabled ? 1 : 0
  filename   = "${var.scratch_dir}/far-sa.json"
  depends_on = [null_resource.far_download]
}
data "http" "jwt" {
  count  = local.enabled ? 1 : 0
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
  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HELM_BIN=helm
      command -v helm >/dev/null 2>&1 || {
        T=$(mktemp -d); curl -fsSL -o "$T/h.tgz" https://get.helm.sh/helm-v3.17.2-linux-amd64.tar.gz
        tar -xzf "$T/h.tgz" -C "$T"; HELM_BIN="$T/linux-amd64/helm"; }
      mkdir -p "${var.scratch_dir}/manifest"; cd "${var.scratch_dir}/manifest"
      echo "${trimspace(data.local_file.far_sa[0].content)}" | $HELM_BIN registry login -u _json_key_base64 --password-stdin repo.f5.com
      $HELM_BIN pull oci://repo.f5.com/release/f5-bigip-k8s-manifest --version "${var.f5_bigip_k8s_manifest_version}" -d .
      tar -xzf "f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.tgz"
      V=$(grep -A1 "charts/f5-license-proxy" "f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}/bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml" \
            | grep "version:" | awk '{print $2}' | tr -d "\"'" | head -1)
      printf '%s' "$V" > "${var.scratch_dir}/flp-version.txt"
    EOT
  }
}
data "external" "flp_version" {
  count      = local.enabled && var.flp_chart_version == "" ? 1 : 0
  program    = ["bash", "-c", "printf '{\"v\":\"%s\"}' \"$(cat ${var.scratch_dir}/flp-version.txt 2>/dev/null | tr -d '[:space:]')\""]
  depends_on = [null_resource.resolve_flp_version]
}

# ── prod_jwks (F5's public signature-verification keyset) — extracted from the
# f5-license-proxy chart, mirroring the helm module's chart pull. Skipped when the
# phase supplies flp_prod_jwks_b64 directly.
resource "null_resource" "extract_prod_jwks" {
  count = local.enabled && var.flp_prod_jwks_b64 == "" ? 1 : 0
  triggers = {
    tag = local.flp_tag
    dir = var.scratch_dir
  }
  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HELM_BIN=helm
      command -v helm >/dev/null 2>&1 || {
        T=$(mktemp -d); curl -fsSL -o "$T/h.tgz" https://get.helm.sh/helm-v3.17.2-linux-amd64.tar.gz
        tar -xzf "$T/h.tgz" -C "$T"; HELM_BIN="$T/linux-amd64/helm"; }
      mkdir -p "${var.scratch_dir}/flp-chart"; cd "${var.scratch_dir}/flp-chart"
      echo "${trimspace(data.local_file.far_sa[0].content)}" | $HELM_BIN registry login -u _json_key_base64 --password-stdin repo.f5.com
      $HELM_BIN pull oci://repo.f5.com/charts/f5-license-proxy --version "${local.flp_tag}" -d .
      tar -xzf "f5-license-proxy-${local.flp_tag}.tgz"
      grep -ohE 'prod_jwks.txt: [A-Za-z0-9+/=]+' "f5-license-proxy-${local.flp_tag}"/templates/*.yaml \
        | awk '{print $2}' | base64 -d > "${var.scratch_dir}/prod_jwks.txt"
    EOT
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
    jwt_token             = trimspace(data.http.jwt[0].response_body)
    external_ip           = "" # private reach: the box uses its own VPC IP as the SAN
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
  }) : ""
}

resource "ibm_is_instance" "flp" {
  count          = local.enabled ? 1 : 0
  name           = "flp-vsi"
  vpc            = data.ibm_is_vpc.cluster[0].id
  zone           = local.zone
  profile        = var.flp_vsi_profile
  image          = local.ubuntu_image_id
  resource_group = data.ibm_resource_group.rg[0].id
  keys           = []

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
