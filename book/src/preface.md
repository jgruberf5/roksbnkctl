# Preface

## How to read this book

This book is the canonical user-facing documentation for `roksbnkctl`, a single-binary CLI for deploying and validating F5 BIG-IP Next for Kubernetes (BNK) on top of IBM Cloud Red Hat OpenShift Kubernetes Service (ROKS).

## Who this book is for

- **BNK evaluators** kicking the tires on F5 BIG-IP Next for Kubernetes who want a low-friction path to a working trial deployment.
- **F5 sales engineers (SEs)** who need a repeatable demo and proof-of-concept toolchain for customer engagements.
- **Customer engineers** standing up BNK in their own IBM Cloud account, either for evaluation or as the foundation of a production rollout.

## Linear vs reference

The book is organized so it can be read either way:

- **Linear**: Parts I-VII walk the reader from concepts through their first deployment, day-2 operations, and the built-in test suite. New users should read in order.
- **Reference**: Part VIII (Command reference, Configuration reference, Terraform variable reference, Glossary) is exhaustive and indexed for lookups during day-to-day work.

Part IX is for contributors who want to build `roksbnkctl` from source or extend it.

## Prerequisites

This book assumes:

- Basic familiarity with **IBM Cloud** — you have an account, you know what an API key is, and you've used the IBM Cloud console at least once.
- Basic familiarity with **Kubernetes** — you know what a pod, service, and namespace are; you've run `kubectl` (or `oc`) before.
- A working terminal on Linux or macOS. Windows is supported for `roksbnkctl` itself, with documented limitations around interactive SSH.

You do **not** need prior experience with Terraform, OpenShift, or F5 BIG-IP Next — the tool and this book introduce them as needed.
