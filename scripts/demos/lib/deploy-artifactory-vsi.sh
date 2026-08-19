#!/usr/bin/env bash
#
# deploy-artifactory-vsi.sh — stand up a self-hosted JFrog Artifactory on ONE
# IBM Cloud VSI, to act as the mirror target for the FAR-replication demo.
#
#   ./deploy-artifactory-vsi.sh              # create VPC + VSI + Artifactory + TLS
#   ./deploy-artifactory-vsi.sh --destroy    # remove everything it created
#
# WHY JCR (JFrog Container Registry) AND NOT ARTIFACTORY OSS
#
# Docker repository support is a LICENSED Artifactory feature. An OSS instance
# has no Docker repository type at all, so it cannot be a BNK mirror — there is
# nothing to point at. JCR is JFrog's free edition built specifically around
# Docker and Helm repositories, which is exactly the shape a FAR mirror needs,
# and it needs no licence key. Set ARTIFACTORY_IMAGE to the -pro image if you
# have a licence and want the full product; everything downstream is identical,
# because roksbnkctl only ever speaks the OCI registry API to it.
#
# WHAT THIS DELIBERATELY DOES NOT DO
#
# It does not create the Docker repository, and it does not set the admin
# password. Both are first-login UI steps in Artifactory, and the customer guide
# walks through them on purpose: they are what a real deployment does by hand,
# and hiding them behind a script would misrepresent the work involved. The
# script's job ends at "Artifactory is reachable over HTTPS".
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
STATE_FILE="${STATE_FILE:-$SCRIPT_DIR/../.bootstrap-state/artifactory.env}"

: "${IBMCLOUD_API_KEY:?set IBMCLOUD_API_KEY}"
: "${ART_DOMAIN:?set ART_DOMAIN — the public DNS name Caddy gets a certificate for}"

# ART_* wins, then the demo's own REGION/RESOURCE_GROUP, then the default. The
# guide has the reader put REGION and RESOURCE_GROUP in .env; reading only the
# ART_* names would silently ignore what they set and deploy somewhere else.
REGION="${ART_REGION:-${REGION:-us-east}}"
ZONE="${ART_ZONE:-${REGION}-1}"
# Lowercase "default" — the IBM Cloud account default, and what every other demo
# here uses. Names are CASE-SENSITIVE, so the capitalised "Default" this script
# shipped with matched nothing and failed with "Could not get resource group".
# Override with ART_RESOURCE_GROUP.
RG="${ART_RESOURCE_GROUP:-${RESOURCE_GROUP:-default}}"
# Artifactory wants real memory; 4x16 is the smallest profile it runs happily on.
# A 2x8 boots and then thrashes under the first replicate, which looks like a
# network fault and is not one.
PROFILE="${ART_PROFILE:-bx2-4x16}"
P="${ART_PREFIX:-bnk-artifactory}"
ARTIFACTORY_IMAGE="${ARTIFACTORY_IMAGE:-releases-docker.jfrog.io/jfrog/artifactory-jcr:latest}"
CADDY_IMAGE="${ART_CADDY_IMAGE:-caddy:2}"
# 10.246/16 keeps this clear of the services VPC (10.241), forge (10.242) and
# the demo clusters (10.243). Overlapping prefixes on a shared Transit Gateway
# blackhole traffic silently, so the separation is not cosmetic — even though
# this demo attaches to no gateway today.
VPC_CIDR="${ART_VPC_CIDR:-10.246.0.0/18}"

VPC_NAME="$P-vpc"; SUBNET_NAME="$P-subnet"; PGW_NAME="$P-pgw"
SG_NAME="$P-sg";  VSI_NAME="$P-vsi";        KEY_NAME="${ART_SSH_KEY_NAME:-bnk-svc-key}"

say(){ printf '==> %s\n' "$*" >&2; }
die(){ printf '✗ %s\n' "$*" >&2; exit 1; }

ic(){ ibmcloud "$@"; }

# icjson — run an ibmcloud command expecting JSON, and FAIL LOUDLY when it is not.
#
# `ibmcloud ... --output json | jq ...` hides the real problem: ibmcloud writes
# usage errors and API errors to STDOUT as plain text, so jq is handed
# "Error code: not_found" and reports `Invalid numeric literal at line 1,
# column 10` — column 10 being the width of "Error code". The actual message is
# never shown. Every JSON call goes through here so the operator sees what IBM
# Cloud actually said.
icjson(){
  local out rc=0
  out="$(ibmcloud "$@" --output json 2>&1)" || rc=$?
  if [[ $rc -ne 0 ]] || ! jq -e . >/dev/null 2>&1 <<<"$out"; then
    printf '✗ ibmcloud %s failed:\n' "$1 $2" >&2
    printf '%s\n' "$out" | head -20 >&2
    exit 1
  fi
  printf '%s' "$out"
}

login(){
  # Log in WITHOUT a resource group first: -g fails the whole login when the
  # group does not exist, which is what made a wrong name look like an auth
  # problem. The group is targeted separately below, where it can be reported.
  ic login --apikey "$IBMCLOUD_API_KEY" -r "$REGION" >/dev/null \
    || die "ibmcloud login failed — check IBMCLOUD_API_KEY and that $REGION is a valid region"

  if ! ic target -r "$REGION" -g "$RG" >/dev/null 2>&1; then
    printf '✗ resource group %s not found in this account.\n\n' "$RG" >&2
    printf '  Set ART_RESOURCE_GROUP to one of:\n' >&2
    ic resource groups --output json 2>/dev/null \
      | jq -r 'if type=="array" then (.[]|"    " + .name + (if (.default==true or .default=="true") then "   (account default)" else "" end)) else empty end' >&2
    exit 1
  fi
}

destroy(){
  login
  say "destroying $P-* in $REGION"
  # Reverse dependency order. Every delete tolerates absence: a partial create
  # is the normal reason to be running this, so a missing resource is success.
  local fip vsi sub pgw sg vpc
  fip=$(ic is floating-ips --output json 2>/dev/null | jq -r --arg n "$P-fip" '.[]|select(.name==$n)|.id' | head -1)
  [[ -n "${fip:-}" ]] && ic is floating-ip-release "$fip" -f >/dev/null 2>&1 || true
  vsi=$(ic is instances --output json 2>/dev/null | jq -r --arg n "$VSI_NAME" '.[]|select(.name==$n)|.id' | head -1)
  if [[ -n "${vsi:-}" ]]; then
    ic is instance-delete "$vsi" -f >/dev/null 2>&1 || true
    say "waiting for the VSI to go away before its subnet"
    for _ in $(seq 1 60); do
      ic is instance "$vsi" >/dev/null 2>&1 || break
      sleep 5
    done
  fi
  sub=$(ic is subnets --output json 2>/dev/null | jq -r --arg n "$SUBNET_NAME" '.[]|select(.name==$n)|.id' | head -1)
  pgw=$(ic is public-gateways --output json 2>/dev/null | jq -r --arg n "$PGW_NAME" '.[]|select(.name==$n)|.id' | head -1)
  [[ -n "${sub:-}" ]] && ic is subnet-delete "$sub" -f >/dev/null 2>&1 || true
  [[ -n "${pgw:-}" ]] && ic is public-gateway-delete "$pgw" -f >/dev/null 2>&1 || true
  sg=$(ic is security-groups --output json 2>/dev/null | jq -r --arg n "$SG_NAME" '.[]|select(.name==$n)|.id' | head -1)
  [[ -n "${sg:-}" ]] && ic is security-group-delete "$sg" -f >/dev/null 2>&1 || true
  vpc=$(ic is vpcs --output json 2>/dev/null | jq -r --arg n "$VPC_NAME" '.[]|select(.name==$n)|.id' | head -1)
  [[ -n "${vpc:-}" ]] && ic is vpc-delete "$vpc" -f >/dev/null 2>&1 || true
  rm -f "$STATE_FILE"
  say "destroyed"
}

# BEFORE the --destroy dispatch, not after. destroy() resolves every resource id
# through jq, so without it each lookup yields an empty string, every delete is
# skipped, and the script reports "destroyed" with everything still running and
# still billable. A teardown that lies is worse than one that refuses.
command -v jq >/dev/null || die "jq is required"

[[ "${1:-}" == "--destroy" ]] && { destroy; exit 0; }

login

say "VPC $VPC_NAME"
VPC_ID=$(icjson is vpcs | jq -r --arg n "$VPC_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$VPC_ID" ]]; then
  VPC_ID=$(icjson is vpc-create "$VPC_NAME" | jq -r .id)
  ic is vpc-address-prefix-create "$P-prefix" "$VPC_ID" "$ZONE" "$VPC_CIDR" >/dev/null
fi

say "subnet + public gateway"
SUBNET_ID=$(icjson is subnets | jq -r --arg n "$SUBNET_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$SUBNET_ID" ]]; then
  SUBNET_ID=$(icjson is subnet-create "$SUBNET_NAME" "$VPC_ID" --zone "$ZONE" --ipv4-cidr-block "$VPC_CIDR" | jq -r .id)
fi
PGW_ID=$(icjson is public-gateways | jq -r --arg n "$PGW_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$PGW_ID" ]]; then
  PGW_ID=$(icjson is public-gateway-create "$PGW_NAME" "$VPC_ID" "$ZONE" | jq -r .id)
fi
ic is subnet-update "$SUBNET_ID" --public-gateway-id "$PGW_ID" >/dev/null 2>&1 || true

# 443 and 80 inbound: 80 is not decorative — Caddy needs it for the ACME HTTP-01
# challenge, and without it the certificate never issues and every push fails
# with a TLS error that looks like a registry problem.
say "security group (22, 80, 443)"
SG_ID=$(icjson is security-groups | jq -r --arg n "$SG_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$SG_ID" ]]; then
  SG_ID=$(icjson is security-group-create "$SG_NAME" "$VPC_ID" | jq -r .id)
  for port in 22 80 443; do
    ic is security-group-rule-add "$SG_ID" inbound tcp --port-min "$port" --port-max "$port" --remote 0.0.0.0/0 >/dev/null
  done
  ic is security-group-rule-add "$SG_ID" outbound icmp_tcp_udp --remote 0.0.0.0/0 >/dev/null
fi

# ARCHITECTURE, not just the OS name. IBM Cloud publishes the same Ubuntu
# release for amd64 and s390x (IBM Z), and "newest ubuntu-24-04" can select the
# s390x one — which instance-create then rejects with "Image OS architecture
# s390x is not supported by the instance profile bx2-4x16". Both other demo
# scripts filter on amd64; this one had not.
IMAGE_ID=$(icjson is images --visibility public \
  | jq -r '[.[]|select(.status=="available")
              |select(.operating_system.architecture=="amd64")
              |select(.operating_system.name|test("ubuntu-24-04"))]
           |sort_by(.created_at)|last|.id')
[[ -n "$IMAGE_ID" && "$IMAGE_ID" != "null" ]] || die "no Ubuntu 24.04 stock image found in $REGION"

# Artifactory needs three secrets before it will start, and none of them are
# self-generated in a plain docker-compose deployment. Created here so they are
# baked into cloud-init and the FIRST boot succeeds.
ART_DB_PASSWORD="${ART_DB_PASSWORD:-$(openssl rand -hex 16)}"
ART_MASTER_KEY="${ART_MASTER_KEY:-$(openssl rand -hex 32)}"
ART_JOIN_KEY="${ART_JOIN_KEY:-$(openssl rand -hex 32)}"

CLOUD_INIT=$(mktemp)
cat >"$CLOUD_INIT" <<CIEOF
#cloud-config
package_update: true
write_files:
  - path: /opt/artifactory/docker-compose.yml
    permissions: '0600'
    content: |
      # POSTGRESQL IS MANDATORY. Artifactory's Access service refuses to start
      # against the embedded Derby database -- "DB Type derby is not allowed:
      # Cannot start the application with a database other than PostgreSQL" --
      # so a single-container deployment can never work, however long you wait.
      # The failure surfaces late and indirectly: Artifactory's own API answers
      # on 8081 while Access never binds 8046, and every other service sits in a
      # "Cluster join: Retry N" loop that reads like slow startup.
      services:
        postgres:
          image: postgres:16-alpine
          container_name: artifactory-db
          restart: unless-stopped
          environment:
            POSTGRES_DB: artifactory
            POSTGRES_USER: artifactory
            POSTGRES_PASSWORD: $ART_DB_PASSWORD
          volumes:
            - /opt/artifactory/pgdata:/var/lib/postgresql/data
          healthcheck:
            test: ["CMD-SHELL", "pg_isready -U artifactory -d artifactory"]
            interval: 10s
            timeout: 5s
            retries: 20

        artifactory:
          image: $ARTIFACTORY_IMAGE
          container_name: artifactory
          restart: unless-stopped
          # Gated on the healthcheck, not just on the container existing:
          # Artifactory fails its schema creation if postgres is still starting.
          depends_on:
            postgres:
              condition: service_healthy
          ports: ["127.0.0.1:8081:8081", "127.0.0.1:8082:8082"]
          environment:
            JF_SHARED_DATABASE_TYPE: postgresql
            JF_SHARED_DATABASE_DRIVER: org.postgresql.Driver
            JF_SHARED_DATABASE_URL: jdbc:postgresql://postgres:5432/artifactory
            JF_SHARED_DATABASE_USERNAME: artifactory
            JF_SHARED_DATABASE_PASSWORD: $ART_DB_PASSWORD
          volumes:
            - /opt/artifactory/var:/var/opt/jfrog/artifactory
          ulimits:
            nofile: {soft: 32000, hard: 40000}
  - path: /opt/artifactory/Caddyfile
    content: |
      $ART_DOMAIN {
        # Artifactory serves the whole platform, UI and Docker registry alike,
        # on 8082. 8081 is the direct-to-Artifactory port that bypasses the
        # router; proxying 8082 is what makes /v2/ work for a docker client.
        reverse_proxy 127.0.0.1:8082
        request_body {
          max_size 10GB
        }
      }
runcmd:
  - curl -fsSL https://get.docker.com | sh
  - systemctl enable --now docker
  # Artifactory runs as uid 1030 inside the image and will not start if it
  # cannot write its own data directory.
  # The keys must exist BEFORE the first start. Booting without them does not
  # merely delay startup: the router fatals, the half-built database is left
  # inconsistent, and a later boot WITH the keys then fails on
  # accessJdbcHelperImpl -- recoverable only by discarding the data directory.
  - install -d -o 1030 -g 1030 -m 755 /opt/artifactory/var/etc/security
  - printf '%s\n' '$ART_MASTER_KEY' > /opt/artifactory/var/etc/security/master.key
  - printf '%s\n' '$ART_JOIN_KEY'   > /opt/artifactory/var/etc/security/join.key
  - chmod 600 /opt/artifactory/var/etc/security/master.key /opt/artifactory/var/etc/security/join.key
  - chown -R 1030:1030 /opt/artifactory/var
  - docker compose -f /opt/artifactory/docker-compose.yml up -d
  - docker run -d --name artifactory-caddy --restart unless-stopped --network host
      -v /opt/artifactory/Caddyfile:/etc/caddy/Caddyfile:ro
      -v /opt/artifactory/caddy-data:/data $CADDY_IMAGE
CIEOF

# The SSH key by ID, not name. --keys is documented as accepting either, but the
# working demo scripts resolve it first and so does this one — a name that does
# not exist otherwise fails inside instance-create, where the error is about the
# instance rather than the key.
KEY_ID=$(icjson is keys | jq -r --arg n "$KEY_NAME" '.[]|select(.name==$n)|.id' | head -1)
[[ -n "$KEY_ID" ]] || die "SSH key '$KEY_NAME' not found in $REGION — set ART_SSH_KEY_NAME, or add it with 'ibmcloud is key-create'"

say "VSI $VSI_NAME ($PROFILE)"
VSI_ID=$(icjson is instances | jq -r --arg n "$VSI_NAME" '.[]|select(.name==$n)|.id' | head -1)
VSI_EXISTED=0
if [[ -n "$VSI_ID" ]]; then VSI_EXISTED=1; fi
if [[ -z "$VSI_ID" ]]; then
  # --metadata-service true is REQUIRED, not optional. The IBM Cloud "minimal"
  # Ubuntu images — the only ones left — run cloud-init against the metadata
  # service, and it is DISABLED by default. Without it the VSI boots, the SSH key
  # never reaches authorized_keys, and none of the user-data below ever runs: no
  # docker, no Artifactory, and nothing to indicate why.
  #
  # Security groups are attached AFTER creation via security-group-target-add
  # (see below). instance-create has no --security-groups flag; passing one fails
  # with a usage dump, which is what `--sgs` exists for but the working scripts
  # do not use either.
  VSI_ID=$(icjson is instance-create "$VSI_NAME" "$VPC_ID" "$ZONE" "$PROFILE" "$SUBNET_ID" \
    --image "$IMAGE_ID" --keys "$KEY_ID" \
    --metadata-service true \
    --resource-group-name "$RG" \
    --user-data @"$CLOUD_INIT" | jq -r .id)
fi
rm -f "$CLOUD_INIT"
[[ -n "$VSI_ID" && "$VSI_ID" != "null" ]] || die "instance-create returned no id for $VSI_NAME"

# EVERYTHING above -- the domain, the database password, both JFrog keys -- is
# delivered by cloud-init, which runs ONCE, at creation. Reusing an existing VSI
# therefore keeps whatever it was built with, and a re-run with a different
# ART_DOMAIN silently keeps the OLD one: Caddy goes on requesting a certificate
# for a name nobody asked for, and the wait below times out looking like a DNS
# fault. Say so rather than let it be discovered.
if [[ "$VSI_EXISTED" == "1" ]]; then
  say "reusing the existing $VSI_NAME — cloud-init does NOT re-run"
  say "  its domain, database password and JFrog keys are whatever it was BUILT with."
  say "  To point it at a different name, edit /opt/artifactory/Caddyfile on the VSI"
  say "  and restart the artifactory-caddy container, or --destroy and redeploy."
fi

say "waiting for the VSI to run"
for _ in $(seq 1 60); do
  st=$(icjson is instance "$VSI_ID" | jq -r .status)
  [[ "$st" == "running" ]] && break
  sleep 10
done
[[ "$st" == "running" ]] || die "VSI $VSI_NAME never reached running (last status: $st)"

INST=$(icjson is instance "$VSI_ID")
# A modern VSI exposes a virtual network interface (VNI) BEHIND a network
# attachment; an older one a legacy network interface (NIC). On a VNI instance
# .primary_network_interface.id is the ATTACHMENT id, and an attachment can
# neither take a floating IP ("The specified target is a network attachment
# which can not be attached to a floating ip directly") nor a security group.
# Prefer the VNI, exactly as deploy-far-registry.sh and provision-vsi.sh do.
VNI_ID=$(jq -r '.primary_network_attachment.virtual_network_interface.id // empty' <<<"$INST")
NIC_ID=$(jq -r '.primary_network_interface.id // empty' <<<"$INST")
PRIV_IP=$(jq -r '.primary_network_attachment.virtual_network_interface.primary_ip.address
                 // .primary_network_interface.primary_ip.address // empty' <<<"$INST")
[[ -n "$VNI_ID$NIC_ID" ]] || die "$VSI_NAME has neither a VNI nor a NIC to attach to"

# Attached HERE, not at instance-create, which has no flag for it. Without this
# the VSI carries only the VPC default group, 80 and 443 are unreachable, Caddy
# never completes its ACME challenge, and the HTTPS wait below times out with
# nothing to explain why. So a failure here is FATAL rather than a shrug — an
# earlier version reported "may already be attached" while actually failing.
say "attaching security group $SG_NAME"
if [[ -n "$VNI_ID" ]]; then
  ic is security-group-target-add "$SG_ID" "$VNI_ID" --trt virtual_network_interface >/dev/null 2>&1 \
    || ic is security-group-target-add "$SG_ID" "$VNI_ID" >/dev/null 2>&1 \
    || die "could not attach $SG_NAME to the VSI's network interface"
else
  ic is security-group-network-interface-add "$SG_ID" "$NIC_ID" >/dev/null 2>&1 \
    || die "could not attach $SG_NAME to the VSI's network interface"
fi

FIP=$(icjson is floating-ips | jq -r --arg n "$P-fip" '.[]|select(.name==$n)|.address' | head -1)
if [[ -z "$FIP" ]]; then
  if [[ -n "$VNI_ID" ]]; then
    FIP=$(icjson is floating-ip-reserve "$P-fip" --vni "$VNI_ID" | jq -r .address)
  else
    FIP=$(icjson is floating-ip-reserve "$P-fip" --nic "$NIC_ID" --in "$VSI_ID" | jq -r .address)
  fi
fi
[[ -n "$FIP" && "$FIP" != "null" ]] || die "could not obtain a floating IP for $VSI_NAME"

mkdir -p "$(dirname "$STATE_FILE")"
cat >"$STATE_FILE" <<EOF
ART_VPC_ID=$VPC_ID
ART_VSI_ID=$VSI_ID
ART_PRIVATE_IP=$PRIV_IP
ART_FLOATING_IP=$FIP
ART_DOMAIN=$ART_DOMAIN
ART_URL=https://$ART_DOMAIN
EOF

say "floating ip $FIP — point $ART_DOMAIN at it NOW if you have not already"
say "waiting for Artifactory to answer (first boot initialises its database; 5-10 min)"
for i in $(seq 1 120); do
  if curl -skf -m 5 "https://$ART_DOMAIN/artifactory/api/system/ping" >/dev/null 2>&1; then
    say "Artifactory is up at https://$ART_DOMAIN"
    exit 0
  fi
  # A DNS name that does not resolve to $FIP is by far the most common reason
  # this loop never finishes, and it is invisible from the error alone.
  if [[ $i -eq 30 ]]; then
    # getent, not dig: dig ships in dnsutils/bind-utils and is absent on plenty
    # of hosts (including a default WSL install), where it fails with "command
    # not found" -- which reads exactly like "the name does not resolve" and
    # sends the operator to fix DNS that was never broken.
    say "still waiting — confirm the name resolves to $FIP:"
    say "    getent hosts $ART_DOMAIN     # or: dig +short $ART_DOMAIN"
  fi
  sleep 10
done
die "Artifactory did not answer at https://$ART_DOMAIN. Check: DNS -> $FIP, ports 80+443 open, and 'docker logs artifactory' on the VSI."
