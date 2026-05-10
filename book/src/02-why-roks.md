# Why ROKS (Red Hat OpenShift on IBM Cloud)

This book and the `roksbnkctl` tool target **ROKS** — IBM Cloud's managed Red Hat OpenShift offering — specifically. Other Kubernetes flavours can run BNK, and most of the patterns you'll learn here translate, but the bundled Terraform that `roksbnkctl` ships only knows how to provision a ROKS cluster.

This chapter explains the rationale behind that choice. If you're already using ROKS, you can skim this. If you're evaluating whether ROKS is the right substrate for your BNK trial, read in full.

## What ROKS is

**ROKS** is short for **R**ed Hat **O**penShift on IBM Cloud. It's IBM Cloud's managed-OpenShift service: you ask IBM for a cluster, IBM provisions the masters, etcd, the OpenShift control plane, and a pool of worker nodes; you get a kubeconfig and start deploying.

ROKS clusters are **real OpenShift**. They run the same Operator Lifecycle Manager (OLM), the same `oc` CLI, the same SecurityContextConstraints (SCC) model, the same routes-and-services machinery you'd find on any OpenShift install. The only thing IBM has done differently is take responsibility for keeping the control plane and the underlying infrastructure healthy.

## What IBM manages, what you manage

The boundary between "IBM's responsibility" and "your responsibility" is the principal value proposition of any managed Kubernetes service. For ROKS the line falls roughly here:

| Concern | Owner |
|---|---|
| Master nodes (API server, scheduler, controllers) | IBM |
| etcd (persistence + backups) | IBM |
| OpenShift control plane (OLM, ingress operator, image registry) | IBM |
| OpenShift version upgrades for the control plane | IBM (you opt in to a major-version bump) |
| Worker node provisioning (VPC VSIs, subnets, security groups) | IBM, on your behalf via the cluster API |
| Worker node OS patching and CVE remediation | IBM |
| Worker pool sizing and lifecycle (`workers create/delete`) | You |
| Pod workloads running on the cluster | You |
| Application-level RBAC, network policy, TLS, service accounts | You |
| BNK install + configuration | You — this is what `roksbnkctl` automates |

The thing to internalise: with ROKS you do **not** rack hardware, install RHEL, run `openshift-install`, manage etcd backups, or chase CVE patches across a worker fleet. IBM does all of that. You start at "I have an OpenShift cluster" and go from there.

## Why managed-OpenShift over self-managed for BNK evaluation

If you want to **evaluate BNK quickly**, the calculus is straightforward. Self-managed OpenShift is a multi-week lift before you have a cluster:

- Provision the underlying VMs (OpenStack / vSphere / bare metal).
- Run `openshift-install` and debug whatever doesn't go right.
- Configure DNS, load balancers, container registry mirrors.
- Stand up monitoring + logging + cert-manager.
- Now you can start thinking about BNK.

ROKS compresses that to one Terraform `apply` of `~50` minutes. You get back a kubeconfig that authenticates against a real OpenShift cluster, with cert-manager already installable via OLM, and a worker pool of the size and zone topology you specified. From there, the BNK install is the same set of CRDs and Helm charts it would be on any OpenShift cluster.

For a sales-engineering demo or a customer proof-of-concept, "I have a cluster in 50 minutes" beats "I have a cluster in 2 weeks" every time. That trade-off is the reason this book exists in this shape.

## Why OpenShift (not just any Kubernetes) for BNK

BNK runs on conformant Kubernetes generally, but it integrates more cleanly with OpenShift specifically because:

- **Operator-driven install** — BNK is shipped as a set of operators. OpenShift has Operator Lifecycle Manager (OLM) as a first-class citizen, so the install pattern is familiar to OpenShift admins.
- **SecurityContextConstraints (SCC)** — TMM pods need elevated capabilities (notably `NET_ADMIN`, raw socket access, hugepages). OpenShift's SCC model formalises that grant; on upstream Kubernetes you'd be configuring PodSecurityAdmission policies by hand.
- **Routes** — OpenShift's `Route` CRD predates and is more capable than `Ingress`. BNK can act as an alternate Route implementation, slotting into existing OpenShift application architectures without forcing teams to migrate.
- **Image streams + the internal registry** — useful for the BNK supply chain (FAR images, license bundles) which can be mirrored once and consumed by many installs.

If you're already an OpenShift shop, BNK fits naturally. If you're not, BNK still works but you'll need to translate this book's OpenShift-specific examples (SCCs, `oc adm policy`, `Route`) to your platform's equivalents.

## What's out of scope for this book

A short list of Kubernetes flavours this book does **not** cover:

- **EKS / AKS / GKE** — BNK runs on these, but `roksbnkctl up` won't provision them. You'd use cloud-specific tooling, then deploy BNK on top with the standard Helm charts F5 publishes.
- **Self-managed OpenShift** on bare metal or VMs — same: no `roksbnkctl up`. You'd use `openshift-install`, then deploy BNK.
- **K3s, RKE2, microk8s** — BNK's not formally supported on these for production; useful for local dev work but outside this book's scope.

The patterns from later chapters — workspaces, the `--on` flag, the connectivity / DNS / throughput tests — would still be useful on any of these, but the lifecycle commands (`init`, `up`, `down`, `cluster register`) assume ROKS.

## What you need before continuing

To follow this book end-to-end you need:

- An **IBM Cloud account** with billing enabled. The free tier won't provision a worker pool; you'll need a Pay-As-You-Go or Subscription account.
- An **IBM Cloud API key** with permission to create ROKS clusters in the target account.
- A **resource group** to scope cluster resources to. The default `Default` group works fine for a single-user evaluation; production deployments tend to use a dedicated group per environment.

The next chapters walk through installation and the quick-start path. By the end of [Chapter 7](./07-quick-start.md) you'll have a deployed BNK trial on a fresh ROKS cluster.
