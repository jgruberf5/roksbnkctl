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

  # ── Cluster VPC addressing ────────────────────────────────────────────────
  # IBM's DEFAULT ("auto") address prefix management gives every VPC in a region
  # the SAME per-zone prefixes. Two roksbnkctl-created cluster VPCs on one Transit
  # Gateway therefore overlap, and the gateway silently blackholes one of them —
  # surfacing as intermittent image-pull timeouts while every SG and ACL allows the
  # traffic. See issue #46.
  #
  # Supplying cluster_vpc_cidr switches this VPC to MANUAL prefixes carved from that
  # block, so a second cluster can be given a different one and share the gateway.
  # Empty keeps "auto" — today's behaviour, and no plan diff for an existing
  # workspace, because moving a live subnet's CIDR would replace it (and with it the
  # cluster).
  # cluster_absent too: these prefixes live inside ibm_is_vpc.cluster_vpc, which
  # is now count = 0 in that case. Without this they would be planned against a
  # VPC that is never created (#76).
  manual_prefixes = var.cluster_vpc_cidr != "" && !var.use_existing_cluster_vpc && !var.cluster_absent

  # Created or adopted — everything downstream uses these two, never the resources.
  cluster_subnet_ids = var.use_existing_cluster_subnets ? var.existing_cluster_subnet_ids : [
    try(ibm_is_subnet.cluster_subnet_zone1[0].id, null),
    try(ibm_is_subnet.cluster_subnet_zone2[0].id, null),
    try(ibm_is_subnet.cluster_subnet_zone3[0].id, null),
  ]
  cluster_subnet_zones = var.use_existing_cluster_subnets ? data.ibm_is_subnet.existing_cluster[*].zone : local.zones

  # /16 → one /18 per zone. The historical default 10.241.0.0/16 reproduces exactly
  # what "auto" assigned (10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18), so opting
  # in on a NEW cluster with the default changes no addresses.
  zone_prefixes = local.manual_prefixes ? [
    for i in range(3) : cidrsubnet(var.cluster_vpc_cidr, 2, i)
  ] : []

  # Each zone's cluster subnet: the first /24 of that zone's prefix — 256 addresses,
  # matching the total_ipv4_address_count used in the auto case.
  zone_subnets = local.manual_prefixes ? [
    for p in local.zone_prefixes : cidrsubnet(p, 6, 0)
  ] : []

  available_openshift_versions = data.ibm_container_cluster_versions.cluster_versions.valid_openshift_versions

  # Filter to versions matching the requested major.minor prefix (e.g. "4.20").
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
  # try(), because the created-VPC branch can now be count = 0 (cluster_absent).
  # Indexing [0] unguarded throws "Invalid index" at plan time the moment that
  # happens — and on the standalone FLP path nothing consumes these anyway.
  cluster_vpc_id = var.use_existing_cluster_vpc ? (
    var.existing_cluster_vpc_id != "" ? var.existing_cluster_vpc_id : data.ibm_is_vpc.existing_cluster_vpc[0].id
  ) : try(ibm_is_vpc.cluster_vpc[0].id, "")

  cluster_vpc_crn = var.use_existing_cluster_vpc ? data.ibm_is_vpc.existing_cluster_vpc[0].crn : try(ibm_is_vpc.cluster_vpc[0].crn, "")

  cluster_vpc_default_sg = var.use_existing_cluster_vpc ? data.ibm_is_vpc.existing_cluster_vpc[0].default_security_group : try(ibm_is_vpc.cluster_vpc[0].default_security_group, "")

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

# Look up the adopted cluster VPC.
#
# Resolve it by ID when one is supplied — `resources.cluster_vpc.existing` is
# documented as (and rendered by roksbnkctl as) a VPC *ID*. Looking it up by NAME
# instead used `cluster_vpc_name`, which is derived from the workspace PREFIX, so it
# is the name of the VPC this workspace WOULD have created — never the name of a VPC
# borrowed from somewhere else. Adopting an existing VPC therefore always failed with
# "No VPC found with name <prefix>-cluster-vpc", even though the correct ID had been
# passed and the ID is what every consumer actually wants.
#
# The by-name lookup is kept for the case where no ID is given (a VPC in this
# workspace's own naming scheme, e.g. one the cluster phase created earlier).
data "ibm_is_vpc" "existing_cluster_vpc" {
  count      = var.use_existing_cluster_vpc ? 1 : 0
  identifier = var.existing_cluster_vpc_id != "" ? var.existing_cluster_vpc_id : null
  name       = var.existing_cluster_vpc_id == "" ? var.cluster_vpc_name : null
}

# Create Cluster VPC (only if not using existing)
resource "ibm_is_vpc" "cluster_vpc" {
  # cluster_absent as well as use_existing_cluster_vpc.
  #
  # use_existing_cluster_vpc = false means CREATE, not "do not adopt" — this is
  # the only resource in the module gated on that flag alone, and the module has
  # no count, so it instantiates in every phase's root including the FLP phase.
  # A standalone FLP VSI building its own VPC (#76) sets cluster_absent, and
  # without this gate it would also create a stray <prefix>-cluster-vpc plus its
  # three address prefixes: quota consumed, nothing using them, and the next
  # `cluster up` in the same workspace failing on a duplicate VPC name.
  count          = var.use_existing_cluster_vpc || var.cluster_absent ? 0 : 1
  name           = var.cluster_vpc_name
  resource_group = data.ibm_resource_group.resource_group.id
  tags           = ["terraform", "cluster"]
  # "auto" (the IBM default) hands every VPC in the region identical prefixes —
  # see local.manual_prefixes. "manual" lets cluster_vpc_cidr place this one.
  address_prefix_management = local.manual_prefixes ? "manual" : "auto"

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# One address prefix per zone, created ONLY in manual mode. In auto mode IBM makes
# these itself and this resource is absent, so an existing workspace sees no change.
resource "ibm_is_vpc_address_prefix" "cluster_zone" {
  count = local.manual_prefixes ? 3 : 0
  name  = "${var.cluster_vpc_name}-zone${count.index + 1}"
  vpc   = ibm_is_vpc.cluster_vpc[0].id
  zone  = local.zones[count.index]
  cidr  = local.zone_prefixes[count.index]
}

# Get available instance profiles in cluster region for worker node selection
data "ibm_is_instance_profiles" "cluster_worker_profiles" {
  # Profiles are region-agnostic, but we'll filter based on requirements
}

# ============================================================
# OpenShift Cluster Resources
# ============================================================

# Create subnets for OpenShift cluster in each zone
# ── Adopted cluster subnets (BYO network) ────────────────────────────────────
# Placing a cluster in a VPC someone else allocated is only half of what "bring
# your own network" means: address space is handed out centrally, and the subnets
# already carry the ACLs and routing that make them acceptable. Creating fresh
# subnets inside that VPC lands the cluster outside all of it.
#
# The zone comes from the SUBNET, not from local.zones — a pre-created subnet lives
# where its owner put it, and the cluster's zone blocks have to agree with that or
# IBM rejects the worker pool.
data "ibm_is_subnet" "existing_cluster" {
  count      = var.use_existing_cluster_subnets ? length(var.existing_cluster_subnet_ids) : 0
  identifier = var.existing_cluster_subnet_ids[count.index]

  lifecycle {
    precondition {
      condition     = var.use_existing_cluster_vpc
      error_message = "existing_cluster_subnet_ids requires use_existing_cluster_vpc: a subnet cannot be adopted independently of the VPC that contains it. Set resources.cluster_vpc = { create: false, existing: <vpc-id> }."
    }
    precondition {
      condition     = length(var.existing_cluster_subnet_ids) == 3
      error_message = "existing_cluster_subnet_ids needs exactly 3 subnet ids, one per zone, in zone order — a ROKS cluster spans three availability zones."
    }
    precondition {
      condition     = length(distinct(var.existing_cluster_subnet_ids)) == length(var.existing_cluster_subnet_ids)
      error_message = "existing_cluster_subnet_ids contains a duplicate — each zone needs its own subnet."
    }
  }
}

resource "ibm_is_subnet" "cluster_subnet_zone1" {
  count                    = var.create_cluster && !var.use_existing_cluster_subnets ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone1"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[0]
  ipv4_cidr_block          = local.manual_prefixes ? local.zone_subnets[0] : null
  total_ipv4_address_count = local.manual_prefixes ? null : 256
  depends_on               = [ibm_is_vpc_address_prefix.cluster_zone]
  resource_group           = data.ibm_resource_group.resource_group.id
  # Attach the zone's public gateway INLINE (not via a separate
  # ibm_is_subnet_public_gateway_attachment). Deleting the subnet then removes the
  # association implicitly — no UnsetSubnetPublicGateway call that fails with "the
  # specified subnet has no public gateway" when a SHARED gateway was already
  # deleted by the VPC-owning cluster. See local.pgw_zone1 (reused or created).
  public_gateway = local.pgw_zone1

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet" "cluster_subnet_zone2" {
  count                    = var.create_cluster && !var.use_existing_cluster_subnets ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone2"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[1]
  ipv4_cidr_block          = local.manual_prefixes ? local.zone_subnets[1] : null
  total_ipv4_address_count = local.manual_prefixes ? null : 256
  depends_on               = [ibm_is_vpc_address_prefix.cluster_zone]
  resource_group           = data.ibm_resource_group.resource_group.id
  public_gateway           = local.pgw_zone2

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

resource "ibm_is_subnet" "cluster_subnet_zone3" {
  count                    = var.create_cluster && !var.use_existing_cluster_subnets ? 1 : 0
  name                     = "${var.openshift_cluster_name}-subnet-zone3"
  vpc                      = local.cluster_vpc_id
  zone                     = local.zones[2]
  ipv4_cidr_block          = local.manual_prefixes ? local.zone_subnets[2] : null
  total_ipv4_address_count = local.manual_prefixes ? null : 256
  depends_on               = [ibm_is_vpc_address_prefix.cluster_zone]
  resource_group           = data.ibm_resource_group.resource_group.id
  public_gateway           = local.pgw_zone3

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# Public gateways for the cluster subnets.
#
# IBM Cloud allows exactly ONE public gateway per zone per VPC. So a SECOND cluster
# brought up in a VPC that already has them (the shared-VPC topology — e.g. an
# air-gapped cluster sharing a VPC with the cluster running the F5 License Proxy)
# must ATTACH to the existing gateways, not create its own. Creating unconditionally
# fails the apply with:
#
#   CreatePublicGatewayWithContext failed: Creating a new public gateway will put the
#   user over quota. Allocated: 1, Requested: 1, Quota: 1
#
# So: look the VPC's gateways up, reuse one when the zone already has it, and create
# only for zones that do not.
#
# Only relevant when ADOPTING an existing VPC. A freshly created VPC cannot already
# have public gateways, and its id (ibm_is_vpc.cluster_vpc[0].id) is unknown until
# apply — feeding that into the gateway `count` below breaks the plan with
# "Invalid count argument". So gate the lookup on use_existing_cluster_vpc: for a new
# VPC the reuse map is empty, the counts resolve at plan time, and we always create.
data "ibm_is_public_gateways" "vpc" {
  count = var.create_cluster && var.use_existing_cluster_vpc ? 1 : 0
}

locals {
  # Security-group source CIDRs. Both default to open, preserving the historical
  # behaviour: :80 is the ingress path and is meant to be publicly reachable,
  # and the VPC default SG governs the cluster's own data path (see the rule).
  cluster_http_cidrs           = length(var.cluster_http_allowed_cidrs) > 0 ? var.cluster_http_allowed_cidrs : ["0.0.0.0/0"]
  cluster_vpc_default_sg_cidrs = length(var.cluster_vpc_default_sg_inbound_cidrs) > 0 ? var.cluster_vpc_default_sg_inbound_cidrs : ["0.0.0.0/0"]

  # zone → id, for public gateways already in THIS cluster's VPC.
  existing_pgw_by_zone = var.use_existing_cluster_vpc ? {
    for g in try(data.ibm_is_public_gateways.vpc[0].public_gateways, []) :
    g.zone => g.id if try(g.vpc, "") == local.cluster_vpc_id
  } : {}

  pgw_existing_zone1 = lookup(local.existing_pgw_by_zone, local.zones[0], "")
  pgw_existing_zone2 = lookup(local.existing_pgw_by_zone, local.zones[1], "")
  pgw_existing_zone3 = lookup(local.existing_pgw_by_zone, local.zones[2], "")

  # The gateway each subnet attaches to: the one already in the zone, else ours.
  # cluster_public_gateway = false → null on every subnet: no worker Internet egress
  # (a private/disconnected cluster; the operator must provide private connectivity —
  # VPEs / private service endpoints — for image pulls and IBM Cloud services).
  pgw_zone1 = var.cluster_public_gateway ? (local.pgw_existing_zone1 != "" ? local.pgw_existing_zone1 : try(ibm_is_public_gateway.cluster_gateway_zone1[0].id, null)) : null
  pgw_zone2 = var.cluster_public_gateway ? (local.pgw_existing_zone2 != "" ? local.pgw_existing_zone2 : try(ibm_is_public_gateway.cluster_gateway_zone2[0].id, null)) : null
  pgw_zone3 = var.cluster_public_gateway ? (local.pgw_existing_zone3 != "" ? local.pgw_existing_zone3 : try(ibm_is_public_gateway.cluster_gateway_zone3[0].id, null)) : null
}

resource "ibm_is_public_gateway" "cluster_gateway_zone1" {
  # Adopted subnets bring their own egress. Creating a gateway here would put an
  # unrequested, billable resource in someone else's VPC attached to NOTHING — our
  # subnets are count=0 on that path, so nothing consumes it — in a network whose
  # owner configured egress deliberately.
  count          = var.create_cluster && !var.use_existing_cluster_subnets && var.cluster_public_gateway && local.pgw_existing_zone1 == "" ? 1 : 0
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
  # Adopted subnets bring their own egress. Creating a gateway here would put an
  # unrequested, billable resource in someone else's VPC attached to NOTHING — our
  # subnets are count=0 on that path, so nothing consumes it — in a network whose
  # owner configured egress deliberately.
  count          = var.create_cluster && !var.use_existing_cluster_subnets && var.cluster_public_gateway && local.pgw_existing_zone2 == "" ? 1 : 0
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
  # Adopted subnets bring their own egress. Creating a gateway here would put an
  # unrequested, billable resource in someone else's VPC attached to NOTHING — our
  # subnets are count=0 on that path, so nothing consumes it — in a network whose
  # owner configured egress deliberately.
  count          = var.create_cluster && !var.use_existing_cluster_subnets && var.cluster_public_gateway && local.pgw_existing_zone3 == "" ? 1 : 0
  name           = "${var.openshift_cluster_name}-gateway-zone3"
  vpc            = local.cluster_vpc_id
  zone           = local.zones[2]
  resource_group = data.ibm_resource_group.resource_group.id

  timeouts {
    create = "30m"
    delete = "30m"
  }
}

# NOTE: subnet↔gateway attachment is now INLINE on each ibm_is_subnet
# (public_gateway = local.pgw_zoneN) rather than a separate
# ibm_is_subnet_public_gateway_attachment. This makes teardown tolerant in the
# shared-VPC topology: deleting the subnet removes the association implicitly, so a
# gateway already deleted by the VPC-owning cluster no longer breaks the adopter's
# destroy with "the specified subnet has no public gateway".

# :80 on the cluster security group — the ingress/ALB path, which is meant to be
# publicly reachable, so the default stays open. cluster_http_allowed_cidrs
# narrows it for a cluster whose ingress serves a known set of sources.
#
# count -> for_each changed these rules' state addresses. For the default (open)
# source the rule itself is unchanged, so rename it in state rather than letting
# terraform destroy-and-recreate it: destroy and create are independent graph
# nodes, and the window between them drops live traffic (:80 ingress here, the
# VPC data path below) — or, ordered the other way, races the IBM VPC API into
# a duplicate-rule failure mid-apply.
moved {
  from = ibm_is_security_group_rule.cluster_tcp_80[0]
  to   = ibm_is_security_group_rule.cluster_tcp_80["0.0.0.0/0"]
}

resource "ibm_is_security_group_rule" "cluster_tcp_80" {
  for_each = var.create_cluster ? toset(local.cluster_http_cidrs) : toset([])

  group     = local.cluster_security_group
  direction = "inbound"
  remote    = each.value
  protocol  = "tcp"
  port_min  = 80
  port_max  = 80

  depends_on = [ibm_container_vpc_cluster.openshift_cluster]
}


# Inbound to the cluster VPC's DEFAULT security group — all protocols, all ports.
#
# Gated on var.create_cluster so the second-phase apply (which forces
# create_cluster=false via the bnk-phase override) does not add a duplicate rule.
# The cluster phase owns this rule; the trial phase has no business managing it.
#
# The source defaults to 0.0.0.0/0, which is the historical behaviour and is
# preserved deliberately: this SG governs the VPC the cluster's own data path
# runs in, and narrowing it by default would change that path on every existing
# deployment without a live-cluster validation behind it. It is worth narrowing —
# IBM ships a VPC default SG denying inbound, so this inverts a safe default for
# every resource later placed in the VPC without an explicit SG — which is what
# cluster_vpc_default_sg_inbound_cidrs is for.
moved {
  from = ibm_is_security_group_rule.cluster_sg_inbound_all[0]
  to   = ibm_is_security_group_rule.cluster_sg_inbound_all["0.0.0.0/0"]
}

resource "ibm_is_security_group_rule" "cluster_sg_inbound_all" {
  for_each = var.create_cluster ? toset(local.cluster_vpc_default_sg_cidrs) : toset([])

  group     = local.cluster_vpc_default_sg
  direction = "inbound"
  remote    = each.value
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
  # The zones come from the adopted subnets, so three subnets in one zone yields
  # three identical zone blocks. IBM rejects that late in the apply with a message
  # about the worker pool, long after the plan looked fine — catch it at plan time.
  lifecycle {
    precondition {
      condition     = !var.use_existing_cluster_subnets || length(distinct(local.cluster_subnet_zones)) == 3
      error_message = "the three subnets in existing_cluster_subnet_ids must be in three DIFFERENT zones — a ROKS cluster spans three availability zones and IBM rejects a worker pool with duplicate zones."
    }
  }

  count             = var.create_cluster ? 1 : 0
  name              = var.openshift_cluster_name
  vpc_id            = local.cluster_vpc_id
  flavor            = local.cluster_worker_flavor
  worker_count      = var.workers_per_zone
  kube_version      = local.openshift_version
  resource_group_id = data.ibm_resource_group.resource_group.id
  cos_instance_crn  = var.create_cos_instance ? ibm_resource_instance.cos_instance[0].crn : null

  zones {
    subnet_id = local.cluster_subnet_ids[0]
    name      = local.cluster_subnet_zones[0]
  }

  zones {
    subnet_id = local.cluster_subnet_ids[1]
    name      = local.cluster_subnet_zones[1]
  }

  zones {
    subnet_id = local.cluster_subnet_ids[2]
    name      = local.cluster_subnet_zones[2]
  }

  disable_public_service_endpoint     = false
  disable_outbound_traffic_protection = true

  tags = ["terraform", "openshift"]

  timeouts {
    create = "120m"
    delete = "90m"
  }

  # The subnets carry their public gateway inline, so depending on the subnets is
  # enough to guarantee egress is wired before the cluster comes up.
  depends_on = [
    ibm_is_subnet.cluster_subnet_zone1,
    ibm_is_subnet.cluster_subnet_zone2,
    ibm_is_subnet.cluster_subnet_zone3
  ]
}

# Look up existing cluster when not creating a new one — but ONLY when a cluster
# name/id was supplied. A cluster-less phase (e.g. a standalone FLP VSI that joins an
# existing VPC without any ROKS cluster) passes create_cluster=false with an empty
# name; skip the lookup then so it doesn't error resolving a cluster named "".
data "ibm_container_vpc_cluster" "existing_cluster" {
  count             = !var.create_cluster && var.openshift_cluster_name != "" && !var.cluster_absent ? 1 : 0
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
  count             = var.create_cluster ? 3 * var.workers_per_zone : 0
  cluster_name_id   = ibm_container_vpc_cluster.openshift_cluster[0].id
  worker_id         = element(data.ibm_container_vpc_cluster.cluster_info[0].workers, count.index)
  resource_group_id = data.ibm_resource_group.resource_group.id
}

# Map worker nodes to their respective zones
locals {
  # Create a map of zone to worker IP (only when cluster is created)
  zone_worker_map = var.create_cluster && length(data.ibm_container_vpc_cluster_worker.cluster_workers) > 0 ? {
    for worker in data.ibm_container_vpc_cluster_worker.cluster_workers :
    worker.network_interfaces[0].subnet_id => worker.network_interfaces[0].ip_address...
  } : {}

  # Get zone-specific worker IPs
  zone1_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? try(local.zone_worker_map[local.cluster_subnet_ids[0]][0], null) : null
  zone2_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? try(local.zone_worker_map[local.cluster_subnet_ids[1]][0], null) : null
  zone3_worker_ip = var.create_cluster && length(local.zone_worker_map) > 0 ? try(local.zone_worker_map[local.cluster_subnet_ids[2]][0], null) : null

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
  config_dir      = var.kubeconfig_dir
  depends_on      = [ibm_container_vpc_cluster.openshift_cluster]
}

# ============================================================
# Delete Gateway API Admission Policy
# ============================================================
# Removes the OpenShift ingress operator's validating admission policy
# binding that can block Gateway API CRD operations.
#
# Converted from a host `curl -X DELETE ... || true` to `roksbnkctl tfx delete`
# (the Windows-native terraform helper): NO interpreter is set, so on Windows
# terraform execs roksbnkctl.exe via `cmd.exe /C` directly — no bash/curl needed.
# The command is flags-only (no pipes, no shell builtins); the kube token is passed
# via the environment (KUBE_TOKEN), never on the command line. --ignore-not-found
# keeps it idempotent, matching the old `|| true`. --insecure matches `curl -sk`.
locals {
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"
}

resource "null_resource" "delete_gatewayapi_admission_policy" {
  count = var.create_cluster ? 1 : 0

  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx delete --kube-host ${data.ibm_container_cluster_config.cluster_config[0].host} --insecure --gvr admissionregistration.k8s.io/v1/validatingadmissionpolicybindings --name openshift-ingress-operator-gatewayapi-crd-admission --ignore-not-found"
    environment = {
      KUBE_TOKEN = data.ibm_container_cluster_config.cluster_config[0].token
    }
  }

  depends_on = [data.ibm_container_cluster_config.cluster_config]
}
