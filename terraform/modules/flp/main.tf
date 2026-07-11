# ==============================================================================
# F5 License Proxy (FLP) module
# ==============================================================================
# Deploys the in-cluster F5 License Proxy: a root CA + three mTLS leaf certs, the
# four Secrets the f5-license-proxy chart consumes, and the chart itself pulled
# from the configured registry (Harbor mirror or FAR). Outputs the root CA + the
# service endpoint the BNK License CR points at (teem*Url + licenseProxyServerRootCaPath).
#
# Optional + gated: every resource keys off local.enabled (var.deploy_flp), so
# the module is a complete no-op in the cluster/BNK/testing/gateway phases that
# carry it in the shared root config.

locals {
  enabled = var.deploy_flp

  # Registry/mirror contract — IDENTICAL to the BNK install (renderBNKFields emits
  # far_chart_repo_url/far_image_repo_url/use_registry_mirror). Each empty input
  # coalesces back to far_repo_url, so an un-mirrored apply resolves both hosts to
  # repo.f5.com exactly as before.
  far_registry_hostname = replace(var.far_repo_url, "https://", "")
  far_chart_hostname    = replace(coalesce(var.far_chart_repo_url, var.far_repo_url), "https://", "")
  far_image_hostname    = replace(coalesce(var.far_image_repo_url, var.far_repo_url), "https://", "")

  # The chart consumes <component>.image.repository as a PREFIX it appends
  # image.name to, so it must end in "/images" (the mirror preserves images/<name>
  # under <ns>, exactly as FAR serves repo.f5.com/images/<name>).
  image_repository = "${local.far_image_hostname}/images"

  # FAR service account (the _json_key_base64 SA) extracted from the auth tarball —
  # authenticates the helm OCI chart pull off the mirror + the far-secret.
  far_service_account_b64 = local.enabled ? data.local_file.cne_pull_64_json_file[0].content : ""
  far_auth_value          = base64encode("_json_key_base64:${local.far_service_account_b64}")
  far_docker_config_json = replace(
    jsonencode({
      auths = { (local.far_registry_hostname) = { auth = local.far_auth_value } }
    }),
    ":", ": "
  )

  kube_token = try(data.ibm_container_cluster_config.cluster_config[0].token, "")

  # Chart-pull auth for the in-process helm provider (copied from the FLO module):
  # FAR creds off the mirror, iamapikey for an ICR mirror, the cluster token for
  # the in-cluster registry route.
  is_icr_mirror = var.use_registry_mirror && can(regex("(^|[.])icr[.]io(/|$)", local.far_chart_hostname))
  chart_pull_username = (
    !var.use_registry_mirror ? "_json_key_base64" :
    local.is_icr_mirror ? "iamapikey" : "unused"
  )
  chart_pull_password = (
    !var.use_registry_mirror ? local.far_service_account_b64 :
    local.is_icr_mirror ? var.ibmcloud_api_key : local.kube_token
  )

  # Off the mirror the pods need the FAR dockerconfig pull secret; in mirror mode
  # it is dropped (RBAC / the mirror's own pull path handles it), matching the BNK
  # install's use_registry_mirror behaviour.
  flp_image_pull_secret = !var.use_registry_mirror ? "far-secret" : ""

  # mTLS leaf certs: name → SAN DNS list (from the f5-license-proxy chart + docs).
  leaves = {
    postgresql = ["postgresql", "localhost"]
    vault      = ["vault", "localhost", "vault-postgresql-service"]
    flp        = ["flp", "localhost"]
  }
  # leaf → the Secret name the chart mounts it from.
  leaf_secret = {
    postgresql = "postgresql-mtls-secret"
    vault      = "vault-ssl-secret"
    flp        = "flp-mtls-secret"
  }

  # Helm values: point every component's image.repository at the resolved host
  # prefix and set the (conditional) pull secret. Everything else stays at the
  # chart defaults (isProxy* forward-proxy flags off — direct egress).
  flp_helm_values = {
    vaultInit  = { image = { repository = local.image_repository } }
    vault      = { image = { repository = local.image_repository } }
    postgresql = { image = { repository = local.image_repository } }
    flp = {
      image            = { repository = local.image_repository }
      imagePullSecrets = local.flp_image_pull_secret
    }
  }
}

# ── COS: FAR auth tarball → _json_key_base64 SA ───────────────────────────────

data "ibm_resource_groups" "all" {
  count = local.enabled ? 1 : 0
}

data "ibm_resource_group" "rg" {
  count = local.enabled ? 1 : 0
  name = var.ibmcloud_resource_group != "" ? var.ibmcloud_resource_group : [
    for rg in data.ibm_resource_groups.all[0].resource_groups : rg.name if rg.is_default == true
  ][0]
}

data "ibm_resource_instance" "cos" {
  count             = local.enabled ? 1 : 0
  name              = var.ibmcloud_cos_instance_name
  resource_group_id = data.ibm_resource_group.rg[0].id
  service           = "cloud-object-storage"
}

# Short-lived IAM bearer token for the COS S3 REST calls (FAR tgz + JWT).
data "http" "iam_token" {
  count  = local.enabled ? 1 : 0
  url    = "https://iam.cloud.ibm.com/identity/token"
  method = "POST"
  request_headers = {
    "Content-Type" = "application/x-www-form-urlencoded"
  }
  request_body = "grant_type=urn%3Aibm%3Aparams%3Aoauth%3Agrant-type%3Aapikey&apikey=${var.ibmcloud_api_key}"
}

resource "null_resource" "far_archive_download" {
  count = local.enabled ? 1 : 0
  triggers = {
    bucket      = var.ibmcloud_resources_cos_bucket
    filename    = var.f5_cne_far_auth_file
    region      = var.ibmcloud_cos_bucket_region
    scratch_dir = var.scratch_dir
  }
  provisioner "local-exec" {
    command = <<-EOT
      mkdir -p "${var.scratch_dir}"
      curl -s -f -o "${var.scratch_dir}/${var.f5_cne_far_auth_file}" \
        -H "Authorization: Bearer ${jsondecode(data.http.iam_token[0].response_body).access_token}" \
        -H "ibm-service-instance-id: ${data.ibm_resource_instance.cos[0].guid}" \
        "https://s3.${var.ibmcloud_cos_bucket_region}.cloud-object-storage.appdomain.cloud/${var.ibmcloud_resources_cos_bucket}/${var.f5_cne_far_auth_file}"
    EOT
  }
}

resource "null_resource" "far_tgz_extractor" {
  count = local.enabled ? 1 : 0
  triggers = {
    archive_id  = null_resource.far_archive_download[0].id
    scratch_dir = var.scratch_dir
  }
  provisioner "local-exec" {
    command = <<-EOT
      mkdir -p "${var.scratch_dir}"
      tar -xzf "${var.scratch_dir}/${var.f5_cne_far_auth_file}" -C "${var.scratch_dir}/"
      tar -tzf "${var.scratch_dir}/${var.f5_cne_far_auth_file}" | grep '\.json$' | head -1 > "${var.scratch_dir}/far_extracted_filename.txt"
    EOT
  }
}

data "local_file" "far_extracted_filename" {
  count      = local.enabled ? 1 : 0
  filename   = "${var.scratch_dir}/far_extracted_filename.txt"
  depends_on = [null_resource.far_tgz_extractor]
}

data "local_file" "cne_pull_64_json_file" {
  count      = local.enabled ? 1 : 0
  filename   = "${var.scratch_dir}/${trimspace(data.local_file.far_extracted_filename[0].content)}"
  depends_on = [null_resource.far_tgz_extractor]
}

# ── COS: subscription JWT → flp-jwt-secret ────────────────────────────────────
data "http" "jwt_download" {
  count  = local.enabled ? 1 : 0
  url    = "https://s3.${var.ibmcloud_cos_bucket_region}.cloud-object-storage.appdomain.cloud/${var.ibmcloud_resources_cos_bucket}/${var.f5_cne_subscription_jwt_file}"
  method = "GET"
  request_headers = {
    "Authorization"           = "Bearer ${jsondecode(data.http.iam_token[0].response_body).access_token}"
    "ibm-service-instance-id" = data.ibm_resource_instance.cos[0].guid
  }
}

# ── TLS: root CA + three mTLS leaves ──────────────────────────────────────────

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
  validity_period_hours = 87600 # 10 years
  allowed_uses          = ["cert_signing", "crl_signing", "digital_signature"]
}

resource "tls_private_key" "leaf" {
  for_each    = local.enabled ? local.leaves : {}
  algorithm   = "ECDSA"
  ecdsa_curve = "P256"
}

resource "tls_cert_request" "leaf" {
  for_each        = local.enabled ? local.leaves : {}
  private_key_pem = tls_private_key.leaf[each.key].private_key_pem
  dns_names       = each.value
  subject {
    common_name  = each.key
    organization = "F5"
  }
}

resource "tls_locally_signed_cert" "leaf" {
  for_each              = local.enabled ? local.leaves : {}
  cert_request_pem      = tls_cert_request.leaf[each.key].cert_request_pem
  ca_private_key_pem    = tls_private_key.ca[0].private_key_pem
  ca_cert_pem           = tls_self_signed_cert.ca[0].cert_pem
  validity_period_hours = 87600
  allowed_uses          = ["server_auth", "client_auth", "key_encipherment", "digital_signature"]
}

# ── namespace + secrets ───────────────────────────────────────────────────────

resource "kubernetes_namespace_v1" "flp" {
  count = local.enabled && var.flp_namespace != "default" ? 1 : 0
  metadata {
    name = var.flp_namespace
  }
}

# The three mTLS secrets the chart mounts (postgresql/vault/flp).
resource "kubernetes_secret_v1" "mtls" {
  for_each = local.enabled ? local.leaf_secret : {}
  metadata {
    name      = each.value
    namespace = var.flp_namespace
  }
  data = {
    "ca.crt"  = tls_self_signed_cert.ca[0].cert_pem
    "tls.crt" = tls_locally_signed_cert.leaf[each.key].cert_pem
    "tls.key" = tls_private_key.leaf[each.key].private_key_pem
  }
  depends_on = [kubernetes_namespace_v1.flp]
}

# The subscription JWT the FLP presents to F5 licensing.
resource "kubernetes_secret_v1" "flp_jwt" {
  count = local.enabled ? 1 : 0
  metadata {
    name      = "flp-jwt-secret"
    namespace = var.flp_namespace
  }
  data = {
    jwt_secret = trimspace(data.http.jwt_download[0].response_body)
  }
  depends_on = [kubernetes_namespace_v1.flp]
}

# OpenShift SCC. The f5-license-proxy pods run with fsGroup 1000 and the Vault
# container needs the IPC_LOCK capability (mlock) — both are rejected by ROKS's
# default `restricted-v2` SCC. Grant the FLP namespace's service accounts the
# `privileged` SCC (namespace-scoped RoleBinding to the SCC ClusterRole), the
# same mechanism `oc adm policy add-scc-to-group privileged` uses. The helm
# release depends on this so the SCC is in place before the pods schedule.
resource "kubernetes_role_binding_v1" "flp_scc" {
  count = local.enabled ? 1 : 0
  metadata {
    name      = "flp-scc-privileged"
    namespace = var.flp_namespace
  }
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = "system:openshift:scc:privileged"
  }
  subject {
    kind      = "Group"
    name      = "system:serviceaccounts:${var.flp_namespace}"
    api_group = "rbac.authorization.k8s.io"
  }
  depends_on = [kubernetes_namespace_v1.flp]
}

# FAR image pull secret — only off the mirror path (mirror mode drops it).
resource "kubernetes_secret_v1" "far_secret" {
  count = local.enabled && !var.use_registry_mirror ? 1 : 0
  metadata {
    name      = "far-secret"
    namespace = var.flp_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.far_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.flp]
}

# ── the FLP chart ─────────────────────────────────────────────────────────────

resource "helm_release" "flp" {
  count = local.enabled ? 1 : 0

  name             = "f5-license-proxy"
  repository       = "oci://${local.far_chart_hostname}/charts"
  chart            = "f5-license-proxy"
  version          = var.flp_chart_version != "" ? var.flp_chart_version : null
  namespace        = var.flp_namespace
  create_namespace = false

  repository_username = local.chart_pull_username
  repository_password = local.chart_pull_password

  # FLP readiness (vault unseal → postgres → proxy) IS the meaningful signal here,
  # so block on it — unlike the FLO operator whose readiness is gated downstream.
  wait    = true
  timeout = 600

  values = [yamlencode(local.flp_helm_values)]

  depends_on = [
    kubernetes_namespace_v1.flp,
    kubernetes_secret_v1.mtls,
    kubernetes_secret_v1.flp_jwt,
    kubernetes_secret_v1.far_secret,
    kubernetes_role_binding_v1.flp_scc,
  ]
}
