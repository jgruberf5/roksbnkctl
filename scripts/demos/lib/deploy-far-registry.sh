#!/usr/bin/env bash
#
# deploy-far-registry.sh — stand up a working private OCI registry (Harbor) to
# demonstrate `roksbnkctl registry replicate --target generic`.
#
# Two modes:
#   adopt      --host <floating-ip> --ssh-key <priv-key>    provision an EXISTING VSI
#   provision  --key-name <ibmcloud-ssh-key> --ssh-key ...  create a NEW VSI, then provision
#
# In both modes the host ends up running:
#   - Harbor (goharbor.io) on 127.0.0.1:8080, behind
#   - Caddy on 80/443, which obtains a real Let's Encrypt certificate for
#     <floating-ip>.sslip.io and reverse-proxies to Harbor.
# A private Harbor project (default "bnk-mirror") is created via Harbor's REST
# API and becomes registry.generic_repo_prefix.
#
# Why the TLS hop is mandatory: `roksbnkctl registry replicate` drives crane
# against registry.generic_host over HTTPS and never passes crane.Insecure
# (internal/registry/mirror/mirror.go: Engine.Insecure is test-only; no CLI path
# sets it). A plain-HTTP registry simply cannot be replicated to. sslip.io
# resolves <ip>.sslip.io -> <ip>, so Caddy can satisfy an ACME HTTP-01 challenge
# and the resulting cert is trusted by Go's system pool with zero operator setup.
#
# Why Harbor and not Artifactory: current Artifactory/JCR cannot be scripted.
# Verified against artifactory-jcr 7.176:
#   - it refuses to start on its bundled Derby DB ("Cannot start the application
#     with a database other than PostgreSQL"), so it is no longer single-container;
#   - PUT /api/repositories returns "This REST API is available only in
#     Artifactory Pro", so the mirror repo cannot be created via REST; and
#   - seeding artifactory.config.import.xml bricks startup under JCR's license
#     guard ("Only repositories of type Generic, Docker and Helm are allowed"),
#     for both docker and generic repo types.
# Harbor's project API is unlicensed, so the whole flow is unattended. Harbor is
# already a supported `generic` target (book/src/10b-registry-targets.md).
#
# Usage:
#   scripts/demos/lib/deploy-far-registry.sh -w prod --key-name my-ibm-key --ssh-key ~/.ssh/id_rsa
#   scripts/demos/lib/deploy-far-registry.sh --host 52.116.1.2 --ssh-key ~/.ssh/id_rsa
#   scripts/demos/lib/deploy-far-registry.sh -w prod --destroy
#
# Requires: ssh, plus (provision mode only) ibmcloud CLI + `vpc-infrastructure`
# plugin and jq. `roksbnkctl` is used to configure the workspace when -w is given.
set -euo pipefail

usage() {
  cat <<'USAGE'
deploy-far-registry.sh — a functional private OCI registry (Harbor) for the FAR replication demo

Usage:
  deploy-far-registry.sh [-w WS] --key-name NAME --ssh-key PATH [placement flags]
  deploy-far-registry.sh [-w WS] --host IP --ssh-key PATH
  deploy-far-registry.sh [-w WS] --destroy
  deploy-far-registry.sh --help

Modes:
  --host IP           adopt an EXISTING VSI reachable at this floating IP over SSH.
                      No IBM Cloud API calls are made; the VSI's security group
                      must already allow inbound 22, 80 and 443.
  --key-name NAME     create a NEW VSI, authorized by an SSH key ALREADY registered
                      in IBM Cloud VPC (`ibmcloud is keys`). --ssh-key must be the
                      matching private key. Mutually exclusive with --host.

Options:
  -w, --workspace WS  roksbnkctl workspace. Supplies default placement from
                      ~/.roksbnkctl/WS/cluster-outputs.json, and receives the
                      `registry target generic` wiring at the end.
  --ssh-key PATH      private key used to reach the VSI (required)
  --ssh-user USER     SSH user (default: root -- the login on stock IBM Cloud
                      Ubuntu images. Pass 'ubuntu' etc. if yours differs.)
  --project NAME      Harbor project to create (default: bnk-mirror). This becomes
                      registry.generic_repo_prefix.
  --domain HOST       serve on this name instead of <fip>.sslip.io. Use a real DNS
                      name you control that already resolves to the floating IP.
                      (Harbor rejects a bare IP as its hostname, so a name is
                      always required -- that is why sslip.io is the default.)
  --admin-password P  Harbor admin password (default: generated once, then reused
                      from the state dir on later runs)
  --no-configure      skip writing `registry target generic` into the workspace
  --no-verify         skip the docker login/push/pull smoke test on the VSI
  --destroy           tear down (Harbor + data; also the VSI, FIP and SG if this
                      script created them). The IBM Cloud SSH key is never
                      deleted -- it was yours before this ran.
  -h, --help          show this help

Placement (provision mode; defaults come from the workspace cluster outputs):
  --region R  --zone Z  --vpc ID  --subnet ID  --resource-group ID  --profile P

Environment:
  IBMCLOUD_API_KEY    override the credential (default: an existing `ibmcloud
                      login`, else the workspace key via `roksbnkctl apikey`)
  PROFILE             VSI profile (default: cx2-4x8 -- Harbor wants >=2 vCPU / 4 GB)
  IMAGE               VSI image id (default: newest Ubuntu LTS amd64)
  HARBOR_VERSION      Harbor release tag (default: latest from GitHub, else v2.15.2)
  CADDY_IMAGE         TLS front-end image (default: caddy:2)
USAGE
}

# ── argument parsing ────────────────────────────────────────────────────────
WS="" HOST="" KEY_NAME="" SSH_KEY="" SSH_USER="" PROJECT="bnk-mirror" DOMAIN=""
ADMIN_PASSWORD="" ACTION="deploy" CONFIGURE=1 VERIFY=1
REGION="" ZONE="" VPC="" SUBNET="" RG=""
PROFILE="${PROFILE:-cx2-4x8}"
HARBOR_VERSION="${HARBOR_VERSION:-}"
CADDY_IMAGE="${CADDY_IMAGE:-caddy:2}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    -h|--help)         usage; exit 0 ;;
    -w|--workspace)    WS="$2"; shift 2 ;;
    --host)            HOST="$2"; shift 2 ;;
    --key-name)        KEY_NAME="$2"; shift 2 ;;
    --ssh-key)         SSH_KEY="$2"; shift 2 ;;
    --ssh-user)        SSH_USER="$2"; shift 2 ;;
    --project|--repo)  PROJECT="$2"; shift 2 ;;
    --domain)          DOMAIN="$2"; shift 2 ;;
    --admin-password)  ADMIN_PASSWORD="$2"; shift 2 ;;
    --region)          REGION="$2"; shift 2 ;;
    --zone)            ZONE="$2"; shift 2 ;;
    --vpc)             VPC="$2"; shift 2 ;;
    --subnet)          SUBNET="$2"; shift 2 ;;
    --resource-group)  RG="$2"; shift 2 ;;
    --profile)         PROFILE="$2"; shift 2 ;;
    --no-configure)    CONFIGURE=0; shift ;;
    --no-verify)       VERIFY=0; shift ;;
    --destroy)         ACTION="destroy"; shift ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

need() { command -v "$1" >/dev/null || { echo "missing required tool: $1" >&2; exit 2; }; }
need ssh

[[ -n "$HOST" && -n "$KEY_NAME" ]] && { echo "--host and --key-name are mutually exclusive: --host adopts an existing VSI, --key-name creates one" >&2; exit 2; }

HOME_DIR="${ROKSBNKCTL_HOME:-$HOME/.roksbnkctl}"
STATE_DIR="$HOME_DIR/${WS:-standalone}/far-registry"
CREDS_FILE="$STATE_DIR/credentials.env"
CREATED_MARKER="$STATE_DIR/created-by-script"   # present only when WE made the VSI

PREFIX="${WS:-far}-far-registry"
VSI_NAME="$PREFIX"
SG_NAME="$PREFIX-sg"
FIP_NAME="$PREFIX-fip"

mkdir -p "$STATE_DIR"

# Reuse a previously generated admin password. Harbor stores harbor_admin_password
# in its database on FIRST install and ignores the file thereafter, so on a re-run
# the live password is the one the first run set -- regenerating would lock us out.
if [[ -z "$ADMIN_PASSWORD" && -f "$CREDS_FILE" ]]; then
  ADMIN_PASSWORD="$(sed -n 's/^ADMIN_PASSWORD=//p' "$CREDS_FILE" | head -1)"
fi
if [[ -z "$ADMIN_PASSWORD" ]]; then
  need openssl
  # Harbor requires 8-20 chars with upper, lower and a digit.
  ADMIN_PASSWORD="Far$(openssl rand -hex 6)A1"
fi

# ── IBM Cloud helpers (provision + provisioned-destroy only) ────────────────
id_of() { ibmcloud is "$1" --output json 2>/dev/null | jq -r --arg n "$2" '.[]|select(.name==$n)|.id' | head -1; }

resolve_apikey() {
  local k
  if [[ -n "$WS" ]] && k=$(roksbnkctl -w "$WS" apikey 2>/dev/null) && [[ -n "$k" ]]; then printf '%s' "$k"; return 0; fi
  [[ -n "${IBMCLOUD_API_KEY:-}" ]] && { printf '%s' "$IBMCLOUD_API_KEY"; return 0; }
  return 1
}

# ibmcloud_login reuses an existing session; `is regions` only answers when logged
# in. stdin is closed so a key mapped to several accounts cannot block on the
# interactive account-selection prompt.
ibmcloud_login() {
  need ibmcloud; need jq
  if ibmcloud is regions >/dev/null 2>&1 </dev/null; then
    [[ -n "$REGION" ]] && ibmcloud target -r "$REGION" >/dev/null
    return 0
  fi
  local api_key
  if api_key=$(resolve_apikey); then
    echo "==> logging in to IBM Cloud (region ${REGION:-default})…"
    ibmcloud login --apikey "$api_key" ${REGION:+-r "$REGION"} >/dev/null </dev/null
    unset api_key
  else
    echo "not logged in, and no credential found. Run 'ibmcloud login', set IBMCLOUD_API_KEY, or pass -w <workspace>." >&2
    exit 1
  fi
}

# load_placement fills region/zone/vpc/rg from the workspace cluster outputs,
# leaving anything already set by a flag untouched.
load_placement() {
  local outputs="$HOME_DIR/$WS/cluster-outputs.json"
  if [[ -n "$WS" && -f "$outputs" ]]; then
    need jq
    [[ -z "$REGION" ]] && REGION=$(jq -r '.region // empty' "$outputs")
    [[ -z "$VPC"    ]] && VPC=$(jq -r '.vpc_id // empty' "$outputs")
    [[ -z "$RG"     ]] && RG=$(jq -r '.resource_group_id // empty' "$outputs")
  fi
  [[ -z "$ZONE" && -n "$REGION" ]] && ZONE="${REGION}-1"
  # Never let the trailing test decide the function's status: when REGION comes
  # from a flag (no cluster-outputs.json) it is empty here, the test is false, and
  # `set -e` would kill the script with no message.
  return 0
}

# ── teardown ────────────────────────────────────────────────────────────────
if [[ "$ACTION" == "destroy" ]]; then
  # Remote teardown first, while the host is still reachable.
  if [[ -f "$CREDS_FILE" ]]; then
    remote_host=$(sed -n 's/^HOST=//p' "$CREDS_FILE" | head -1)
    remote_user=$(sed -n 's/^SSH_USER=//p' "$CREDS_FILE" | head -1)
    remote_key=${SSH_KEY:-$(sed -n 's/^SSH_KEY=//p' "$CREDS_FILE" | head -1)}
    if [[ -n "$remote_host" && -n "$remote_key" && -f "$remote_key" ]]; then
      echo "==> removing Harbor from $remote_host…"
      ssh -i "$remote_key" -o IdentitiesOnly=yes -o StrictHostKeyChecking=no \
          -o UserKnownHostsFile=/dev/null -o ConnectTimeout=10 \
          "${remote_user:-root}@$remote_host" \
          'sudo sh -c "cd /opt/far-registry/harbor 2>/dev/null && docker compose down -v >/dev/null 2>&1; docker rm -f far-caddy >/dev/null 2>&1; rm -rf /opt/far-registry"' \
        >/dev/null 2>&1 || echo "  (host unreachable; skipping remote cleanup)"
    fi
  fi

  # Delete by NAME, not by the created-by-script marker. The marker lives under
  # the workspace state dir, which a re-run (or a demo screenplay that reseeds the
  # workspace) can wipe -- and then the registry VSI would be orphaned. These
  # names are script-owned ("<ws>-far-registry*"), and --host/adopt mode never
  # creates anything under them, so name-keyed deletion is safe.
  if [[ -n "$WS" || -f "$CREATED_MARKER" ]]; then
    load_placement; ibmcloud_login
    found=0
    fip=$(id_of floating-ips "$FIP_NAME"); [[ -n "$fip" ]] && { found=1; echo "==> releasing FIP $FIP_NAME"; ibmcloud is floating-ip-release "$fip" -f >/dev/null; }
    vsi=$(id_of instances "$VSI_NAME");    [[ -n "$vsi" ]] && { found=1; echo "==> deleting VSI $VSI_NAME";  ibmcloud is instance-delete "$vsi" -f >/dev/null; }
    for _ in $(seq 1 30); do [[ -z "$(id_of instances "$VSI_NAME")" ]] && break; sleep 5; done
    sg=$(id_of security-groups "$SG_NAME"); [[ -n "$sg" ]] && { found=1; echo "==> deleting SG $SG_NAME"; ibmcloud is security-group-delete "$sg" -f >/dev/null 2>&1 || echo "  (SG still in use; retry shortly)"; }
    if [[ $found -eq 1 ]]; then
      echo "==> the IBM Cloud SSH key was pre-existing and was left in place"
    else
      echo "==> nothing named $PREFIX* found; nothing to delete"
    fi
  else
    echo "==> no workspace and no created-by-script marker; left the host itself alone"
  fi
  rm -f "$CREDS_FILE" "$CREATED_MARKER"
  echo "==> teardown complete"
  exit 0
fi

[[ -n "$SSH_KEY" ]] || { echo "--ssh-key is required" >&2; exit 2; }
[[ -f "$SSH_KEY" ]] || { echo "ssh key not found: $SSH_KEY" >&2; exit 2; }
[[ -n "$HOST" || -n "$KEY_NAME" ]] || { echo "one of --host (adopt a VSI) or --key-name (create one) is required" >&2; usage >&2; exit 2; }

# ── provision a new VSI ─────────────────────────────────────────────────────
if [[ -z "$HOST" ]]; then
  load_placement
  ibmcloud_login
  [[ -n "$VPC" && -n "$REGION" ]] || { echo "need --vpc and --region (or -w <ws> with cluster-outputs.json)" >&2; exit 1; }
  [[ -n "$RG" ]] || RG=$(ibmcloud target --output json | jq -r '.resource_group.guid // empty')
  [[ -n "$RG" ]] || { echo "no resource group; pass --resource-group" >&2; exit 1; }

  echo "==> provisioning in region=$REGION zone=$ZONE vpc=$VPC profile=$PROFILE"

  KEY_ID=$(id_of keys "$KEY_NAME")
  [[ -n "$KEY_ID" ]] || { echo "SSH key '$KEY_NAME' is not registered in this region/account. Known keys:" >&2; ibmcloud is keys >&2; exit 1; }

  if [[ -z "$SUBNET" ]]; then
    SUBNET=$(ibmcloud is subnets --output json | jq -r --arg vpc "$VPC" --arg z "$ZONE" \
      '.[]|select(.vpc.id==$vpc)|select(.zone.name==$z)|.id' | head -1)
  fi
  [[ -n "$SUBNET" ]] || { echo "no subnet in VPC $VPC zone $ZONE; pass --subnet" >&2; exit 1; }

  SG_ID=$(id_of security-groups "$SG_NAME")
  if [[ -z "$SG_ID" ]]; then
    echo "==> creating security group $SG_NAME (22, 80, 443 in; all out)"
    SG_ID=$(ibmcloud is security-group-create "$SG_NAME" "$VPC" --resource-group-id "$RG" --output json | jq -r '.id')
    # 80 is not cosmetic: Caddy's ACME HTTP-01 challenge is served there.
    for p in 22 80 443; do
      ibmcloud is security-group-rule-add "$SG_ID" inbound tcp --port-min "$p" --port-max "$p" --remote 0.0.0.0/0 >/dev/null
    done
    ibmcloud is security-group-rule-add "$SG_ID" outbound all --remote 0.0.0.0/0 >/dev/null
  fi

  IMAGE_ID="${IMAGE:-}"
  if [[ -z "$IMAGE_ID" ]]; then
    echo "==> selecting the newest Ubuntu LTS image…"
    # Every IBM Cloud Ubuntu stock image is a "minimal" build now, so they cannot
    # be filtered out. Minimal images take the SSH key ONLY from the metadata
    # service — hence --metadata-service true + the cloud-config user-data below.
    IMAGE_ID=$(ibmcloud is images --output json | jq -r '
      [ .[] | select(.status=="available")
            | select(.operating_system.architecture=="amd64")
            | select(.operating_system.name|ascii_downcase|test("ubuntu"))
            | select(.operating_system.version|test("24\\.04|22\\.04")) ]
      | sort_by(.operating_system.version, .created_at) | last | .id')
  fi
  [[ -n "$IMAGE_ID" && "$IMAGE_ID" != "null" ]] || { echo "could not resolve an Ubuntu image; set IMAGE=<id>" >&2; exit 1; }

  VSI_ID=$(id_of instances "$VSI_NAME")
  if [[ -z "$VSI_ID" ]]; then
    echo "==> creating VSI $VSI_NAME…"
    # IBM Cloud VPC MINIMAL images (the only Ubuntu ones left) inject SSH keys via
    # cloud-init reading the metadata service, which is DISABLED by default — the
    # key gets attached but never lands in root's authorized_keys. Enable it, and
    # also pass a cloud-config user-data that writes the key directly. Belt and
    # suspenders, mirroring scripts/demos/lib/provision-vsi.sh.
    USER_DATA="$(printf '#cloud-config\nssh_authorized_keys:\n  - %s\n' "$(ssh-keygen -y -f "$SSH_KEY")")"
    VSI_ID=$(ibmcloud is instance-create "$VSI_NAME" "$VPC" "$ZONE" "$PROFILE" "$SUBNET" \
      --image "$IMAGE_ID" --keys "$KEY_ID" --resource-group-id "$RG" \
      --metadata-service true --user-data "$USER_DATA" --output json | jq -r '.id')
    touch "$CREATED_MARKER"
  else
    echo "==> VSI $VSI_NAME already exists ($VSI_ID)"
  fi

  # Modern VSIs expose a virtual network interface (VNI) behind a network
  # ATTACHMENT; older ones a legacy network interface (NIC). On a VNI instance
  # .primary_network_interface.id is the attachment id, which cannot take a
  # floating IP ("The specified target is a network attachment which can not be
  # attached to a floating ip directly"). Prefer the VNI, as provision-vsi.sh does.
  INST_JSON=$(ibmcloud is instance "$VSI_ID" --output json)
  VNI_ID=$(jq -r '.primary_network_attachment.virtual_network_interface.id // empty' <<<"$INST_JSON")
  NIC_ID=$(jq -r '.primary_network_interface.id // empty' <<<"$INST_JSON")

  echo "==> attaching the security group…"
  if [[ -n "$VNI_ID" ]]; then
    ibmcloud is security-group-target-add "$SG_ID" "$VNI_ID" --trt virtual_network_interface >/dev/null 2>&1 || true
  elif [[ -n "$NIC_ID" ]]; then
    ibmcloud is security-group-network-interface-add "$SG_ID" "$NIC_ID" >/dev/null 2>&1 || true
  else
    echo "no VNI or NIC on $VSI_NAME to attach the security group to" >&2; exit 1
  fi

  FIP_ID=$(id_of floating-ips "$FIP_NAME")
  if [[ -z "$FIP_ID" ]]; then
    echo "==> reserving + binding floating IP $FIP_NAME"
    if [[ -n "$VNI_ID" ]]; then
      HOST=$(ibmcloud is floating-ip-reserve "$FIP_NAME" --vni "$VNI_ID" --resource-group-id "$RG" --output json | jq -r '.address')
    else
      HOST=$(ibmcloud is floating-ip-reserve "$FIP_NAME" --nic "$NIC_ID" --in "$VSI_ID" --resource-group-id "$RG" --output json | jq -r '.address')
    fi
  else
    HOST=$(ibmcloud is floating-ip "$FIP_ID" --output json | jq -r '.address')
  fi
  [[ -n "$HOST" && "$HOST" != "null" ]] || { echo "could not obtain a floating IP address for $VSI_NAME" >&2; exit 1; }
fi

: "${SSH_USER:=root}"              # stock IBM Cloud Ubuntu images log in as root
: "${DOMAIN:=${HOST}.sslip.io}"    # sslip.io resolves <ip>.sslip.io -> <ip>

echo "==> host $HOST  user $SSH_USER  domain $DOMAIN  project $PROJECT"

SSH_OPTS=(-i "$SSH_KEY" -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new
          -o UserKnownHostsFile="$STATE_DIR/known_hosts" -o ConnectTimeout=10 -o BatchMode=yes)

echo "==> waiting for SSH on $HOST…"
for i in $(seq 1 60); do
  ssh "${SSH_OPTS[@]}" "$SSH_USER@$HOST" true 2>/dev/null && break
  [[ $i -eq 60 ]] && { echo "SSH never came up on $HOST as $SSH_USER (check the key, the user, and inbound 22)" >&2; exit 1; }
  sleep 5
done

umask 077
cat >"$CREDS_FILE" <<EOF
HOST=$HOST
DOMAIN=$DOMAIN
SSH_USER=$SSH_USER
SSH_KEY=$SSH_KEY
PROJECT=$PROJECT
ADMIN_USERNAME=admin
ADMIN_PASSWORD=$ADMIN_PASSWORD
EOF

# ── remote provisioning ─────────────────────────────────────────────────────
REMOTE="$(mktemp)"; trap 'rm -f "$REMOTE"' EXIT
cat >"$REMOTE" <<'REMOTE_SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
: "${FAR_DOMAIN:?}" "${FAR_PROJECT:?}" "${FAR_PASSWORD:?}" "${FAR_CADDY_IMAGE:?}"
ROOT=/opt/far-registry
HARBOR_DIR="$ROOT/harbor"

if ! command -v curl >/dev/null 2>&1 || ! command -v tar >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y >/dev/null && apt-get install -y curl tar ca-certificates >/dev/null
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "--> installing docker"
  curl -fsSL https://get.docker.com | sh >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin is missing" >&2; exit 1; }

mkdir -p "$ROOT/caddy"

# Caddy terminates TLS on 443 and proxies to Harbor's nginx on 8080; host
# networking is what lets it reach 127.0.0.1:8080. Harbor's nginx binds
# 0.0.0.0:8080 and harbor.yml offers no bind address, so 8080 is kept private by
# the security group (only 22/80/443 are opened) -- in --host mode make sure the
# VSI's own security group does not expose 8080.
cat >"$ROOT/caddy/Caddyfile" <<EOF
{
	admin off
}
$FAR_DOMAIN {
	reverse_proxy 127.0.0.1:8080
}
EOF

if [[ ! -f "$HARBOR_DIR/docker-compose.yml" ]]; then
  ver="${FAR_HARBOR_VERSION:-}"
  if [[ -z "$ver" ]]; then
    ver=$(curl -s -m 20 https://api.github.com/repos/goharbor/harbor/releases/latest \
          | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
  fi
  [[ -n "$ver" ]] || ver=v2.15.2
  echo "--> installing Harbor $ver"
  mkdir -p "$ROOT"
  curl -sL -m 600 -o "$ROOT/harbor.tgz" \
    "https://github.com/goharbor/harbor/releases/download/${ver}/harbor-online-installer-${ver}.tgz"
  tar xzf "$ROOT/harbor.tgz" -C "$ROOT"
  rm -f "$ROOT/harbor.tgz"

  cd "$HARBOR_DIR"
  cp harbor.yml.tmpl harbor.yml
  # Drop the whole top-level https: block -- Caddy owns TLS. Everything from
  # "https:" up to the next top-level key goes.
  awk '/^https:/{skip=1} /^[a-z_]+:/{ if($0 !~ /^https:/) skip=0 } !skip' harbor.yml >harbor.yml.tmp
  mv harbor.yml.tmp harbor.yml
  db_pw="$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  # external_url is what makes Harbor hand Docker clients an https:// token realm
  # and https:// redirects; without it they get sent to http://<domain>:8080.
  # Harbor rejects a bare IP as hostname, hence the sslip.io name.
  #
  # It is INSERTED after hostname rather than uncommented in place: the awk above
  # skips from "https:" to the next top-level key, and the template's commented
  # "# external_url:" line sits in between, so it is already gone by now.
  sed -i \
    -e "s|^hostname: .*|hostname: $FAR_DOMAIN|" \
    -e "/^hostname: /a external_url: https://$FAR_DOMAIN" \
    -e "s|^  port: 80$|  port: 8080|" \
    -e "s|^harbor_admin_password: .*|harbor_admin_password: $FAR_PASSWORD|" \
    -e "s|^  password: root123$|  password: $db_pw|" \
    -e "s|^data_volume: .*|data_volume: $ROOT/data|" \
    harbor.yml
  grep -q "^external_url: https://$FAR_DOMAIN" harbor.yml || { echo "failed to set external_url in harbor.yml" >&2; exit 1; }
  ./install.sh >/dev/null
else
  echo "--> Harbor already installed; starting it"
  cd "$HARBOR_DIR" && docker compose up -d >/dev/null
fi

echo "--> waiting for Harbor on 127.0.0.1:8080"
for i in $(seq 1 60); do
  curl -sf -m 5 http://127.0.0.1:8080/api/v2.0/systeminfo >/dev/null 2>&1 && break
  if [[ $i -eq 60 ]]; then
    echo "Harbor never answered /api/v2.0/systeminfo" >&2
    (cd "$HARBOR_DIR" && docker compose ps) >&2 || true
    exit 1
  fi
  sleep 5
done
echo "--> Harbor is up"

if ! docker ps --format '{{.Names}}' | grep -qx far-caddy; then
  docker rm -f far-caddy >/dev/null 2>&1 || true
  echo "--> starting Caddy (Let's Encrypt for $FAR_DOMAIN)"
  docker run -d --name far-caddy --restart unless-stopped --network host \
    -v "$ROOT/caddy/Caddyfile:/etc/caddy/Caddyfile:ro" \
    -v "$ROOT/caddy/data:/data" -v "$ROOT/caddy/config:/config" \
    "$FAR_CADDY_IMAGE" >/dev/null
fi

# Create the project. 201 = created, 409 = already there; both are success.
echo "--> ensuring Harbor project '$FAR_PROJECT'"
code=$(curl -s -o /tmp/proj.out -w '%{http_code}' -m 30 -u "admin:$FAR_PASSWORD" \
  -X POST http://127.0.0.1:8080/api/v2.0/projects \
  -H 'Content-Type: application/json' \
  -d "{\"project_name\":\"$FAR_PROJECT\",\"public\":false}")
case "$code" in
  201) echo "--> project created" ;;
  409) echo "--> project already exists" ;;
  401) echo "admin password rejected -- Harbor was installed by an earlier run with a different password." >&2
       echo "  pass --admin-password with that password, or --destroy and redeploy." >&2; exit 1 ;;
  *)   echo "project create failed (HTTP $code):" >&2; cat /tmp/proj.out >&2; exit 1 ;;
esac

if [[ "${FAR_VERIFY:-1}" == "1" ]]; then
  echo "--> waiting for the Let's Encrypt certificate on $FAR_DOMAIN"
  for i in $(seq 1 36); do
    curl -sf -m 5 "https://$FAR_DOMAIN/api/v2.0/systeminfo" >/dev/null 2>&1 && break
    if [[ $i -eq 36 ]]; then
      echo "no working TLS on https://$FAR_DOMAIN after 3 minutes" >&2
      echo "  Caddy needs inbound 80 (ACME HTTP-01) and 443 reachable from the internet." >&2
      docker logs --tail 30 far-caddy >&2 || true
      exit 1
    fi
    sleep 5
  done

  # Push the exact nested shape ocireg.PushRef emits: <host>/<prefix>/images/<name>.
  echo "--> smoke test: docker login + push + pull through https://$FAR_DOMAIN"
  printf '%s' "$FAR_PASSWORD" | docker login "$FAR_DOMAIN" -u admin --password-stdin >/dev/null
  docker pull -q busybox:latest >/dev/null
  probe="$FAR_DOMAIN/$FAR_PROJECT/images/smoketest:probe"
  docker tag busybox:latest "$probe"
  docker push -q "$probe" >/dev/null
  docker rmi "$probe" >/dev/null
  docker pull -q "$probe" >/dev/null
  docker rmi "$probe" >/dev/null 2>&1 || true
  docker logout "$FAR_DOMAIN" >/dev/null 2>&1 || true
  echo "--> smoke test passed: pushed and pulled $probe"
fi
REMOTE_SCRIPT

echo "==> provisioning Harbor over SSH (first install takes several minutes)…"
ssh "${SSH_OPTS[@]}" "$SSH_USER@$HOST" \
  "sudo env FAR_DOMAIN='$DOMAIN' FAR_PROJECT='$PROJECT' FAR_PASSWORD='$ADMIN_PASSWORD' \
            FAR_CADDY_IMAGE='$CADDY_IMAGE' FAR_HARBOR_VERSION='$HARBOR_VERSION' FAR_VERIFY='$VERIFY' \
            bash -s" <"$REMOTE"

# ── wire the workspace ──────────────────────────────────────────────────────
if [[ "$CONFIGURE" == "1" && -n "$WS" ]] && command -v roksbnkctl >/dev/null; then
  echo "==> configuring workspace '$WS' registry target"
  roksbnkctl -w "$WS" registry target generic >/dev/null
  roksbnkctl -w "$WS" registry target generic_host "$DOMAIN" >/dev/null
  roksbnkctl -w "$WS" registry target generic_repo_prefix "$PROJECT" >/dev/null
  roksbnkctl -w "$WS" registry target generic_username admin >/dev/null
  printf '%s' "$ADMIN_PASSWORD" | roksbnkctl -w "$WS" registry target generic_password --password-stdin >/dev/null
  roksbnkctl -w "$WS" registry target
fi

cat <<DONE

==> FAR demo registry ready
    Host:        $HOST  (ssh -i $SSH_KEY $SSH_USER@$HOST)
    Registry:    https://$DOMAIN/$PROJECT
    Web UI:      https://$DOMAIN/   (user: admin)
    Credentials: $CREDS_FILE   (admin password -- never printed here, so this
                 summary is safe to screen-share or record)

Replicate FAR into it:
    roksbnkctl -w ${WS:-<ws>} registry diff
    roksbnkctl -w ${WS:-<ws>} registry replicate --target generic
    roksbnkctl -w ${WS:-<ws>} registry verify

Artifacts land at https://$DOMAIN/$PROJECT/{images,charts,...}/<name> and are
browsable in the Harbor UI under the '$PROJECT' project.
Tear down: $0 ${WS:+-w $WS }--destroy
DONE
