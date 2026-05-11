# Glossary

Plain-English definitions of the terms used across the book. Project-specific concepts, IBM-Cloud-specific products, OpenShift / Kubernetes admission concepts, and the F5 BIG-IP Next networking vocabulary all live here. Entries are deliberately one or two sentences; the deep-dive lives in the linked chapter where applicable.

## A — D

**`api_key_b64`**
Base64-encoded IBM Cloud API key stored inline in the workspace `config.yaml`. **Obfuscation, not encryption** — anyone with file-read access decodes instantly. The field name deliberately doesn't match the plaintext-secret rejection regex. See [Chapter 14 §"Source 3"](./14-credentials-resolver.md#source-3--workspace-api_key_b64).

**Backend** (`--backend`)
The execution context for a tool dispatch. One of `local` (os/exec on the host), `docker` (containerised), `k8s` (in-cluster ops pod or Job), or `ssh:<target>` (a registered SSH endpoint). See [Chapter 17](./17-execution-backends.md).

**BNK**
BIG-IP Next for Kubernetes. F5's Kubernetes-native CNF deployment of BIG-IP, made up of FLO (F5 Lifecycle Operator) + CNE Instance + License + CIS. The reason this CLI exists. See [Chapter 1](./01-what-is-bnk.md).

**CIS**
*Two unrelated CISes appear in this stack.* Inside the cluster, **CIS** is F5's **Container Ingress Services** — the F5 controller that watches Kubernetes Ingress + Route resources and programs the BIG-IP data plane. At the IBM Cloud account level, **CIS** is **Cloud Internet Services** — IBM's DNS, CDN, WAF, and DDoS-protection product. Context disambiguates; when in doubt, "F5 CIS" vs "IBM Cloud CIS".

**ClusterIP** (k8s)
A `Service` type that gives a Service an internal cluster IP, reachable only from inside the cluster. Used by the throughput suite's `east-west` mode.

**ClusterRole** (k8s)
A cluster-scoped RBAC role granting verbs on resources. The ops pod's least-privilege ClusterRole grants `jobs.create` in `roksbnkctl-test` but not `pods.delete` in `default`.

**CNE Instance**
A Custom Resource defined by FLO. Represents one deployed instance of the BNK data plane (TMM pods + control plane). Sizing is `Small`/`Medium`/`Large`. See [Chapter 10](./10-deploying-bnk-trials.md).

**Cobra**
[`github.com/spf13/cobra`](https://github.com/spf13/cobra) — the Go CLI library `roksbnkctl` is built on. The command tree at [`internal/cli/`](https://github.com/jgruberf5/roksbnkctl/tree/main/internal/cli) is a cobra command tree.

**COS**
IBM **Cloud Object Storage** — S3-compatible object store. The BNK supply chain bucket lives on a COS instance. See [Chapter 25](./25-cos-supply-chain.md).

**`cred resolver chain`**
The ordered lookup for `IBMCLOUD_API_KEY`: env var → OS keychain → workspace `api_key_b64` → interactive prompt. The chain stops at the first source that yields a non-empty value. See [Chapter 14](./14-credentials-resolver.md#the-ibmcloud_api_key-resolver-chain).

**CRN**
**Cloud Resource Name** — IBM's globally-unique resource identifier. Starts with `crn:v1:` and encodes account, region, service, and resource ID. Most `roksbnkctl cos` commands accept either a friendly name (resolved at runtime) or a CRN.

## E — J

**east-west**
Network direction term: traffic between two endpoints **inside** the cluster (pod-to-pod, service-to-service). The throughput suite's `--mode east-west` measures CNI fabric throughput. See [Chapter 22](./22-throughput-testing.md#--mode-east-west).

**Embedded HCL**
The Terraform source tree compiled into the `roksbnkctl` binary via Go's `//go:embed` directive. The default `tf_source` is `embedded`. Rebuilding the binary picks up HCL changes. See [Chapter 31 §"The embedded HCL"](./31-building-from-source.md#the-embedded-hcl).

**`envFrom`** (k8s)
A Pod spec field that references a Secret or ConfigMap and projects all of its keys as environment variables into the container. The k8s backend's ops pod uses `envFrom: secretRef: roksbnkctl-ibm-creds` to receive the API key without listing it in the manifest plaintext.

**`extra_hosts`**
The workspace config's list of additional URLs to probe under `roksbnkctl test connectivity`. In v1.0 the value is a bare `[]string` of URLs; per-host method/expected-status overrides are deferred. See [Chapter 20](./20-connectivity-testing.md).

**FAR**
**F5 Application Runtime** — the container-image distribution of the BIG-IP Next data plane. FLO pulls FAR images from `repo.f5.com` using the auth key in the COS supply-chain bucket.

**FLO**
**F5 Lifecycle Operator** — the Kubernetes operator that owns the CNE Instance + License + supporting resources. The control plane piece of BNK. (The acronym sometimes also surfaces as "F5 Logging Operator" — context disambiguates; in this book it always means Lifecycle Operator.)

**FQDN**
Fully Qualified Domain Name — the absolute form of a DNS name ending with a trailing dot (`www.example.com.`).

**FAR auth key**
The credential tarball (`f5-far-auth-key.tgz`) that FLO uses to pull FAR images from `repo.f5.com`. Lives in the COS supply-chain bucket. Rotated periodically; see [Chapter 25 §"Licence rotation"](./25-cos-supply-chain.md#licence-rotation).

**ghcr.io**
GitHub Container Registry — where the `roksbnkctl-tools-*` images are published. The k8s backend pulls from `ghcr.io/jgruberf5/roksbnkctl-tools-{ibmcloud,iperf3}`.

**GSLB**
**Global Server Load Balancing** — DNS-driven traffic management where the answer a name returns depends on the requesting resolver's network vantage. The thing [Chapter 21](./21-dns-testing-gslb.md) is built to validate.

**`--gslb-compare`**
The DNS-probe flag that fans out across all configured backends in parallel and emits a comparison JSON with `gslb_divergence: true|false`. The signature workflow for "is the GSLB rule taking effect". See [Chapter 21 §"The --gslb-compare workflow"](./21-dns-testing-gslb.md#the---gslb-compare-workflow).

**HCL**
HashiCorp Configuration Language — the syntax of Terraform `.tf` files. The upstream HCL is bundled into the binary; see *Embedded HCL*.

**`ibmcloud`**
*Two senses.* The IBM Cloud CLI binary (which `roksbnkctl ibmcloud …` passes through to or replaces, depending on the backend). And the YAML block in `config.yaml` (`ibmcloud:`) holding region, resource group, and API key source.

**`ImagePullBackOff`** (k8s)
A Pod status indicating the image couldn't be pulled from the registry. Usually a network or auth problem; sometimes a tag-doesn't-exist problem. See [Chapter 26 §"ImagePullBackOff…"](./26-troubleshooting.md#symptom-imagepullbackoff-on-the-ops-pod-or-throughput-pod).

**`iperf3 mode`** (`--mode`)
The throughput-suite flag selecting `north-south` (LoadBalancer Service, client outside the cluster) or `east-west` (ClusterIP Service, client inside the cluster). See [Chapter 22 §"The two modes"](./22-throughput-testing.md#the-two-modes).

**JWT**
**JSON Web Token** — the signed-token format BNK uses for the subscription licence (`trial.jwt` in the COS supply-chain bucket).

## K — N

**`k`** (`roksbnkctl k <verb>`)
The internalised kubectl subtree. `roksbnkctl k get/apply/describe/delete/exec/logs/port-forward` — built on `k8s.io/client-go` directly so no host `kubectl` binary is required. See [Chapter 24](./24-day-2-ops.md).

**`kubeconfig`**
The Kubernetes client-configuration file (clusters, contexts, credentials). Defaults to `~/.kube/config`. `roksbnkctl up` auto-fetches the admin kubeconfig post-apply.

**LoadBalancer** (k8s Service type)
A Service type that provisions an external endpoint (a cloud LB on managed Kubernetes; an external IP on bare-metal CNI). Used by the throughput suite's `north-south` mode and by BNK's exposed VIPs.

**Long-lived ops pod**
The k8s backend's persistent execution context. Deployed by `roksbnkctl ops install`; subsequent `--backend k8s` dispatches `kubectl exec` into the same pod rather than starting a fresh Pod each call. Contrasted with the *one-shot Job pattern* used for iperf3 and DNS probes. See [Chapter 19](./19-in-cluster-ops-pod.md).

**Manifest version** (`f5_bigip_k8s_manifest_version`)
The version pin on the f5-bigip-k8s-manifest Helm chart. Transitively pins both the FLO and CIS versions (both are extracted from the manifest chart). See [Chapter 13](./13-terraform-variables.md).

**mdBook**
[rust-lang/mdBook](https://rust-lang.github.io/mdBook/) — the static-site generator the book is built with. Markdown source under `book/src/`, HTML output under `book/book/`. See [Chapter 31 §"The book build"](./31-building-from-source.md#the-book-build).

**miekg/dns**
[github.com/miekg/dns](https://github.com/miekg/dns) — the Go DNS library the GSLB probe is built on. Same library CoreDNS uses; gives `roksbnkctl test dns` full record-type coverage and per-query server selection. See [Chapter 21 §"The roksbnkctl test dns flag surface"](./21-dns-testing-gslb.md#the-roksbnkctl-test-dns-flag-surface).

**north-south**
Network direction term: traffic crossing the cluster boundary — from outside the cluster *to* a pod inside, or vice versa. The throughput suite's `--mode north-south` measures inbound LoadBalancer-path throughput. See [Chapter 22](./22-throughput-testing.md#--mode-north-south).

**NXDOMAIN**
DNS response code indicating "this name does not exist". `roksbnkctl test dns` against a non-existent name exits 1 with rcode=`NXDOMAIN`.

## O — R

**`--on <target>`**
The persistent CLI flag dispatching an `ibmcloud`/`exec`/`shell`/`kubectl`/`oc` passthrough over SSH to a named target instead of running it locally. The other half of the SSH-client + `--on` feature alongside the *SSH backend*. See [Chapter 16](./16-on-flag-ssh-jumphosts.md).

**OpenShift**
Red Hat's enterprise Kubernetes distribution. ROKS = managed OpenShift on IBM Cloud.

**Ops pod**
Shorthand for the long-lived k8s-backend execution pod deployed in the `roksbnkctl-ops` namespace by `roksbnkctl ops install`. See [Chapter 19](./19-in-cluster-ops-pod.md).

**`passthrough`**
A command that proxies its argv to an underlying tool. `roksbnkctl ibmcloud …` passes through to the `ibmcloud` CLI; `roksbnkctl kubectl …` passes through to `kubectl`. Passthroughs run on whatever backend is selected (local by default).

**PRD**
**Product Requirements Document**. The project uses numbered PRDs under [`docs/prd/`](https://github.com/jgruberf5/roksbnkctl/tree/main/docs/prd) to coordinate larger feature work. See [Chapter 32 §"The PRD process"](./32-extending-roksbnkctl.md#the-prd-process).

**`PHASE_FROM=`**
The env-var resume mechanism on the e2e driver scripts. `PHASE_FROM=L ./scripts/e2e-test-backends.sh` fast-forwards past phases A-K. See [Chapter 23 §"Resuming a partial run"](./23-e2e-test-plan.md#resuming-a-partial-run).

**RBAC**
**Role-Based Access Control** — the Kubernetes authorization model. The ops pod has a least-privilege RBAC binding; see *ClusterRole*.

**`restricted-v2`**
The default OpenShift `PodSecurity` policy / SCC at admission. Rejects pods that run as root, allow privilege escalation, or hold the `ALL` capability set. All `roksbnkctl`-managed pods (ops pod, iperf3 server, DNS probe Job) are written to satisfy `restricted-v2`. See [Chapter 22 §"The bundled image and the runAsNonRoot constraint"](./22-throughput-testing.md#the-bundled-image-and-the-runasnonroot-constraint).

**redactor**
The output-stream wrapper at [`internal/exec/redact.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/redact.go) that masks the IBM API key value in any subprocess's stdout/stderr before it reaches the user's terminal or the log. The defence-in-depth net for credential leaks. See [Chapter 14 §"The redactor"](./14-credentials-resolver.md#the-redactor).

**ROKS**
**Red Hat OpenShift on IBM Cloud** — IBM's managed OpenShift offering. The cluster `roksbnkctl up` provisions. See [Chapter 2](./02-why-roks.md).

**`runAsNonRoot`**
A Pod / container `securityContext` field. Required `true` by `restricted-v2`. Images that have `USER root` in the Dockerfile fail admission with this set.

**RTT**
**Round-Trip Time** — measured in milliseconds for each DNS query. `roksbnkctl test dns -o json` surfaces p50/p95/p99 across the run.

## S — Z

**Schematic JSON**
The deployer-rendered JSON document describing a BNK deployment. Lives in the COS supply-chain bucket; not consumed at install time, kept for forensics.

**SCC**
**Security Context Constraint** — OpenShift's pod-admission policy. `restricted-v2` is the default; pods that violate it (e.g., by running as root) are rejected by the admission controller. See [Chapter 22](./22-throughput-testing.md#the-bundled-image-and-the-runasnonroot-constraint).

**Secret** (k8s)
A namespaced resource holding key/value data, typically base64-encoded credentials. The k8s backend creates `roksbnkctl-ibm-creds` in the `roksbnkctl-ops` namespace at `ops install` time.

**`secretRef`** (k8s)
The Pod spec form that references a Secret for environment-variable projection. Used together with `envFrom` for the ops pod's credential injection.

**Service** (k8s sense)
A Kubernetes resource that provides a stable endpoint for accessing one or more Pods. Types: `ClusterIP` (default), `NodePort`, `LoadBalancer`, `ExternalName`. See *ClusterIP*, *LoadBalancer*.

**SPDY**
**Speedy** (protocol). The websocket-like, multiplexed-stream protocol Kubernetes uses for `exec` and `port-forward`. `roksbnkctl k exec` is a SPDY client implementation on top of `k8s.io/client-go`'s SPDY executor.

**SSH backend**
The `--backend ssh:<target>` execution path. Runs the tool on a registered SSH endpoint via the [`internal/remote.Client`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/remote/ssh.go) wrapper. See [Chapter 17 §"SSH backend"](./17-execution-backends.md#ssh-backend).

**TGW**
**Transit Gateway** — IBM Cloud's VPC-to-VPC connectivity service. The upstream HCL provisions a TGW between the cluster VPC and the testing-client VPC so the jumphost can reach the cluster's internal endpoints.

**`tfvars`** (`terraform.tfvars`)
Variable-value file for Terraform — assigns concrete values to the HCL's `variable` blocks. `roksbnkctl` auto-renders one from `config.yaml`; user overrides layer on top via `terraform.tfvars.user` and `--var-file`. See [Chapter 13](./13-terraform-variables.md).

**`tf_source`**
The workspace `config.yaml` block selecting where the Terraform source comes from: `embedded` (compiled into the binary; the default), `github` (downloaded tarball), `local` (an on-disk directory). See [Chapter 12 §"tf_source:"](./12-workspace-config.md#tf_source).

**TLS** (`--insecure`)
Transport Layer Security. The `--insecure` flag on `roksbnkctl test connectivity` skips TLS certificate validation for every probe in the run (session-wide, not per-host).

**TMM**
**Traffic Management Microkernel** — the BIG-IP data-plane process. BNK runs TMM as a Pod; the CNE Instance specifies how many and at what size.

**TOFU**
**Trust On First Use** — the SSH-style host-key acceptance pattern. On first connection to a new SSH target, `roksbnkctl` prompts the user to verify the fingerprint; subsequent connections check against the saved fingerprint in `~/.roksbnkctl/known_hosts`. A fingerprint mismatch refuses to connect. See [Chapter 16 §"Host-key handling"](./16-on-flag-ssh-jumphosts.md).

**Trusted Profile**
An IBM IAM construct that lets a Kubernetes ServiceAccount assume IBM Cloud permissions. FLO uses one to authenticate against IBM Cloud APIs without storing an API key in the cluster.

**TTL**
**Time To Live** — DNS-record cache duration in seconds. `roksbnkctl test dns -o json` surfaces each answer's TTL.

**v1.0**
The release this book is the launch deliverable for. All E2E phases pass on a clean dev box; doctor green-by-default with terraform-only required.

**VPE**
**Virtual Private Endpoint** — IBM Cloud's private-network access point for managed services. Sometimes left dangling after a cluster destroy (see [Chapter 26 §"orphan IBM Cloud resources"](./26-troubleshooting.md#symptom-terraform-destroy-leaves-orphan-ibm-cloud-resources-lbs-security-groups-vpes)).

**VPC**
**Virtual Private Cloud** — IBM Cloud's network-isolation primitive. The cluster lives in one VPC; the testing client jumphost lives in another, connected via TGW.

**VSI**
**Virtual Server Instance** — IBM Cloud's general-purpose VM. The jumphosts are VSIs.

**workspace**
A named slot under `~/.roksbnkctl/<name>/` containing one `config.yaml`, one Terraform state directory, and (usually) one kubeconfig. The kubectl-style multi-environment isolation primitive. See [Chapter 6](./06-workspaces.md).

**`ws` / `workspace`**
The CLI subtree managing workspaces. `roksbnkctl ws new/use/list/delete`.

## Cross-references

- [Chapter 1](./01-what-is-bnk.md) — BNK context.
- [Chapter 2](./02-why-roks.md) — ROKS context.
- [Chapter 14](./14-credentials-resolver.md) — credentials terminology.
- [Chapter 17](./17-execution-backends.md) — backend terminology.
- [Chapter 21](./21-dns-testing-gslb.md) — DNS / GSLB terminology.
- [Chapter 22](./22-throughput-testing.md) — throughput / SCC terminology.
