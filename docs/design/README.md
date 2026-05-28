# Design notes

These documents capture the design rationale behind each major subsystem of `awsbnkctl`. They are written as Product Requirement Documents (PRDs) — drafted before implementation, then updated with the architectural decisions made during the build.

Read these if you want to understand **why** the code is shaped the way it is, not just what it does.

## Contents

| Spec | Subject |
|---|---|
| [`specs/00-OVERVIEW.md`](specs/00-OVERVIEW.md) | Architectural pivot — why Terraform was removed |
| [`specs/01-SSH-AND-ON-FLAG.md`](specs/01-SSH-AND-ON-FLAG.md) | SSH-via-EICE design + the `--on` flag |
| [`specs/02-KUBECTL-INTERNAL.md`](specs/02-KUBECTL-INTERNAL.md) | The `k` subcommand — `kubectl` internalised via `client-go` |
| [`specs/03-EXECUTION-BACKENDS.md`](specs/03-EXECUTION-BACKENDS.md) | Local / Docker / k8s execution backends |
| [`specs/04-CREDENTIALS.md`](specs/04-CREDENTIALS.md) | BNK supply-chain credential resolution |
| [`specs/05-E2E-TEST-PLAN.md`](specs/05-E2E-TEST-PLAN.md) | End-to-end test strategy |
| [`specs/06-CLUSTER-TRIAL-PHASE-SPLIT.md`](specs/06-CLUSTER-TRIAL-PHASE-SPLIT.md) | Phase-graph split between cluster + trial |
| [`specs/07-EKS-CLUSTER-SRIOV.md`](specs/07-EKS-CLUSTER-SRIOV.md) | EKS cluster shape + SR-IOV considerations |
| [`specs/08-S3-SUPPLY-CHAIN-IRSA.md`](specs/08-S3-SUPPLY-CHAIN-IRSA.md) | S3 supply chain + IRSA for FAR pulls |
| [`specs/09-SCENARIOS-FRAMEWORK.md`](specs/09-SCENARIOS-FRAMEWORK.md) | Built-in traffic validation framework |
| [`specs/10-DEMO-EXPERIENCE.md`](specs/10-DEMO-EXPERIENCE.md) | `up --demo` + the launch renderer + `demo run` |

Each spec follows the same shape: **why** (the gap or problem), **scope** (in / out), **decomposition** (Architect-validated slice breakdown), and **acceptance criteria**.

## Status

These are living documents — drafts at authoring time, then updated as the Architect → Builder → Reviewer loop converges. Each spec carries a `Status:` line at the top noting whether it is *draft*, *in-flight*, or *shipped*.
