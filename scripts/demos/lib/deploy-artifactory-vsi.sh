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

[[ "${1:-}" == "--destroy" ]] && { destroy; exit 0; }

command -v jq >/dev/null || die "jq is required"
login

say "VPC $VPC_NAME"
VPC_ID=$(ic is vpcs --output json | jq -r --arg n "$VPC_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$VPC_ID" ]]; then
  VPC_ID=$(ic is vpc-create "$VPC_NAME" --output json | jq -r .id)
  ic is vpc-address-prefix-create "$P-prefix" "$VPC_ID" "$ZONE" "$VPC_CIDR" >/dev/null
fi

say "subnet + public gateway"
SUBNET_ID=$(ic is subnets --output json | jq -r --arg n "$SUBNET_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$SUBNET_ID" ]]; then
  SUBNET_ID=$(ic is subnet-create "$SUBNET_NAME" "$VPC_ID" --zone "$ZONE" --ipv4-cidr-block "$VPC_CIDR" --output json | jq -r .id)
fi
PGW_ID=$(ic is public-gateways --output json | jq -r --arg n "$PGW_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$PGW_ID" ]]; then
  PGW_ID=$(ic is public-gateway-create "$PGW_NAME" "$VPC_ID" "$ZONE" --output json | jq -r .id)
fi
ic is subnet-update "$SUBNET_ID" --public-gateway-id "$PGW_ID" >/dev/null 2>&1 || true

# 443 and 80 inbound: 80 is not decorative — Caddy needs it for the ACME HTTP-01
# challenge, and without it the certificate never issues and every push fails
# with a TLS error that looks like a registry problem.
say "security group (22, 80, 443)"
SG_ID=$(ic is security-groups --output json | jq -r --arg n "$SG_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$SG_ID" ]]; then
  SG_ID=$(ic is security-group-create "$SG_NAME" "$VPC_ID" --output json | jq -r .id)
  for port in 22 80 443; do
    ic is security-group-rule-add "$SG_ID" inbound tcp --port-min "$port" --port-max "$port" --remote 0.0.0.0/0 >/dev/null
  done
  ic is security-group-rule-add "$SG_ID" outbound all --remote 0.0.0.0/0 >/dev/null
fi

IMAGE_ID=$(ic is images --visibility public --output json \
  | jq -r '[.[]|select(.operating_system.name|test("ubuntu-24-04"))|select(.status=="available")]|sort_by(.created_at)|last|.id')
[[ -n "$IMAGE_ID" && "$IMAGE_ID" != "null" ]] || die "no Ubuntu 24.04 stock image found in $REGION"

CLOUD_INIT=$(mktemp)
cat >"$CLOUD_INIT" <<CIEOF
#cloud-config
package_update: true
write_files:
  - path: /opt/artifactory/docker-compose.yml
    content: |
      services:
        artifactory:
          image: $ARTIFACTORY_IMAGE
          container_name: artifactory
          restart: unless-stopped
          ports: ["127.0.0.1:8081:8081", "127.0.0.1:8082:8082"]
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
  - mkdir -p /opt/artifactory/var/etc && chown -R 1030:1030 /opt/artifactory/var
  - docker compose -f /opt/artifactory/docker-compose.yml up -d
  - docker run -d --name artifactory-caddy --restart unless-stopped --network host
      -v /opt/artifactory/Caddyfile:/etc/caddy/Caddyfile:ro
      -v /opt/artifactory/caddy-data:/data $CADDY_IMAGE
CIEOF

say "VSI $VSI_NAME ($PROFILE)"
VSI_ID=$(ic is instances --output json | jq -r --arg n "$VSI_NAME" '.[]|select(.name==$n)|.id' | head -1)
if [[ -z "$VSI_ID" ]]; then
  VSI_ID=$(ic is instance-create "$VSI_NAME" "$VPC_ID" "$ZONE" "$PROFILE" "$SUBNET_ID" \
    --image "$IMAGE_ID" --keys "$KEY_NAME" --security-groups "$SG_ID" \
    --user-data @"$CLOUD_INIT" --output json | jq -r .id)
fi
rm -f "$CLOUD_INIT"

say "waiting for the VSI to run"
for _ in $(seq 1 60); do
  st=$(ic is instance "$VSI_ID" --output json | jq -r .status)
  [[ "$st" == "running" ]] && break
  sleep 10
done

VNI_ID=$(ic is instance "$VSI_ID" --output json | jq -r '.primary_network_interface.id')
PRIV_IP=$(ic is instance "$VSI_ID" --output json | jq -r '.primary_network_interface.primary_ip.address')
FIP=$(ic is floating-ips --output json | jq -r --arg n "$P-fip" '.[]|select(.name==$n)|.address' | head -1)
if [[ -z "$FIP" ]]; then
  FIP=$(ic is floating-ip-reserve "$P-fip" --nic "$VNI_ID" --in "$VSI_ID" --output json | jq -r .address)
fi

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
    say "still waiting — confirm: dig +short $ART_DOMAIN  ==  $FIP"
  fi
  sleep 10
done
die "Artifactory did not answer at https://$ART_DOMAIN. Check: DNS -> $FIP, ports 80+443 open, and 'docker logs artifactory' on the VSI."
