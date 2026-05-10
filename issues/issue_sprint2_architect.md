# Sprint 2 — architect issues

*No issues filed.*

All seven chapter files (5, 6, 8, 9, 10, 11, 24) replaced their stubs with real prose. No `Coming in Sprint 2` placeholder text remains in any of them. Internal cross-links to other chapter files were verified by listing each link target against `book/src/`; all resolve. GitHub-canonical URLs to `internal/cli/inspect.go`, `internal/config/workspace.go`, `internal/doctor/check.go`, `internal/doctor/doctor.go`, and `docs/prd/02-KUBECTL-INTERNAL.md` all point at files present in this repo's `main` tree.

The link to `internal/k8s/golden_test.go` from chapter 24 references a file the validator agent is creating in this same sprint — present once their work merges. Documented in chapter 24 alongside the `make test-live` callout.

`mdbook` was not available locally in this sprint's worktree (vs Sprint 1, when the architect downloaded it). Build verification will rely on book CI on the integrator's branch. No build-affecting changes were made beyond chapter content edits — the seven files are pure markdown additions; `book.toml`, `SUMMARY.md`, and infra are untouched.

Chapter 24's example commands were cross-checked against the staff agent's landed `internal/cli/k_*.go` files (`k_root.go`, `k_get.go`, `k_apply.go`, `k_delete.go`, `k_describe.go`, `k_exec.go`, `k_logs.go`, `k_port_forward.go`, `k_aliases.go`). Two corrections were applied vs PRD 02's original wording based on staff's actual implementation:

- Top-level alias for `apply` is **NOT** wired (would shadow the existing `roksbnkctl apply` lifecycle verb that runs `terraform apply`). Chapter 24 documents this: only `get` and `logs` have top-level aliases. Staff agent has the same caveat in `k_aliases.go` and tracked it in their own issue file.
- The `roksbnkctl k logs` verb takes a literal pod name (kubectl-style); the existing `roksbnkctl logs <component>` keeps the BNK-component label-selector path. Top-level `roksbnkctl logs <pod-or-component>` falls through from component to pod-name based on whether the first arg matches a known component label. Chapter 24 reflects this accurately.
