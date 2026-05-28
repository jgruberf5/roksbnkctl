# Design notes

These documents capture the design rationale behind major subsystems of `awsbnkctl`. They are written as Product Requirement Documents (PRDs) — drafted before implementation, then updated with the architectural decisions made during the build.

Read these if you want to understand **why** the code is shaped the way it is, not just what it does.

## Contents

| Spec | Subject | Status |
|---|---|---|
| [`specs/04-CREDENTIALS.md`](specs/04-CREDENTIALS.md) | Credential propagation: AWS standard chain + IRSA | stable |
| [`specs/08-S3-SUPPLY-CHAIN-IRSA.md`](specs/08-S3-SUPPLY-CHAIN-IRSA.md) | S3 supply chain + IRSA for FAR pulls | stable |
| [`specs/09-SCENARIOS-FRAMEWORK.md`](specs/09-SCENARIOS-FRAMEWORK.md) | Built-in traffic validation framework | stable |
| [`specs/10-DEMO-EXPERIENCE.md`](specs/10-DEMO-EXPERIENCE.md) | `up --demo` + the launch renderer + `demo run` | shipped |

Each spec follows the same shape: **why** (the gap or problem), **scope** (in / out), **decomposition** (slice breakdown), and **acceptance criteria**.

## Architectural context

- **[`docs/POST_TERRAFORM_DIRECTION.md`](../POST_TERRAFORM_DIRECTION.md)** — the project-wide architectural pivot from Terraform-driven to AWS-SDK-driven provisioning. Read this first for the "why" behind the current code structure.
- **[`docs/FORGE_MCP_INTEGRATION.md`](../FORGE_MCP_INTEGRATION.md)** — the forge handoff design (write-only, register on EKS-Active, soft-fail with retry).
- **[`docs/upstream-issues/`](../upstream-issues/)** — known issues + workarounds in upstream BNK (with reproduction steps).

## History

Earlier PRDs covering the pre-pivot Terraform-embedded design (SSH-on-flag, kubectl-internalisation, execution backends, cluster/trial phase split, EKS+SR-IOV) are preserved in git history. They describe a previous shape of the tool and are no longer reflective of the current codebase.
