# Troubleshooting

Common failure modes you'll hit when running `roksbnkctl` against real IBM Cloud accounts, organised as **symptom → root cause → fix**. The entries here are mined from the issue logs accumulated over Sprints 0-5 plus the failure shapes documented in [PRD 05 §"Risks"](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md).

Use the page as a lookup table. If your symptom isn't here, [Chapter 23 — The E2E test plan](./23-e2e-test-plan.md) lists what every phase asserts; reverse-engineering from the assertions can narrow your diagnosis. For deeper-than-here debugging, the per-phase log files under `/tmp/roksbnkctl-e2e-backends/` are the first stop.

## Install and init

### Symptom: `roksbnkctl init` errors with `plaintext secret detected`

**Root cause**: an existing `~/.roksbnkctl/<workspace>/config.yaml` has a credential value sitting in a field whose name matches the rejection regex (`api_key`, `password`, `token`, `secret_access_key`, `hmac_secret`). The rejection is a deliberate safety net — see [Chapter 14 §"What's safe to commit vs not"](./14-credentials-resolver.md#whats-safe-to-commit-vs-not).

**Fix**: move the credential into `IBMCLOUD_API_KEY` (env var) or the OS keychain (`roksbnkctl init` writes it via [`zalando/go-keyring`](https://pkg.go.dev/github.com/zalando/go-keyring)). For a single-user dev box, the supported plaintext-on-disk channel is `ibmcloud.api_key_b64` — base64-encoded, which doesn't trip the regex.

### Symptom: `roksbnkctl init` interactive prompts loop forever asking for the API key

**Root cause**: you're running under CI / a non-TTY shell and `roksbnkctl` can't read stdin. The interactive prompt fallback is the last step in the [credential resolver chain](./14-credentials-resolver.md#the-ibmcloud_api_key-resolver-chain) and it doesn't gracefully skip when stdin is closed.

**Fix**: set `IBMCLOUD_API_KEY` in the env, or pre-populate the keychain entry. For batch / CI runs, the documented invocation is:

```bash
IBMCLOUD_API_KEY=$(cat /path/to/secret) roksbnkctl init -w my-workspace
```

Pre-setting `IBMCLOUD_API_KEY` skips the API-key prompt (it's the first link in the resolver chain). `init` still prompts for the remaining workspace metadata (region, resource group, cluster name) on TTY-bound stdin — a fully non-interactive bootstrap is on the v1.x roadmap.

### Symptom: doctor reports `terraform: not found` on a fresh dev box

**Root cause**: `terraform` is the only strictly-required host tool for v1.0 (everything else is internalised). Doctor checks PATH; if your shell session hasn't sourced the install location it'll miss.

**Fix**: install terraform via your package manager (`brew install terraform`, `apt-get install terraform`, etc.) and re-source the shell, or set the `TERRAFORM_BIN` env var pointing at the binary explicitly.

## `roksbnkctl up` lifecycle

### Symptom: `terraform apply` errors `timeout while waiting for state to become 'normal'`

**Root cause**: IBM Cloud's control plane is occasionally 5-15 minutes slow propagating cluster state — a known transient. The cluster *was* created; the API just hasn't caught up to reporting it as Ready.

**Fix**: `roksbnkctl up` retries the apply automatically up to 3 attempts with a 60-second sleep between (see [`applyWithRetry` in `internal/cli/lifecycle.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/cli/lifecycle.go)). If all three retries fail, just re-run `roksbnkctl up` manually — terraform's state is durable, and the second attempt skips every resource that's already provisioned.

### Symptom: `roksbnkctl up` returns success but `roksbnkctl k get nodes` says `No resources found`

**Root cause**: the ROKS cluster's worker nodes take 5-10 minutes to provision *after* the cluster's master endpoint returns Ready. Terraform considers the cluster "applied" as soon as the master is up; the workers come up asynchronously.

**Fix**: wait 5-10 minutes and re-run. If you want a deterministic gate, watch the IBM Cloud console's cluster page until the worker count matches `workers_per_zone × zones`, then proceed. There's no `roksbnkctl wait` command in v1.0 — that's a v1.x addition.

### Symptom: `roksbnkctl up` post-apply hook fails fetching the admin kubeconfig with a 404

**Root cause**: the IBM Cloud kubeconfig API ([`/global/v2/applications/kubeconfig`](https://cloud.ibm.com/docs/openshift?topic=openshift-cs_cli_install)) returns 404 for ~30-60 seconds after the cluster create call returns. The cluster exists but the kubeconfig endpoint hasn't materialised.

**Fix**: the binary retries with exponential backoff and usually succeeds within a minute. If it still 404s after the retry budget, run `roksbnkctl kubeconfig --download -w <workspace>` to retry just the fetch without re-applying.

### Symptom: `Error: Inappropriate value for attribute "kubeconfig_dir": directory does not exist`

**Root cause**: the upstream HCL's IBM provider doesn't `MkdirAll` for the kubeconfig output directory; it expects the parent dir to exist already. The variable's default (`/work/.bnk/scratch/kubeconfig`) is the in-container path; on a direct-on-host run it's a path that doesn't exist.

**Fix**: `roksbnkctl` writes a workspace-scoped override (`kubeconfig_dir = ~/.roksbnkctl/<ws>/state/kubeconfig`) and creates the dir at apply time. If you're hand-rolling terraform without `roksbnkctl up`, `mkdir -p ~/.roksbnkctl/<ws>/state/kubeconfig` first.

### Symptom: `terraform destroy` leaves orphan IBM Cloud resources (LBs, security groups, VPEs)

**Root cause**: ROKS occasionally leaves dangling cluster-owned resources after the cluster itself is destroyed — the destroy returns success but the IBM Cloud account still shows a load balancer or a Virtual Private Endpoint Gateway tagged with the deleted cluster's ID.

**Fix**: run `roksbnkctl ibmcloud is load-balancers | grep <cluster-name>` (and similar for `vpc-endpoint-gateways`, `security-groups`) and `ibmcloud is load-balancer-delete` each orphan by ID. A future `roksbnkctl cluster destroy --sweep-orphans` will automate this — for now, manual.

## Workspaces

### Symptom: `roksbnkctl ws delete <name>` succeeds but subsequent commands still use the deleted workspace

**Root cause**: workspace context is set by the `--workspace`/`-w` flag (or the persistent value the active shell remembers from the last `roksbnkctl ws use`); deleting the workspace directory doesn't reset that context, so subsequent commands try to operate on a non-existent workspace dir.

**Fix**: switch to another workspace **before** deleting the current one:

```bash
roksbnkctl ws use default
roksbnkctl ws delete my-old-workspace
```

The parking-lot pattern is the recommended flow: keep a `default` workspace as the always-safe destination after deletes. Documented in [Chapter 6 — Workspaces](./06-workspaces.md).

### Symptom: `workspace "<name>" has terraform-managed resources; pass --force to delete anyway`

**Root cause**: the workspace's `terraform.tfstate` is non-empty — live infrastructure exists. `roksbnkctl ws delete` refuses to orphan the resources by removing the state file out from under them.

**Fix**: run `roksbnkctl down -w <name> --auto` first to destroy the resources, then `roksbnkctl ws delete <name>` (no `--force` needed once state is empty). If you genuinely want to abandon the infra and clean up by hand later, `roksbnkctl ws delete --force` skips the check.

## Backends

### Symptom: `--backend docker` errors with `Cannot connect to the Docker daemon`

**Root cause**: dockerd isn't running, or your user isn't in the `docker` group, or you're on a system that needs a separate rootless-docker socket path.

**Fix**:
- Linux with system docker: `sudo systemctl start docker`; add yourself to the `docker` group (`sudo usermod -aG docker $USER`) and log out + back in.
- Linux with rootless docker: `systemctl --user start docker`; set `DOCKER_HOST=unix:///run/user/$(id -u)/docker.sock`.
- macOS / Windows: launch Docker Desktop / Rancher Desktop.

Verify with `docker info | head -1` — if that fails, `roksbnkctl --backend docker` will too.

### Symptom: `--backend k8s` errors with `ops pod not found in roksbnkctl-ops namespace`

**Root cause**: you haven't run `roksbnkctl ops install` against the target cluster. The k8s backend dispatches into a long-lived ops pod that has to be provisioned first.

**Fix**:

```bash
roksbnkctl ops install
```

Verify with `roksbnkctl k get pod -n roksbnkctl-ops` — the pod should be Running. See [Chapter 19](./19-in-cluster-ops-pod.md) for the install model.

### Symptom: `--backend ssh:<target>` errors with `tool not found: iperf3 — run with --bootstrap to apt-install`

**Root cause**: the SSH target doesn't have the tool installed, and `roksbnkctl` doesn't auto-install without explicit opt-in (because apt-installing on a production jumphost without consent is rude).

**Fix**: pass `--bootstrap` once per fresh target:

```bash
roksbnkctl --backend ssh:jumphost --bootstrap test throughput
```

The bootstrap step runs `apt-get install -y <tool>` (or the equivalent for ibmcloud — adding the IBM apt repo first). Subsequent calls skip the install check and run normally. See [Chapter 17 §"SSH backend"](./17-execution-backends.md#ssh-backend) for the bootstrap mechanism.

### Symptom: `--backend ssh:jumphost` errors `host key mismatch for jumphost (got SHA256:..., known_hosts has SHA256:...)`

**Root cause**: the jumphost was re-provisioned (terraform destroy + apply) and now has a fresh host key, but `~/.roksbnkctl/known_hosts` still has the old fingerprint. TOFU refuses to silently accept the change — that's the threat model the prompt exists to defend against.

**Fix**: if you know the re-provision is legitimate, delete the stale entry:

```bash
ssh-keygen -R '<jumphost-ip>' -f ~/.roksbnkctl/known_hosts
# Or for the whole roksbnkctl known_hosts:
rm ~/.roksbnkctl/known_hosts
```

The next `roksbnkctl --on jumphost` call will TOFU-prompt with the new fingerprint. For CI use the `--insecure-host-key` flag, which records the key on first contact without prompting.

## OpenShift and PodSecurity

### Symptom: throughput test pod fails admission: `violates PodSecurity "restricted:v1.x": runAsNonRoot != true`

**Root cause**: the throughput suite's default iperf3 image is `networkstatic/iperf3:latest` which runs as root. OpenShift's `restricted-v2` SCC rejects root pods.

**Fix**: set the workspace config to use the bundled image, which is built `USER 1000`:

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
test:
  throughput:
    image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0
```

[Chapter 22 §"The bundled image and the `runAsNonRoot` constraint"](./22-throughput-testing.md#the-bundled-image-and-the-runasnonroot-constraint) is the full backstory. The same chapter's §"OpenShift SCC failure mode" lists the three error-message variants OpenShift produces.

### Symptom: `roksbnkctl ops install` errors `ServiceAccount "roksbnkctl-ops" forbidden: violates PodSecurityPolicy`

**Root cause**: rare — the cluster is running PodSecurityPolicy (the deprecated predecessor to PodSecurity admission) and the ops pod's ServiceAccount doesn't have the SCC binding it needs.

**Fix**: the ops manifest assumes `restricted-v2` is acceptable. If your cluster forces `privileged`, that's a cluster-policy question outside `roksbnkctl`'s control — talk to your cluster admin about granting `restricted-v2` to the `roksbnkctl-ops` namespace.

### Symptom: `ImagePullBackOff` on the ops pod or throughput pod

**Root cause**: most commonly, the cluster can't reach the image registry. Three sub-causes:

1. The cluster's egress NAT doesn't route to `ghcr.io` (the image host for `roksbnkctl-tools-*`).
2. The image tag doesn't exist for the version you're running (e.g., you built `roksbnkctl` from `main` at a commit between releases, and `:dev` isn't published).
3. ghcr.io itself is rate-limiting unauthenticated pulls (rare; usually only an issue for shared CI hosts hitting ghcr.io en masse).

**Fix**:
- Check egress with `roksbnkctl k exec <ops-pod> -- curl -sI https://ghcr.io` — if that hangs, you have a network path issue, not a roksbnkctl issue.
- Check the tag with `docker manifest inspect ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<version>` — if 404, pin to a tagged release version in workspace config rather than running from `main` head.
- For rate-limit issues, pre-pull images to a local registry mirror and override the workspace `test.throughput.image` to point there.

## DNS

### Symptom: `roksbnkctl test dns` returns NXDOMAIN against an internal GSLB record that you know exists

**Root cause**: your laptop's resolver chain doesn't have a route to the internal GSLB VIP. The default `--server system` uses your `/etc/resolv.conf`, which resolves against your office or ISP resolver — neither of which knows about the cluster-private GSLB.

**Fix**: query the GSLB VIP explicitly, or query from inside the cluster:

```bash
# Query the GSLB VIP directly
roksbnkctl test dns --target www.example.com --type A --server 169.45.91.5

# Or run the probe from inside the cluster (the cluster's resolvers reach the GSLB)
roksbnkctl test dns --target www.example.com --type A --backend k8s --server cluster
```

[Chapter 21 §"Server resolution"](./21-dns-testing-gslb.md#server-resolution) is the full `--server` reference.

### Symptom: `--gslb-compare` always reports `gslb_divergence: false` against a target you expect to diverge

**Root cause**: the chosen target's GSLB rule isn't differentiating your `local` vantage (laptop) from your `k8s` vantage (cluster). Two common shapes:

1. The name is fronted by an anycast resolver fleet (Cloudflare, Google Public DNS) — same answer everywhere by design.
2. Your laptop and your cluster are both in the same geographic region from GSLB's perspective (both in North America hitting the same datacenter).

**Fix**: pick a target known to be geo-resolved (`www.google.com` is the canonical "different IPs from different regions" example), or add an SSH-based vantage (`--backend ssh:eu-bastion`) to bring in a third region. [Chapter 21 §"GSLB cross-vantage compare"](./21-dns-testing-gslb.md#the---gslb-compare-workflow) covers the multi-vantage workflow.

### Symptom: `roksbnkctl test dns --backend docker` errors `DNS probe doesn't benefit from docker`

**Root cause**: design choice. Docker containers share the host's network namespace by default, so a docker-backend probe has the same network identity as a `--backend local` probe — no GSLB-relevant vantage difference.

**Fix**: use `--backend local`, `--backend k8s`, or `--backend ssh:<target>` instead.

## Cluster registration

### Symptom: `roksbnkctl cluster register <name>` errors `cluster not found`

**Root cause**: the cluster name doesn't exist in the workspace's resource group, or the API key doesn't have visibility into the resource group.

**Fix**: verify the name with `roksbnkctl ibmcloud ks cluster ls --output json | jq '.[].name'`, and verify the resource-group scope in workspace config matches where the cluster lives. If the cluster is in a different resource group, set `ibmcloud.resource_group` in the workspace config to that group.

### Symptom: `register` succeeds but `roksbnkctl k get nodes` immediately errors `Unauthorized`

**Root cause**: the kubeconfig was fetched but the auth token has already expired, or the IAM-based token that the kubeconfig embeds doesn't match the API key that's currently in env. Common after a 1-hour idle window.

**Fix**:

```bash
roksbnkctl kubeconfig --download --cluster <name>
```

The token refresh is automatic on every `up`/`apply`, but `register` against a cluster you didn't just provision sometimes lands you with a stale token in the kubeconfig.

## COS supply chain

### Symptom: FLO fails to start with `failed to pull FAR image: 403 Forbidden`

**Root cause**: the `f5-far-auth-key.tgz` object in the bucket has stale credentials (the F5-side pull key was rotated, but the bucket still has the old one).

**Fix**: re-issue the key on the F5 side and upload to COS:

```bash
roksbnkctl cos object put bnk-schematics-resources/f5-far-auth-key.tgz \
  ./new-f5-far-auth-key.tgz \
  --instance bnk-orchestration

# Restart FLO so it re-reads
roksbnkctl k delete pod -n f5-bnk -l app=flo
```

See [Chapter 25 §"Worked example"](./25-cos-supply-chain.md#worked-example-rotating-cos-supply-chain-assets) for the full flow.

### Symptom: `cos object put` for a 3 GB file errors midway with `RequestTimeout`

**Root cause**: the multipart upload SDK encountered a transient COS HTTP timeout on one of the part uploads. Multipart uploads aren't currently resumed from the failure point — they restart from zero.

**Fix**: re-run the `cos object put`. If it fails reproducibly on the same part, the underlying network is the problem (your egress link is saturated, or COS is having a regional outage — check the IBM Cloud status page).

### Symptom: `cos bucket delete` errors `Bucket not empty`

**Root cause**: COS requires buckets to be empty before delete; there's no `--recursive` flag on bucket delete today.

**Fix**: list and delete each object, then delete the bucket:

```bash
roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration | \
  awk 'NR>1 {print $1}' | \
  xargs -I{} roksbnkctl cos object delete "bnk-schematics-resources/{}" --instance bnk-orchestration

roksbnkctl cos bucket delete bnk-schematics-resources --instance bnk-orchestration
```

Don't forget to abort any pending multipart uploads first — they don't appear in the standard object list but they do prevent bucket deletion. The workaround for now is `ibmcloud cos list-multipart-uploads` followed by `ibmcloud cos abort-multipart-upload` until v1.x lands a native command.

## Networking

### Symptom: `roksbnkctl test connectivity` reports `Get "https://...": dial tcp: i/o timeout` for an internal-only URL

**Root cause**: connectivity probes run from `--backend local` by default. From your laptop, internal-only URLs (cluster-private VIPs, internal GSLB names) aren't reachable.

**Fix**: route the probe through the cluster's network — either via `--backend k8s` (when it lands for the connectivity suite — currently k8s-backend is iperf3 + DNS only; connectivity stays local for v1.0) or via an SSH target inside the cluster's VPC (`--backend ssh:cluster-jumphost`).

### Symptom: `roksbnkctl test connectivity` fails with `x509: certificate signed by unknown authority` against a self-signed internal endpoint

**Root cause**: the URL's TLS cert isn't in the host's trust store.

**Fix**: pass `--insecure` (session-wide; skips TLS validation for every probe in the run). The flag is deliberately session-wide rather than per-host — see [Chapter 20 §"Mixed TLS-trust posture"](./20-connectivity-testing.md). For mixed trust posture across multiple internal endpoints, run two separate `test connectivity` invocations, one per trust group.

## CI-specific

### Symptom: nightly e2e run fails on phase D with `Error: Provider configuration is missing`

**Root cause**: a `terraform init` cache invalidation under `~/.roksbnkctl/<ws>/state/.terraform/` left a partial provider download. Happens after a CI worker is recycled mid-init.

**Fix**: `rm -rf ~/.roksbnkctl/<ws>/state/.terraform/` then re-run `roksbnkctl up`. Terraform-init re-downloads the providers cleanly. For CI workers that get recycled often, add a pre-step that purges `.terraform/` before each run.

### Symptom: cred audit (phase M) reports `IBMCLOUD_API_KEY found in docker inspect output`

**Root cause**: real stop-ship — credentials leaked into a docker container's runtime env. Check `internal/exec/docker.go::buildEnvArgv` for any code path that passes the credential by value (`-e IBMCLOUD_API_KEY=<value>`) rather than by reference (`-e IBMCLOUD_API_KEY` — let docker pull from the caller's env).

**Fix**: file an issue immediately, do not tag a release until this is green. Phase M is the v1.0 release gate; a leak here means the redactor or the cred-passing logic regressed. See [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) for the threat model.

## Getting more help

When the symptom isn't on this page:

1. Re-run with `--verbose` (`-v`) — the verbose output usually surfaces the root cause directly.
2. Check `/tmp/roksbnkctl-e2e-backends/<phase>-<ts>.log` for the per-phase trail.
3. Cross-reference [Chapter 23 — The E2E test plan](./23-e2e-test-plan.md) — the phase-by-phase pass criteria usually narrow down where the breakage lives.
4. File an issue on [github.com/jgruberf5/roksbnkctl](https://github.com/jgruberf5/roksbnkctl/issues) with the verbose output, the `roksbnkctl --version` stamp, and the per-phase log if there is one.
