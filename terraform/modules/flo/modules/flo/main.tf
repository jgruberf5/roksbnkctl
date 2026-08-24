locals {
  # BNK 2.4 subsumes work this module does for 2.3 (#171). Named once so each
  # gated resource reads as a statement about the release rather than repeating
  # a version comparison. `!= "2.4"` rather than `== "2.3"`: an unrecognised
  # line keeps the 2.3 behaviour, which is additive and harmless, where treating
  # it as 2.4 would silently drop resources a future release may still need.
  line_pre_24 = var.bnk_line != "2.4"

  global_enabled = var.enabled
  use_kubectl    = var.enabled

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
  # secret is dropped (RBAC handles pulls), so the count gate collapses to 0.
  far_secret_kubectl = var.enabled && !var.use_registry_mirror ? 1 : 0

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

  # macvlan-conf is 2.3-only; on 2.4 the product supplies its own internal NAD.
  cneinstance_network_attachments = local.line_pre_24 ? [local.nad_name_computed, "macvlan-conf"] : [local.nad_name_computed]

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

    namespace = var.flo_namespace
    # ROKS IS OpenShift, and F5's chart says these platforms "may have specific
    # installation logic in the component controllers". Under Generic the CNE
    # controller looks for the kubeadm-config ConfigMap that only kubeadm-built
    # clusters have, aborts at Reconciled=False, and never creates TMM's internal
    # macvlan NAD (#189) — while every other signal still reports healthy.
    #
    # OCP fixes that and is verified to. It is NOT the default yet because a
    # reconciling F5Tmm then asks for a persistent volume, and TMM's replicas are
    # pinned to separate nodes across separate zones by the placement F5's own
    # reference prescribes; one ReadWriteOnce zonal volume cannot serve them.
    # ROKS IS OpenShift — this tool builds and adopts nothing else
    # (kube_version = local.openshift_version, tags ["terraform","openshift"]).
    # So OCP is not a choice, it is the only truthful value, and F5's chart says
    # these platforms "may have specific installation logic in the component
    # controllers".
    #
    # Under "Generic" the CNE controller looks for the kubeadm-config ConfigMap
    # that only kubeadm-built clusters have, aborts at Reconciled=False, and never
    # creates TMM's internal macvlan NAD (#189) — while every other signal reports
    # healthy. The 3/3 TMM pods that made installs look fine were a side effect of
    # that failure: the controller never got far enough to ask for TMM's volume.
    containerPlatform        = "OCP"
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

# ==============================================================================
# BNK bring-up (terraform-native)
# ------------------------------------------------------------------------------
#   - kubernetes_namespace_v1 / kubernetes_secret_v1 (helm prerequisites)
#   - helm_release (FLO + CIS, wait = true)
#   - kubectl_manifest + wait_for (cert issuers, NADs, SCC bindings,
#     node-labeler).
# ==============================================================================

locals {
  # FLO/CIS chart versions discovered terraform-side from the FAR manifest.
  #
  # The gate is `global_enabled` ALONE, and deliberately says nothing about where FAR
  # came from. The extract (null_resource.extract_flo_version → data.external.versions)
  # runs whenever global_enabled, so gating the RESULT on the source discards versions
  # that were resolved perfectly well.
  #
  # This has now regressed twice. The first gate was `&& var.use_cos_bucket`, which
  # blanked the version for local-FAR-plus-mirror; the fix added
  # `|| var.use_registry_mirror`, which still blanks it for local FAR with NO mirror —
  # a connected cluster installing from local files (issue #50). Enumerating sources is
  # the wrong shape: every new combination is another way to be wrong. The versions do
  # not depend on the source, so the condition must not mention it.
  #
  # An empty version here does not fail cleanly. It interpolates into
  #   ... pull-chart --chart <c> --version  --registry-login repo.f5.com ...
  # where the flag swallows the NEXT token and everything shifts, so the user sees
  # `unknown command "repo.f5.com"` and nothing about a missing version. The
  # preconditions on the pull resources below turn that into a real message.
  flo_chart_version = local.global_enabled ? try(data.external.versions[0].result.flo, "") : ""
  cis_chart_version = local.global_enabled ? try(data.external.versions[0].result.cis, "") : ""

  # NAD (ens3) manifest — spec.config is a JSON string.
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

# ONE NAMESPACE OR TWO (#66).
#
# BNK installs into two namespaces by default. Pointing both settings at the
# same name is a legitimate ask — fewer RBAC surfaces, one thing to grant a team
# — and used to be impossible: two resources declared the same metadata.name,
# so one created it and the other got AlreadyExists.
#
# The guard is on the UTILS resource rather than the FLO one because the FLO
# namespace is the one everything else depends on; making it conditional would
# scatter `try()` through every depends_on. When the names are equal there is
# exactly one namespace and kubernetes_namespace_v1.flo owns it — which is why
# the far/mirror secrets below are guarded the same way and in the same
# direction.
#
# VERIFIED against BNK 2.3, not merely permitted: a full install with both
# namespaces set to f5-bnk produced 30 pods in that namespace and no second
# namespace, on a fresh cluster and on a re-install onto an existing one. The
# open question was FLO's own tolerance — it is handed sharedComponentNamespace
# equal to its main namespace — and it tolerates it.
resource "kubernetes_namespace_v1" "f5_utils" {
  count = local.use_kubectl && var.utils_namespace != var.flo_namespace ? 1 : 0
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

# Same name, same namespace when collapsed — so it is the same secret, already
# created by far_secret_flo above (#66).
resource "kubernetes_secret_v1" "far_secret_utils" {
  count = local.far_secret_kubectl != 0 && var.utils_namespace != var.flo_namespace ? 1 : 0
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

# Collapsed namespaces make this the same object as mirror_secret_flo (#66).
resource "kubernetes_secret_v1" "mirror_secret_utils" {
  count = local.use_kubectl && local.has_mirror_creds && var.utils_namespace != var.flo_namespace ? 1 : 0
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

# SSA, not kubernetes_secret_v1, so a RE-RUN can adopt what a failed run left behind.
#
# kubernetes_secret_v1 does not adopt: if the object exists in the cluster but not in
# terraform state, it fails with `secrets "f5-bigip-ctlr-login" already exists` — and
# it fails the same way on every retry, so a partially-failed apply becomes
# unrecoverable without manual kubectl surgery. That is worse than the original
# failure, and it is what issue #50 hit: the reporter's log shows the identical error
# three times over, once per attempt.
#
# server_side_apply + force_conflicts takes ownership of a pre-existing object
# instead. yaml_body is a sensitive attribute in alekc/kubectl, so the BIG-IP
# password does not appear in plan output — verified against the provider schema, and
# the same pattern the license module already uses for licenseserver-rootca.
#
# NOTE for existing installs: terraform will destroy the kubernetes_secret_v1 and
# create the kubectl_manifest, so the Secret is briefly recreated. CIS re-reads it
# from the mount; there is no restart. The five other kubernetes_secret_v1 resources
# in this module share the non-adopting behaviour and are candidates for the same
# treatment — deliberately left alone here so one bug fix does not churn five live
# credentials at once.
resource "kubectl_manifest" "bigip_ctlr_login" {
  count             = local.use_kubectl ? 1 : 0
  server_side_apply = true
  force_conflicts   = true
  field_manager     = "roksbnkctl"
  yaml_body = yamlencode({
    apiVersion = "v1"
    kind       = "Secret"
    type       = "Opaque"
    metadata = {
      name      = "f5-bigip-ctlr-login"
      namespace = var.flo_namespace
    }
    stringData = {
      username = var.bigip_username
      password = var.bigip_password
      url      = replace(var.bigip_url, "https://", "")
    }
  })
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

# 2.4 does not use macvlan-conf (#171). The guide's NAD list is ens3-ipvlan-l2
# only, and the product creates its own internal NAD as `macvlan-internal` on
# dummy0, owned by the F5Tmm CR. Dropping the name from
# cneinstance_network_attachments is NOT sufficient — the resource has to be
# count-gated off, or the object is orphaned on the cluster and risks a second,
# conflicting internal interface against a name the guide reserves.
resource "kubectl_manifest" "nad_macvlan" {
  count             = local.use_kubectl && local.line_pre_24 ? 1 : 0
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
  lifecycle {
    precondition {
      condition     = local.flo_chart_version != ""
      error_message = "The f5-lifecycle-operator chart version could not be resolved from the FAR manifest, so `helm pull` would run with an empty --version. That does not fail cleanly: the empty value makes the flag swallow the next argument and every later one shifts, which surfaces as `unknown command \"repo.f5.com\"` and says nothing about a version. Check that the manifest downloaded and that extract_flo_version wrote flo-version.txt under the workspace scratch dir."
    }
  }

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
  count = local.use_kubectl && local.line_pre_24 ? 1 : 0
  lifecycle {
    precondition {
      condition     = local.cis_chart_version != ""
      error_message = "The f5-bnk-cis chart version could not be resolved from the FAR manifest, so `helm pull` would run with an empty --version. That does not fail cleanly: the empty value makes the flag swallow the next argument and every later one shifts, which surfaces as `unknown command \"repo.f5.com\"` and says nothing about a version. Check that the manifest downloaded and that extract_flo_version wrote cis-version.txt under the workspace scratch dir."
    }
  }

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

  # Deploy the FLO operator chart WITHOUT blocking on helm-level pod readiness
  # (`--wait=false`). Real
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
# Gate it on the module being enabled at all.
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

resource "helm_release" "cis" {
  count = local.use_kubectl && local.line_pre_24 ? 1 : 0

  name = "f5-bnk-cis"
  # Install from the locally-staged archive (see null_resource.cis_chart_pull) — no
  # provider OCI login, same as helm_release.flo above.
  chart            = local.cis_chart_archive
  namespace        = var.flo_namespace
  create_namespace = false

  # `--wait=false`. CIS readiness is not helm-gated here either.
  wait    = false
  timeout = 300

  values = [yamlencode(local.cis_helm_values)]

  depends_on = [
    helm_release.flo,
    kubectl_manifest.bigip_ctlr_login,
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
  # CIS is integrated into FLO on 2.4, so its SCC bindings go with the chart (#171).
  for_each          = local.use_kubectl && local.line_pre_24 ? { cis = local.scc_clusterrolebinding.cis, cis_default = local.scc_clusterrolebinding.cis_default } : {}
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
  count             = local.use_kubectl && local.line_pre_24 ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_sa_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "node_labeler_role" {
  count             = local.use_kubectl && local.line_pre_24 ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_role_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "node_labeler_binding" {
  count             = local.use_kubectl && local.line_pre_24 ? 1 : 0
  yaml_body         = yamlencode(local.node_labeler_binding_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.node_labeler_role, kubectl_manifest.node_labeler_sa]
}

resource "kubectl_manifest" "node_labeler_job" {
  count             = local.use_kubectl && local.line_pre_24 ? 1 : 0
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

locals {
  # Empty means DERIVE the name FLO actually creates:
  #   f5-cne-controller-<flo_namespace>-f5-cne-controller-serviceaccount
  #
  # That is FLO's construction — <release>-<chart>-serviceaccount, with the
  # namespace baked in — so it is not a static default and cannot be one.
  #
  # It is a MATCHER, not a pointer. The IBM IAM trust relationship evaluates a
  # pod's service-account token against crn/namespace/name, all EQUALS. A name
  # that does not match the account the CNE controller actually runs as makes the
  # profile unassumable, with no error anywhere — the pod just loses its IBM
  # Cloud permissions. A confirmed BNK 2.3 install runs as the long name.
  #
  # Set the variable only if you can also make FLO name the account differently;
  # roksbnkctl cannot, since FLO creates it in response to the CNEInstance and
  # the spec has no service-account field.
  trusted_profile_sa = var.trusted_profile_sa_name != "" ? var.trusted_profile_sa_name : "f5-cne-controller-${var.flo_namespace}-f5-cne-controller-serviceaccount"
}

# WHY MULTIPLE CLUSTERS CAN SHARE ONE SERVICE ACCOUNT NAME.
#
# Trusted profile NAMES are unique per IBM Cloud ACCOUNT, and this one carries
# the cluster name — so two clusters produce two profiles and never collide,
# whatever service account they link.
#
# The LINK is scoped by cluster CRN + namespace + service account name. The CRN
# differs per cluster, so the same SA name in the same namespace on two clusters
# resolves to two distinct links under two distinct profiles. That is what makes
# var.trusted_profile_sa_name safe to default to a short, shared, human-readable
# value instead of one padded with the cluster and namespace to force uniqueness.
#
# So: do NOT "fix" a perceived collision risk by interpolating the cluster name
# into the service account. The SA name has to match the account the CNE
# controller pod actually runs as; it is not a uniqueness knob, and padding it
# breaks the link instead of protecting it.
resource "ibm_iam_trusted_profile" "cne_controller" {
  count       = local.global_enabled ? 1 : 0
  name        = "${var.openshift_cluster_name}-f5-cne-controller-${var.flo_namespace}"
  description = "Trusted profile for F5 CNE controller service account ${local.trusted_profile_sa} in namespace ${var.flo_namespace} on cluster ${var.openshift_cluster_name}"
}

resource "ibm_iam_trusted_profile_link" "cne_controller_roks" {
  count      = local.global_enabled ? 1 : 0
  profile_id = ibm_iam_trusted_profile.cne_controller[0].id
  cr_type    = "ROKS_SA"
  link {
    crn       = var.openshift_cluster_crn
    namespace = var.flo_namespace
    name      = local.trusted_profile_sa
  }
  # The LINK's own name is unique within its profile, not within the account, and
  # each cluster has its own profile — so a fixed name is correct here.
  name = "f5-cne-controller-roks-link"
}

resource "ibm_iam_trusted_profile_policy" "cne_controller_vpc" {
  count  = local.global_enabled ? 1 : 0
  iam_id = ibm_iam_trusted_profile.cne_controller[0].iam_id
  roles  = var.trusted_profile_roles

  resource_attributes {
    name  = "serviceName"
    value = "is"
  }

  resource_attributes {
    name  = "vpcId"
    value = var.cluster_vpc_id
  }
}

# Second access policy: Kubernetes Service, scoped to THIS cluster, Viewer (#166).
#
# The VPC policy above lets the controller write network objects; this one lets it
# read the cluster it is running in. BNK 2.4's CNE controller needs both — applying
# an Infra CR makes it enumerate the cluster's workers to next-hop the routes it
# adds to the custom routing table, and it cannot do that with a VPC policy alone.
#
# Viewer only, deliberately. The controller reads cluster topology; it has no reason
# to mutate the cluster through the IKS API, and the VPC policy already carries the
# write capability it does need. An over-broad profile here would be invisible until
# something used it.
#
# Created on both lines. 2.3's controller does not consult it, so this is inert
# there rather than conditional — a `count` on the line would make the profile's
# shape depend on the manifest version, which is a worse thing to reason about
# during a 2.3 -> 2.4 move than one unused read grant.
resource "ibm_iam_trusted_profile_policy" "cne_controller_cluster" {
  count  = local.global_enabled ? 1 : 0
  iam_id = ibm_iam_trusted_profile.cne_controller[0].iam_id
  roles  = ["Viewer"]

  resource_attributes {
    name  = "serviceName"
    value = "containers-kubernetes"
  }

  # serviceInstance, not clusterId. IAM rejects the latter outright —
  # "Invalid Attribute(s): clusterId", HTTP 400 — and it does so at APPLY time,
  # after the cluster is built, because a policy body is only validated when the
  # API sees it. The VPC policy above scopes with vpcId, which is why the wrong
  # name looked plausible; containers-kubernetes uses the generic
  # serviceInstance slot for the cluster id.
  resource_attributes {
    name  = "serviceInstance"
    value = var.openshift_cluster_id
  }
}
