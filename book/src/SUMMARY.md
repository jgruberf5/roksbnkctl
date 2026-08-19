# Summary

[Preface](./preface.md)

# Part I — Concepts

- [What is BIG-IP Next for Kubernetes (BNK)](./01-what-is-bnk.md)
- [Why ROKS (Red Hat OpenShift on IBM Cloud)](./02-why-roks.md)
- [What roksbnkctl does (and doesn't do)](./03-what-roksbnkctl-does.md)

# Part II — Getting Started

- [Installation](./04-installation.md)
- [Doctor: checking your environment](./05-doctor.md)
- [Workspaces](./06-workspaces.md)
- [Quick start: from API key to deployed BNK](./07-quick-start.md)
- [Unattended setup: file, URL, or environment](./07a-unattended-setup.md)
- [GitHub Actions CI as an example](./07b-github-actions-ci.md)

# Part III — Cluster Lifecycle

- [The cluster phase (cluster up/down)](./08-cluster-phase.md)
- [The three-phase lifecycle (Cluster / BNK / Testing)](./08a-three-phase-lifecycle.md)
- [Registering an existing cluster](./09-registering-existing-cluster.md)
- [Sharing a Transit Gateway across clusters](./09a-transit-gateway-sharing.md)
- [Deploying BNK trials on top](./10-deploying-bnk-trials.md)
- [Air-gapped install: mirroring BNK into a private registry](./10a-air-gapped-install.md)
- [Registry targets: ICR and Artifactory](./10b-registry-targets.md)
- [Licensing BNK with the F5 License Proxy (FLP)](./10c-flp-licensing.md)
- [Tearing down](./11-tearing-down.md)

# Part IV — Configuration

- [Workspace config (config.yaml)](./12-workspace-config.md)
- [Remote state (COS / S3)](./12a-remote-state.md)
- [Terraform variables (terraform.tfvars)](./13-terraform-variables.md)
- [Credentials and the resolver chain](./14-credentials-resolver.md)
- [SSH targets](./15-ssh-targets.md)

# Part V — Remote Execution

- [The --on flag and SSH jumphosts](./16-on-flag-ssh-jumphosts.md)
- [Execution backends: local, docker, k8s, ssh](./17-execution-backends.md)
- [Choosing a backend per tool](./18-choosing-backend.md)
- [The in-cluster ops pod](./19-in-cluster-ops-pod.md)

# Part VI — Testing

- [Connectivity testing](./20-connectivity-testing.md)
- [DNS testing for GSLB](./21-dns-testing-gslb.md)
- [Throughput testing](./22-throughput-testing.md)
- [The performance matrix](./22a-performance-matrix.md)
- [The E2E test plan](./23-e2e-test-plan.md)

# Part VII — Operations

- [Day-2 ops: status, logs, k get/apply/exec](./24-day-2-ops.md)
- [Registering the cluster with BNK Forge](./24a-bnk-forge-registration.md)
- [COS supply chain management](./25-cos-supply-chain.md)
- [Troubleshooting](./26-troubleshooting.md)

# Part VIII — Reference

- [Command reference](./27-command-reference.md)
- [Configuration reference](./28-configuration-reference.md)
- [Terraform variable reference](./29-terraform-variable-reference.md)
- [Glossary](./30-glossary.md)

# Part IX — Contributing

- [Building from source](./31-building-from-source.md)
- [Extending roksbnkctl](./32-extending-roksbnkctl.md)

# Part X — Agentic mode

- [Agentic mode](./33-agentic-mode.md)

# Appendices

- [A — The four cluster topologies, end to end](./appendix-a-disconnected-roks-cluster.md)
- [B — Replicating FAR into an existing registry](./appendix-b-artifactory-mirror.md)
