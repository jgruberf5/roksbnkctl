# What roksbnkctl does (and doesn't do)

`roksbnkctl` is a single-binary CLI for deploying and validating F5 BIG-IP Next for Kubernetes (BNK) onto IBM Cloud ROKS. It exists to compress a multi-step deployment — clone the right Terraform, configure it, run terraform, fetch a kubeconfig, install BNK, run smoke tests — into three commands.

This chapter is about scope. What `roksbnkctl` owns, what it deliberately does not own, and what's coming in future releases. Read it before you reach for the tool to do something it isn't trying to do.

## The 3-command happy path

The everyday user-facing flow is three commands:

```bash
roksbnkctl init        # answer a few prompts about region, RG, cluster name
roksbnkctl up          # terraform plan + apply (~50 min for fresh ROKS + BNK)
roksbnkctl test        # connectivity + DNS + throughput against the deployment
```

That's it. From "I have an IBM Cloud API key" to "deployed BNK with a passing throughput test" with no manual `terraform apply`, no hand-editing kubeconfig paths, no chasing down BNK Helm charts.

When the deployment is done you tear it back down with:

```bash
roksbnkctl down
```

[Chapter 7](./07-quick-start.md) walks through this end-to-end with sample output.

## What roksbnkctl owns

`roksbnkctl`'s scope is everything between "you have an IBM Cloud API key" and "you have a working BNK install you can run tests against". Concretely:

- **Workspace state** — kubectl-style per-environment isolation under `~/.roksbnkctl/<workspace>/`. Each workspace has its own config, terraform state, kubeconfig, scratch artefacts. Switch with `roksbnkctl ws use <name>` or override per-command with `-w <name>`.
- **Terraform-exec orchestration** — wraps HashiCorp's `terraform-exec` library to drive `terraform init/plan/apply/destroy` with the right state file, the right `TF_DATA_DIR`, the right tfvars layering. You don't run `terraform` directly; `roksbnkctl up` does.
- **Kubeconfig fetch** — after a successful `up`, fetches the admin kubeconfig from IBM Cloud's container service API and writes it to `~/.kube/config` at mode 0600. Retries on the 404s that happen during cluster propagation lag.
- **COS supply chain** — the BNK install needs FAR images and JWT licenses staged in IBM Cloud Object Storage. `roksbnkctl cos instance/bucket/object` handles instance creation, bucket lifecycle, and streaming object I/O (multipart for large files) without making you `pip install` the IBM COS SDK separately.
- **Post-deploy validation** — `roksbnkctl test` runs three suites: HTTP/HTTPS connectivity (built-in `net/http`, no external `curl`), DNS resolution (built-in `net.Resolver`, no external `dig`), and iperf3 throughput (deploys an `iperf3 -s` pod into the cluster, runs the client, parses JSON output, tears down).
- **Credentials handling** — IBM Cloud API key resolution chain: env vars (`IBMCLOUD_API_KEY` etc.), OS keychain (macOS Keychain / libsecret / Windows Credential Manager via `zalando/go-keyring`), opt-in base64 in workspace config, interactive prompt as last resort. Plaintext keys in `config.yaml` are rejected.

If any of those words don't make sense yet, don't worry — later chapters cover each in depth.

## What roksbnkctl does *not* try to do

Equally important: the explicit non-goals. `roksbnkctl` deliberately stays out of these spaces because well-established tools already cover them:

- **Not a generic IBM Cloud CLI.** That's `ibmcloud`. If you want to manage VPCs, IAM policies, classic infrastructure, Watson, or any of the hundred-plus other IBM Cloud services, use `ibmcloud`. `roksbnkctl ibmcloud <args...>` exists as a convenience passthrough that loads workspace credentials, but it doesn't try to replace `ibmcloud`'s surface.
- **Not a generic Kubernetes CLI.** That's `kubectl`. `roksbnkctl kubectl <args...>` is again a passthrough that loads the workspace's kubeconfig; it does not try to be a kubectl re-implementation. (Phase 2 internalises a small subset — `roksbnkctl k get/apply/logs/exec/port-forward` — so the happy path doesn't require a host `kubectl` binary, but that's targeted convenience, not replacement.)
- **Not an OpenShift admin tool.** That's `oc`. Same story: `roksbnkctl oc <args...>` passthrough, no attempt to re-implement.
- **Not a BNK runtime UI.** Once BNK is deployed, you configure it through its CRDs (`F5BigIpCtx`, `F5IngressTls`, etc.). `roksbnkctl` doesn't ship a TUI / web UI for editing those — it gets you to a deployed BNK and steps out of the way.
- **Not a Terraform authoring tool.** The HCL lives in this repo's `terraform/` directory and is embedded into the binary at build time. `roksbnkctl` runs that HCL; it doesn't help you write more of it. If you fork the HCL, point `roksbnkctl` at your fork via `tf_source: github` or `tf_source: local`.
- **Not an arbitrary workload deployer.** BNK is the workload. The iperf3 / nginx fixtures used by `roksbnkctl test` exist only to validate BNK; they're not a general-purpose deployment surface.

The principle is "do one thing well". `roksbnkctl` does BNK-on-ROKS lifecycle and validation. Every other concern is delegated to the right purpose-built tool.

## The relationship to bundled HCL

A core design decision worth surfacing: the Terraform that drives the deployment lives **in this repo** under `terraform/`, and is embedded into the `roksbnkctl` binary at build time via Go's `embed` package.

This means:

- **One install** gets you the CLI + a matched HCL pair. No "clone the right tag of the terraform repo separately" step.
- **Versioning is unified.** A `roksbnkctl v0.7` release ships with a specific snapshot of the HCL. Upgrading the binary upgrades the HCL atomically. There's no skew between "binary version" and "Terraform version".
- **Power users can override.** The workspace config has a `tf_source:` block:

  ```yaml
  tf_source:
    type: embedded     # default; uses HCL bundled into the binary
    # type: local
    # path: /path/to/your/terraform
    # type: github
    # repo: yourfork/roksbnkctl-terraform
    # ref: my-branch
  ```

  `tf_source: local` is the right setting if you're iterating on the HCL itself. `tf_source: github` lets you point at a fork of the terraform repo if you've published one separately. The default — `embedded` — covers the everyday case.

[Chapter 13](./13-terraform-variables.md) covers the tfvars layering rules; this is just the elevator pitch for "the HCL ships with the binary".

## What's coming in future releases

This is the v0.7 surface. The roadmap to v1.0 has a few significant pieces still to land. Brief preview so you know what to expect:

- **v0.8 — kubectl internalisation.** `roksbnkctl k get/apply/logs/exec/port-forward` becomes a first-class verb that talks to the cluster directly via `client-go`, with no host `kubectl` binary required. The `kubectl` passthrough remains as an escape hatch for advanced flags. After v0.8, the only required prereq on PATH is `terraform`.
- **v0.9 — four execution backends.** Every external tool (`ibmcloud`, `iperf3`, `terraform`) becomes selectable across `local | docker | k8s | ssh` backends via `--backend`. You'll be able to run iperf3 entirely in-cluster without installing it on the host, run ibmcloud in a Docker container with a pinned version, or proxy any tool through a jumphost. [Chapter 17](./17-execution-backends.md) covers the user-facing surface; the design rationale lives in [PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md).
- **v0.9 — GSLB-aware DNS testing.** The DNS probe becomes [miekg/dns](https://github.com/miekg/dns)-based with multi-vantage support, so you can verify that BNK's GSLB is returning different answers from different network locations.
- **v1.0 — book launched, full E2E test plan green, dogfood feedback integrated.** The `https://jgruberf5.github.io/roksbnkctl/book/` site (this book) ships with all 32 chapters polished, every code example verified, diagrams in place.

The book follows the code: each sprint adds chapters for what just landed. By the time you read this in production, the chapter list reflects everything the binary at that version supports.

## Pointers to the next chapters

- [Chapter 4 — Installation](./04-installation.md) gets the binary on your machine.
- [Chapter 7 — Quick start](./07-quick-start.md) walks the 3-command happy path with sample output.
- [Chapter 16 — The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md) covers the v0.7-flagship feature: running passthrough commands over SSH against an auto-discovered jumphost, useful in customer-firewalled and air-gapped scenarios.
