# ============================================================
# Root Terraform Configuration
# IBM Cloud Testing Jumphosts
# ============================================================

terraform {
  required_version = ">= 1.0"
  required_providers {
    ibm = {
      source  = "IBM-Cloud/ibm"
      version = ">= 1.60.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0.0"
    }
    null = {
      source  = "hashicorp/null"
      version = ">= 3.0.0"
    }
  }
}

# ============================================================
# Locals
# ============================================================

locals {
  # Shared user_data script installed on every jumphost at boot
  jumphost_user_data = <<-EOF
    #!/bin/bash
    set -e

    # Add shared public key to authorized_keys first — before any long-running
    # steps — so Terraform remote-exec provisioners can connect via this key
    # as soon as the SSH daemon is reachable, without waiting for apt-get.
    mkdir -p /home/ubuntu/.ssh /root/.ssh
    chmod 700 /home/ubuntu/.ssh /root/.ssh
    echo "${length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(tls_private_key.jumphost_shared_key[0].public_key_openssh) : ""}" >> /home/ubuntu/.ssh/authorized_keys
    echo "${length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(tls_private_key.jumphost_shared_key[0].public_key_openssh) : ""}" >> /root/.ssh/authorized_keys
    chmod 600 /home/ubuntu/.ssh/authorized_keys /root/.ssh/authorized_keys
    chown ubuntu:ubuntu /home/ubuntu/.ssh /home/ubuntu/.ssh/authorized_keys

    apt-get update
    apt-get upgrade -y
    # nghttp2-client provides h2load — the L7 load generator for
    # `roksbnkctl test matrix` (HTTPRoute, http/https). iperf3 covers the
    # matrix L4 family. Both are preinstalled here so the matrix runs from
    # a jumphost with no --bootstrap apt step.
    apt-get install -y curl wget apt-transport-https ca-certificates gnupg lsb-release software-properties-common iperf3 nghttp2-client dnsutils net-tools netcat-openbsd

    # IBM Cloud CLI
    curl -fsSL https://clis.cloud.ibm.com/install/linux | sh

    # Docker
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
    apt-get update
    apt-get install -y docker-ce docker-ce-cli containerd.io
    systemctl enable docker
    systemctl start docker
    usermod -aG docker ubuntu

    # Helm
    curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash

    # kubectl
    curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
    install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
    rm kubectl

    # OpenShift CLI (oc)
    wget https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz
    tar -xzf openshift-client-linux.tar.gz
    install -o root -g root -m 0755 oc /usr/local/bin/oc
    install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
    rm -f openshift-client-linux.tar.gz oc kubectl

    # IBM Cloud plugins — container-service exposes `ibmcloud oc` for
    # OpenShift commands (no separate openshift plugin in the repo).
    ibmcloud plugin install container-service -f
    ibmcloud plugin install vpc-infrastructure -f

    # ----------------------------------------------------------
    # Kubeconfig provisioning — BOUNDED RETRY + LOUD FAILURE MARKER
    # ----------------------------------------------------------
    # Sprint 14 / get-well (issues/issue_sprint13_architect.md Issue 2,
    # option C part A). The ROKS cluster may not be Ready when the
    # jumphost boots; the previous bare `|| true` swallowed *every*
    # failure (cluster-not-ready, region/RG mismatch, transient IAM)
    # with no retry, no log, no marker — the jumphost came up with no
    # kubeconfig and stayed broken until a human noticed. This block:
    #   - retries the login + `ks cluster config --admin` with backoff,
    #     finite attempts / total timeout (cluster-Ready readiness gate),
    #   - on exhaustion FAILS LOUDLY: a diagnostic line in
    #     /var/log/jumphost-setup.log AND a sentinel file
    #     /var/log/jumphost-kubeconfig-FAILED — visible, not silent,
    #   - never aborts the rest of cloud-init (the rest of setup must
    #     still complete; the loud marker is the fix for the silent
    #     swallow, not a hard abort).
    # NOTE: the script runs under `set -e`; every step here is wrapped
    # so a non-zero attempt does not kill cloud-init mid-boot.
    #
    # Tunables (no terraform variables added — boot-time constants):
    KCFG_MAX_ATTEMPTS=30        # finite attempt cap
    KCFG_SLEEP_SECONDS=20       # backoff between attempts (~10 min total)
    KCFG_LOG=/var/log/jumphost-setup.log
    KCFG_FAIL_MARKER=/var/log/jumphost-kubeconfig-FAILED
    : > "$KCFG_LOG" || true
    rm -f "$KCFG_FAIL_MARKER" || true

    kcfg_log() { echo "[$(date -u '+%Y-%m-%dT%H:%M:%SZ')] $*" >> "$KCFG_LOG" 2>/dev/null || true; }

    mkdir -p /root/.kube

    # root profile: bounded retry of login + `ks cluster config --admin`.
    # Success = /root/.kube/config exists after the admin config call.
    kcfg_attempt=1
    kcfg_ok=0
    while [ "$kcfg_attempt" -le "$KCFG_MAX_ATTEMPTS" ]; do
      kcfg_log "root kubeconfig attempt $kcfg_attempt/$KCFG_MAX_ATTEMPTS"
      if ibmcloud login --apikey "${var.ibmcloud_api_key}" -r "${var.ibmcloud_cluster_region}"${var.ibmcloud_resource_group != "" ? " -g \"${var.ibmcloud_resource_group}\"" : ""} >> "$KCFG_LOG" 2>&1 \
        && ibmcloud ks cluster config --cluster "${var.roks_cluster_name_or_id}" --admin >> "$KCFG_LOG" 2>&1 \
        && [ -f /root/.kube/config ]; then
        kcfg_ok=1
        kcfg_log "root kubeconfig acquired on attempt $kcfg_attempt"
        break
      fi
      kcfg_log "root kubeconfig attempt $kcfg_attempt failed (cluster may not be Ready yet); retrying in $KCFG_SLEEP_SECONDS s"
      kcfg_attempt=$((kcfg_attempt + 1))
      sleep "$KCFG_SLEEP_SECONDS"
    done

    # Also log in as the ubuntu user so roksbnkctl --on jumphost
    # (which SSHes in as ubuntu) sees a configured API endpoint. Without
    # this, ubuntu's ibmcloud profile is unconfigured and any call
    # fails with "No API endpoint set". The login fork also installs
    # the same plugins under ubuntu's profile. An unconfigured ubuntu
    # ibmcloud profile is its own failure mode (it gates the part-B
    # roksbnkctl self-heal, which runs `ibmcloud ks cluster config` as
    # ubuntu), so it gets the same bounded-retry posture.
    kcfg_ubuntu_attempt=1
    kcfg_ubuntu_ok=0
    while [ "$kcfg_ubuntu_attempt" -le "$KCFG_MAX_ATTEMPTS" ]; do
      kcfg_log "ubuntu ibmcloud login attempt $kcfg_ubuntu_attempt/$KCFG_MAX_ATTEMPTS"
      if su - ubuntu -c "ibmcloud login --apikey '${var.ibmcloud_api_key}' -r '${var.ibmcloud_cluster_region}'${var.ibmcloud_resource_group != "" ? " -g '${var.ibmcloud_resource_group}'" : ""}" >> "$KCFG_LOG" 2>&1; then
        kcfg_ubuntu_ok=1
        kcfg_log "ubuntu ibmcloud profile configured on attempt $kcfg_ubuntu_attempt"
        break
      fi
      kcfg_log "ubuntu ibmcloud login attempt $kcfg_ubuntu_attempt failed; retrying in $KCFG_SLEEP_SECONDS s"
      kcfg_ubuntu_attempt=$((kcfg_ubuntu_attempt + 1))
      sleep "$KCFG_SLEEP_SECONDS"
    done
    su - ubuntu -c "ibmcloud config --check-version=false" >> "$KCFG_LOG" 2>&1 || true
    su - ubuntu -c "ibmcloud plugin install container-service -f" >> "$KCFG_LOG" 2>&1 || true
    su - ubuntu -c "ibmcloud plugin install vpc-infrastructure -f" >> "$KCFG_LOG" 2>&1 || true

    if [ -f /root/.kube/config ]; then
      chmod 600 /root/.kube/config
      mkdir -p /home/ubuntu/.kube
      cp /root/.kube/config /home/ubuntu/.kube/config
      chown -R ubuntu:ubuntu /home/ubuntu/.kube
      chmod 600 /home/ubuntu/.kube/config
      kcfg_log "copied /root/.kube/config -> /home/ubuntu/.kube/config (0600 ubuntu:ubuntu)"
    fi

    # Loud failure: if either the root kubeconfig or the ubuntu profile
    # never came up, drop a sentinel + a clear diagnostic. Do NOT exit —
    # the rest of cloud-init (keys, etc.) must still complete; the marker
    # is what makes the failure visible instead of silently swallowed.
    if [ "$kcfg_ok" -ne 1 ] || [ ! -f /home/ubuntu/.kube/config ]; then
      kcfg_log "FAILED: /home/ubuntu/.kube/config not provisioned after $KCFG_MAX_ATTEMPTS attempts. The ROKS cluster (${var.roks_cluster_name_or_id}) was not reachable/Ready during boot, or there is a region/resource-group/IAM mismatch. Re-run: ibmcloud login --apikey <key> -r ${var.ibmcloud_cluster_region}; ibmcloud ks cluster config --cluster ${var.roks_cluster_name_or_id} --admin  — or use roksbnkctl --on <target> kubectl (self-heals)."
      {
        echo "jumphost kubeconfig provisioning FAILED at $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
        echo "cluster=${var.roks_cluster_name_or_id} region=${var.ibmcloud_cluster_region}"
        echo "root kubeconfig ok=$kcfg_ok; ubuntu profile ok=$kcfg_ubuntu_ok"
        echo "see $KCFG_LOG for the per-attempt detail"
      } > "$KCFG_FAIL_MARKER" 2>/dev/null || true
    elif [ "$kcfg_ubuntu_ok" -ne 1 ]; then
      kcfg_log "WARNING: /home/ubuntu/.kube/config provisioned but the ubuntu ibmcloud profile login never succeeded; roksbnkctl --on self-heal may need a fresh login."
      {
        echo "jumphost ubuntu ibmcloud profile NOT configured at $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
        echo "kubeconfig itself is present; self-heal via roksbnkctl --on may require re-login"
      } > "$KCFG_FAIL_MARKER" 2>/dev/null || true
    else
      kcfg_log "kubeconfig provisioning OK"
    fi

    # Write shared private key and public key files (authorized_keys already
    # written at boot-top above).
    echo "${length(tls_private_key.jumphost_shared_key) > 0 ? base64encode(tls_private_key.jumphost_shared_key[0].private_key_openssh) : ""}" | base64 -d > /home/ubuntu/.ssh/id_rsa
    cp /home/ubuntu/.ssh/id_rsa /root/.ssh/id_rsa
    chmod 600 /home/ubuntu/.ssh/id_rsa /root/.ssh/id_rsa
    chown ubuntu:ubuntu /home/ubuntu/.ssh/id_rsa

    echo "${length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(tls_private_key.jumphost_shared_key[0].public_key_openssh) : ""}" > /home/ubuntu/.ssh/id_rsa.pub
    echo "${length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(tls_private_key.jumphost_shared_key[0].public_key_openssh) : ""}" > /root/.ssh/id_rsa.pub
    chmod 644 /home/ubuntu/.ssh/id_rsa.pub /root/.ssh/id_rsa.pub
    chown ubuntu:ubuntu /home/ubuntu/.ssh/id_rsa.pub

    # Append (do NOT truncate) — the kubeconfig retry/marker block above
    # has already written its per-attempt diagnostic to this log.
    echo "Setup completed at $(date)" >> /var/log/jumphost-setup.log
    EOF

  # ----------------------------------------------------------
  # TGW jumphost — VPC resolution and image/profile selection
  # ----------------------------------------------------------

  tgw_vpc_id = var.testing_create_tgw_jumphost ? (
    var.testing_create_client_vpc ? ibm_is_vpc.client_vpc[0].id : data.ibm_is_vpc.existing_client_vpc[0].id
  ) : null

  tgw_vpc_crn = var.testing_create_tgw_jumphost ? (
    var.testing_create_client_vpc ? ibm_is_vpc.client_vpc[0].crn : data.ibm_is_vpc.existing_client_vpc[0].crn
  ) : null

  tgw_vpc_name = var.testing_create_tgw_jumphost ? (
    var.testing_create_client_vpc ? ibm_is_vpc.client_vpc[0].name : data.ibm_is_vpc.existing_client_vpc[0].name
  ) : null

  # Default SG of the newly created client VPC (used only when create_client_vpc = true)
  tgw_new_vpc_default_sg = (var.testing_create_tgw_jumphost && var.testing_create_client_vpc) ? ibm_is_vpc.client_vpc[0].default_security_group : null

  # Source CIDRs for the two testing planes, following the flp_vsi module's
  # split: management access (SSH to a jumphost with a public floating IP) is
  # open by default because the operator connects from wherever they are, while
  # in-fabric test traffic arrives over the Transit Gateway and is private.
  testing_rfc1918       = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"]
  testing_ssh_cidrs     = length(var.testing_jumphost_allowed_cidrs) > 0 ? var.testing_jumphost_allowed_cidrs : ["0.0.0.0/0"]
  testing_client_vpc_in = length(var.testing_client_vpc_inbound_cidrs) > 0 ? var.testing_client_vpc_inbound_cidrs : local.testing_rfc1918

  tgw_jumphost_zone = var.testing_create_tgw_jumphost ? data.ibm_is_zones.vpc_region_zones[0].zones[0] : null

  tgw_ubuntu_images = var.testing_create_tgw_jumphost ? [
    for image in data.ibm_is_images.tgw_ubuntu_images[0].images :
    image if length(regexall("ubuntu-22-04.*minimal.*amd64", lower(image.name))) > 0
  ] : []
  tgw_jumphost_image_id = length(local.tgw_ubuntu_images) > 0 ? local.tgw_ubuntu_images[0].id : null

  # Exclude confidential-computing profiles (the *dc family, e.g. bx3dc): they
  # require a CC-enabled image, so on the stock minimal Ubuntu image the VM
  # boots ("running"/health=ok) but never brings up networking and drops all
  # TCP on every port. The IBM profile API can list one first — in eu-de
  # bx3dc-4x20 is the first >=4-vCPU profile — so without this filter the
  # [0] pick silently produces an unreachable jumphost. sort() makes the
  # remaining choice deterministic across regions instead of API-order-dependent.
  tgw_eligible_profiles = (var.testing_create_tgw_jumphost && var.testing_jumphost_profile == "") ? [
    for profile in data.ibm_is_instance_profiles.tgw_profiles[0].profiles :
    profile if profile.vcpu_count[0].value >= var.testing_min_vcpu_count && profile.memory[0].value >= var.testing_min_memory_gb && length(regexall("dc-[0-9]", profile.name)) == 0
  ] : []
  tgw_jumphost_profile = var.testing_jumphost_profile != "" ? var.testing_jumphost_profile : (
    length(local.tgw_eligible_profiles) > 0 ? sort([for p in local.tgw_eligible_profiles : p.name])[0] : "bx2-4x16"
  )

  # ----------------------------------------------------------
  # Cluster jumphosts — zone set and image/profile selection
  # ----------------------------------------------------------

  cluster_zones = var.testing_create_cluster_jumphosts ? toset(data.ibm_is_zones.cluster_region_zones[0].zones) : toset([])

  cluster_ubuntu_images = var.testing_create_cluster_jumphosts ? [
    for image in data.ibm_is_images.cluster_ubuntu_images[0].images :
    image if length(regexall("ubuntu-22-04.*minimal.*amd64", lower(image.name))) > 0
  ] : []
  cluster_jumphost_image_id = length(local.cluster_ubuntu_images) > 0 ? local.cluster_ubuntu_images[0].id : null

  # Same confidential-computing exclusion + deterministic sort as the TGW
  # jumphost above (this is the path that actually bit test-005: eu-de's first
  # eligible profile was the confidential bx3dc-4x20, giving cluster jumphosts
  # that ran but were unreachable on every port).
  cluster_eligible_profiles = (var.testing_create_cluster_jumphosts && var.testing_jumphost_profile == "") ? [
    for profile in data.ibm_is_instance_profiles.cluster_profiles[0].profiles :
    profile if profile.vcpu_count[0].value >= var.testing_min_vcpu_count && profile.memory[0].value >= var.testing_min_memory_gb && length(regexall("dc-[0-9]", profile.name)) == 0
  ] : []
  cluster_jumphost_profile = var.testing_jumphost_profile != "" ? var.testing_jumphost_profile : (
    length(local.cluster_eligible_profiles) > 0 ? sort([for p in local.cluster_eligible_profiles : p.name])[0] : "bx2-4x16"
  )

  # Map of zone → existing PGW ID for the cluster VPC.
  # IBM Cloud allows only one PGW per zone per VPC; the cluster VPC already
  # has one per zone for its worker nodes, so we reuse them instead of
  # creating new ones (which would exceed the quota).
  # Skip when create_roks_cluster=true: cluster PGWs are provisioned concurrently
  # in the same apply; run a second apply once the cluster is stable to attach.
  cluster_pgw_by_zone = (var.testing_create_cluster_jumphosts && !var.create_roks_cluster) ? {
    for pgw in data.ibm_is_public_gateways.cluster_pgws[0].public_gateways :
    pgw.zone => pgw.id
    if pgw.vpc == var.cluster_vpc_id
  } : {}
}

# ============================================================
# Shared SSH Key Pair
# One RSA key pair is generated per workspace apply and embedded
# in the user_data of every jumphost. Every host therefore
# accepts connections from any other host using this key,
# enabling passwordless SSH across all jumphosts.
#
# Count-gated on the union of the two jumphost toggles
# (Sprint 23, issues/issue_sprint23_staff.md Issue 1): the key is
# only useful when at least one jumphost actually exists. The
# second/bnk-phase override (internal/orchestration/second_phase_reuse.go)
# forces BOTH testing_create_cluster_jumphosts and
# testing_create_tgw_jumphost to false, so this resource flips to
# count=0 in the trial phase — the cluster-shared key is owned by
# the cluster phase only, and the trial phase never re-manages it.
# Pre-Sprint-23: ungated → leaked into trial state on every Split-shape
# `roksbnkctl up`. Outputs and downstream user_data / connection
# references all use `[0]` (safe because they are only evaluated when
# at least one jumphost resource is itself active).
# ============================================================

resource "tls_private_key" "jumphost_shared_key" {
  count     = (var.testing_create_cluster_jumphosts || var.testing_create_tgw_jumphost) ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 4096
}

# ============================================================
# TGW JUMPHOST
# A single jumphost in a client VPC, optionally connected to
# the cluster network via an existing Transit Gateway.
# Provider: ibm.vpc_region (client VPC region)
# ============================================================

# Client VPC (created only when create_client_vpc = true)
resource "ibm_is_vpc" "client_vpc" {
  count          = (var.testing_create_tgw_jumphost && var.testing_create_client_vpc) ? 1 : 0
  provider       = ibm.vpc_region
  name           = var.testing_client_vpc_name
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "testing"]

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Inbound to the client VPC's DEFAULT security group, for in-fabric test traffic.
#
# All protocols and ports, because a test can exercise any of them — but NOT from
# any source. This widens a VPC default SG, which IBM ships denying inbound, so
# every resource later placed in this VPC without an explicit SG inherits it. At
# 0.0.0.0/0 that is a fail-open default waiting for the first VSI to be given a
# floating IP; scoped to the private ranges the Transit Gateway carries, it
# admits the traffic the tests actually generate and nothing from the internet.
resource "ibm_is_security_group_rule" "tgw_vpc_default_sg_inbound_all" {
  for_each = (var.testing_create_tgw_jumphost && var.testing_create_client_vpc) ? toset(local.testing_client_vpc_in) : toset([])

  provider  = ibm.vpc_region
  group     = local.tgw_new_vpc_default_sg
  direction = "inbound"
  remote    = each.value
}

resource "ibm_is_subnet" "tgw_jumphost_subnet" {
  count                    = var.testing_create_tgw_jumphost ? 1 : 0
  provider                 = ibm.vpc_region
  name                     = "${var.testing_tgw_jumphost_name}-subnet"
  vpc                      = local.tgw_vpc_id
  zone                     = local.tgw_jumphost_zone
  total_ipv4_address_count = 256
  resource_group           = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_public_gateway" "tgw_jumphost_gateway" {
  count          = var.testing_create_tgw_jumphost ? 1 : 0
  provider       = ibm.vpc_region
  name           = "${var.testing_tgw_jumphost_name}-gateway"
  vpc            = local.tgw_vpc_id
  zone           = local.tgw_jumphost_zone
  resource_group = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet_public_gateway_attachment" "tgw_jumphost_subnet_gateway" {
  count          = var.testing_create_tgw_jumphost ? 1 : 0
  provider       = ibm.vpc_region
  subnet         = ibm_is_subnet.tgw_jumphost_subnet[0].id
  public_gateway = ibm_is_public_gateway.tgw_jumphost_gateway[0].id
}

resource "ibm_is_security_group" "tgw_jumphost_sg" {
  count          = var.testing_create_tgw_jumphost ? 1 : 0
  provider       = ibm.vpc_region
  name           = "${var.testing_tgw_jumphost_name}-sg"
  vpc            = local.tgw_vpc_id
  resource_group = data.ibm_resource_group.resource_group.id
}

resource "ibm_is_security_group_rule" "tgw_jumphost_ssh_inbound" {
  for_each  = var.testing_create_tgw_jumphost ? toset(local.testing_ssh_cidrs) : toset([])
  provider  = ibm.vpc_region
  group     = ibm_is_security_group.tgw_jumphost_sg[0].id
  direction = "inbound"
  remote    = each.value
  protocol  = "tcp"
  port_min  = 22
  port_max  = 22
}

resource "ibm_is_security_group_rule" "tgw_jumphost_outbound" {
  count     = var.testing_create_tgw_jumphost ? 1 : 0
  provider  = ibm.vpc_region
  group     = ibm_is_security_group.tgw_jumphost_sg[0].id
  direction = "outbound"
  remote    = "0.0.0.0/0"
}

resource "ibm_is_instance" "tgw_jumphost" {
  count          = var.testing_create_tgw_jumphost ? 1 : 0
  provider       = ibm.vpc_region
  name           = var.testing_tgw_jumphost_name
  vpc            = local.tgw_vpc_id
  zone           = local.tgw_jumphost_zone
  profile        = local.tgw_jumphost_profile
  image          = local.tgw_jumphost_image_id
  keys           = var.testing_ssh_key_name != "" ? [data.ibm_is_ssh_key.tgw_ssh_key[0].id] : []
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "testing", "jumphost", "tgw"]

  primary_network_interface {
    subnet          = ibm_is_subnet.tgw_jumphost_subnet[0].id
    security_groups = [ibm_is_security_group.tgw_jumphost_sg[0].id]
  }

  user_data  = local.jumphost_user_data
  depends_on = [ibm_is_subnet_public_gateway_attachment.tgw_jumphost_subnet_gateway]

  timeouts {
    create = "30m"
    update = "30m"
    delete = "30m"
  }
}

resource "ibm_is_floating_ip" "tgw_jumphost_fip" {
  count          = var.testing_create_tgw_jumphost ? 1 : 0
  provider       = ibm.vpc_region
  name           = "${var.testing_tgw_jumphost_name}-fip"
  target         = ibm_is_instance.tgw_jumphost[0].primary_network_interface[0].id
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "testing", "jumphost", "tgw"]

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Connect the client VPC to an existing Transit Gateway
resource "ibm_tg_connection" "tgw_vpc_connection" {
  count        = (var.testing_create_tgw_jumphost && var.testing_transit_gateway_name != "") ? 1 : 0
  gateway      = data.ibm_tg_gateway.transit_gateway[0].id
  network_type = "vpc"
  name         = local.tgw_vpc_name
  network_id   = local.tgw_vpc_crn

  timeouts {
    create = "30m"
    update = "30m"
    delete = "30m"
  }
}

# ============================================================
# CLUSTER JUMPHOSTS
# One jumphost per availability zone, placed directly inside
# the cluster VPC. All share a single security group and SSH key.
# Provider: ibm (default — cluster region)
# ============================================================

# Shared security group for all cluster jumphosts
resource "ibm_is_security_group" "cluster_jumphost_sg" {
  count          = var.testing_create_cluster_jumphosts ? 1 : 0
  name           = "${var.testing_cluster_jumphost_name_prefix}-sg"
  vpc            = var.cluster_vpc_id
  resource_group = data.ibm_resource_group.resource_group.id
}

resource "ibm_is_security_group_rule" "cluster_jumphost_ssh_inbound" {
  for_each  = var.testing_create_cluster_jumphosts ? toset(local.testing_ssh_cidrs) : toset([])
  group     = ibm_is_security_group.cluster_jumphost_sg[0].id
  direction = "inbound"
  remote    = each.value
  protocol  = "tcp"
  port_min  = 22
  port_max  = 22
}

resource "ibm_is_security_group_rule" "cluster_jumphost_outbound" {
  count     = var.testing_create_cluster_jumphosts ? 1 : 0
  group     = ibm_is_security_group.cluster_jumphost_sg[0].id
  direction = "outbound"
  remote    = "0.0.0.0/0"
}

# Per-zone subnet, gateway, instance, and floating IP
resource "ibm_is_subnet" "cluster_jumphost_subnet" {
  for_each                 = local.cluster_zones
  name                     = "${var.testing_cluster_jumphost_name_prefix}-${each.key}-subnet"
  vpc                      = var.cluster_vpc_id
  zone                     = each.key
  total_ipv4_address_count = 256
  resource_group           = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet_public_gateway_attachment" "cluster_jumphost_subnet_gateway" {
  for_each       = local.cluster_pgw_by_zone
  subnet         = ibm_is_subnet.cluster_jumphost_subnet[each.key].id
  public_gateway = each.value
}

resource "ibm_is_instance" "cluster_jumphost" {
  for_each       = local.cluster_zones
  name           = "${var.testing_cluster_jumphost_name_prefix}-${each.key}"
  vpc            = var.cluster_vpc_id
  zone           = each.key
  profile        = local.cluster_jumphost_profile
  image          = local.cluster_jumphost_image_id
  keys           = var.testing_ssh_key_name != "" ? [data.ibm_is_ssh_key.cluster_ssh_key[0].id] : []
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "testing", "jumphost", "cluster"]

  primary_network_interface {
    subnet          = ibm_is_subnet.cluster_jumphost_subnet[each.key].id
    security_groups = [ibm_is_security_group.cluster_jumphost_sg[0].id]
  }

  user_data  = local.jumphost_user_data
  depends_on = [ibm_is_subnet_public_gateway_attachment.cluster_jumphost_subnet_gateway]

  timeouts {
    create = "30m"
    update = "30m"
    delete = "30m"
  }
}

resource "ibm_is_floating_ip" "cluster_jumphost_fip" {
  for_each       = local.cluster_zones
  name           = "${var.testing_cluster_jumphost_name_prefix}-${each.key}-fip"
  target         = ibm_is_instance.cluster_jumphost[each.key].primary_network_interface[0].id
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "testing", "jumphost", "cluster"]

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# ============================================================
# /etc/hosts Entries — Inter-Jumphost Name Resolution
#
# Runs after all floating IPs are assigned. Connects to each
# jumphost via the shared SSH key and writes a fenced block of
# <floating-ip>  <hostname> entries so hosts can reach each
# other by name without a DNS server.
#
# Hostnames assigned:
#   Cluster jumphosts : cluster-<zone>   e.g. cluster-ca-tor-1
#   TGW jumphost      : tgw-jumphost
#
# The sed command removes any previous block before re-writing,
# making the provisioner idempotent on re-apply.
#
# NOTE: requires network access from the Terraform runner to
# each floating IP on port 22. Run terraform apply from a host with
# that reachability (e.g. locally, or a VPN-connected runner).
# ============================================================

resource "null_resource" "cluster_jumphost_hosts" {
  for_each = ibm_is_floating_ip.cluster_jumphost_fip

  triggers = {
    cluster_ips = jsonencode({ for z, f in ibm_is_floating_ip.cluster_jumphost_fip : z => f.address })
    tgw_ip      = try(ibm_is_floating_ip.tgw_jumphost_fip[0].address, "")
  }

  connection {
    type        = "ssh"
    host        = ibm_is_floating_ip.cluster_jumphost_fip[each.key].address
    user        = "ubuntu"
    private_key = tls_private_key.jumphost_shared_key[0].private_key_openssh
    timeout     = "15m"
  }

  provisioner "remote-exec" {
    inline = concat(
      [
        "sudo sed -i '/# BEGIN terraform-jumphosts/,/# END terraform-jumphosts/d' /etc/hosts",
        "printf '# BEGIN terraform-jumphosts\\n' | sudo tee -a /etc/hosts",
      ],
      [for zone, fip in ibm_is_floating_ip.cluster_jumphost_fip :
        "printf '${fip.address}  cluster-${zone}\\n' | sudo tee -a /etc/hosts"
      ],
      # Private primary IPs under a -internal name, so traffic between jumphosts
      # (the perf matrix's client → server path) stays on the private VPC/TGW
      # network instead of hairpinning out through the public floating IPs.
      [for zone, inst in ibm_is_instance.cluster_jumphost :
        "printf '${inst.primary_network_interface[0].primary_ipv4_address}  cluster-${zone}-internal\\n' | sudo tee -a /etc/hosts"
      ],
      var.testing_create_tgw_jumphost ? [
        "printf '${ibm_is_floating_ip.tgw_jumphost_fip[0].address}  tgw-jumphost\\n' | sudo tee -a /etc/hosts",
        "printf '${ibm_is_instance.tgw_jumphost[0].primary_network_interface[0].primary_ipv4_address}  tgw-jumphost-internal\\n' | sudo tee -a /etc/hosts",
      ] : [],
      ["printf '# END terraform-jumphosts\\n' | sudo tee -a /etc/hosts"]
    )
  }
}

resource "null_resource" "tgw_jumphost_hosts" {
  count = var.testing_create_tgw_jumphost ? 1 : 0

  triggers = {
    cluster_ips = jsonencode({ for z, f in ibm_is_floating_ip.cluster_jumphost_fip : z => f.address })
    tgw_ip      = ibm_is_floating_ip.tgw_jumphost_fip[0].address
  }

  connection {
    type        = "ssh"
    host        = ibm_is_floating_ip.tgw_jumphost_fip[0].address
    user        = "ubuntu"
    private_key = tls_private_key.jumphost_shared_key[0].private_key_openssh
    timeout     = "15m"
  }

  provisioner "remote-exec" {
    inline = concat(
      [
        "sudo sed -i '/# BEGIN terraform-jumphosts/,/# END terraform-jumphosts/d' /etc/hosts",
        "printf '# BEGIN terraform-jumphosts\\n' | sudo tee -a /etc/hosts",
        "printf '${ibm_is_floating_ip.tgw_jumphost_fip[0].address}  tgw-jumphost\\n' | sudo tee -a /etc/hosts",
        "printf '${ibm_is_instance.tgw_jumphost[0].primary_network_interface[0].primary_ipv4_address}  tgw-jumphost-internal\\n' | sudo tee -a /etc/hosts",
      ],
      [for zone, fip in ibm_is_floating_ip.cluster_jumphost_fip :
        "printf '${fip.address}  cluster-${zone}\\n' | sudo tee -a /etc/hosts"
      ],
      # Private primary IPs under a -internal name (private VPC/TGW path).
      [for zone, inst in ibm_is_instance.cluster_jumphost :
        "printf '${inst.primary_network_interface[0].primary_ipv4_address}  cluster-${zone}-internal\\n' | sudo tee -a /etc/hosts"
      ],
      ["printf '# END terraform-jumphosts\\n' | sudo tee -a /etc/hosts"]
    )
  }
}
