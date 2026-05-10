# Sprint 2 — architect issues, resolution notes

The architect agent filed `*No issues filed.*` for Sprint 2 — the seven chapter authoring tasks (chapters 5, 6, 8, 9, 10, 11, 24) completed cleanly. Per-chapter line counts: ch5=188, ch6=236, ch8=250, ch9=232, ch10=206, ch11=207, ch24=338.

The architect's `*No issues filed.*` note documents two minor corrections vs PRD 02's wording, both driven by reading the staff agent's actual `internal/cli/k_*.go` files:

1. **Top-level `apply` alias dropped.** Would shadow the existing `roksbnkctl apply` lifecycle verb (which runs `terraform apply`). Chapter 24 only documents `get` and `logs` as top-level aliases. Staff agent has the same caveat in `k_aliases.go` and tracks it in their own Issue 1.
2. **`logs` command-fallthrough lives at top level, not on `k logs`.** `roksbnkctl k logs <pod>` is strict pod-name mode; the existing `roksbnkctl logs <component-or-pod>` falls through component → pod-name based on label match. Chapter 24 reflects this accurately.

**Status**: ✅ no issues to resolve — pass-through confirmation only

The chapters land as the v0.8-blocking documentation deliverables. Tech-writer agent (next in sequence) reviews them for example correctness against the staff agent's actual implementation.
