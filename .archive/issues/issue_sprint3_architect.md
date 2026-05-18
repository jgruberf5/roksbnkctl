# Sprint 3 — architect issues

*No issues filed.*

All five chapter files (12, 13, 14, 15, 17) replaced their stubs with real prose. No `Coming in Sprint 3` placeholder text remains in any of them. The intentional `*Coming in Sprint 4.*` markers in chapter 17's deep-dive sections are preserved (k8s backend deep-dive, ssh backend deep-dive, per-backend "when to use it" table) per the architect prompt's explicit guidance.

Internal cross-links were verified by listing each link target against `book/src/`. All 14 distinct cross-referenced chapters exist:

- `06-workspaces.md`, `09-registering-existing-cluster.md`, `12-workspace-config.md`, `13-terraform-variables.md`, `14-credentials-resolver.md`, `15-ssh-targets.md`, `16-on-flag-ssh-jumphosts.md`, `17-execution-backends.md`, `18-choosing-backend.md`, `19-in-cluster-ops-pod.md`, `25-cos-supply-chain.md`, `28-configuration-reference.md`, `29-terraform-variable-reference.md`, `31-building-from-source.md`.

Chapters 18, 19, 28, 29, 31 are still stubs from Sprint 0 — the cross-references forward-link to them in the same shape Sprint 1 + Sprint 2 used (forward-references resolve to a stub, never broken). They land in Sprint 4 (18, 19) and Sprint 6 (28, 29, 31).

Chapter 14's resolver-chain order (env → keychain → config-b64 → prompt) was cross-checked against the staff agent's landed `internal/cred/resolver.go` after their Sprint 3 implementation merged into the worktree. The chain order, env-var precedence list, error messages, and pin-to-source semantics in the chapter match the implementation byte-for-byte. The redactor description in chapter 14 was adjusted from `***REDACTED***` to `[REDACTED]` after reading staff's `internal/exec/redact.go` const `redactMarker`.

`mdbook` was not available locally in this sprint's worktree (same as Sprint 2). Build verification will rely on book CI on the integrator's branch. No build-affecting changes were made beyond chapter content edits — the five files are pure markdown additions; `book.toml`, `SUMMARY.md`, and infra are untouched.

PRD links use the GitHub-canonical URL pattern (`https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/<NN>-<NAME>.md`) per Sprint 1 Issue 9 fix to avoid the published-book 404. Source-file references (`internal/cred/resolver.go`, `internal/config/secrets.go`, `internal/cli/targets.go`, `internal/remote/`, `terraform/variables.tf`, `terraform/terraform.tfvars.example`) likewise use GitHub-canonical URLs.
