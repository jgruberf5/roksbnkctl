# ============================================================
# License Module
# ============================================================
# Deploys the F5 BNK License Custom Resource
# Must be deployed after CNEInstance/FLO to ensure the License CRD exists

locals {
  global_enabled = var.enabled
  use_kubectl    = var.enabled && var.bnk_cr_mode == "kubectl"
  use_legacy     = var.enabled && var.bnk_cr_mode == "legacy_curl"
  jwt_token      = local.global_enabled && var.use_cos_bucket ? trimspace(data.http.jwt_download[0].response_body) : var.jwt_token

  # F5 License Proxy mode. In FLP mode the CWC talks to the in-cluster FLP instead
  # of F5 directly, so the License CR carries the three teem*Url endpoints (the FLP
  # service) plus licenseProxyServerRootCaPath — the fixed path where CWC mounts the
  # FLP root CA (from the `licenseserver-rootca` Secret this module writes below).
  # The JWT is STILL required in every mode (License CRD spec.required: [jwt]).
  is_flp       = var.license_mode == "f5licenseproxy"
  flp_ca_path  = "/etc/cm20/licenseserver-rootca/licenseserver-rootca.txt"
  flp_teem_url = "${var.flp_license_server_url}/ee/v1"

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
  write_flp_ca = local.use_kubectl && local.is_flp && var.license_server_root_ca != ""
}

resource "kubectl_manifest" "licenseserver_rootca" {
  count             = local.write_flp_ca ? 1 : 0
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
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

# Wait for License CRD to be available (legacy mode only — kubectl mode gates
# on the FLO/CNEInstance ordering + the License status.state field).
resource "time_sleep" "wait_for_license_crd" {
  count           = local.use_legacy ? 1 : 0
  create_duration = "30s"
  triggers = {
    cneinstance_dependency = var.cneinstance_dependency != null ? tostring(var.cneinstance_dependency) : "direct-apply"
  }
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

# Create License CR in utils namespace via curl Server-Side Apply (legacy mode)
resource "null_resource" "bnk_license" {
  count = local.use_legacy ? 1 : 0

  triggers = {
    jwt             = local.jwt_token
    license_mode    = var.license_mode
    kube_host       = var.kube_host
    token           = var.kube_token
    utils_namespace = var.utils_namespace
  }

  provisioner "local-exec" {
    command = <<-EOT
      # Wait for the License CRD's API group to be served.
      code=000
      for i in $(seq 1 30); do
        code=$(curl -sk -o /dev/null -w "%%{http_code}" \
          -H "Authorization: Bearer ${var.kube_token}" \
          "${var.kube_host}/apis/k8s.f5net.com/v1/")
        [ "$code" = "200" ] && break
        echo "Waiting for License CRD (attempt $i/30, status=$code)..."
        sleep 10
      done
      if [ "$code" != "200" ]; then
        echo "ERROR: License CRD not available after 300s (last HTTP status: $code)" >&2
        exit 1
      fi
      # Apply the License CR. Retry on transient/admission-webhook-not-ready
      # errors (4xx/5xx) — the API group can be served before the FLO admission
      # webhook is reachable, producing 403/503 responses for ~30-60s.
      patch_body='${jsonencode(local.license_manifest)}'
      patch_url="${var.kube_host}/apis/k8s.f5net.com/v1/namespaces/${var.utils_namespace}/licenses/bnk-license?fieldManager=terraform&force=true"
      patch_status=000
      for i in $(seq 1 30); do
        body_file=$(mktemp)
        patch_status=$(curl -sk -o "$body_file" -w "%%{http_code}" -X PATCH \
          -H "Authorization: Bearer ${var.kube_token}" \
          -H "Content-Type: application/apply-patch+yaml" \
          "$patch_url" -d "$patch_body")
        case "$patch_status" in
          2??)
            rm -f "$body_file"
            break
            ;;
          *)
            echo "License PATCH attempt $i/30 returned HTTP $patch_status, retrying in 10s..." >&2
            sed -E 's/("jwt":")[^"]*/\1<redacted>/g' "$body_file" >&2 || true
            rm -f "$body_file"
            sleep 10
            ;;
        esac
      done
      case "$patch_status" in
        2??) echo "License applied (HTTP $patch_status)";;
        *)   echo "ERROR: License PATCH failed after 30 attempts (last HTTP $patch_status)" >&2; exit 1;;
      esac
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.kube_host}/apis/k8s.f5net.com/v1/namespaces/${self.triggers.utils_namespace}/licenses/bnk-license" || true
    EOT
  }

  depends_on = [time_sleep.wait_for_license_crd[0]]
}

# ============================================================
# Sprint 27 — kubectl mode (terraform-native)
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

  # Wait on the LicenseActive CONDITION, not status.state == "Verification
  # Complete". In BNK 2.3 (controller 14.59.x) a successfully verified license
  # settles to status.state = "Active" (with stateDescription "License
  # verification complete") and condition LicenseActive=True — the literal
  # "Verification Complete" is never a terminal state value, so a field match on
  # it hangs until the timeout even though the license is live. The condition is
  # version-independent and is the real "TMM is licensed" gate.
  wait_for {
    condition {
      type   = "LicenseActive"
      status = "True"
    }
  }

  # The controller must license TMM and verify the subscription JWT; allow well
  # past the provider default so a cold first-time verification doesn't trip.
  timeouts {
    create = "15m"
  }

  # In FLP mode the CA Secret must exist before the license verifies (CWC reads it
  # to trust the proxy). In other modes this list element is absent (count 0).
  depends_on = [var.cneinstance_dependency, kubectl_manifest.licenseserver_rootca]
}
