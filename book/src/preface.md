# Preface

## Foreword

Standing up F5 BIG-IP Next for Kubernetes (BNK) on IBM Cloud Red Hat OpenShift (ROKS) used to be a multi-step deployment that hit a different surface at every step. A `terraform init/plan/apply` against an HCL tree somebody handed you. A manual `ibmcloud ks cluster config` to pull a kubeconfig. A separate IBM Cloud CLI install with its own apt-source dance. A manual `oc adm policy add-scc-to-user privileged` to let iperf3 actually run. SSH plumbing, env-var plumbing, kubeconfig plumbing — each one a small thing, and together a half-day of yak-shaving before BNK was even on the cluster.

`roksbnkctl` collapses that into a single static binary plus four interchangeable execution backends (`local`, `docker`, `k8s`, `ssh:<target>`) plus an opt-in in-cluster ops pod. One command brings a workspace up; one command tears it down; the connectivity, DNS, and throughput tests run from whichever network vantage the question actually requires. The tool exists because the manual path has too many moving parts for somebody who just wants to evaluate BNK or run a customer demo.

This book is the user-facing documentation for `roksbnkctl`. It ships alongside the v1.0 binary.

## Who this book is for

Four audiences:

- **BNK evaluators** kicking the tires on F5 BIG-IP Next for Kubernetes who want a low-friction path to a working trial deployment.
- **F5 sales engineers (SEs)** who need a repeatable demo and proof-of-concept toolchain for customer engagements.
- **Customer engineers** standing up BNK in their own IBM Cloud account, either for evaluation or as the foundation of a production rollout.
- **Contributors** who want to extend `roksbnkctl` — add a backend, add a test suite, ship a new chapter. See [Part IX — Contributing](./32-extending-roksbnkctl.md).

## How to read this book

The book is organised so it can be read either way.

- **Linear**: Parts I-VII walk from concepts through your first deployment, day-2 operations, and the built-in test suite. New users should read in order.
- **Reference**: Part VIII (Command reference, Configuration reference, Terraform variable reference, Glossary) is exhaustive and indexed for lookups during day-to-day work. Search at the top of every page also reaches into reference material.

If you have 30 minutes and an IBM Cloud account, skip straight to [Chapter 7 — Quick start](./07-quick-start.md). It's the canonical "first cluster up" walkthrough and the rest of the book makes more sense after you've seen the happy path end-to-end.

Part IX is for contributors who want to build `roksbnkctl` from source or extend it.

## Prerequisites

This book assumes:

- Basic familiarity with **IBM Cloud** — you have an account, you know what an API key is, and you've used the IBM Cloud console at least once.
- Basic familiarity with **Kubernetes** — you know what a pod, service, and namespace are; you've run `kubectl` (or `oc`) before.
- A working terminal on Linux or macOS. Windows is supported for `roksbnkctl` itself, with documented limitations around interactive SSH (see [Chapter 16](./16-on-flag-ssh-jumphosts.md)).

You do **not** need prior experience with:

- **Terraform** — `roksbnkctl` embeds a vetted HCL tree and drives `terraform` for you. You can ignore the underlying HCL until you want to customise it ([Chapter 13](./13-terraform-variables.md)).
- **OpenShift specifics** — the tool treats ROKS as Kubernetes with a thin SCC + project overlay; the few OpenShift-specific gotchas are called out in [Chapter 22](./22-throughput-testing.md) and [Chapter 26](./26-troubleshooting.md).
- **F5 BIG-IP Next** — BNK is the thing the book deploys; you don't need to be a Big-IP engineer to evaluate it. [Chapter 1](./01-what-is-bnk.md) is the 5-minute "what is this product" primer.

## Book conventions

- **Code blocks**: shell commands use `bash` syntax highlighting; YAML snippets use `yaml`; HCL fragments use `hcl`; sample command output is shown in plain `text` blocks to distinguish output from input.
- **Cross-references**: every chapter ends with a "Cross-references" section linking related chapters. Inline links use the form `[Chapter 7 — Quick start](./07-quick-start.md)` — a chapter number, an em-dash, the chapter title, and the relative path to the chapter source.
- **PRD links**: design documents under `docs/prd/` are linked as full GitHub URLs (e.g. `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md`) so they resolve from the published book at GitHub Pages. The PRDs are the design surface; the book is the user surface — read PRDs only if you're contributing or want the *why* behind a design call.
- **Forward references to post-v1.0 work**: where a feature is explicitly queued for a v1.x release (e.g. `terraform --backend k8s`, multi-hop SSH `ProxyJump`), the prose flags it in future tense and points at [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) §"What's deliberately deferred to post-v1.0" for the roadmap.

Welcome.
