# What is BIG-IP Next for Kubernetes (BNK)

F5 BIG-IP Next for Kubernetes (BNK) is F5's containerised, Kubernetes-native re-imagining of the BIG-IP data plane. It runs the BIG-IP Traffic Management Microkernel (TMM) as pods inside a cluster, and exposes its configuration surface through Custom Resources rather than the classic TMSH / iControl REST APIs. The point of BNK is to give Kubernetes workloads the L4 and L7 traffic management features F5 customers already rely on — advanced load balancing, TLS termination, WAF policy, GSLB — without bolting an external appliance onto the cluster's edge.

This chapter sets the context for the rest of the book. If you already deploy and operate BNK day-to-day you can skim it; if you arrived here knowing generic Kubernetes but new to F5's product family, read it first.

## Where BNK fits in F5's product family

F5 has historically delivered traffic management as the **BIG-IP** appliance: a hardened Linux box (physical or virtual) running TMOS, with TMM as the data-plane kernel module. BIG-IP works extremely well at the cluster edge — north-south traffic — but it sits *outside* the cluster and is configured through its own control surface.

The next-generation lineage is **BIG-IP Next**: the same TMM data plane refactored to run as a regular Linux process, configurable through declarative APIs instead of imperative TMSH. BIG-IP Next ships in three deployment shapes:

- **BIG-IP Next for VMs / Bare Metal** — same form factor as classic BIG-IP, modernised control plane.
- **BIG-IP Next Service Proxy for Kubernetes (SPK)** — telco-focused, for 5G core workloads.
- **BIG-IP Next for Kubernetes (BNK)** — general-purpose, runs inside any conformant Kubernetes cluster.

BNK is the focus of this book. It is the option you pick when your workloads already live in Kubernetes and you want F5 traffic management without standing up a separate appliance fleet.

## What problems BNK solves

A standard Kubernetes cluster ships with a basic Service / Ingress story: kube-proxy iptables rules, a community ingress controller, maybe an external load balancer in front. That covers the common case but falls short when you need:

- **Real L7 traffic management for north-south traffic** — fine-grained routing, header manipulation, TLS termination with custom cipher suites, mTLS enforcement, advanced HTTP/2 + HTTP/3 handling, WAF policy enforcement at the edge.
- **East-west service mesh-style features without a sidecar** — connection pooling, circuit breaking, retries, observability for pod-to-pod traffic, applied at a per-namespace or per-workload granularity.
- **GSLB-style global traffic management** — health-checked DNS responses that send a client to the nearest healthy cluster, integrated with the cluster's own service health.
- **Compliance and regulated workloads** — DDoS mitigation, behavioural anomaly detection, audit logging that an enterprise security team will accept.

BNK delivers all of the above as cluster-native primitives. You install it once, and from then on you express traffic management intent through CRDs (`F5BigIpCtx`, `F5IngressTls`, `F5GslbPool`, etc.) committed alongside your application manifests.

## The components

BNK isn't a single binary; it's a set of cooperating components installed into a cluster. The pieces you'll see most often:

- **TMM (Traffic Management Microkernel)** — the data plane. Runs as DaemonSet pods on dedicated worker nodes. Every packet handled by BNK passes through TMM.
- **FLO (F5 Lifecycle Operator)** — the control-plane operator. Watches BNK CRDs and reconciles them into TMM data-plane configuration. Owns the lifecycle of the TMM pods themselves: image pulls, version upgrades, rolling restarts.
- **CIS (Container Ingress Services)** — Kubernetes-native ingress controller piece. Watches `Ingress` and BNK ingress CRDs, programmes TMM to terminate the corresponding traffic.
- **CNE Instance** — Cloud-Native Edge configuration, the umbrella resource that ties a BNK install to its tenant context.
- **Cert-Manager** — not strictly an F5 component, but a hard dependency. BNK uses cert-manager to mint and rotate the certificates TMM presents to clients.

Deeper chapters reference these names; you don't need to memorise them now. The thing to take away is that BNK is an operator + DaemonSet pattern: the operator (FLO) reconciles your declarative intent into running data-plane pods (TMM).

## Where BNK runs

BNK runs on a **conformant Kubernetes cluster**. F5 publishes a support matrix — read it for definitive answers — but in practice you'll see BNK deployed on:

- **Managed Kubernetes**: ROKS (IBM Cloud's managed OpenShift), OpenShift Dedicated, EKS, AKS, GKE.
- **Self-managed OpenShift** on bare metal or VMs.
- **Upstream Kubernetes** in private clouds, with an LB provider that BNK can integrate with.

This book targets **ROKS specifically**. The next chapter explains why. The decisions and patterns documented here will translate to other Kubernetes flavours, but the bundled Terraform that `roksbnkctl` ships only knows how to provision ROKS.

## North-south and east-west, in one install

It's worth calling out explicitly: BNK is not "just an ingress controller" and it's not "just a service mesh data plane". It's both, in the same install:

- **North-south** (client outside the cluster talking to a workload inside) — BNK fronts a `LoadBalancer`-typed service, terminates TLS, applies WAF policy, routes to backend pods. Replaces the role a hardware BIG-IP or community ingress controller would play.
- **East-west** (pod-to-pod or namespace-to-namespace inside the cluster) — BNK can be inserted into the path with no application sidecar, providing per-workload connection pooling, retries, and observability.

A single BNK install can handle both at once. Customer architectures often start with the north-south story (the obvious replacement for an existing BIG-IP appliance), then expand into east-west as the team gets comfortable with the operator-driven configuration model.

## Pointer to F5's official docs

Everything in this chapter is intentionally a sketch — enough to make the rest of this book legible. For definitive and up-to-date product information, including the full CRD reference, version compatibility matrix, sizing guidance, and license model, see F5's official BNK documentation: <https://clouddocs.f5.com/bigip-next/latest/>.

The rest of this book focuses on deploying BNK with `roksbnkctl` and validating that the deployment works end-to-end. It does not duplicate F5's product documentation; it complements it.

For an at-a-glance view of how `roksbnkctl`'s components fit together — the four execution backends, the cluster, the jumphost, the IBM Cloud control plane — see the architecture diagram at the top of [Chapter 17 — Execution backends](./17-execution-backends.md). For the happy-path lifecycle from one command to the next, see [Chapter 7 — Quick start](./07-quick-start.md).
