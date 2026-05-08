locals {
  global_enabled          = var.enabled
  far_registry_hostname   = replace(var.far_repo_url, "https://", "")
  image_repository        = "${local.far_registry_hostname}/images"
  far_service_account_b64 = local.global_enabled && var.use_cos_bucket ? data.local_file.cne_pull_64_json_file[0].content : ""
  far_auth_value          = base64encode("_json_key_base64:${local.far_service_account_b64}")
  far_docker_config_json = replace(
    jsonencode({
      auths = {
        (local.far_registry_hostname) = {
          auth = local.far_auth_value
        }
      }
    }),
    ":",
    ": "
  )

  nad_name_computed = "ens3-ipvlan-l2"

  nad_config_host_device = jsonencode({
    cniVersion = "0.3.1"
    type       = "host-device"
    device     = var.nad_interface_name
  })

  nad_config_ipvlan = jsonencode({
    cniVersion = "0.3.1"
    type       = "ipvlan"
    master     = var.nad_interface_name
    mode       = var.nad_ipvlan_mode
    ipam = {
      type = "static"
      addresses = [
        {
          address = var.nad_ipvlan_address
        }
      ]
    }
  })

  cneinstance_network_attachments = [local.nad_name_computed, "macvlan-conf"]

  cis_helm_values = {
    global = {
      certmgr = {
        external = true
        issuerRef = {
          name = var.cluster_issuer_name
          kind = "ClusterIssuer"
        }
      }
    }

    rbac = {
      create = true
    }

    namespace = var.flo_namespace

    bigip_login_secret = "f5-bigip-ctlr-login"

    image = {
      repository = local.image_repository
      repo       = "f5-bnk-cis"
      pullSecrets = [
        "far-secret"
      ]
    }
  }

  flo_helm_values = {
    global = {
      imagePullSecrets = [
        {
          name = "far-secret"
        }
      ]
      certmgr = {
        clusterIssuer = var.cluster_issuer_name
      }
    }

    namespace                = var.flo_namespace
    containerPlatform        = "Generic"
    sharedComponentNamespace = var.utils_namespace

    image = {
      repository = local.image_repository
      pullPolicy = "Always"
    }

    "f5-spk-crds-common" = {
      versionValidator = {
        image = {
          repository = local.image_repository
        }
      }
    }

    "f5-spk-crds-service-proxy" = {
      versionValidator = {
        image = {
          repository = local.image_repository
        }
      }
    }

    "f5-ipam-operator" = {
      image = {
        repository = local.image_repository
        pullPolicy = "Always"
      }
      namespace        = var.flo_namespace
      nameOverride     = "f5-ipam-operator"
      fullnameOverride = "f5-ipam-operator"
    }

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

# Exchange API key for a short-lived IAM bearer token
data "http" "iam_token" {
  count  = local.global_enabled && var.use_cos_bucket ? 1 : 0
  url    = "https://iam.cloud.ibm.com/identity/token"
  method = "POST"
  request_headers = {
    "Content-Type" = "application/x-www-form-urlencoded"
    "Accept"       = "application/json"
  }
  request_body = "grant_type=urn:ibm:params:oauth:grant-type:apikey&apikey=${var.ibmcloud_api_key}"
}

resource "null_resource" "far_archive_download" {
  count = local.global_enabled && var.use_cos_bucket ? 1 : 0

  # scratch_dir included so a path change forces a re-download —
  # otherwise stale state from a previous /tmp-based apply would
  # leave the data.local_file resources reading a non-existent path.
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
        -H "ibm-service-instance-id: ${data.ibm_resource_instance.cos_instance[0].guid}" \
        "https://s3.${var.ibmcloud_cos_bucket_region}.cloud-object-storage.appdomain.cloud/${var.ibmcloud_resources_cos_bucket}/${var.f5_cne_far_auth_file}"
    EOT
  }
}

resource "null_resource" "cne_far_tgz_extractor" {
  count = local.global_enabled && var.use_cos_bucket ? 1 : 0

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
  count      = local.global_enabled && var.use_cos_bucket ? 1 : 0
  filename   = "${var.scratch_dir}/far_extracted_filename.txt"
  depends_on = [null_resource.cne_far_tgz_extractor]
}

locals {
  far_extracted_filename = var.use_cos_bucket && local.global_enabled ? trimspace(data.local_file.far_extracted_filename[0].content) : ""
}

locals {
  # NAD config strings JSON-escaped for embedding as string values in curl payloads
  nad_config_ipvlan_esc      = replace(local.nad_config_ipvlan, "\"", "\\\"")
  nad_config_host_device_esc = replace(local.nad_config_host_device, "\"", "\\\"")
  macvlan_config = jsonencode({
    cniVersion = "0.3.1"
    type       = "macvlan"
    master     = "dummy0"
    mode       = "bridge"
    ipam = {
      type      = "static"
      addresses = [{ address = "192.168.1.100/24", gateway = "192.168.1.1" }]
    }
  })
  macvlan_config_esc = replace(local.macvlan_config, "\"", "\\\"")

  # Base64-encoded secret values — safe to embed in JSON without further escaping
  bigip_username_b64    = base64encode(var.bigip_username)
  bigip_password_b64    = base64encode(var.bigip_password)
  bigip_url_b64         = base64encode(replace(var.bigip_url, "https://", ""))
  far_docker_config_b64 = base64encode(local.far_docker_config_json)
}

data "local_file" "cne_pull_64_json_file" {
  count      = local.global_enabled && var.use_cos_bucket ? 1 : 0
  filename   = "${var.scratch_dir}/${local.far_extracted_filename}"
  depends_on = [null_resource.cne_far_tgz_extractor]
}

# NAD CRD — already exists in ROKS clusters; no-op placeholder kept for reference.
# If the CRD were absent, apply it with:
#   curl -sf <URL> | kubectl apply -f -
# We fetch the URL only to validate it is reachable; the manifest is never applied.
data "http" "nad_crd" {
  count = 0 # CRD already exists in cluster — no need to fetch
  url   = "https://raw.githubusercontent.com/k8snetworkplumbingwg/network-attachment-definition-client/master/artifacts/networks-crd.yaml"
}

# Create NetworkAttachmentDefinition in FLO namespace using kubernetes_manifest
resource "null_resource" "network_attachment_definition" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name      = local.nad_name_computed
    namespace = var.flo_namespace
    host      = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/k8s.cni.cncf.io/v1/namespaces/${var.flo_namespace}/network-attachment-definitions/${local.nad_name_computed}?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition","metadata":{"name":"${local.nad_name_computed}","namespace":"${var.flo_namespace}"},"spec":{"config":"${var.nad_cni_type == "host-device" ? local.nad_config_host_device_esc : local.nad_config_ipvlan_esc}"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/k8s.cni.cncf.io/v1/namespaces/${self.triggers.namespace}/network-attachment-definitions/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.flo_namespace]
}

# Create macvlan NetworkAttachmentDefinition
resource "null_resource" "macvlan_network_attachment_definition" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name      = "macvlan-conf"
    namespace = var.flo_namespace
    host      = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/k8s.cni.cncf.io/v1/namespaces/${var.flo_namespace}/network-attachment-definitions/macvlan-conf?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"k8s.cni.cncf.io/v1","kind":"NetworkAttachmentDefinition","metadata":{"name":"macvlan-conf","namespace":"${var.flo_namespace}"},"spec":{"config":"${local.macvlan_config_esc}"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/k8s.cni.cncf.io/v1/namespaces/${self.triggers.namespace}/network-attachment-definitions/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.flo_namespace]
}

# Apply ClusterIssuer via curl server-side apply — idempotent across test runs.
resource "null_resource" "cluster_issuers" {
  count = local.global_enabled && var.cert_manager_crd_ready ? 1 : 0

  triggers = {
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/cert-manager.io/v1/clusterissuers/selfsigned-cluster-issuer?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"cert-manager.io/v1","kind":"ClusterIssuer","metadata":{"name":"selfsigned-cluster-issuer"},"spec":{"selfSigned":{}}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/cert-manager.io/v1/clusterissuers/selfsigned-cluster-issuer" || true
    EOT
  }
}

# Self-signed certificate for CA
resource "null_resource" "ca_certificate" {
  count = local.global_enabled && var.cert_manager_crd_ready ? 1 : 0

  triggers = {
    host      = var.kube_host
    token     = var.kube_token
    namespace = var.cert_manager_namespace
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/cert-manager.io/v1/namespaces/${var.cert_manager_namespace}/certificates/ext-ca?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"cert-manager.io/v1","kind":"Certificate","metadata":{"name":"ext-ca","namespace":"${var.cert_manager_namespace}"},"spec":{"isCA":true,"commonName":"ext-ca","secretName":"ext-ca","issuerRef":{"name":"selfsigned-cluster-issuer","kind":"ClusterIssuer","group":"cert-manager.io"}}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/cert-manager.io/v1/namespaces/${self.triggers.namespace}/certificates/ext-ca" || true
    EOT
  }

  depends_on = [null_resource.cluster_issuers]
}

# CA cluster issuer
resource "null_resource" "ca_cluster_issuer" {
  count = local.global_enabled && var.cert_manager_crd_ready ? 1 : 0

  triggers = {
    name  = var.cluster_issuer_name
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/cert-manager.io/v1/clusterissuers/${var.cluster_issuer_name}?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"cert-manager.io/v1","kind":"ClusterIssuer","metadata":{"name":"${var.cluster_issuer_name}"},"spec":{"ca":{"secretName":"ext-ca"}}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/cert-manager.io/v1/clusterissuers/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.ca_certificate]
}

# Pull f5-bigip-k8s-manifest chart to extract FLO and CIS versions
resource "null_resource" "extract_flo_version" {
  count = local.global_enabled ? 1 : 0
  provisioner "local-exec" {
    command = <<-EOT
      set -e
      # Ensure Helm >= 3.8.0 is available (helm registry requires 3.8+).
      # Schematics runtime ships an older version. Download directly for linux/amd64
      # instead of using get-helm-3, which requires uname (not available in Schematics).
      HELM_MIN="3.8.0"
      HELM_BIN="helm"
      helm_ok() {
        local v
        v=$(helm version --short 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1) || return 1
        # busybox sort (Alpine) lacks -C; -c is portable across busybox + GNU.
        # 2>/dev/null suppresses the "sort: line N: disorder:" diagnostic so
        # only the exit code matters.
        printf '%s\n%s\n' "$HELM_MIN" "$v" | sort -V -c 2>/dev/null
      }
      if ! helm_ok; then
        HELM_VERSION="3.17.2"
        # Per-resource scratch dir — terraform runs provisioners in
        # parallel by default, so a shared /tmp/helm-install path
        # races ("Text file busy" when one process writes while
        # another extracts).
        HELM_TMP=$(mktemp -d "$${TMPDIR:-/tmp}/helm-install-XXXXXX")
        curl -fsSL -o "$HELM_TMP/helm.tar.gz" \
          "https://get.helm.sh/helm-v$HELM_VERSION-linux-amd64.tar.gz"
        tar -xzf "$HELM_TMP/helm.tar.gz" -C "$HELM_TMP"
        HELM_BIN="$HELM_TMP/linux-amd64/helm"
      fi
      mkdir -p ${var.manifest_download_dir}
      cd ${var.manifest_download_dir}
      echo "${local.far_service_account_b64}" | $HELM_BIN registry login -u _json_key_base64 --password-stdin ${replace(var.far_repo_url, "https://", "")}
      $HELM_BIN pull oci://${replace(var.far_repo_url, "https://", "")}/release/f5-bigip-k8s-manifest --version "${var.f5_bigip_k8s_manifest_version}" -d .
      tar -xzf f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.tgz
      FLO_VERSION=$(grep -A 1 "charts/f5-lifecycle-operator" f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}/bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml | grep "version:" | awk '{print $2}' | tr -d '"' | tr -d "'")
      echo "$FLO_VERSION" > ${var.manifest_download_dir}/flo-version.txt
      CIS_VERSION=$(grep -A 1 "charts/f5-bnk-cis" f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}/bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml | grep "version:" | awk '{print $2}' | tr -d '"' | tr -d "'")
      echo "$CIS_VERSION" > ${var.manifest_download_dir}/cis-version.txt
    EOT
  }

  triggers = {
    manifest_version      = var.f5_bigip_k8s_manifest_version
    manifest_download_dir = var.manifest_download_dir
  }

  depends_on = [null_resource.cne_far_tgz_extractor]
}

# Read extracted FLO/CIS versions after extract_flo_version provisioner runs.
# depends_on defers evaluation to apply time (file exists) rather than plan time
# (file absent). The bash program returns "" gracefully when files are missing,
# so destroy-phase refresh does not abort even in a fresh ephemeral container.
data "external" "versions" {
  count = local.global_enabled ? 1 : 0

  program = [
    "bash", "-c",
    "F=$(cat ${var.manifest_download_dir}/flo-version.txt 2>/dev/null | tr -d '[:space:]'); C=$(cat ${var.manifest_download_dir}/cis-version.txt 2>/dev/null | tr -d '[:space:]'); printf '{\"flo\":\"%s\",\"cis\":\"%s\"}' \"$F\" \"$C\"",
  ]

  depends_on = [null_resource.extract_flo_version]
}

# Create f5-utils namespace via curl server-side apply — idempotent; no provider
# existence-check so it succeeds even when the namespace was left by a prior run.
resource "null_resource" "f5_utils" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name  = var.utils_namespace
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HOST="${var.kube_host}"
      TOKEN="${var.kube_token}"
      NS="${var.utils_namespace}"
      NS_RESP=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n')
      PHASE=$(echo "$NS_RESP" | grep -o '"phase":"[^"]*"' | cut -d'"' -f4 || true)
      if [ "$PHASE" = "Terminating" ]; then
        echo "Namespace $NS is Terminating - forcing deletion" >&2
        GV=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/apis/k8s.f5.com" | tr -d ' \t\r\n' \
          | grep -o '"groupVersion":"k8s.f5.com/[^"]*"' | head -1 \
          | sed 's|.*k8s.f5.com/||' | tr -d '"' || true)
        if [ -z "$GV" ]; then GV="v1alpha1"; fi
        for rtype in afms cnecontrollers cneinstances downloaders dssms f5tmms; do
          NAMES=$(curl -s -H "Authorization: Bearer $TOKEN" \
            "$HOST/apis/k8s.f5.com/$GV/namespaces/$NS/$rtype" | tr -d ' \t\r\n' \
            | grep -o '"name":"[^"]*"' | cut -d'"' -f4 || true)
          for rname in $NAMES; do
            echo "  Removing finalizers from $rtype/$rname" >&2
            curl -s -X PATCH \
              -H "Authorization: Bearer $TOKEN" \
              -H "Content-Type: application/merge-patch+json" \
              "$HOST/apis/k8s.f5.com/$GV/namespaces/$NS/$rtype/$rname" \
              -d '{"metadata":{"finalizers":[]}}' >/dev/null || true
          done
        done
        # Force-remove the namespace kubernetes finalizer to unblock stuck deletions
        FINAL_BODY=$(printf '{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"%s"},"spec":{"finalizers":[]}}' "$NS")
        curl -s -X PUT \
          -H "Authorization: Bearer $TOKEN" \
          -H "Content-Type: application/json" \
          "$HOST/api/v1/namespaces/$NS/finalize" \
          -d "$FINAL_BODY" >/dev/null || true
        for i in $(seq 1 24); do
          NS_CHECK=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n' || true)
          echo "$NS_CHECK" | grep -q '"code":404' && { echo "Namespace $NS deleted" >&2; break; }
          [ "$i" = "24" ] && { echo "ERROR: namespace $NS still exists after 120s" >&2; exit 1; }
          sleep 5
        done
      fi
      PATCH_BODY=$(printf '{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"%s"}}' "$NS")
      BODY=$(curl -s -X PATCH \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/apply-patch+yaml" \
        "$HOST/api/v1/namespaces/$NS?fieldManager=terraform&force=true" \
        -d "$PATCH_BODY" | tr -d ' \t\r\n')
      if ! echo "$BODY" | grep -q '"kind":"Namespace"'; then
        # OpenShift may return 500 "timedoutwaitingforthecondition" even when
        # the namespace was created successfully.  Verify via a GET before failing.
        NS_VERIFY=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n')
        if ! echo "$NS_VERIFY" | grep -q '"kind":"Namespace"'; then
          echo "ERROR: namespace PATCH failed and namespace does not exist: $BODY" >&2
          exit 1
        fi
        echo "WARNING: namespace PATCH returned non-200 but namespace exists: $BODY" >&2
      fi
      for i in $(seq 1 30); do
        PHASE=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n' \
          | grep -o '"phase":"[^"]*"' | cut -d'"' -f4 || true)
        [ "$PHASE" = "Active" ] && exit 0
        sleep 2
      done
      echo "ERROR: namespace $NS did not become Active within 60s" >&2
      exit 1
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/${self.triggers.name}" || true
    EOT
  }
}

# Create FLO namespace (skip if it's "default" - always exists)
resource "null_resource" "flo_namespace" {
  count = local.global_enabled && var.flo_namespace != "default" ? 1 : 0

  triggers = {
    name  = var.flo_namespace
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HOST="${var.kube_host}"
      TOKEN="${var.kube_token}"
      NS="${var.flo_namespace}"
      NS_RESP=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n')
      PHASE=$(echo "$NS_RESP" | grep -o '"phase":"[^"]*"' | cut -d'"' -f4 || true)
      if [ "$PHASE" = "Terminating" ]; then
        echo "Namespace $NS is Terminating - forcing deletion" >&2
        GV=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/apis/k8s.f5.com" | tr -d ' \t\r\n' \
          | grep -o '"groupVersion":"k8s.f5.com/[^"]*"' | head -1 \
          | sed 's|.*k8s.f5.com/||' | tr -d '"' || true)
        if [ -z "$GV" ]; then GV="v1alpha1"; fi
        for rtype in afms cnecontrollers cneinstances downloaders dssms f5tmms; do
          NAMES=$(curl -s -H "Authorization: Bearer $TOKEN" \
            "$HOST/apis/k8s.f5.com/$GV/namespaces/$NS/$rtype" | tr -d ' \t\r\n' \
            | grep -o '"name":"[^"]*"' | cut -d'"' -f4 || true)
          for rname in $NAMES; do
            echo "  Removing finalizers from $rtype/$rname" >&2
            curl -s -X PATCH \
              -H "Authorization: Bearer $TOKEN" \
              -H "Content-Type: application/merge-patch+json" \
              "$HOST/apis/k8s.f5.com/$GV/namespaces/$NS/$rtype/$rname" \
              -d '{"metadata":{"finalizers":[]}}' >/dev/null || true
          done
        done
        # Force-remove the namespace kubernetes finalizer to unblock stuck deletions
        FINAL_BODY=$(printf '{"kind":"Namespace","apiVersion":"v1","metadata":{"name":"%s"},"spec":{"finalizers":[]}}' "$NS")
        curl -s -X PUT \
          -H "Authorization: Bearer $TOKEN" \
          -H "Content-Type: application/json" \
          "$HOST/api/v1/namespaces/$NS/finalize" \
          -d "$FINAL_BODY" >/dev/null || true
        for i in $(seq 1 24); do
          NS_CHECK=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n' || true)
          echo "$NS_CHECK" | grep -q '"code":404' && { echo "Namespace $NS deleted" >&2; break; }
          [ "$i" = "24" ] && { echo "ERROR: namespace $NS still exists after 120s" >&2; exit 1; }
          sleep 5
        done
      fi
      PATCH_BODY=$(printf '{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"%s"}}' "$NS")
      BODY=$(curl -s -X PATCH \
        -H "Authorization: Bearer $TOKEN" \
        -H "Content-Type: application/apply-patch+yaml" \
        "$HOST/api/v1/namespaces/$NS?fieldManager=terraform&force=true" \
        -d "$PATCH_BODY" | tr -d ' \t\r\n')
      if ! echo "$BODY" | grep -q '"kind":"Namespace"'; then
        # OpenShift may return 500 "timedoutwaitingforthecondition" even when
        # the namespace was created successfully.  Verify via a GET before failing.
        NS_VERIFY=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n')
        if ! echo "$NS_VERIFY" | grep -q '"kind":"Namespace"'; then
          echo "ERROR: namespace PATCH failed and namespace does not exist: $BODY" >&2
          exit 1
        fi
        echo "WARNING: namespace PATCH returned non-200 but namespace exists: $BODY" >&2
      fi
      for i in $(seq 1 30); do
        PHASE=$(curl -s -H "Authorization: Bearer $TOKEN" "$HOST/api/v1/namespaces/$NS" | tr -d ' \t\r\n' \
          | grep -o '"phase":"[^"]*"' | cut -d'"' -f4 || true)
        [ "$PHASE" = "Active" ] && exit 0
        sleep 2
      done
      echo "ERROR: namespace $NS did not become Active within 60s" >&2
      exit 1
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/${self.triggers.name}" || true
    EOT
  }
}

# Create BIG-IP login secret for CIS controller
resource "null_resource" "bigip_ctlr_login" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name      = "f5-bigip-ctlr-login"
    namespace = var.flo_namespace
    host      = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/api/v1/namespaces/${var.flo_namespace}/secrets/f5-bigip-ctlr-login?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"v1","kind":"Secret","metadata":{"name":"f5-bigip-ctlr-login","namespace":"${var.flo_namespace}"},"type":"Opaque","data":{"username":"${local.bigip_username_b64}","password":"${local.bigip_password_b64}","url":"${local.bigip_url_b64}"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/${self.triggers.namespace}/secrets/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.flo_namespace]
}

# Create FAR image pull secret in flo namespace
resource "null_resource" "far_secret_flo" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name      = "far-secret"
    namespace = var.flo_namespace
    host      = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/api/v1/namespaces/${var.flo_namespace}/secrets/far-secret?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"v1","kind":"Secret","metadata":{"name":"far-secret","namespace":"${var.flo_namespace}"},"type":"kubernetes.io/dockerconfigjson","data":{".dockerconfigjson":"${local.far_docker_config_b64}"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/${self.triggers.namespace}/secrets/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.flo_namespace]
}

# Create FAR image pull secret in f5-utils namespace
resource "null_resource" "far_secret_utils" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name      = "far-secret"
    namespace = var.utils_namespace
    host      = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/api/v1/namespaces/${var.utils_namespace}/secrets/far-secret?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"v1","kind":"Secret","metadata":{"name":"far-secret","namespace":"${var.utils_namespace}"},"type":"kubernetes.io/dockerconfigjson","data":{".dockerconfigjson":"${local.far_docker_config_b64}"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/${self.triggers.namespace}/secrets/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.f5_utils]
}

# Install f5-lifecycle-operator using Helm CLI local-exec
resource "null_resource" "f5_lifecycle_operator" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    manifest_version      = var.f5_bigip_k8s_manifest_version
    manifest_download_dir = var.manifest_download_dir
    flo_namespace         = var.flo_namespace
    kube_host             = var.kube_host
    kube_token            = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HELM_MIN="3.8.0"
      HELM_BIN="helm"
      helm_ok() {
        local v
        v=$(helm version --short 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1) || return 1
        # busybox sort (Alpine) lacks -C; -c is portable across busybox + GNU.
        # 2>/dev/null suppresses the "sort: line N: disorder:" diagnostic so
        # only the exit code matters.
        printf '%s\n%s\n' "$HELM_MIN" "$v" | sort -V -c 2>/dev/null
      }
      if ! helm_ok; then
        HELM_VERSION="3.17.2"
        # Per-resource scratch dir — terraform runs provisioners in
        # parallel by default, so a shared /tmp/helm-install path
        # races ("Text file busy" when one process writes while
        # another extracts).
        HELM_TMP=$(mktemp -d "$${TMPDIR:-/tmp}/helm-install-XXXXXX")
        curl -fsSL -o "$HELM_TMP/helm.tar.gz" \
          "https://get.helm.sh/helm-v$HELM_VERSION-linux-amd64.tar.gz"
        tar -xzf "$HELM_TMP/helm.tar.gz" -C "$HELM_TMP"
        HELM_BIN="$HELM_TMP/linux-amd64/helm"
      fi
      FLO_VERSION=$(cat ${var.manifest_download_dir}/flo-version.txt | tr -d '[:space:]')
      echo "${local.far_service_account_b64}" | $HELM_BIN registry login \
        -u _json_key_base64 --password-stdin \
        ${replace(var.far_repo_url, "https://", "")}
      printf '%s' "${base64encode(jsonencode(local.flo_helm_values))}" | base64 -d > /tmp/flo-helm-values.json
      $HELM_BIN upgrade --install flo \
        oci://${replace(var.far_repo_url, "https://", "")}/charts/f5-lifecycle-operator \
        --version "$FLO_VERSION" \
        --namespace "${var.flo_namespace}" \
        --create-namespace \
        --values /tmp/flo-helm-values.json \
        --wait=false \
        --timeout 300s \
        --kube-apiserver="${var.kube_host}" \
        --kube-token="${var.kube_token}" \
        --kube-insecure-skip-tls-verify=true
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      HELM_BIN="helm"
      [ -f /tmp/helm-install/linux-amd64/helm ] && HELM_BIN="/tmp/helm-install/linux-amd64/helm"
      $HELM_BIN uninstall flo \
        --namespace "${self.triggers.flo_namespace}" \
        --kube-apiserver="${self.triggers.kube_host}" \
        --kube-token="${self.triggers.kube_token}" \
        --kube-insecure-skip-tls-verify=true \
        --ignore-not-found || true
    EOT
  }

  depends_on = [
    null_resource.extract_flo_version,
    null_resource.flo_namespace,
    null_resource.far_secret_flo,
    null_resource.ca_cluster_issuer
  ]
}

# Install f5-bnk-cis using Helm CLI local-exec
resource "null_resource" "f5_bnk_cis" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    manifest_version      = var.f5_bigip_k8s_manifest_version
    manifest_download_dir = var.manifest_download_dir
    flo_namespace         = var.flo_namespace
    kube_host             = var.kube_host
    kube_token            = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      HELM_MIN="3.8.0"
      HELM_BIN="helm"
      helm_ok() {
        local v
        v=$(helm version --short 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1) || return 1
        # busybox sort (Alpine) lacks -C; -c is portable across busybox + GNU.
        # 2>/dev/null suppresses the "sort: line N: disorder:" diagnostic so
        # only the exit code matters.
        printf '%s\n%s\n' "$HELM_MIN" "$v" | sort -V -c 2>/dev/null
      }
      if ! helm_ok; then
        HELM_VERSION="3.17.2"
        # Per-resource scratch dir — terraform runs provisioners in
        # parallel by default, so a shared /tmp/helm-install path
        # races ("Text file busy" when one process writes while
        # another extracts).
        HELM_TMP=$(mktemp -d "$${TMPDIR:-/tmp}/helm-install-XXXXXX")
        curl -fsSL -o "$HELM_TMP/helm.tar.gz" \
          "https://get.helm.sh/helm-v$HELM_VERSION-linux-amd64.tar.gz"
        tar -xzf "$HELM_TMP/helm.tar.gz" -C "$HELM_TMP"
        HELM_BIN="$HELM_TMP/linux-amd64/helm"
      fi
      CIS_VERSION=$(cat ${var.manifest_download_dir}/cis-version.txt | tr -d '[:space:]')
      echo "${local.far_service_account_b64}" | $HELM_BIN registry login \
        -u _json_key_base64 --password-stdin \
        ${replace(var.far_repo_url, "https://", "")}
      printf '%s' "${base64encode(jsonencode(local.cis_helm_values))}" | base64 -d > /tmp/cis-helm-values.json
      $HELM_BIN upgrade --install f5-bnk-cis \
        oci://${replace(var.far_repo_url, "https://", "")}/charts/f5-bnk-cis \
        --version "$CIS_VERSION" \
        --namespace "${var.flo_namespace}" \
        --create-namespace \
        --values /tmp/cis-helm-values.json \
        --wait=false \
        --timeout 300s \
        --kube-apiserver="${var.kube_host}" \
        --kube-token="${var.kube_token}" \
        --kube-insecure-skip-tls-verify=true
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      HELM_BIN="helm"
      [ -f /tmp/helm-install/linux-amd64/helm ] && HELM_BIN="/tmp/helm-install/linux-amd64/helm"
      $HELM_BIN uninstall f5-bnk-cis \
        --namespace "${self.triggers.flo_namespace}" \
        --kube-apiserver="${self.triggers.kube_host}" \
        --kube-token="${self.triggers.kube_token}" \
        --kube-insecure-skip-tls-verify=true \
        --ignore-not-found || true
    EOT
  }

  depends_on = [
    null_resource.extract_flo_version,
    null_resource.flo_namespace,
    null_resource.far_secret_flo,
    null_resource.ca_cluster_issuer,
  ]
}

# Apply privileged SCC to flo-f5-lifecycle-operator service account using Kubernetes RBAC
# This approach works with IBM Schematics and doesn't require 'oc' CLI
resource "null_resource" "flo_scc_privileged" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name  = "system:openshift:scc:privileged:${var.flo_namespace}:flo-f5-lifecycle-operator"
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/system:openshift:scc:privileged:${var.flo_namespace}:flo-f5-lifecycle-operator?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRoleBinding","metadata":{"name":"system:openshift:scc:privileged:${var.flo_namespace}:flo-f5-lifecycle-operator"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"system:openshift:scc:privileged"},"subjects":[{"kind":"ServiceAccount","name":"flo-f5-lifecycle-operator","namespace":"${var.flo_namespace}"}]}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.f5_lifecycle_operator[0]]
}

# Apply privileged SCC to f5-bigip-ctlr-serviceaccount for CIS
resource "null_resource" "cis_scc_privileged" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name  = "system:openshift:scc:privileged:${var.flo_namespace}:f5-bigip-ctlr-serviceaccount"
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/system:openshift:scc:privileged:${var.flo_namespace}:f5-bigip-ctlr-serviceaccount?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRoleBinding","metadata":{"name":"system:openshift:scc:privileged:${var.flo_namespace}:f5-bigip-ctlr-serviceaccount"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"system:openshift:scc:privileged"},"subjects":[{"kind":"ServiceAccount","name":"f5-bigip-ctlr-serviceaccount","namespace":"${var.flo_namespace}"}]}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.f5_bnk_cis[0]]
}

# Apply privileged SCC to default service account for CIS
resource "null_resource" "cis_default_scc_privileged" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    name  = "system:openshift:scc:privileged:${var.flo_namespace}:default"
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/system:openshift:scc:privileged:${var.flo_namespace}:default?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRoleBinding","metadata":{"name":"system:openshift:scc:privileged:${var.flo_namespace}:default"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"system:openshift:scc:privileged"},"subjects":[{"kind":"ServiceAccount","name":"default","namespace":"${var.flo_namespace}"}]}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.f5_bnk_cis[0]]
}

# Wait for SCC policies to be applied and pods to start
resource "time_sleep" "wait_for_flo_scc_policies" {
  count           = local.global_enabled ? 1 : 0
  create_duration = "30s"
  triggers = {
    scc_policies_applied = "1"
  }
  depends_on = [null_resource.flo_scc_privileged, null_resource.cis_scc_privileged, null_resource.cis_default_scc_privileged]
}

# Wait for FLO pods to start after SCC policies applied.
# Replaces the former kubernetes_resources data source which validated the k8s
# REST client at plan time (fails when cluster doesn't exist yet).
resource "time_sleep" "wait_for_flo_pods" {
  count           = local.global_enabled ? 1 : 0
  create_duration = "60s"
  depends_on      = [time_sleep.wait_for_flo_scc_policies[0]]
}

# Create service account for node labeler via curl server-side apply — idempotent.
resource "null_resource" "node_labeler_sa" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/api/v1/namespaces/kube-system/serviceaccounts/node-labeler?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"node-labeler","namespace":"kube-system"}}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/api/v1/namespaces/kube-system/serviceaccounts/node-labeler" || true
    EOT
  }

  depends_on = [var.cert_manager_crd_ready]
}

# Create cluster role for node labeling
resource "null_resource" "node_labeler_role" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterroles/node-labeler?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRole","metadata":{"name":"node-labeler"},"rules":[{"apiGroups":[""],"resources":["nodes"],"verbs":["get","list","patch","update"]}]}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/rbac.authorization.k8s.io/v1/clusterroles/node-labeler" || true
    EOT
  }

  depends_on = [null_resource.node_labeler_sa]
}

# Bind role to service account
resource "null_resource" "node_labeler_binding" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/node-labeler?fieldManager=terraform&force=true" \
        -d '{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"ClusterRoleBinding","metadata":{"name":"node-labeler"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"node-labeler"},"subjects":[{"kind":"ServiceAccount","name":"node-labeler","namespace":"kube-system"}]}'
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/node-labeler" || true
    EOT
  }

  depends_on = [null_resource.node_labeler_role]
}

# Create a Job to label all nodes via curl server-side apply
resource "null_resource" "node_labeler_job" {
  count = local.global_enabled ? 1 : 0

  triggers = {
    host  = var.kube_host
    token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      JOB_NAME="node-labeler-$(date +%Y%m%d%H%M%S)"
      curl -sf -X POST \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/json" \
        -k "${var.kube_host}/apis/batch/v1/namespaces/kube-system/jobs" \
        -d "{\"apiVersion\":\"batch/v1\",\"kind\":\"Job\",\"metadata\":{\"name\":\"$JOB_NAME\",\"namespace\":\"kube-system\"},\"spec\":{\"backoffLimit\":3,\"template\":{\"metadata\":{\"name\":\"node-labeler\"},\"spec\":{\"serviceAccountName\":\"node-labeler\",\"restartPolicy\":\"Never\",\"containers\":[{\"name\":\"labeler\",\"image\":\"bitnami/kubectl:latest\",\"command\":[\"/bin/sh\",\"-c\",\"kubectl label nodes --all app=f5-tmm --overwrite && echo All nodes labeled successfully\"]}]}}}}"
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      # Jobs are ephemeral — nothing to clean up on destroy
      true
    EOT
  }

  depends_on = [
    null_resource.network_attachment_definition,
    null_resource.f5_lifecycle_operator[0],
    null_resource.node_labeler_binding,
  ]
}

# ==============================================================================
# IBM IAM Trusted Profile for CNE Controller Service Account
# ==============================================================================

resource "ibm_iam_trusted_profile" "cne_controller" {
  count       = local.global_enabled ? 1 : 0
  name        = "${var.openshift_cluster_name}-f5-cne-controller-${var.flo_namespace}"
  description = "Trusted profile for F5 CNE controller service account in namespace ${var.flo_namespace} on cluster ${var.openshift_cluster_name}"
}

resource "ibm_iam_trusted_profile_link" "cne_controller_roks" {
  count      = local.global_enabled ? 1 : 0
  profile_id = ibm_iam_trusted_profile.cne_controller[0].id
  cr_type    = "ROKS_SA"
  link {
    crn       = var.openshift_cluster_crn
    namespace = var.flo_namespace
    name      = "f5-cne-controller-${var.flo_namespace}-f5-cne-controller-serviceaccount"
  }
  name = "f5-cne-controller-roks-link"
}

resource "ibm_iam_trusted_profile_policy" "cne_controller_vpc" {
  count  = local.global_enabled ? 1 : 0
  iam_id = ibm_iam_trusted_profile.cne_controller[0].iam_id
  roles  = ["Viewer", "Editor"]

  resource_attributes {
    name  = "serviceName"
    value = "is"
  }

  resource_attributes {
    name  = "vpcId"
    value = var.cluster_vpc_id
  }
}
