# Memory

> Shared long-term memory. Read on demand when relevant. Lead curates. Keep under 100 lines.

Last Updated: 2026-05-21

---

## Project Facts

- **Post-Terraform direction (2026-05-21):** awsbnkctl is moving off Terraform to strict Go SDK with imperative phased provisioning. aws-gpu-setup PoC is the reference for the *method* (not vendored). cluster.yaml is the intent. Variant directories under `internal/k8s/manifests/<pattern>/` hold cluster-side YAML. See `docs/POST_TERRAFORM_DIRECTION.md`.

## Decisions (Index)

- **D-001** Remove TF; provisioning is strict Go SDK with imperative phases — `.agent/DECISIONS.md`
- **D-002** State = AWS tags (truth) + local IDs cache (rebuildable) — `.agent/DECISIONS.md`
- **D-003** Intent format: `cluster.yaml` (Kubernetes-style apiVersion/kind, Go-struct-validated) — `.agent/DECISIONS.md`
- **D-004** K8s manifests in variant directories selected by `cluster.yaml: pattern:` — `.agent/DECISIONS.md`
- **D-005** SSO sentinel pattern from aws-gpu-setup ported to Go SDK middleware — `.agent/DECISIONS.md`
- **D-006** Forge register fires on EKS-Active (not end of `up`); soft-fail with retry — `.agent/DECISIONS.md`
- **D-007** Tracer-bullet first slice: cluster.yaml → tagged VPC + subnets only — `.agent/DECISIONS.md`
- **D-008** `awsbnkctl validate <cluster.yaml>` subcommand (AWS-free schema check) — `.agent/DECISIONS.md`
- **D-009** mwiget/kindbnkctl as peer-reference for *bnkctl family (forge: block + REST-fallback + scenarios pattern) — `.agent/DECISIONS.md`

## Conventions (Index)

- AWS resource tags: every awsbnkctl-created resource carries `awsbnkctl:cluster=<name>` + `awsbnkctl:component=<kind>` + `awsbnkctl:pattern=<name>` + `awsbnkctl:managed=true` + `Name`.
- Idempotency: phase functions tolerate "already gone" by swallowing service-specific NotFound codes (see `docs/POST_TERRAFORM_DIRECTION.md` §7 for the per-service list).
- K8s apply path: client-go via existing `internal/k8s/apply.go`. NO `kubectl` exec (strict Go SDK rule applies symmetrically to k8s).

## Gotchas (Index)

- SSO mid-run expiry: without the sentinel middleware (D-005), downstream phases silently no-op and produce a false-positive `up` success. Port `lib/lab-core.sh`'s `aws_q` + `check_auth_or_die` pattern into Go before shipping any multi-phase work.
- NAT GW deletion: EIP must be unassociated before release. Port `aws-gpu-setup/down.sh`'s `wait_gone` post-condition for EIP AssociationId clearing.

## Handoffs

- [handoff] 2026-05-22 05:00 — slices 01-06 shipped; slice 07 is next — `.agent/handoff/2026-05-22-0500-slice-07-next.md`

## Preferences

- Operator prefers imperative, sequential, log-friendly code over reconciler/graph abstractions. Per grill session 2026-05-21: explicit pushback on "reconciler with WaitUntilReady interface" as overengineering.
- `awsbnkctl status` / `doctor` ALWAYS query AWS directly, NEVER forge. Stale-cache class of bug. Documented in `docs/FORGE_MCP_INTEGRATION.md` §0.
- Destroy gating: single interactive confirm + `--keep-X` flag family (`--keep-iam`, `--keep-keypair`, `--skip-bnk`, `--keep-forge-link`). Two-factor `--confirm-cluster` was considered and rejected as overcomplication.
