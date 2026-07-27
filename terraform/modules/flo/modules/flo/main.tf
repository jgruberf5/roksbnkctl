locals {
  global_enabled = var.enabled
  use_kubectl    = var.enabled && var.bnk_cr_mode == "kubectl"
  use_legacy     = var.enabled && var.bnk_cr_mode == "legacy_curl"

  far_registry_hostname = replace(var.far_repo_url, "https://", "")

  # Sprint 29 air-gap mirror — chart/image host split. Each empty input
  # coalesces back to far_repo_url, so an un-mirrored apply is byte-identical:
  # both hosts resolve to far_registry_hostname exactly as before.
  far_chart_hostname = replace(coalesce(var.far_chart_repo_url, var.far_repo_url), "https://", "")
  far_image_hostname = replace(coalesce(var.far_image_repo_url, var.far_repo_url), "https://", "")

  # The chart consumes image_repository as a PREFIX it appends the image name to,
  # so it must end in "/images" — the mirror preserves the artifact's images/<name>
  # path under <ns> (PushRef uses the full a.Name "images/<name>"), exactly as FAR
  # serves repo.f5.com/images/<name>. far_image_hostname coalesces back to
  # far_registry_hostname off the mirror path, so this is byte-identical there.
  # (The CNEInstance spec.registry.uri, by contrast, is the BARE host — the CNE
  # controller appends "/images/<name>" itself.)
  image_repository = "${local.far_image_hostname}/images"

  # Which dockerconfig secret the pods pull with.
  #
  #   off the mirror  → far-secret (the FAR credential)
  #   ICR / in-cluster mirror → none; RBAC (system:image-puller) authorizes the pull
  #   EXTERNAL mirror with credentials → mirror-secret, built from them
  #
  # That last case is the one that was missing. Dropping the pull secret for ANY
  # mirror assumed the mirror authorizes by RBAC — true for the in-cluster registry,
  # false for a private Harbor/Artifactory, whose pods then pull anonymously and get
  # 401/ImagePullBackOff. The only way that ever "worked" was to make the mirror
  # project world-readable, which for a registry holding F5's proprietary images is
  # not an acceptable requirement.
  mirror_pull_secret_name = "mirror-secret"
  image_pull_secrets_flo = (
    local.has_mirror_creds ? [{ name = local.mirror_pull_secret_name }] :
    var.use_registry_mirror ? [] : [{ name = "far-secret" }]
  )
  image_pull_secrets_cis = (
    local.has_mirror_creds ? [local.mirror_pull_secret_name] :
    var.use_registry_mirror ? [] : ["far-secret"]
  )

  # dockerconfigjson for the external mirror. The auth key is the bare registry host
  # — kubelet matches image refs by host prefix — so strip any repo-prefix path the
  # image host carries (e.g. harbor.example.com/bnk-mirror).
  mirror_registry_host = split("/", local.far_image_hostname)[0]
  mirror_docker_config_json = replace(
    jsonencode({
      auths = {
        (local.mirror_registry_host) = {
          auth = base64encode("${var.registry_mirror_username}:${var.registry_mirror_password}")
        }
      }
    }),
    ":", ": "
  )

  # far-secret is provisioned only off the mirror path. In mirror mode the
  # secret is dropped (RBAC handles pulls), so the count gates collapse to 0.
  far_secret_legacy  = local.use_legacy && !var.use_registry_mirror ? 1 : 0
  far_secret_kubectl = local.use_kubectl && !var.use_registry_mirror ? 1 : 0

  # node-labeler helper image. Off the mirror path it's the public
  # bitnami/kubectl:latest (byte-identical default). In mirror mode it pulls
  # from the in-cluster image host (the BOM mirrors bitnami/kubectl:latest
  # under the mirror namespace) so the air-gapped cluster needs no public pull.
  node_labeler_image      = var.use_registry_mirror ? "${local.far_image_hostname}/bitnami/kubectl:latest" : "bitnami/kubectl:latest"
  far_service_account_b64 = local.global_enabled && var.use_cos_bucket ? data.local_file.cne_pull_64_json_file[0].content : var.far_service_account_b64
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
      repository  = local.image_repository
      repo        = "f5-bnk-cis"
      pullSecrets = local.image_pull_secrets_cis
    }
  }

  flo_helm_values = {
    global = {
      imagePullSecrets = local.image_pull_secrets_flo
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

  # cos-get: download the FAR auth tarball (binary → a file, via the COS SDK). No
  # interpreter → cmd.exe execs roksbnkctl.exe on Windows; key via env.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx cos-get --instance-crn ${data.ibm_resource_instance.cos_instance[0].crn} --bucket ${var.ibmcloud_resources_cos_bucket} --key ${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/${var.f5_cne_far_auth_file} --region ${var.ibmcloud_cos_bucket_region}"
    environment = {
      IBMCLOUD_API_KEY = var.ibmcloud_api_key
    }
  }
}

resource "null_resource" "cne_far_tgz_extractor" {
  count = local.global_enabled && var.use_cos_bucket ? 1 : 0

  triggers = {
    archive_id  = null_resource.far_archive_download[0].id
    scratch_dir = var.scratch_dir
  }

  # far-extract: write the single _json_key_base64 service-account JSON (Go
  # tar-extract, no host tar/grep).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx far-extract --tarball ${var.scratch_dir}/${var.f5_cne_far_auth_file} --out ${var.scratch_dir}/far-sa.json"
  }
}

locals {
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"
  # far-extract writes the service-account JSON to a fixed path (far-sa.json), so
  # the old "find the .json filename inside the tarball" indirection is gone.
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
  filename   = "${var.scratch_dir}/far-sa.json"
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
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy && var.cert_manager_crd_ready ? 1 : 0

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
  count = local.use_legacy && var.cert_manager_crd_ready ? 1 : 0

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
  count = local.use_legacy && var.cert_manager_crd_ready ? 1 : 0

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

  # The f5-bigip-k8s-manifest chart is mirrored like any other chart (it is a BOM
  # artifact — see bnkbom.ManifestChartName), so pull it from the same chart host as
  # everything else: FAR off the mirror, the mirror under it. That keeps a mirrored
  # install fully disconnected. Unlike the FLP phase, the FLO default path also
  # yamldecodes the whole manifest (data.local_file.bnk_manifest → the CNEManifest
  # CR), so we pull-file it to a FIXED on-disk path (bnk-manifest.yaml) rather than a
  # throwaway temp dir, then resolve both sub-chart versions from that local file
  # (--manifest-file, no second pull). No interpreter → cmd.exe execs roksbnkctl.exe.

  # 1. pull the manifest chart once → a stable path both this resource and
  #    data.local_file.bnk_manifest read.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx helm-value pull-file --chart oci://${local.far_chart_hostname}/release/f5-bigip-k8s-manifest --version ${var.f5_bigip_k8s_manifest_version} --file bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml --registry-login ${local.chart_login_host} --username ${local.chart_pull_username} --password-env HELM_REGISTRY_PW --out ${var.manifest_download_dir}/bnk-manifest.yaml"
    environment = {
      HELM_REGISTRY_PW = local.chart_pull_password
    }
  }

  # 2. FLO version from the already-pulled manifest (no network).
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx helm-value chart-version --manifest-file ${var.manifest_download_dir}/bnk-manifest.yaml --subchart charts/f5-lifecycle-operator --out ${var.manifest_download_dir}/flo-version.txt"
  }

  # 3. CIS version from the same manifest.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx helm-value chart-version --manifest-file ${var.manifest_download_dir}/bnk-manifest.yaml --subchart charts/f5-bnk-cis --out ${var.manifest_download_dir}/cis-version.txt"
  }

  triggers = {
    manifest_version      = var.f5_bigip_k8s_manifest_version
    manifest_download_dir = var.manifest_download_dir
  }

  depends_on = [null_resource.cne_far_tgz_extractor]
}

# Read extracted FLO/CIS versions after extract_flo_version provisioner runs.
# depends_on defers evaluation to apply time (file exists) rather than plan time
# (file absent). tfx read-json emits "" for a missing file (like the shell's
# `cat … 2>/dev/null`), so a destroy-phase refresh does not abort even in a fresh
# ephemeral container.
data "external" "versions" {
  count = local.global_enabled ? 1 : 0

  program = [
    local.roksbnkctl_bin, "tfx", "read-json",
    "--pair", "flo=${var.manifest_download_dir}/flo-version.txt",
    "--pair", "cis=${var.manifest_download_dir}/cis-version.txt",
  ]

  depends_on = [null_resource.extract_flo_version]
}

# Create f5-utils namespace via curl server-side apply — idempotent; no provider
# existence-check so it succeeds even when the namespace was left by a prior run.
resource "null_resource" "f5_utils" {
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy && var.flo_namespace != "default" ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count = local.far_secret_legacy

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
  count = local.far_secret_legacy

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
  count = local.use_legacy ? 1 : 0

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
      echo "${local.chart_pull_password}" | $HELM_BIN registry login \
        -u "${local.chart_pull_username}" --password-stdin \
        ${local.chart_login_host}
      printf '%s' "${base64encode(jsonencode(local.flo_helm_values))}" | base64 -d > /tmp/flo-helm-values.json
      $HELM_BIN upgrade --install flo \
        oci://${local.far_chart_hostname}/charts/f5-lifecycle-operator \
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
  count = local.use_legacy ? 1 : 0

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
      echo "${local.chart_pull_password}" | $HELM_BIN registry login \
        -u "${local.chart_pull_username}" --password-stdin \
        ${local.chart_login_host}
      printf '%s' "${base64encode(jsonencode(local.cis_helm_values))}" | base64 -d > /tmp/cis-helm-values.json
      $HELM_BIN upgrade --install f5-bnk-cis \
        oci://${local.far_chart_hostname}/charts/f5-bnk-cis \
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
# This approach uses Kubernetes RBAC directly and doesn't require the 'oc' CLI
resource "null_resource" "flo_scc_privileged" {
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count           = local.use_legacy ? 1 : 0
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
  count           = local.use_legacy ? 1 : 0
  create_duration = "60s"
  depends_on      = [time_sleep.wait_for_flo_scc_policies[0]]
}

# Create service account for node labeler via curl server-side apply — idempotent.
resource "null_resource" "node_labeler_sa" {
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
  count = local.use_legacy ? 1 : 0

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
        -d "{\"apiVersion\":\"batch/v1\",\"kind\":\"Job\",\"metadata\":{\"name\":\"$JOB_NAME\",\"namespace\":\"kube-system\"},\"spec\":{\"backoffLimit\":3,\"template\":{\"metadata\":{\"name\":\"node-labeler\"},\"spec\":{\"serviceAccountName\":\"node-labeler\",\"restartPolicy\":\"Never\",\"containers\":[{\"name\":\"labeler\",\"image\":\"${local.node_labeler_image}\",\"command\":[\"/bin/sh\",\"-c\",\"kubectl label nodes --all app=f5-tmm --overwrite && echo All nodes labeled successfully\"]}]}}}}"
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
# Sprint 27 — kubectl mode (terraform-native)
# ------------------------------------------------------------------------------
# Replaces every legacy null_resource/curl + time_sleep above with:
#   - kubernetes_namespace_v1 / kubernetes_secret_v1 (helm prerequisites)
#   - helm_release (FLO + CIS, wait = true)
#   - kubectl_manifest + wait_for (cert issuers, NADs, SCC bindings,
#     node-labeler).
# The spec locals (NAD configs, helm values) are shared with the legacy path.
# ==============================================================================

locals {
  # FLO/CIS chart versions discovered terraform-side from the FAR manifest.
  flo_chart_version = local.global_enabled && var.use_cos_bucket ? try(data.external.versions[0].result.flo, "") : ""
  cis_chart_version = local.global_enabled && var.use_cos_bucket ? try(data.external.versions[0].result.cis, "") : ""

  # NAD (ens3) manifest — spec.config is a JSON string, same as the legacy curl.
  nad_ens3_manifest = {
    apiVersion = "k8s.cni.cncf.io/v1"
    kind       = "NetworkAttachmentDefinition"
    metadata = {
      name      = local.nad_name_computed
      namespace = var.flo_namespace
    }
    spec = {
      config = var.nad_cni_type == "host-device" ? local.nad_config_host_device : local.nad_config_ipvlan
    }
  }

  nad_macvlan_manifest = {
    apiVersion = "k8s.cni.cncf.io/v1"
    kind       = "NetworkAttachmentDefinition"
    metadata = {
      name      = "macvlan-conf"
      namespace = var.flo_namespace
    }
    spec = {
      config = local.macvlan_config
    }
  }

  selfsigned_issuer_manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "ClusterIssuer"
    metadata   = { name = "selfsigned-cluster-issuer" }
    spec       = { selfSigned = {} }
  }

  ca_certificate_manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "Certificate"
    metadata = {
      name      = "ext-ca"
      namespace = var.cert_manager_namespace
    }
    spec = {
      isCA       = true
      commonName = "ext-ca"
      secretName = "ext-ca"
      issuerRef = {
        name  = "selfsigned-cluster-issuer"
        kind  = "ClusterIssuer"
        group = "cert-manager.io"
      }
    }
  }

  ca_cluster_issuer_manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "ClusterIssuer"
    metadata   = { name = var.cluster_issuer_name }
    spec       = { ca = { secretName = "ext-ca" } }
  }

  scc_clusterrolebinding = {
    flo = {
      sa   = "flo-f5-lifecycle-operator"
      name = "system:openshift:scc:privileged:${var.flo_namespace}:flo-f5-lifecycle-operator"
    }
    cis = {
      sa   = "f5-bigip-ctlr-serviceaccount"
      name = "system:openshift:scc:privileged:${var.flo_namespace}:f5-bigip-ctlr-serviceaccount"
    }
    cis_default = {
      sa   = "default"
      name = "system:openshift:scc:privileged:${var.flo_namespace}:default"
    }
  }

  node_labeler_sa_manifest = {
    apiVersion = "v1"
    kind       = "ServiceAccount"
    metadata   = { name = "node-labeler", namespace = "kube-system" }
  }

  node_labeler_role_manifest = {
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRole"
    metadata   = { name = "node-labeler" }
    rules = [{
      apiGroups = [""]
      resources = ["nodes"]
      verbs     = ["get", "list", "patch", "update"]
    }]
  }

  node_labeler_binding_manifest = {
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata   = { name = "node-labeler" }
    roleRef = {
      apiGroup = "rbac.authorization.k8s.io"
      kind     = "ClusterRole"
      name     = "node-labeler"
    }
    subjects = [{
      kind      = "ServiceAccount"
      name      = "node-labeler"
      namespace = "kube-system"
    }]
  }

  # Stable-name Job (NOT generateName) so kubectl_manifest can wait_for Complete;
  # ttlSecondsAfterFinished GC's the finished Job so re-applies don't collide.
  node_labeler_job_manifest = {
    apiVersion = "batch/v1"
    kind       = "Job"
    metadata   = { name = "node-labeler", namespace = "kube-system" }
    spec = {
      backoffLimit            = 3
      ttlSecondsAfterFinished = var.node_labeler_job_ttl_seconds
      template = {
        metadata = { name = "node-labeler" }
        spec = merge({
          serviceAccountName = "node-labeler"
          restartPolicy      = "Never"
          containers = [{
            name    = "labeler"
            image   = local.node_labeler_image
            command = ["/bin/sh", "-c", "kubectl label nodes --all app=f5-tmm --overwrite && echo All nodes labeled successfully"]
          }]
          # In mirror mode this image comes from the mirror too, so a PRIVATE external
          # mirror needs the pull secret here as well — kube-system, not the FLO
          # namespace. Absent off the mirror path (the image is public then).
          }, local.has_mirror_creds ? {
          imagePullSecrets = [{ name = local.mirror_pull_secret_name }]
        } : {})
      }
    }
  }
}

# --- Namespaces (helm prerequisites — precede the charts) -------------------

resource "kubernetes_namespace_v1" "f5_utils" {
  count = local.use_kubectl ? 1 : 0
  metadata {
    name = var.utils_namespace
  }
}

resource "kubernetes_namespace_v1" "flo" {
  count = local.use_kubectl && var.flo_namespace != "default" ? 1 : 0
  metadata {
    name = var.flo_namespace
  }
}

# --- Secrets (image-pull + CIS login — precede the charts) ------------------

resource "kubernetes_secret_v1" "far_secret_flo" {
  count = local.far_secret_kubectl
  metadata {
    name      = "far-secret"
    namespace = var.flo_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.far_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.flo]
}

resource "kubernetes_secret_v1" "far_secret_utils" {
  count = local.far_secret_kubectl
  metadata {
    name      = "far-secret"
    namespace = var.utils_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.far_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.f5_utils]
}

# Pull secret for an EXTERNAL registry mirror (a private Harbor/Artifactory), in the
# same two namespaces far-secret covers. Without it the pods pull anonymously and get
# ImagePullBackOff — and the only workaround was to make the mirror world-readable,
# which is not something to ask of a registry holding F5's proprietary images.
resource "kubernetes_secret_v1" "mirror_secret_flo" {
  count = local.use_kubectl && local.has_mirror_creds ? 1 : 0
  metadata {
    name      = local.mirror_pull_secret_name
    namespace = var.flo_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.mirror_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.flo]
}

resource "kubernetes_secret_v1" "mirror_secret_kube_system" {
  count = local.use_kubectl && local.has_mirror_creds ? 1 : 0
  metadata {
    name      = local.mirror_pull_secret_name
    namespace = "kube-system"
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.mirror_docker_config_json
  }
}

resource "kubernetes_secret_v1" "mirror_secret_utils" {
  count = local.use_kubectl && local.has_mirror_creds ? 1 : 0
  metadata {
    name      = local.mirror_pull_secret_name
    namespace = var.utils_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.mirror_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.f5_utils]
}

resource "kubernetes_secret_v1" "bigip_ctlr_login" {
  count = local.use_kubectl ? 1 : 0
  metadata {
    name      = "f5-bigip-ctlr-login"
    namespace = var.flo_namespace
  }
  type = "Opaque"
  data = {
    username = var.bigip_username
    password = var.bigip_password
    url      = replace(var.bigip_url, "https://", "")
  }
  depends_on = [kubernetes_namespace_v1.flo]
}

# --- cert-manager CRs (issuer chain) ----------------------------------------
# Depend on the cert-manager helm_release (CRD-before-CR), passed in via
# var.cert_manager_ready_dependency from the wrapping module.

resource "kubectl_manifest" "selfsigned_issuer" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.selfsigned_issuer_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "ca_certificate" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.ca_certificate_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  wait_for {
    condition {
      type   = "Ready"
      status = "True"
    }
  }

  depends_on = [kubectl_manifest.selfsigned_issuer]
}

resource "kubectl_manifest" "ca_cluster_issuer" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.ca_cluster_issuer_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  wait_for {
    condition {
      type   = "Ready"
      status = "True"
    }
  }

  depends_on = [kubectl_manifest.ca_certificate]
}

# --- NADs (no status — no wait). Only need the flo namespace. ---------------

resource "kubectl_manifest" "nad_ens3" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.nad_ens3_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubernetes_namespace_v1.flo]
}

resource "kubectl_manifest" "nad_macvlan" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.nad_macvlan_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubernetes_namespace_v1.flo]
}

# --- Charts: FLO + CIS (wait = true → real rollout readiness) ---------------

locals {
  # OCI chart-pull credentials, by registry backend:
  #   - Off the mirror: repo.f5.com (FAR) with the _json_key_base64 service
  #     account.
  #   - Under an ICR mirror (registry target=icr, host *.icr.io — the Sprint 30
  #     default): IBM Container Registry only accepts `iamapikey` + an IBM Cloud
  #     API key. It REJECTS a bearer token with "The requested authentication
  #     method is not supported", so the OpenShift-token path below cannot be
  #     reused here — authenticate with the workspace API key instead.
  #   - Under the OpenShift in-cluster registry route: the route validates the
  #     cluster's OpenShift token and ignores the username (the FAR creds are
  #     rejected with "unable to validate token").
  is_icr_mirror = var.use_registry_mirror && can(regex("(^|[.])icr[.]io(/|$)", local.far_chart_hostname))

  # An EXTERNAL mirror (e.g. Harbor) authenticates chart pulls with its own
  # basic-auth credentials — not the kube token (in-cluster OpenShift registry) or
  # an IAM key (ICR). Signalled by registry_mirror_password being set.
  has_mirror_creds = var.use_registry_mirror && !local.is_icr_mirror && var.registry_mirror_password != ""

  chart_pull_username = (
    !var.use_registry_mirror ? "_json_key_base64" :
    local.is_icr_mirror ? "iamapikey" :
    local.has_mirror_creds ? var.registry_mirror_username : "unused"
  )
  chart_pull_password = (
    !var.use_registry_mirror ? local.far_service_account_b64 :
    local.is_icr_mirror ? var.ibmcloud_api_key :
    local.has_mirror_creds ? var.registry_mirror_password : var.kube_token
  )

  # `helm registry login` takes a bare registry HOST; far_chart_hostname carries a
  # repo-prefix path for a mirror (e.g. host/bnk-mirror), so strip it — the
  # credential must be stored under the host the subsequent `helm pull` resolves.
  chart_login_host = split("/", local.far_chart_hostname)[0]

  # Local chart archives, staged by the tfx pull-chart provisioners below. The
  # helm_release resources install from these LOCAL paths (chart = the .tgz) rather
  # than an oci:// repository, so the terraform helm PROVIDER performs no OCI login.
  # The provider's login stores the credential via a docker credential helper, and
  # on Windows the multi-KB FAR password overflows the Credential Manager blob ("The
  # stub received bad data"); dropping the provider creds instead made it pull
  # anonymously (403). tfx pull-chart authenticates inline (helm pull
  # --registry-config), the same mechanism the manifest/version pulls already use.
  flo_chart_archive = "${var.manifest_download_dir}/f5-lifecycle-operator-${local.flo_chart_version}.tgz"
  cis_chart_archive = "${var.manifest_download_dir}/f5-bnk-cis-${local.cis_chart_version}.tgz"
}

# Stage the FLO and CIS chart archives on disk via tfx (inline auth, no login/store).
# No interpreter → cmd.exe execs roksbnkctl.exe directly on Windows.
resource "null_resource" "flo_chart_pull" {
  count = local.use_kubectl ? 1 : 0
  triggers = {
    version = local.flo_chart_version
    archive = local.flo_chart_archive
  }
  provisioner "local-exec" {
    command     = "${local.roksbnkctl_bin} tfx helm-value pull-chart --chart oci://${local.far_chart_hostname}/charts/f5-lifecycle-operator --version ${local.flo_chart_version} --registry-login ${local.chart_login_host} --username ${local.chart_pull_username} --password-env HELM_REGISTRY_PW --out ${local.flo_chart_archive}"
    environment = { HELM_REGISTRY_PW = local.chart_pull_password }
  }
  depends_on = [data.external.versions]
}

resource "null_resource" "cis_chart_pull" {
  count = local.use_kubectl ? 1 : 0
  triggers = {
    version = local.cis_chart_version
    archive = local.cis_chart_archive
  }
  provisioner "local-exec" {
    command     = "${local.roksbnkctl_bin} tfx helm-value pull-chart --chart oci://${local.far_chart_hostname}/charts/f5-bnk-cis --version ${local.cis_chart_version} --registry-login ${local.chart_login_host} --username ${local.chart_pull_username} --password-env HELM_REGISTRY_PW --out ${local.cis_chart_archive}"
    environment = { HELM_REGISTRY_PW = local.chart_pull_password }
  }
  depends_on = [data.external.versions]
}

resource "helm_release" "flo" {
  count = local.use_kubectl ? 1 : 0

  name = "flo"
  # Install from the locally-staged archive (see null_resource.flo_chart_pull):
  # the helm provider loads it from disk and does NO OCI login — the login's
  # credential-store step fails on Windows, and dropping the creds pulls anonymously.
  chart            = local.flo_chart_archive
  namespace        = var.flo_namespace
  create_namespace = false

  # Match the legacy `helm upgrade --install ... --wait=false`: deploy the FLO
  # operator chart WITHOUT blocking on helm-level pod readiness. Real
  # readiness is gated downstream by the CNEInstance / License CR `wait_for`
  # status conditions (the operator must be running to reconcile those), which
  # is the meaningful signal. The operator Deployment's own helm readiness
  # races with the cert-manager-issued webhook certs applied later, so a
  # `wait = true` here just times out ("context deadline exceeded").
  wait    = false
  timeout = 300

  values = [yamlencode(local.flo_helm_values)]

  depends_on = [
    kubernetes_namespace_v1.flo,
    kubernetes_secret_v1.far_secret_flo,
    kubectl_manifest.ca_cluster_issuer,
    data.external.versions,
    null_resource.flo_chart_pull,
  ]
}

# ── CNEManifest CR — the disconnected FLO install ────────────────────────────
# FLO resolves the BNK manifest by LISTING cluster-scoped CNEManifest CRs and
# matching spec.version (GetManifest). Only when none matches does it fall back to
# pulling the manifest chart from the CNEInstance's spec.registry.uri — i.e. the
# MIRROR. But f5-bigip-k8s-manifest is the BOM's *source*, not a BOM member, so it
# is never replicated into a mirror; that fallback therefore 404s on any mirrored
# install and the CNEInstance never reconciles ("No CNEManifest exists which
# contains expected manifestVersion").
#
# So convert the manifest roksbnkctl already downloaded into a CNEManifest CR and
# apply it up front. FLO then reads the manifest from the CLUSTER and never reaches
# out to a registry for it — the install is fully disconnected.
#
# The conversion mirrors FLO's own RetrieveRemoteManifest byte-for-byte: strip the
# "images/" / "charts/" path prefix off each entry (split(name,"/")[1]) and name the
# CR lower("<productType>-<version>") — productType is BNK.
# Both install paths (kubectl + legacy curl) build the CR from this, so gate it on
# the module being enabled at all — not on one path.
data "local_file" "bnk_manifest" {
  count      = local.global_enabled ? 1 : 0
  filename   = "${var.manifest_download_dir}/bnk-manifest.yaml"
  depends_on = [null_resource.extract_flo_version]
}

locals {
  # The release stanza for the version we are installing.
  bnk_manifest_release = one([
    for r in try(yamldecode(data.local_file.bnk_manifest[0].content).releases, []) :
    r if r.version == var.f5_bigip_k8s_manifest_version
  ])

  cnemanifest_spec = {
    version = var.f5_bigip_k8s_manifest_version
    images = [
      for i in try(local.bnk_manifest_release.docker_images, []) :
      { name = split("/", i.name)[1], version = i.version }
    ]
    charts = [
      for c in try(local.bnk_manifest_release.helm_charts, []) :
      { name = split("/", c.name)[1], version = c.version }
    ]
  }

  # Same name FLO would mint for a manifest it fetched itself
  # (manifestName() = lower(productType + "-" + version), productType BNK), so a CR
  # left by an earlier FAR-mode apply is OVERWRITTEN rather than duplicated — two
  # CRs matching one version trips FLO's MultipleMatching guard and stalls the
  # CNEInstance.
  cnemanifest_name = lower("BNK-${var.f5_bigip_k8s_manifest_version}")

  cnemanifest_body = {
    apiVersion = "k8s.f5.com/v1"
    kind       = "CNEManifest"
    metadata   = { name = local.cnemanifest_name }
    spec       = local.cnemanifest_spec
  }
}

resource "kubectl_manifest" "cnemanifest" {
  count             = local.use_kubectl ? 1 : 0
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  yaml_body = yamlencode(local.cnemanifest_body)

  # The CNEManifest CRD ships in the FLO chart's crds subchart.
  depends_on = [helm_release.flo]
}

# Legacy-curl parity for the CNEManifest. Same CR, same name, applied by
# server-side-apply over the REST API — CNEManifest is CLUSTER-scoped, so the path
# carries no /namespaces/ segment.
resource "null_resource" "cnemanifest_legacy" {
  count = local.use_legacy ? 1 : 0

  triggers = {
    manifest  = jsonencode(local.cnemanifest_body)
    kube_host = var.kube_host
    token     = var.kube_token
    name      = local.cnemanifest_name
  }

  provisioner "local-exec" {
    command = <<-EOT
      printf '%s' "${base64encode(jsonencode(local.cnemanifest_body))}" | base64 -d | \
      curl -f -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        -k "${var.kube_host}/apis/k8s.f5.com/v1/cnemanifests/${local.cnemanifest_name}?fieldManager=terraform&force=true" \
        --data-binary @-
    EOT
  }

  # Tolerate a missing CRD/CR on teardown — the FLO release may already be gone.
  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.kube_host}/apis/k8s.f5.com/v1/cnemanifests/${self.triggers.name}" || true
    EOT
  }

  # The CRD arrives with the FLO operator install (legacy path).
  depends_on = [null_resource.f5_lifecycle_operator]
}

resource "helm_release" "cis" {
  count = local.use_kubectl ? 1 : 0

  name = "f5-bnk-cis"
  # Install from the locally-staged archive (see null_resource.cis_chart_pull) — no
  # provider OCI login, same as helm_release.flo above.
  chart            = local.cis_chart_archive
  namespace        = var.flo_namespace
  create_namespace = false

  # Legacy parity: `--wait=false`. CIS readiness is not helm-gated here either.
  wait    = false
  timeout = 300

  values = [yamlencode(local.cis_helm_values)]

  depends_on = [
    helm_release.flo,
    kubernetes_secret_v1.bigip_ctlr_login,
    null_resource.cis_chart_pull,
  ]
}

# --- SCC ClusterRoleBindings (no status — no wait). Need only the charts. ----

resource "kubectl_manifest" "flo_scc_privileged" {
  count             = local.use_kubectl ? 1 : 0
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  yaml_body = yamlencode({
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata   = { name = local.scc_clusterrolebinding.flo.name }
    roleRef = {
      apiGroup = "rbac.authorization.k8s.io"
      kind     = "ClusterRole"
      name     = "system:openshift:scc:privileged"
    }
    subjects = [{
      kind      = "ServiceAccount"
      name      = local.scc_clusterrolebinding.flo.sa
      namespace = var.flo_namespace
    }]
  })
  depends_on = [helm_release.flo]
}

resource "kubectl_manifest" "cis_scc_privileged" {
  for_each          = local.use_kubectl ? { cis = local.scc_clusterrolebinding.cis, cis_default = local.scc_clusterrolebinding.cis_default } : {}
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  yaml_body = yamlencode({
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata   = { name = each.value.name }
    roleRef = {
      apiGroup = "rbac.authorization.k8s.io"
      kind     = "ClusterRole"
      name     = "system:openshift:scc:privileged"
    }
    subjects = [{
      kind      = "ServiceAccount"
      name      = each.value.sa
      namespace = var.flo_namespace
    }]
  })
  depends_on = [helm_release.cis]
}

# --- Node-labeler (SA → Role → Binding → Job, wait Complete) ----------------
# Independent of cert-manager/FLO (architect: drop the cert_manager_crd_ready
# edge); only the SA needs kube-system (always present).

resource "kubectl_manifest" "node_labeler_sa" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_sa_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "node_labeler_role" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_role_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "node_labeler_binding" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_binding_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.node_labeler_role, kubectl_manifest.node_labeler_sa]
}

resource "kubectl_manifest" "node_labeler_job" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_job_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  wait_for {
    condition {
      type   = "Complete"
      status = "True"
    }
  }

  depends_on = [kubectl_manifest.node_labeler_binding, kubernetes_secret_v1.mirror_secret_kube_system]
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
