# Spike S0: BIG-IP VE provisioning on the `bnk-demo` VPC

**Status (2026-06-09):** **PARTIAL — infrastructure path GREEN, self-onboarding RED (within 25 min).**
A 3-NIC PAYG BIG-IP VE launches cleanly on the live `bnk-demo` VPC, boots TMOS
17.5.1.6, brings up `mcpd` and the iControl REST framework (restjavad), and is
reachable on both its public mgmt EIP and privately from the jumphost. **But the
Runtime-Init / Declarative-Onboarding (DO) self-onboard did NOT complete inside
the 25-minute cap** — the admin password our DO declaration was supposed to set
never took effect, so iControl REST never became usable. This is the single
most important finding for F2-B: **do not assume cloud-config `runcmd` +
runtime-init self-onboarding "just works" on this AMI** — see Root cause below.

The VE, ENIs, SG, and EIP are **left running** for F2-B/C build+test (resource
IDs at the bottom).

---

## What was run (the commands that worked)

All calls used `AWS_PROFILE=Users-292785712872 --region ap-southeast-2`,
account `292785712872`. Everything tagged
`awsbnkctl:cluster=bnk-demo` + `awsbnkctl:component=bigip-ve-spike`.

### AMI / subscription
- **AMI: `ami-0fee5bb47d768162b`** — `F5 BIGIP-17.5.1.6-0.0.25 PAYG-Good 25Mbps`,
  x86_64, HVM, ENA, root `/dev/xvda`, `State=available`. Used as a fixed input
  (no Marketplace lookup needed in the spike; F2-B should resolve it by
  name-filter `F5 BIGIP-17.5*PAYG-Good 25Mbps*` + owner to stay version-pinnable).
- **Subscription: ALREADY ACCEPTED.** `run-instances` succeeded with **no
  `OptInRequired`** error. PAYG ⇒ no regKey needed in DO.

### Security group (`bnk-demo-bigip-mgmt-spike` = `sg-078d160b61e5cf336`)
```
aws ec2 create-security-group --group-name bnk-demo-bigip-mgmt-spike \
  --description "BIG-IP VE spike mgmt SG (iControl/SSH)" --vpc-id vpc-0b3a62c6f6953f86f
aws ec2 authorize-security-group-ingress --group-id sg-078d160b61e5cf336 --ip-permissions \
  "IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=<operator-ip>/32}]" \
  "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=<operator-ip>/32}]" \
  "IpProtocol=tcp,FromPort=443,ToPort=443,IpRanges=[{CidrIp=10.0.0.0/16}]"   # in-VPC CIS/jumphost iControl
```

### ENIs (explicit primary IPs so DO self-IPs are known in advance)
```
# eth0 mgmt   subnet-0f2f19a2ab2b87494  10.0.1.50           SG=sg-078d160b61e5cf336 (mgmt)
# eth1 ext    subnet-0dfc1885b0f529d19  10.0.10.50 +10.0.10.120(VIP)  SG=sg-0856d8b37b7a6f62e (bnk-data)
# eth2 int    subnet-0f6c0568b6e7b5119  10.0.20.50          SG=sg-0856d8b37b7a6f62e (bnk-data)
aws ec2 create-network-interface --subnet-id <sub> --private-ip-address <ip> --groups <sg> ...
# secondary VIP on eth1 via repeated --private-ip-addresses:
#   'PrivateIpAddress=10.0.10.50,Primary=true' 'PrivateIpAddress=10.0.10.120,Primary=false'
aws ec2 modify-network-interface-attribute --network-interface-id <eni1/eni2> --no-source-dest-check
```
**Gotcha (tag-spec quoting):** building the `--tag-specifications` string by
splicing into an existing `Tags=[...]` value broke the CLI parser
(`Expected ',' received '}'`). Write the **full** `Tags=[{...},{...}]` literal
per call instead of string-concatenating.

### Launch
```
aws ec2 run-instances --image-id ami-0fee5bb47d768162b --instance-type c5n.2xlarge \
  --user-data file:///tmp/bigip-spike/userdata.yaml \
  --network-interfaces \
    'DeviceIndex=0,NetworkInterfaceId=<eni0-mgmt>' \
    'DeviceIndex=1,NetworkInterfaceId=<eni1-ext>' \
    'DeviceIndex=2,NetworkInterfaceId=<eni2-int>' \
  --tag-specifications 'ResourceType=instance,Tags=[...]'
# then:
aws ec2 allocate-address --domain vpc
aws ec2 associate-address --allocation-id <alloc> --network-interface-id <eni0-mgmt>
```
- `c5n.2xlarge` supports 4 ENIs; 3 attached cleanly at indices 0/1/2.
- **EIP-on-eth0 is a SPIKE shortcut** for polling iControl directly. Production
  must poll over the **jumphost EICE path** (no public IP on the VE) — proven
  viable below.

### Runtime-Init user-data (shape used)
`#cloud-config` writing `/config/cloud/runtime-init.yaml` (DO 1.46.0 + AS3 3.52.2
install ops; inline async DO `Device` declaration provisioning `ltm: nominal`,
admin User, `external`/`internal` VLANs on `1.1`/`1.2`, self-IPs
`10.0.10.50/24` + `10.0.20.50/24`), then a `runcmd` calling
`f5-bigip-runtime-init --config-file ...` (PAYG ⇒ no regKey). See the runbook
for the exact YAML. **This is the part that did not take effect — see below.**

---

## Measured timings (wall-clock from `run-instances`)

| Milestone | Time | Signal |
|-----------|------|--------|
| Instance `running` | **31 s** | `describe-instances State=running` |
| Status checks ok/ok | ~3–4 min | `describe-instance-status` |
| TMOS booted, `mcpd` running | ~2 min | serial console: `mcpd has reached 'running' state` @ boot+~110s |
| mgmt httpd first responds (Apache **HTML 401**) | **116 s** | `curl https://<eip>/mgmt/tm/sys/ready` → 401 page |
| iControl REST framework (restjavad) **up** | ~**872 s (~14.5 min)** | login switches from Apache HTML-401 to JSON `:resterrorresponse` |
| **DO task readable / admin pw applied** | **NEVER (cap 25 min)** | basic-auth + token-login both stayed HTTP 401 the entire run |

`time-to-running = 31s`, `time-to-mgmt-port = 116s`, **`time-to-DO-OK = N/A`**.

---

## Readiness endpoints — what each one actually told us

Polled every 20–30 s, `curl -sk`, basic auth `admin:<pw>`:

- **`GET /mgmt/shared/declarative-onboarding/task`** — the intended gate. Never
  returned a DO task; stayed 401 for 25 min (auth never accepted our DO pw).
- **`GET /mgmt/tm/sys/ready`** — same 401 throughout. Could not be evaluated as a
  gate because auth never succeeded. (We could NOT confirm the architect's
  suspicion that `sys/ready` reports ready before DO finishes — auth blocked it.)
- **`GET /mgmt/shared/appsvcs/info`** (AS3 version) — never reached (401).
- **`POST /mgmt/shared/authn/login`** — the most useful *diagnostic* endpoint.
  Its response shape distinguishes three boot phases:
  - no TCP / `000` → mgmt plane not up yet (< 116 s)
  - **Apache HTML** `<title>401 Unauthorized</title>` → httpd up, **restjavad
    NOT yet up** (116 s … ~14 min, and again transiently during service bounces)
  - **JSON** `{"code":401,"message":"Authentication failed.","kind":":resterrorresponse"}`
    → **restjavad IS up**, credentials simply rejected (~14.5 min onward)

  The Apache-HTML ↔ JSON-401 flapping is restjavad restarting — normally the
  fingerprint of DO bouncing services mid-apply, but here it never resolved into
  a working credential.

### The trustworthy readiness gate for F2-B
Because everything is auth-gated, the real production readiness gate is a
**two-stage** check, not a single endpoint:
1. **Framework-up probe (no valid creds needed):** poll `POST .../authn/login`
   (or any `/mgmt/...`) and treat the transition **Apache-HTML-401 → JSON
   `:resterrorresponse`** as "iControl REST framework is live." This is the
   first point at which DO *could* be queried.
2. **Onboarding-complete gate (authenticated):** once a login token is
   obtainable, poll `GET /mgmt/shared/declarative-onboarding/task` and require
   the latest task `result.status == "OK"`. **`sys/ready` alone is insufficient**
   (it reflects config-load, not DO completion) and should not be the gate.

Sample JSON shape we *did* capture (restjavad up, pre-auth):
```json
{"code":401,"message":"Authentication failed.","referer":"<caller-ip>",
 "restOperationId":6686954,"kind":":resterrorresponse"}
```
(We did **not** capture a healthy DO-task or `appsvcs/info` body — onboarding
never completed. Capture those on the next run once root cause below is fixed.)

---

## Jumphost → BIG-IP reachability (Step 7) — GREEN

From the jumphost (`i-0791f27ffd066fb01`, 10.0.1.80) over `aws
ec2-instance-connect ssh` (EICE):
```
JUMP2BIGIP http=401 t_connect=0.001676s   →  https://10.0.1.50/mgmt/tm/sys/ready
```
TCP+TLS to BIG-IP mgmt **10.0.1.50:443** connects in <2 ms and returns an
HTTP 401 (no creds passed). **The production "poll iControl via the jumphost,
no public IP" path is viable** — the in-VPC SG rule (tcp/443 from 10.0.0.0/16)
is what permits it. This worked even though self-onboarding failed, so the
network/SG design is sound independent of the onboarding bug.

EICE gotchas: this `aws ec2-instance-connect ssh` build has **no `--command`
flag** — it only opens an interactive shell. Driving a one-shot command needs
`expect` (auto-answer the `continue connecting (yes/no)` host-key prompt, then
send the command). It tunnels to the jumphost's **private** IP 10.0.1.80.

---

## ROOT CAUSE / open question for F2-B (the headline finding)

**Self-onboarding via cloud-config `runcmd` → `f5-bigip-runtime-init` did not
apply the DO declaration within 25 min.** Evidence:
- restjavad came up (~14.5 min) → TMOS REST stack is healthy.
- BUT neither our DO admin password **nor** factory `admin/admin` authenticates
  → the box is in neither "fully onboarded" nor "plain factory" state.
- No DO task ever became queryable; no admin pw flip ever occurred.

Most-likely cause (consistent with the runbook's pre-registered risk):
**`f5-bigip-runtime-init` is not preinstalled on this 17.5.1.6 PAYG AMI**, so the
`runcmd` (`/usr/bin/...` then `/usr/local/bin/...`) failed and the DO declaration
was never POSTed. We could **not** confirm on-box (SSH needs a working
credential the failed onboarding never set; the serial console is frozen at boot
and never flushes cloud-final/runtime-init output, so it gives no DO insight).

**Action for F2-B — do NOT rely on the `runcmd` fallback. Options, in order:**
1. **Bootstrap runtime-init explicitly** in user-data: `curl` the F5
   `f5-bigip-runtime-init` installer from
   `github.com/F5Networks/f5-bigip-runtime-init` and run it, rather than assuming
   the binary exists. (The mgmt subnet has IGW egress + the VE has an EIP, so the
   download path is open — confirmed route `0.0.0.0/0 → igw-00d7ae53ba06b4c9b`.)
2. Or skip runtime-init entirely and have F2-B **drive DO/AS3 over iControl REST
   itself** after detecting framework-up (stage-1 probe above): install the DO
   RPM via `/mgmt/shared/iapp/package-management-tasks`, then POST the
   declaration. This keeps onboarding in awsbnkctl's control loop and removes the
   "did cloud-init silently fail?" blind spot.
3. Either way, **verify the AMI's preinstalled tooling first** on the running VE
   (it's left up) once a credential path exists — that single fact decides 1 vs 2.

Whatever path is chosen, F2-B's readiness gate must be the **two-stage**
framework-up-then-DO-task-OK check above, with a timeout budget of **≥ 18–20 min
to restjavad-up** observed here (so cap onboarding polling at ~30 min, not 10).

---

## Other gotchas captured

- **mgmt-plane egress is required** for DO/AS3 RPM install. `bnk-demo` mgmt
  subnet (`subnet-0f2f19a2ab2b87494`) has `0.0.0.0/0 → igw-00d7ae53ba06b4c9b`
  and the VE has an EIP, so egress is fine here. In a no-public-IP production
  layout, ensure the mgmt subnet has a NAT path or the DO/AS3 RPMs are
  side-loaded.
- **Serial console is useless for onboarding debug** — BIG-IP 17.5 freezes the
  console buffer at `mcpd running` (~boot+110s) and never writes
  cloud-final/runtime-init/DO logs there. Plan to read
  `/var/log/cloud/bigIpRuntimeInit.log` over SSH/REST instead.
- **restjavad takes ~14–15 min** to come up on `c5n.2xlarge` first boot — much
  longer than the instance `running` (31 s) or status-ok (~3 min) signals. Don't
  gate on EC2-level health.
- **Password handling:** the admin password lived only in `/tmp/bigip-spike/`
  (mode 600) and the user-data. NOTE: when probing from the jumphost via
  `expect`, the password was echoed into the **jumphost shell command line** —
  avoid that pattern in production; pass secrets via files/stdin, never argv.
  Production must source the admin password from
  `AWSBNKCTL_BIGIP_PASSWORD` / AWS Secrets Manager, **not** embed it in
  user-data (it is world-readable via the instance metadata service).

---

## Resources created — LEFT RUNNING (for F2-B/C build+test)

| Resource | ID | Notes |
|----------|-----|-------|
| Instance | `i-0225b1b67576a2e2e` | c5n.2xlarge, running |
| ENI eth0 mgmt | `eni-0c97985544e050311` | 10.0.1.50, SG bigip-mgmt |
| ENI eth1 external | `eni-069d2e19fb8e552fd` | 10.0.10.50 + VIP 10.0.10.120, src/dest-check off |
| ENI eth2 internal | `eni-033e80c0b1ef91c12` | 10.0.20.50, src/dest-check off |
| SG bigip-mgmt | `sg-078d160b61e5cf336` | `bnk-demo-bigip-mgmt-spike` |
| EIP allocation | `eipalloc-033b3b11b719dfc5e` | → **13.239.127.211** on eth0 |

Mgmt EIP: **13.239.127.211**. Admin password (masked **`5z…zp`**) at
`/tmp/bigip-spike/admin-password.txt` (mode 600) on the operator host — **not**
committed.

Teardown when done: `terminate-instances` → `release-address` → delete the 3
ENIs → delete `sg-078d160b61e5cf336`. (All discoverable by the
`awsbnkctl:component=bigip-ve-spike` tag.)

---

## S0b — Corrected onboarding (SSH-driven)

**Status (2026-06-09):** **GREEN — deterministic onboarding recipe established and proven end-to-end.**
A fresh 3-NIC PAYG BIG-IP VE was launched **with an EC2 SSH key pair** and **no
onboarding user-data** (a deliberately clean box). We got onto it as `admin` over
SSH, root-caused the S0 failure on-box, and drove a full onboarding by hand:
admin password set, LTM confirmed provisioned, dataplane (VLANs + self-IPs)
created and **live to the AWS subnet gateways**, **AS3 3.56.0** installed via
iControl REST, and a `cis` partition created. iControl REST authenticates and
returns `sys/ready = configReady/licenseReady/provisionReady = yes`. The
jumphost→BIG-IP no-public-IP path was re-confirmed. VE left running for F2-B/C.

### THE headline finding (confirms the S0 root cause, on-box this time)

**`f5-bigip-runtime-init` is NOT preinstalled on `ami-0fee5bb47d768162b`
(17.5.1.6 PAYG).** Verified directly on the box:

| Probe | Result |
|-------|--------|
| `which f5-bigip-runtime-init`, `/usr/bin`, `/usr/local/bin` | **absent (NOT_ON_PATH)** |
| `rpm -qa \| grep -i runtime` | **no runtime-init RPM** |
| `rpm -qa \| grep -iE 'declarative\|appsvcs'` (DO / AS3) | **absent — neither preinstalled** |
| Preinstalled F5 automation | only `f5-cloud-lib-17.5.1.6` + `f5-iAppLX-aws-autoscale` iApp |
| `cloud-init --version` | `19.4` (present — so user-data *runs*, but the binary it called didn't exist) |
| `/config/cloud` dir | **does not exist** |
| `sys provision ltm` at first boot | **already `nominal`** (AMI ships LTM nominal) |
| mgmt-subnet egress (`curl https://github.com`) | **HTTP 200, 0.69s** (RPM side-load path is open) |

This is conclusive: in S0, cloud-init ran the `runcmd`, but
`f5-bigip-runtime-init` did not exist, so DO was never POSTed and the admin
password never flipped — exactly matching the S0 symptom (restjavad up, but no
credential ever worked). **Do not assume runtime-init exists on this AMI.**

### Getting onto the box

`ssh -i bigip.pem admin@<eip>` works (lands in `tmsh`; the EC2 key authenticates
user `admin`). SSH first answered at **~4.5 min** after `run-instances`. **First
boot churns for ~25–30 min**: sshd and restjavad bounce repeatedly as services
converge, so individual SSH calls intermittently time out or drop mid-session
until ~29 min uptime. **Mitigations that made the box driveable:**
- `tmsh modify auth user admin shell bash; tmsh save sys config` — makes SSH land
  directly in bash so `scp` and `ssh '... bash -s' < script` work non-interactively
  (the default `tmsh` banner breaks `scp`: *"Received message too long"*).
- A **retrying SSH wrapper** (retry the whole `bash -s < script` up to ~20× on a
  `__DONE__` sentinel) — the only reliable way to push commands through the
  first-boot flap window. Don't trust a single SSH call before ~30 min uptime.
- restjavad/restnoded were `run` and stable by ~28 min uptime; that is the real
  "frameworked-up" point, consistent with S0's ~14.5-min lower bound but with a
  long settling tail on `c5n.2xlarge`.

### The deterministic onboarding recipe (exact ordered ops that worked)

All on-box. Password is **never** on argv: set it from a file/stdin read into a
`PW` var, written to a mode-600 tmsh command file, run with `tmsh -f`, then
shredded. iControl auth uses `curl -sku "admin:$PW"` with `$PW` sourced from the
600 file. (Production sources it from Secrets Manager, not a file.)

```bash
# 0. one-time: make admin SSH land in bash (enables scp / bash -s)
tmsh modify auth user admin shell bash && tmsh save sys config

# 1. set admin password  (PW read from stdin/600-file, NOT argv)
printf 'modify auth user admin password %s\nsave sys config\n' "$PW" > /tmp/.pwcmd  # mode 600
tmsh -f /tmp/.pwcmd ; shred -u /tmp/.pwcmd
#    confirm: curl -sku "admin:$PW" https://localhost/mgmt/tm/sys/ready  -> 200

# 2. LTM provision — already 'nominal' on this AMI; assert, don't assume:
tmsh modify sys provision ltm level nominal   # no-op here; <1s

# 3. dataplane  (3s total, then save)
tmsh create net vlan external interfaces add { 1.1 }
tmsh create net vlan internal interfaces add { 1.2 }
tmsh create net self external-self address 10.0.10.50/24 vlan external allow-service default
tmsh create net self internal-self address 10.0.20.50/24 vlan internal allow-service default
tmsh save sys config
#    proven live: ping -I 10.0.10.50 10.0.10.1  and  -I 10.0.20.50 10.0.20.1  -> 0% loss;
#    interfaces 1.1 / 1.2 'up'.

# 4. AS3 install via iControl REST (NOT runtime-init; ~11s total)
RPM=f5-appsvcs-3.56.0-10.noarch.rpm
curl -sSL -o /var/config/rest/downloads/$RPM \
  https://github.com/F5Networks/f5-appsvcs-extension/releases/download/v3.56.0/$RPM   # 32MB, ~2s
curl -sku "admin:$PW" -H 'Content-Type: application/json' -X POST \
  -d "{\"operation\":\"INSTALL\",\"packageFilePath\":\"/var/config/rest/downloads/$RPM\"}" \
  https://localhost/mgmt/shared/iapp/package-management-tasks
#    poll .../package-management-tasks/<id> until status==FINISHED (~9s)
#    confirm: curl -sku admin:$PW https://localhost/mgmt/shared/appsvcs/info
#             -> {"version":"3.56.0","release":"10",...}  (worker registers ~6s after FINISHED)

# 5. CIS partition
tmsh create auth partition cis && tmsh save sys config
```

### Measured timings (wall-clock)

| Milestone | Time | Signal |
|-----------|------|--------|
| Instance `running` | ~30 s | `describe-instances` |
| SSH (`admin`) first answers | **~4.5 min** | key auth into tmsh |
| First-boot service flap settles (restjavad stable) | **~28–29 min uptime** | 3 consecutive clean SSH + `bigstart status restjavad` = run |
| Admin pw set → iControl auth 200 | seconds (once stable) | `sys/ready` → 200 + JSON |
| Dataplane VLANs+self-IPs created+saved | **3 s** | self-IPs ping gateways, 0% loss |
| AS3 download (32 MB) | **2 s** | mgmt egress IGW |
| AS3 install task FINISHED | **9 s** | package-management-tasks poll |
| AS3 worker live (`appsvcs/info` 200) | **+~6 s** | version 3.56.0 |

### F2-B readiness signals (Step 7) — from the JUMPHOST, no public IP

Over `aws ec2-instance-connect ssh` EICE to the jumphost (10.0.1.80), curling the
BIG-IP mgmt **10.0.1.50** (no creds, so 401 = "reachable + framework up", which is
the point — the BIG-IP pw is **not** passed on the jumphost shell):

```
https://10.0.1.50/mgmt/tm/sys/ready      -> HTTP 401   tcp_connect=0.68ms  tls=18ms
https://10.0.1.50/mgmt/tm/sys/provision  -> HTTP 401
```

Authenticated 200s were proven **on-box** (where the pw stays local):
`sys/ready` = `configReady/licenseReady/provisionReady: yes`,
`sys/provision/ltm` = `nominal`, `appsvcs/info` = `3.56.0`. The in-VPC SG rule
(tcp/443 from `10.0.0.0/16`) is what permits the jumphost path — production needs
no public IP on the VE.

### Recommendation for F2-B: **awsbnkctl drives iControl REST post-framework-up** (Option 2)

Pick **Option 2** from S0 (awsbnkctl owns onboarding over iControl REST), **not**
runtime-init bootstrapping, because:
1. **runtime-init is provably absent** on this pinned AMI, and S0 already showed
   the silent-`runcmd`-failure blind spot. Bootstrapping it in user-data adds a
   download+install step and a second failure mode for no benefit here.
2. Every step we need is a **fast, observable iControl REST / tmsh op** with no
   reboot: pw-set, `ltm nominal` (already set), VLAN/self-IP create, AS3 install
   via `package-management-tasks`, partition create. Total onboarding work after
   framework-up was **well under a minute** of actual ops.
3. Keeping onboarding in awsbnkctl's control loop removes the "did cloud-init
   silently fail?" question entirely — every step returns an HTTP code / task
   status awsbnkctl can gate on.

**F2-B shape:**
- **User-data:** minimal or none. (At most a tiny first-boot tmsh to set the
  admin shell + a bootstrap password from instance metadata — but prefer setting
  the pw via iControl once reachable, sourced from Secrets Manager.)
- **Readiness gate (unchanged from S0, validated again):** two-stage —
  (1) poll any `/mgmt/...` and treat **Apache-HTML-401 → JSON `:resterrorresponse`**
  as framework-up; (2) once a token/basic-auth works, gate on
  `sys/ready` all-`yes`. Budget **≥ 30 min** to framework-stable on `c5n.2xlarge`
  first boot — the flap tail is long.
- **Then drive the recipe above over iControl REST**, from awsbnkctl, via the
  jumphost (no public IP). AS3 RPM should be **side-loaded / pinned** by awsbnkctl
  (we used GitHub v3.56.0; CIS needs ≥3.18 — satisfied with headroom) rather than
  pulled live, so onboarding doesn't depend on the VE's egress.

### Secrets handling

Admin password (masked **`Vh…rH`**) lives only at
`/tmp/bigip-spike2/admin-password.txt` (mode 600) on the operator host — never
committed, never on any argv, never echoed on the jumphost shell. All on-box
temp secret files were `shred`ed after use. Production must source the admin
password from AWS Secrets Manager (e.g. `AWSBNKCTL_BIGIP_PASSWORD`), not a file
or user-data.

### Resources created — LEFT RUNNING (for F2-B/C build+test)

All tagged `awsbnkctl:cluster=bnk-demo` + `awsbnkctl:component=bigip-ve-spike2`.

| Resource | ID | Notes |
|----------|-----|-------|
| Instance | `i-0b5e6a52f31e35efb` | c5n.2xlarge, **running**, onboarded |
| ENI eth0 mgmt | `eni-0cbe3e5326d5bddbb` | 10.0.1.50, SG bigip-mgmt-spike2 |
| ENI eth1 external | `eni-03725572bb1238a4a` | 10.0.10.50 + VIP 10.0.10.120, src/dest-check off |
| ENI eth2 internal | `eni-01425cec14466ed93` | 10.0.20.50, src/dest-check off |
| SG bigip-mgmt | `sg-0ffcf9775cce24340` | `bnk-demo-bigip-mgmt-spike2` (ssh/443 from operator /32 + 443 from 10.0.0.0/16) |
| EIP allocation | `eipalloc-02bf7f26d01d1e018` | → **3.105.193.191** on eth0 |
| Key pair | `bnk-demo-bigip-spike2` | private key at `/tmp/bigip-spike2/bigip.pem` (mode 600, operator host) |

Mgmt EIP: **3.105.193.191** (SSH `admin@`). Admin pw (masked **`Vh…rH`**) at
`/tmp/bigip-spike2/admin-password.txt` (mode 600). **EIP is a spike shortcut** —
production SSH/iControl goes via the jumphost EICE, no public IP.

Teardown when done: `terminate-instances i-0b5e6a52f31e35efb` → `release-address
eipalloc-02bf7f26d01d1e018` → delete the 3 ENIs → delete `sg-0ffcf9775cce24340`
→ `delete-key-pair bnk-demo-bigip-spike2`. (All discoverable by the
`awsbnkctl:component=bigip-ve-spike2` tag.)

---

## Data-path routing limitation (CIS → pods)

**Status:** PROVEN workaround in place; durable fix deferred.

### Root cause

The `bigip-cis` demo's Apply step programs static routes on the BIG-IP for the
cluster's private subnet CIDRs (e.g. `10.0.11.0/24`, `10.0.12.0/24` — the BNK
data-plane subnets declared in the cluster intent). With **VPC-CNI in default
mode**, pods receive IP addresses from the **EKS node subnet** (e.g.
`10.0.1.0/24`), not the BNK data subnets. This causes two problems:

1. **Routes miss actual pod placement.** The /24 routes added for the BNK data
   subnets don't cover the pod IPs that CIS programs into the BIG-IP pool. The
   BIG-IP pool members are unreachable via those routes.

2. **Management subnet overlap causes route rejection.** The BIG-IP management
   self-IP (`10.0.1.50`) lives in the same `/24` as the EKS node/pod subnet. When
   `tmsh` is asked to add a `/24` data-plane route that overlaps the management
   network, it rejects the command:
   ```
   matches management network ... not adding it
   ```
   The route silently fails to install.

### Proven workaround — per-pod /32 host route

A `/32` host route is more specific than the management `/24`, so the BIG-IP
accepts it. Installing a route for the specific pod IP via the internal gateway
yields HTTP 200 end-to-end (client → BIG-IP VIP `10.0.10.120` → in-cluster pod).
This was proven live; a working /32 route is currently left on the live BIG-IP:

```bash
# Run on the BIG-IP (via jumphost SSH) — replace <podIP> with the CIS pool-member IP:
tmsh create net route bnkdemo-cis-pod-manual network <podIP>/32 gw 10.0.20.1
tmsh save sys config
```

The `/32` installs cleanly and the demo Verify assertion passes (HTTP 200 +
whoami marker).

### Durable fix options (deferred — not implemented)

**Option A (preferred):** Place the BIG-IP management ENI in a dedicated subnet
that does NOT overlap the EKS node/pod subnet — for example, a separate
management `/28` or `/24`. Then add `/24` routes to each EKS node subnet via the
internal gateway (`10.0.20.1`). This is clean, requires no per-pod route
management, and survives pod reschedule automatically.

**Option B (fallback):** Program per-pod `/32` host routes for each CIS
pool-member IP via the internal gateway. This works but is brittle: when pods
reschedule the routes go stale and must be refreshed, analogous to the HTTPRoute
pool-member-sync issue (`project_pool_member_sync_root_cause`). Requires a
watch loop or re-run of Apply after pod churn.

### Additional improvement note

The CIS pool member is configured with no health monitor. A future improvement is
to add a `tcp` or `http` monitor and include a post-Apply poll on pool-member
availability before the Verify traffic probe fires.
