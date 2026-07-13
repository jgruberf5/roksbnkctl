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

  # dockerconfigjson for an external mirror's basic-auth (Harbor robot/admin). The
  # auth key is the bare registry host (kubelet matches image refs by host prefix),
  # stripped of the repo-prefix path segment far_image_hostname may carry.
  mirror_registry_host = split("/", local.far_image_hostname)[0]
  mirror_auth_value    = base64encode("${var.registry_mirror_username}:${var.registry_mirror_password}")
  mirror_docker_config_json = replace(
    jsonencode({
      auths = { (local.mirror_registry_host) = { auth = local.mirror_auth_value } }
    }),
    ":", ": "
  )

  kube_token = try(data.ibm_container_cluster_config.cluster_config[0].token, "")

  # Chart-pull auth for the in-process helm provider (copied from the FLO module):
  # FAR creds off the mirror, iamapikey for an ICR mirror, the cluster token for
  # the in-cluster registry route.
  is_icr_mirror = var.use_registry_mirror && can(regex("(^|[.])icr[.]io(/|$)", local.far_chart_hostname))

  # An EXTERNAL registry mirror (e.g. Harbor) authenticates with its own basic-auth
  # credentials, not the kube token (which only the in-cluster OpenShift registry
  # accepts) or an IAM key (ICR). Detected by registry_mirror_password being set.
  has_mirror_creds = var.use_registry_mirror && !local.is_icr_mirror && var.registry_mirror_password != ""

  chart_pull_username = (
    !var.use_registry_mirror ? "_json_key_base64" :
    local.is_icr_mirror ? "iamapikey" :
    local.has_mirror_creds ? var.registry_mirror_username : "unused"
  )
  chart_pull_password = (
    !var.use_registry_mirror ? local.far_service_account_b64 :
    local.is_icr_mirror ? var.ibmcloud_api_key :
    local.has_mirror_creds ? var.registry_mirror_password : local.kube_token
  )

  # Off the mirror the pods pull via the FAR dockerconfig secret. On an in-cluster/
  # ICR mirror pulls go through RBAC, so the secret is dropped (imagePullSecrets []).
  # On an EXTERNAL mirror with credentials the pods need a dockerconfig secret built
  # from those credentials — created below as `flp-mirror-pull` when has_mirror_creds.
  flp_image_pull_secret = (
    !var.use_registry_mirror ? "far-secret" :
    local.has_mirror_creds ? "flp-mirror-pull" : ""
  )

  # `helm registry login` takes a bare registry HOST; far_chart_hostname carries the
  # mirror's repo-prefix path (host/bnk-mirror), so strip it or the credential lands
  # under a host the subsequent `helm pull` never resolves.
  chart_login_host = split("/", local.far_chart_hostname)[0]

  # An explicit pin wins; otherwise take the version the BNK manifest lists for
  # charts/f5-license-proxy (resolved by extract_flp_version below).
  flp_chart_version = (
    var.flp_chart_version != "" ? var.flp_chart_version :
    try(data.external.flp_version[0].result.version, "")
  )

  # mTLS leaf certs: name → SAN DNS list. postgresql/vault keep the chart's
  # in-pod SANs. The flp leaf is ALSO the FLP's public server cert, so it must
  # additionally cover the Service DNS the CWC connects to (the teem*Url) — else
  # the CWC rejects it with "bad certificate" on hostname mismatch.
  flp_svc = "f5-license-proxy.${var.flp_namespace}.svc"
  leaves = {
    postgresql = ["postgresql", "localhost"]
    vault      = ["vault", "localhost", "vault-postgresql-service"]
    flp        = ["flp", "localhost", "f5-license-proxy", local.flp_svc, "${local.flp_svc}.cluster.local"]
  }

  # A remote cluster's CWC dials the proxy at https://<worker-node-ip>:<nodePort>,
  # so that IP must be an IP SAN on the proxy's server cert. DNS SANs do not help —
  # the connection is made to a literal address. Without these the handshake fails
  # exactly as it did before the Service DNS SAN was added: "bad certificate".
  #
  # Worker InternalIPs are the routable VPC addresses. They CHANGE when a worker is
  # replaced, which re-issues the cert on the next apply — acceptable for a NodePort
  # topology, and the reason a stable VIP is preferable long term.
  node_ips = var.flp_node_port_access ? sort(distinct(flatten([
    for n in try(data.kubernetes_nodes.workers[0].nodes, []) : [
      for a in n.status[0].addresses : a.address if a.type == "InternalIP"
    ]
  ]))) : []

  # leaf → IP SANs (only the flp leaf is a public server cert).
  leaf_ips = {
    postgresql = []
    vault      = []
    flp        = local.node_ips
  }

  # leaf → the Secret name the chart mounts it from.
  leaf_secret = {
    postgresql = "postgresql-mtls-secret"
    vault      = "vault-ssl-secret"
    flp        = "flp-mtls-secret"
  }

  # The chart's Service is already type NodePort on this port.
  flp_node_port = 30001

  # Helm values: point every component's image.repository at the resolved host
  # prefix and set the (conditional) pull secret. Everything else stays at the
  # chart defaults (isProxy* forward-proxy flags off — direct egress).
  flp_helm_values = {
    vaultInit = { image = { repository = local.image_repository } }
    vault     = { image = { repository = local.image_repository } }
    postgresql = {
      image = { repository = local.image_repository }
      # Mount the (now dynamic) data PVC where the bitnami image actually writes
      # (/bitnami/postgresql), not the chart's default /var/lib/postgresql — the
      # bitnami entrypoint initialises /bitnami/postgresql/data.
      DataDir = "/bitnami/postgresql"
    }
    flp = {
      image            = { repository = local.image_repository }
      imagePullSecrets = local.flp_image_pull_secret
    }
  }
}

# Post-renderer: the f5-license-proxy chart ships three hostPath PersistentVolumes
# + label-selected PVCs (a single-node/dev model). On ROKS the hostPath dirs are
# root-owned and never chowned to the non-root container UIDs, and the PVs go
# Released on teardown and block re-install. This script (run by helm on the
# rendered manifests) drops the hostPath PVs and rewrites the PVCs to dynamically
# provision from var.flp_storage_class — the CSI driver then chowns each volume to
# fsGroup, so postgres/vault can write. The storage class is baked in so no
# post-render args are needed (older helm providers don't pass them).
resource "local_file" "postrender" {
  count           = local.enabled ? 1 : 0
  filename        = "${var.scratch_dir}/flp-postrender.py"
  file_permission = "0755"
  content         = <<-PY
    #!/usr/bin/env python3
    import sys, re
    SC = "${var.flp_storage_class}"
    # When the proxy must be reachable from OUTSIDE this cluster, the chart's
    # Service is unusable as shipped: it hardcodes externalTrafficPolicy: Local
    # and the deployment has replicas: 1, so ONLY the node currently running the
    # pod answers on the NodePort — every other node refuses, and which node that
    # is changes whenever the pod reschedules. Cluster policy makes every node
    # forward to the pod, so any worker IP is a valid endpoint. (Local exists to
    # preserve the client source IP; licensing does not care.)
    EXTERNAL = ${var.flp_node_port_access ? "True" : "False"}
    docs = sys.stdin.read().split("\n---\n")
    out = []
    for d in docs:
        if re.search(r"^[ \t]*kind:[ \t]*PersistentVolume[ \t]*$", d, re.M) and "hostPath:" in d:
            continue  # drop the chart's hostPath PVs
        if re.search(r"^[ \t]*kind:[ \t]*PersistentVolumeClaim[ \t]*$", d, re.M):
            d = re.sub(r"(storageClassName:[ \t]*).*", r"\g<1>" + SC, d)
            d = re.sub(r"\n[ \t]*selector:[ \t]*\n[ \t]*matchLabels:[ \t]*\n[ \t]*volumeType:[^\n]*", "", d)
        if EXTERNAL and re.search(r"^[ \t]*kind:[ \t]*Service[ \t]*$", d, re.M):
            d = re.sub(r"(externalTrafficPolicy:[ \t]*)Local", r"\g<1>Cluster", d)
        out.append(d)
    sys.stdout.write("\n---\n".join(out))
  PY
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

# Worker node IPs for the proxy's cert SANs — only needed when the proxy is
# exposed outside its cluster.
data "kubernetes_nodes" "workers" {
  count = local.enabled && var.flp_node_port_access ? 1 : 0
}

# ── opening the NodePort to the consuming cluster ─────────────────────────────
# ROKS puts its workers in a security group named kube-<cluster-id>, which does
# NOT admit traffic from another cluster by default — so even with the Service
# exposed, a CWC in a peer cluster is dropped at the SG. Open just the proxy's
# NodePort, and only to the CIDR the operator names.
locals {
  open_node_port = local.enabled && var.flp_node_port_access && var.flp_node_port_source_cidr != ""
}

data "ibm_container_vpc_cluster" "cluster" {
  count = local.open_node_port ? 1 : 0
  name  = var.roks_cluster_name_or_id
}

data "ibm_is_security_group" "cluster_workers" {
  count = local.open_node_port ? 1 : 0
  name  = "kube-${data.ibm_container_vpc_cluster.cluster[0].id}"
}

resource "ibm_is_security_group_rule" "flp_node_port" {
  count     = local.open_node_port ? 1 : 0
  group     = data.ibm_is_security_group.cluster_workers[0].id
  direction = "inbound"
  remote    = var.flp_node_port_source_cidr
  protocol  = "tcp"
  port_min  = local.flp_node_port
  port_max  = local.flp_node_port
}

resource "tls_cert_request" "leaf" {
  for_each        = local.enabled ? local.leaves : {}
  private_key_pem = tls_private_key.leaf[each.key].private_key_pem
  dns_names       = each.value
  ip_addresses    = lookup(local.leaf_ips, each.key, [])
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

# External-mirror image pull secret — the pods pull FLP images from an external
# registry (Harbor) that needs basic auth. Created only when credentials are
# supplied for the mirror; referenced by flp_image_pull_secret above.
resource "kubernetes_secret_v1" "mirror_pull" {
  count = local.enabled && local.has_mirror_creds ? 1 : 0
  metadata {
    name      = "flp-mirror-pull"
    namespace = var.flp_namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.mirror_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.flp]
}

# ── chart version ─────────────────────────────────────────────────────────────
# The BNK manifest lists charts/f5-license-proxy for the release, exactly as it
# lists the FLO and CIS charts — so resolve the FLP chart version from it rather
# than making the user pin one. (An OCI `helm pull` cannot resolve "latest", so an
# empty version is not a usable default; before this it was a hard error.)
# var.flp_chart_version, when set, overrides the manifest.
resource "null_resource" "extract_flp_version" {
  count = local.enabled && var.flp_chart_version == "" ? 1 : 0

  triggers = {
    manifest_version = var.f5_bigip_k8s_manifest_version
    scratch_dir      = var.scratch_dir
  }

  provisioner "local-exec" {
    command = <<-EOT
      set -e
      # helm >= 3.8 is required for `helm registry` (OCI).
      HELM_MIN="3.8.0"
      HELM_BIN="helm"
      helm_ok() {
        local v
        v=$(helm version --short 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1) || return 1
        printf '%s\n%s\n' "$HELM_MIN" "$v" | sort -V -c 2>/dev/null
      }
      if ! helm_ok; then
        HELM_TMP=$(mktemp -d "$${TMPDIR:-/tmp}/helm-install-XXXXXX")
        curl -fsSL -o "$HELM_TMP/helm.tar.gz" "https://get.helm.sh/helm-v3.17.2-linux-amd64.tar.gz"
        tar -xzf "$HELM_TMP/helm.tar.gz" -C "$HELM_TMP"
        HELM_BIN="$HELM_TMP/linux-amd64/helm"
      fi
      mkdir -p "${var.scratch_dir}/f5-manifest"
      cd "${var.scratch_dir}/f5-manifest"
      # Same chart host + credential as the FLP chart itself: FAR off the mirror,
      # the mirror under it (the manifest is a mirrored artifact — see bnkbom).
      echo "${local.chart_pull_password}" | $HELM_BIN registry login -u "${local.chart_pull_username}" --password-stdin ${local.chart_login_host}
      $HELM_BIN pull oci://${local.far_chart_hostname}/release/f5-bigip-k8s-manifest --version "${var.f5_bigip_k8s_manifest_version}" -d .
      tar -xzf f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.tgz
      V=$(grep -A 1 "charts/f5-license-proxy" f5-bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}/bigip-k8s-manifest-${var.f5_bigip_k8s_manifest_version}.yaml \
            | grep "version:" | awk '{print $2}' | tr -d '"' | tr -d "'")
      if [ -z "$V" ]; then
        echo "ERROR: the BNK manifest ${var.f5_bigip_k8s_manifest_version} lists no charts/f5-license-proxy — pin one with bnk.flp.chart_version" >&2
        exit 1
      fi
      printf '%s' "$V" > "${var.scratch_dir}/flp-version.txt"
    EOT
  }
}

data "external" "flp_version" {
  count = local.enabled && var.flp_chart_version == "" ? 1 : 0
  program = [
    "bash", "-c",
    "V=$(cat ${var.scratch_dir}/flp-version.txt 2>/dev/null | tr -d '[:space:]'); printf '{\"version\":\"%s\"}' \"$V\"",
  ]
  depends_on = [null_resource.extract_flp_version]
}

# ── the FLP chart ─────────────────────────────────────────────────────────────

resource "helm_release" "flp" {
  count = local.enabled ? 1 : 0

  name             = "f5-license-proxy"
  repository       = "oci://${local.far_chart_hostname}/charts"
  chart            = "f5-license-proxy"
  version          = local.flp_chart_version
  namespace        = var.flp_namespace
  create_namespace = false

  repository_username = local.chart_pull_username
  repository_password = local.chart_pull_password

  # FLP readiness (vault unseal → postgres → proxy) IS the meaningful signal here,
  # so block on it — unlike the FLO operator whose readiness is gated downstream.
  wait    = true
  timeout = 600

  values = [yamlencode(local.flp_helm_values)]

  # Rewrite the chart's hostPath storage to dynamic ROKS block storage.
  postrender {
    binary_path = local_file.postrender[0].filename
  }

  depends_on = [
    kubernetes_namespace_v1.flp,
    kubernetes_secret_v1.mtls,
    kubernetes_secret_v1.flp_jwt,
    kubernetes_secret_v1.far_secret,
    kubernetes_secret_v1.mirror_pull,
    kubernetes_role_binding_v1.flp_scc,
    local_file.postrender,
  ]
}
