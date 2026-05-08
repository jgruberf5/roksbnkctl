# Get all resource groups
data "ibm_resource_groups" "all_resource_groups" {}

# Get resource group - use default if not specified
data "ibm_resource_group" "resource_group" {
  name = var.resource_group != "" ? var.resource_group : [
    for rg in data.ibm_resource_groups.all_resource_groups.resource_groups :
    rg.name if rg.is_default == true
  ][0]
}

# Get available zones for the cluster region
data "ibm_is_zones" "regional_zones" {
  region = var.cluster_region
}

# Get available OpenShift versions for the cluster region
data "ibm_container_cluster_versions" "cluster_versions" {}

# Use zones from variable or auto-detect from region
locals {
  zones = length(var.zones) > 0 ? var.zones : data.ibm_is_zones.regional_zones.zones

  available_openshift_versions = data.ibm_container_cluster_versions.cluster_versions.valid_openshift_versions

  # Filter to versions matching the requested major.minor prefix (e.g. "4.18").
  # Falls back to all available versions when openshift_cluster_version is empty,
  # which causes the latest overall version to be selected.
  matching_versions = var.openshift_cluster_version != "" ? [
    for v in local.available_openshift_versions :
    v if startswith(v, var.openshift_cluster_version)
  ] : local.available_openshift_versions

  # Pick the latest patch within the matched major.minor; fall back to overall
  # latest if the requested version prefix matches nothing (e.g. not yet available).
  openshift_version = "${reverse(sort(
    length(local.matching_versions) > 0 ? local.matching_versions : local.available_openshift_versions
  ))[0]}_openshift"

  # VPC references (either created or existing)
  cluster_vpc_id = var.use_existing_cluster_vpc ? (
    var.existing_cluster_vpc_id != "" ? var.existing_cluster_vpc_id : data.ibm_is_vpc.existing_cluster_vpc[0].id
  ) : ibm_is_vpc.cluster_vpc[0].id

  cluster_vpc_crn = var.use_existing_cluster_vpc ? data.ibm_is_vpc.existing_cluster_vpc[0].crn : ibm_is_vpc.cluster_vpc[0].crn

  cluster_vpc_default_sg = var.use_existing_cluster_vpc ? data.ibm_is_vpc.existing_cluster_vpc[0].default_security_group : ibm_is_vpc.cluster_vpc[0].default_security_group

  # Dynamically select worker flavor with minimum vCPUs and RAM
  # Use bx2 series (balanced) as it's most widely available across all regions
  # Supports any user-specified minimum requirements (scales from 2x8 to 128x512)
  # Available bx2 flavors: 2x8, 4x16, 8x32, 16x64, 32x128, 48x192, 64x256, 96x384, 128x512
  eligible_worker_profiles = [
    for profile in data.ibm_is_instance_profiles.cluster_worker_profiles.profiles :
    {
      name   = profile.name
      vcpu   = profile.vcpu_count[0].value
      memory = profile.memory[0].value
    }
    if profile.vcpu_count[0].value >= var.min_worker_vcpu_count &&
    profile.memory[0].value >= var.min_worker_memory_gb &&
    can(regex("^bx2-[0-9]+x[0-9]+$", profile.name))
  ]

  # Sort by vCPU first, then memory to get the smallest eligible flavor
  # Transform dash notation to period notation for OpenShift cluster flavors
  cluster_worker_flavor = var.worker_flavor != "" ? var.worker_flavor : (
    length(local.eligible_worker_profiles) > 0 ?
    replace(
      [
        for p in local.eligible_worker_profiles :
        p.name if p.vcpu == min([for prof in local.eligible_worker_profiles : prof.vcpu]...) &&
        p.memory == min([for prof in local.eligible_worker_profiles : prof.memory if prof.vcpu == min([for pr in local.eligible_worker_profiles : pr.vcpu]...)]...)
      ][0],
      "-", "."
    ) : "bx2.4x16"
  )
}

# Data source to look up existing cluster VPC (if using existing)
data "ibm_is_vpc" "existing_cluster_vpc" {
  count = var.use_existing_cluster_vpc ? 1 : 0
  name  = var.cluster_vpc_name
}

# Create Cluster VPC (only if not using existing)
resource "ibm_is_vpc" "cluster_vpc" {
  count          = var.use_existing_cluster_vpc ? 0 : 1
  name           = var.cluster_vpc_name
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "cluster"]

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Get available instance profiles in cluster region for worker node selection
data "ibm_is_instance_profiles" "cluster_worker_profiles" {
  # Profiles are region-agnostic, but we'll filter based on requirements
}

# ============================================================
# OpenShift Cluster Resources
# ============================================================

# Create subnets for OpenShift cluster in each zone
resource "ibm_is_subnet" "cluster_subnet_zone1" {
  count                    = var.create_cluster ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone1"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[0]
  total_ipv4_address_count = 256
  resource_group           = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet" "cluster_subnet_zone2" {
  count                    = var.create_cluster ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone2"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[1]
  total_ipv4_address_count = 256
  resource_group           = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet" "cluster_subnet_zone3" {
  count                    = var.create_cluster ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone3"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[2]
  total_ipv4_address_count = 256
  resource_group           = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Create public gateways for cluster subnets
resource "ibm_is_public_gateway" "cluster_gateway_zone1" {
  count          = var.create_cluster ? 1 : 0
  name           = "${var.openshift_cluster_name}-gateway-zone1"
  vpc            = local.cluster_vpc_id
  zone           = local.zones[0]
  resource_group = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_public_gateway" "cluster_gateway_zone2" {
  count          = var.create_cluster ? 1 : 0
  name           = "${var.openshift_cluster_name}-gateway-zone2"
  vpc            = local.cluster_vpc_id
  zone           = local.zones[1]
  resource_group = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_public_gateway" "cluster_gateway_zone3" {
  count          = var.create_cluster ? 1 : 0
  name           = "${var.openshift_cluster_name}-gateway-zone3"
  vpc            = local.cluster_vpc_id
  zone           = local.zones[2]
  resource_group = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Attach public gateways to cluster subnets
resource "ibm_is_subnet_public_gateway_attachment" "cluster_subnet_gateway_zone1" {
  count          = var.create_cluster ? 1 : 0
  subnet         = ibm_is_subnet.cluster_subnet_zone1[0].id
  public_gateway = ibm_is_public_gateway.cluster_gateway_zone1[0].id
}

resource "ibm_is_subnet_public_gateway_attachment" "cluster_subnet_gateway_zone2" {
  count          = var.create_cluster ? 1 : 0
  subnet         = ibm_is_subnet.cluster_subnet_zone2[0].id
  public_gateway = ibm_is_public_gateway.cluster_gateway_zone2[0].id
}

resource "ibm_is_subnet_public_gateway_attachment" "cluster_subnet_gateway_zone3" {
  count          = var.create_cluster ? 1 : 0
  subnet         = ibm_is_subnet.cluster_subnet_zone3[0].id
  public_gateway = ibm_is_public_gateway.cluster_gateway_zone3[0].id
}

# Allow TCP port 80 from any source (using cluster security group)
resource "ibm_is_security_group_rule" "cluster_tcp_80" {
  count     = var.create_cluster ? 1 : 0
  group     = local.cluster_security_group
  direction = "inbound"
  remote    = "0.0.0.0/0"
  protocol  = "tcp"
  port_min  = 80
  port_max  = 80

  depends_on = [ibm_container_vpc_cluster.openshift_cluster]
}


# Add inbound rule to cluster VPC default security group to allow all traffic
resource "ibm_is_security_group_rule" "cluster_sg_inbound_all" {
  group     = local.cluster_vpc_default_sg
  direction = "inbound"
  remote    = "0.0.0.0/0"
}

# Create Cloud Object Storage instance for OpenShift registry (Optional)
resource "ibm_resource_instance" "cos_instance" {
  count             = var.create_cluster && var.create_cos_instance ? 1 : 0
  name              = var.cos_instance_name != "" ? var.cos_instance_name : "${var.openshift_cluster_name}-cos"
  service           = "cloud-object-storage"
  plan              = "standard"
  location          = "global"
  resource_group_id = data.ibm_resource_group.resource_group.id
  tags              = ["terraform", "openshift"]

  timeouts {
    create = "30m"
    update = "30m"
    delete = "30m"
  }
}

# Create OpenShift cluster
resource "ibm_container_vpc_cluster" "openshift_cluster" {
  count             = var.create_cluster ? 1 : 0
  name              = var.openshift_cluster_name
  vpc_id            = local.cluster_vpc_id
  flavor            = local.cluster_worker_flavor
  worker_count      = var.workers_per_zone
  kube_version      = local.openshift_version
  resource_group_id = data.ibm_resource_group.resource_group.id
  cos_instance_crn  = var.create_cos_instance ? ibm_resource_instance.cos_instance[0].crn : null

  zones {
    subnet_id = ibm_is_subnet.cluster_subnet_zone1[0].id
    name      = local.zones[0]
  }

  zones {
    subnet_id = ibm_is_subnet.cluster_subnet_zone2[0].id
    name      = local.zones[1]
  }

  zones {
    subnet_id = ibm_is_subnet.cluster_subnet_zone3[0].id
    name      = local.zones[2]
  }

  disable_public_service_endpoint     = false
  disable_outbound_traffic_protection = true

  tags = ["terraform", "openshift"]

  timeouts {
    create = "120m"
    delete = "90m"
  }

  depends_on = [
    ibm_is_subnet.cluster_subnet_zone1,
    ibm_is_subnet.cluster_subnet_zone2,
    ibm_is_subnet.cluster_subnet_zone3,
    ibm_is_subnet_public_gateway_attachment.cluster_subnet_gateway_zone1,
    ibm_is_subnet_public_gateway_attachment.cluster_subnet_gateway_zone2,
    ibm_is_subnet_public_gateway_attachment.cluster_subnet_gateway_zone3
  ]
}

# Look up existing cluster when not creating a new one
data "ibm_container_vpc_cluster" "existing_cluster" {
  count             = var.create_cluster ? 0 : 1
  name              = var.openshift_cluster_name
  resource_group_id = data.ibm_resource_group.resource_group.id
}

# Get worker nodes details
data "ibm_container_vpc_cluster" "cluster_info" {
  count             = var.create_cluster ? 1 : 0
  name              = ibm_container_vpc_cluster.openshift_cluster[0].name
  resource_group_id = data.ibm_resource_group.resource_group.id

  depends_on = [ibm_container_vpc_cluster.openshift_cluster]
}

# Get the cluster security group by name pattern kube-<cluster_id>
data "ibm_is_security_group" "cluster_sg" {
  count = var.create_cluster ? 1 : 0
  name  = "kube-${ibm_container_vpc_cluster.openshift_cluster[0].id}"
}

# Get worker node IPs from cluster workers
data "ibm_container_vpc_cluster_worker" "cluster_workers" {
  count             = var.create_cluster ? 3 : 0
  cluster_name_id   = ibm_container_vpc_cluster.openshift_cluster[0].id
  worker_id         = element(data.ibm_container_vpc_cluster.cluster_info[0].workers, count.index)
  resource_group_id = data.ibm_resource_group.resource_group.id
}

# Map worker nodes to their respective zones
locals {
  # Create a map of zone to worker IP (only when cluster is created)
  zone_worker_map = var.create_cluster && length(data.ibm_container_vpc_cluster_worker.cluster_workers) > 0 ? {
    for worker in data.ibm_container_vpc_cluster_worker.cluster_workers :
    worker.network_interfaces[0].subnet_id => worker.network_interfaces[0].ip_address
  } : {}

  # Get zone-specific worker IPs
  zone1_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? local.zone_worker_map[ibm_is_subnet.cluster_subnet_zone1[0].id] : null
  zone2_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? local.zone_worker_map[ibm_is_subnet.cluster_subnet_zone2[0].id] : null
  zone3_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? local.zone_worker_map[ibm_is_subnet.cluster_subnet_zone3[0].id] : null

  # Get cluster security group from data source
  cluster_security_group = var.create_cluster && length(data.ibm_is_security_group.cluster_sg) > 0 ? data.ibm_is_security_group.cluster_sg[0].id : null
}

# ============================================================
# Transit Gateway
# ============================================================

# Create Transit Gateway with global routing
resource "ibm_tg_gateway" "transit_gateway" {
  count          = var.create_transit_gateway ? 1 : 0
  name           = var.transit_gateway_name
  location       = var.cluster_region
  global         = true
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "transit-gateway"]

  timeouts {
    create = "30m"
    update = "30m"
    delete = "60m"
  }
}

# Connect cluster-vpc to Transit Gateway (only when both transit gateway and cluster are created/used)
resource "ibm_tg_connection" "cluster_vpc_connection" {
  count        = var.create_transit_gateway && var.create_cluster ? 1 : 0
  gateway      = ibm_tg_gateway.transit_gateway[0].id
  network_type = "vpc"
  name         = var.cluster_vpc_name
  network_id   = local.cluster_vpc_crn

  timeouts {
    create = "30m"
    update = "30m"
    delete = "60m"
  }
}

# ============================================================
# Cluster Credentials
# ============================================================

data "ibm_container_cluster_config" "cluster_config" {
  count           = var.create_cluster ? 1 : 0
  cluster_name_id = ibm_container_vpc_cluster.openshift_cluster[0].id
  depends_on      = [ibm_container_vpc_cluster.openshift_cluster]
}

# ============================================================
# Delete Gateway API Admission Policy
# ============================================================
# Removes the OpenShift ingress operator's validating admission policy
# binding that can block Gateway API CRD operations.
# Uses curl against the Kubernetes API for Schematics compatibility
# (kubectl is not available in Schematics runtime).

resource "null_resource" "delete_gatewayapi_admission_policy" {
  count = var.create_cluster ? 1 : 0

  provisioner "local-exec" {
    command = <<-EOT
      curl -sk \
        -X DELETE \
        -H "Authorization: Bearer ${data.ibm_container_cluster_config.cluster_config[0].token}" \
        "${data.ibm_container_cluster_config.cluster_config[0].host}/apis/admissionregistration.k8s.io/v1/validatingadmissionpolicybindings/openshift-ingress-operator-gatewayapi-crd-admission" \
        -o /dev/null -w "%%{http_code}" || true
    EOT
  }

  depends_on = [data.ibm_container_cluster_config.cluster_config]
}
