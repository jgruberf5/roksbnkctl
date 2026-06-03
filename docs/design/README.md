# Design notes

These documents capture the design rationale behind major subsystems of `awsbnkctl`.

Read these if you want to understand **why** the code is shaped the way it is, not just what it does.

## Contents

| Spec | Subject | Status |
|---|---|---|
| [`specs/09-SCENARIOS-FRAMEWORK.md`](specs/09-SCENARIOS-FRAMEWORK.md) | Built-in traffic validation framework | stable |
| [`specs/10-DEMO-EXPERIENCE.md`](specs/10-DEMO-EXPERIENCE.md) | `up --demo` + the launch renderer + `demo run` | shipped |

Each spec follows the same shape: **why** (the gap or problem), **scope** (in / out), and **acceptance criteria**.

## Architectural context

- **[`docs/ARCHITECTURE.md`](../ARCHITECTURE.md)** — how awsbnkctl provisions BNK on EKS: the AWS-SDK-driven phased model and the `cluster.yaml` intent format. Read this first for the "why" behind the current code structure.
- **[`docs/FORGE_INTEGRATION.md`](../FORGE_INTEGRATION.md)** — the forge handoff design (write-only, register on EKS-Active, soft-fail with retry).
- **[`docs/upstream-issues/`](../upstream-issues/)** — known issues + workarounds in upstream BNK (with reproduction steps).
