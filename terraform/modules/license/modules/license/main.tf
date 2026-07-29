# ============================================================
# License Module
# ============================================================
# Deploys the F5 BNK License Custom Resource
# Must be deployed after CNEInstance/FLO to ensure the License CRD exists

locals {
  global_enabled = var.enabled
  use_kubectl    = var.enabled
  jwt_token      = local.global_enabled && var.use_cos_bucket ? trimspace(data.http.jwt_download[0].response_body) : var.jwt_token

  # F5 License Proxy mode. In FLP mode the CWC talks to the in-cluster FLP instead
  # of F5 directly, so the License CR carries the three teem*Url endpoints (the FLP
  # service) plus licenseProxyServerRootCaPath — the fixed path where CWC mounts the
  # FLP root CA (from the `licenseserver-rootca` Secret this module writes below).
  # The JWT is STILL required in every mode (License CRD spec.required: [jwt]).
  is_flp      = var.license_mode == "f5licenseproxy"
  flp_ca_path = "/etc/cm20/licenseserver-rootca/licenseserver-rootca.txt"
  # The FLP serves its licensing API under /license-proxy/v1 (its health endpoint
  # is /license-proxy/v1/health) — NOT the /ee/v1 path the FLP uses toward F5
  # upstream. The CWC appends its operations to this base.
  flp_teem_url = "${var.flp_license_server_url}/license-proxy/v1"

  flp_spec_fields = local.is_flp ? {
    teemCertUrl                  = local.flp_teem_url
    teemEntitlementUrl           = local.flp_teem_url
    teemInitialConfigUrl         = local.flp_teem_url
    licenseProxyServerRootCaPath = local.flp_ca_path
  } : {}

  license_manifest = {
    apiVersion = "k8s.f5net.com/v1"
    kind       = "License"
    metadata = {
      name      = "bnk-license"
      namespace = var.utils_namespace
    }
    spec = merge({
      jwt           = local.jwt_token
      operationMode = var.license_mode
    }, local.flp_spec_fields)
  }

  # The CWC chart mounts this Secret (non-optional) at flp_ca_path. In connected/
  # disconnected mode CWC ships it empty; in FLP mode we populate it with the FLP
  # root CA so CWC trusts the proxy's TLS. Written as a real Secret so a CWC
  # rollout picks it up.
  #
  # The FLP spec fields (teem*Url + licenseProxyServerRootCaPath) go onto the
  # License CR whenever is_flp, so the CA Secret must be populated to match:
  # otherwise the CR points at the proxy with an EMPTY licenseserver-rootca, the
  # CWC's TLS handshake to the proxy fails cert verification, and the license
  # never reaches Active while the apply looks successful.
  write_flp_ca = local.global_enabled && local.is_flp && var.license_server_root_ca != ""

  # The CWC (f5-spk-cwc) is deployed by the CNEInstance BEFORE this module writes
  # the FLP CA, so it builds its TLS trust store at pod start with an empty
  # licenseserver-rootca mount. Merely overwriting the Secret does not re-trigger
  # that trust build (the mounted file syncs eventually, but the running client
  # keeps the startup config), so a fresh License CR verification hangs. We restart
  # the CWC once the real CA is in place — keyed on the CA content hash so it only
  # rolls when the CA actually changes (idempotent across re-applies).
  cwc_deployment = "f5-spk-cwc"
  flp_ca_hash    = local.write_flp_ca ? substr(sha256(var.license_server_root_ca), 0, 16) : ""

  # roksbnkctl binary for the tfx-based local-exec conversions (empty => on PATH).
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"
}

resource "kubectl_manifest" "licenseserver_rootca" {
  count             = local.write_flp_ca ? 1 : 0
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  # The CWC (deployed by the CNEInstance) creates the f5-utils namespace + an empty
  # licenseserver-rootca Secret and mounts it; we overwrite that Secret with the FLP
  # root CA. So wait for the CNEInstance before applying, or the namespace won't
  # exist yet.
  depends_on = [var.cneinstance_dependency]
  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Secret"
    type       = "Opaque"
    metadata = {
      name      = "licenseserver-rootca"
      namespace = var.utils_namespace
    }
    stringData = {
      "licenseserver-rootca.txt" = trimspace(var.license_server_root_ca)
    }
  })
}

# Restart the CWC so it rebuilds its TLS trust with the freshly-written FLP CA.
# Triggered on the CA hash → rolls only when the CA content changes. Runs after the
# Secret is applied and before the License CR waits for LicenseActive, so the
# reconciling CWC pod already trusts the proxy. FLP mode only (count 0 otherwise).
resource "null_resource" "cwc_flp_rollout" {
  count = local.write_flp_ca ? 1 : 0

  # kube_token is deliberately NOT a trigger. It comes from
  # data.ibm_container_cluster_config, which mints a fresh IAM token on every
  # refresh — as a trigger it would replace this resource on EVERY apply, bouncing
  # the CWC (and with it BNK licensing/telemetry) on what the operator expects to be
  # a no-op re-apply. The create provisioner reads var.kube_token directly, and
  # there is no destroy provisioner needing it from self.triggers. Only the CA hash
  # should roll the CWC.
  triggers = {
    ca_hash   = local.flp_ca_hash
    kube_host = var.kube_host
    namespace = var.utils_namespace
  }

  # Rolling-restart the CWC so it picks up the new FLP CA: stamp a CA-hash
  # annotation onto its pod template. The CNEInstance may still be creating the
  # Deployment, so wait for it to EXIST first, then strategic-merge the annotation.
  # Two ordered provisioners (no shell chaining); no interpreter → cmd.exe on Windows.

  # 1. Wait for the CWC Deployment to appear (replaces the 404-retry loop).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx wait --kube-host ${var.kube_host} --insecure --gvr apps/v1/deployments --ns ${var.utils_namespace} --name ${local.cwc_deployment} --for jsonpath=metadata.name=${local.cwc_deployment} --timeout 5m"
    environment = {
      KUBE_TOKEN = var.kube_token
    }
  }

  # 2. Strategic-merge the CA-hash annotation → rolling restart. The patch is passed
  # base64-encoded (--patch-b64): no shell metacharacters, so it survives cmd.exe as
  # well as sh (local-exec can't pipe stdin).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx patch --kube-host ${var.kube_host} --insecure --gvr apps/v1/deployments --ns ${var.utils_namespace} --name ${local.cwc_deployment} --type strategic --patch-b64 ${base64encode("{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"roksbnkctl/flp-ca-hash\":\"${local.flp_ca_hash}\"}}}}}")}"
    environment = {
      KUBE_TOKEN = var.kube_token
    }
  }

  depends_on = [kubectl_manifest.licenseserver_rootca]
}

# ==============================================================================
# COS Bucket Resources (when use_cos_bucket = true)
# ==============================================================================

data "ibm_resource_groups" "all_resource_groups" {
  count = local.global_enabled && var.use_cos_bucket ? 1 : 0
}

data "ibm_resource_group" "resource_group" {
  count = local.global_enabled && var.use_cos_bucket ? 1 : 0
  name = var.ibmcloud_resource_group != "" ? var.ibmcloud_resource_group : [
    for rg in data.ibm_resource_groups.all_resource_groups[0].resource_groups :
    rg.name if rg.is_default == true
  ][0]
}

data "ibm_resource_instance" "cos_instance" {
  count             = local.global_enabled && var.use_cos_bucket ? 1 : 0
  name              = var.ibmcloud_cos_instance_name
  resource_group_id = data.ibm_resource_group.resource_group[0].id
  service           = "cloud-object-storage"
}

data "ibm_cos_bucket" "cos_bucket" {
  count                = local.global_enabled && var.use_cos_bucket ? 1 : 0
  bucket_name          = var.ibmcloud_resources_cos_bucket
  resource_instance_id = data.ibm_resource_instance.cos_instance[0].id
  bucket_region        = var.ibmcloud_cos_bucket_region
  bucket_type          = "region_location"
}

data "ibm_cos_bucket_object" "f5_cne_subscription_jwt_object" {
  count           = local.global_enabled && var.use_cos_bucket ? 1 : 0
  bucket_crn      = data.ibm_cos_bucket.cos_bucket[0].crn
  bucket_location = data.ibm_cos_bucket.cos_bucket[0].bucket_region
  key             = var.f5_cne_subscription_jwt_file
}

# Fetch a short-lived IAM bearer token to authenticate the COS S3 REST request
data "http" "iam_token" {
  count  = local.global_enabled && var.use_cos_bucket ? 1 : 0
  url    = "https://iam.cloud.ibm.com/identity/token"
  method = "POST"
  request_headers = {
    "Content-Type" = "application/x-www-form-urlencoded"
  }
  request_body = "grant_type=urn%3Aibm%3Aparams%3Aoauth%3Agrant-type%3Aapikey&apikey=${var.ibmcloud_api_key}"
}

# Download JWT file via COS S3-compatible REST API (body field may be empty for binary content_type)
data "http" "jwt_download" {
  count  = local.global_enabled && var.use_cos_bucket ? 1 : 0
  url    = "https://s3.${var.ibmcloud_cos_bucket_region}.cloud-object-storage.appdomain.cloud/${var.ibmcloud_resources_cos_bucket}/${var.f5_cne_subscription_jwt_file}"
  method = "GET"
  request_headers = {
    "Authorization"           = "Bearer ${jsondecode(data.http.iam_token[0].response_body).access_token}"
    "ibm-service-instance-id" = data.ibm_resource_instance.cos_instance[0].guid
  }
}

# ============================================================
# License CR (terraform-native)
# ============================================================
# License CR as a real terraform resource + wait_for the CWC-mirrored
# status.state == "Verification Complete" (confirmed queryable from the FAR
# License CRD; the in-script CRD-poll/PATCH-retry + time_sleep are deleted —
# the field wait + depends_on ordering replace them). depends_on on the
# CNEInstance (CWC/License CRD present) which itself depends on the FLO chart.

resource "kubectl_manifest" "bnk_license" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.license_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  # SSA only — no provider wait_for. As with the CNEInstance gate, alekc/kubectl
  # 2.4.1's wait_for on a custom condition (here LicenseActive) is unreliable, so
  # the "license is live" gate is a deterministic API poll below
  # (null_resource.license_active).
  #
  # In FLP mode the CA Secret must exist AND the CWC must have restarted to trust it
  # before the license can verify. In other modes those elements are absent (count 0).
  depends_on = [var.cneinstance_dependency, kubectl_manifest.licenseserver_rootca, null_resource.cwc_flp_rollout]
}

# Deterministic "TMM is licensed" gate: poll the License CR until it reports
# licensed — status.state == "Active" (BNK 2.3's terminal value, description
# "License verification complete") OR condition LicenseActive=True. Replaces the
# provider wait_for. This is the gate that makes `bnk up` return success only once
# licensing has actually completed — in connected/disconnected mode (direct to F5)
# and f5licenseproxy mode (through the in-cluster proxy) alike.
#
# Pure bash + curl + coreutils (grep/tr) — NO python3 and NO jq (python3 is absent
# in the tools-runner container). We strip ALL whitespace first (`tr -d '[:space:]'`)
# so the parse works against pretty-printed OR compact API JSON; then match either
# status.state == "Active" or the LicenseActive condition (isolated per-object with
# `tr '{}' '\n'`). Bounded (~15m) and fails loudly rather than hanging.
resource "null_resource" "license_active" {
  count = local.use_kubectl ? 1 : 0

  triggers = {
    license = kubectl_manifest.bnk_license[0].id
  }

  # tfx wait (watch-first, event-driven) replaces the curl+grep+tr poll: block until
  # the License CR reports status.state=Active (BNK 2.3's terminal value). No
  # interpreter → cmd.exe execs roksbnkctl.exe on Windows; token via KUBE_TOKEN env.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx wait --kube-host ${var.kube_host} --insecure --gvr k8s.f5net.com/v1/licenses --ns ${var.utils_namespace} --name bnk-license --for jsonpath=status.state=Active --timeout 15m"
    environment = {
      KUBE_TOKEN = var.kube_token
    }
  }

  depends_on = [kubectl_manifest.bnk_license]
}
