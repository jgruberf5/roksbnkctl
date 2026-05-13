# Sprint 8 — tech-writer issues (resolved)

## Issue 1: carry-in `internal/exec/` WIP gates `v1.1.0` tag
**Status**: surfaced to user (integrator-owned decision)
**Resolution**: tech-writer re-surfaced the carry-in already triaged by staff Issue 1 + validator Issue 1. The Sprint 8 surface itself is clean and tag-ready. The v1.1.0 tag is gated on the user's triage of the pre-Sprint-8 modifications to `internal/cli/cluster.go` + `internal/exec/docker.go` + `internal/exec/k8s.go` + `internal/exec/k8s_install.yaml`. Recommended route per validator: cut `v1.0.3` first with the 4 failing tests + gofmt drift in `internal/exec/` repaired, then tag `v1.1.0` from a clean tree.

This is the only Sprint 8 launch gate.

## Issue 2: chapter 8 §"Legacy single-state workspaces" sample wraps refusal across lines
**Status**: resolved — integration round
**Resolution**: applied tech-writer's suggested fix verbatim. Chapter 8 sample now shows each refusal as a single logical line (matching the binary's actual output), with a note that visual wrapping is a terminal-width artifact and grep against any inline punctuation lands cleanly.

## Issue 3: chapter 10 says "Two prompts fire" but the empty-workspace path fires three
**Status**: resolved — integration round
**Resolution**: applied tech-writer's proposed diff verbatim. Opening clause now correctly says "Three prompts fire in the empty-workspace case ... (On a non-empty workspace where `bnk up` skips the cluster bootstrap, only the latter two fire — and a `ShapeClusterOnly`/`ShapeSplit` `bnk up` is the common iteration case.)". The arithmetic in the closing `--auto` line ("skips all three") was already correct.

## Issue 4: chapter 11 decision tree's v1.0.x branch requires a chapter-hop to confirm shape
**Status**: resolved — integration round
**Resolution**: applied tech-writer's proposed diff verbatim. Decision tree now ends with an inline one-line `ls ~/.roksbnkctl/<workspace>/` shape check — `state-cluster/` present → split shape; absent → legacy. Collapses the two-hop diagnostic loop to one chapter.

## Issue 5: chapter 10 dispatch matrix lacks a pre-amble naming the four shapes
**Status**: deferred — flagged for the next post-v1.1.0 chapter-polish cycle
**Resolution**: not applied this sprint. The column-header parentheticals do enough work for the immediate "what does each shape mean?" question; the deeper "which shape am I on?" answer lives in chapter 8 §"Legacy single-state workspaces" where it belongs. Will revisit if dogfooding feedback on the published book surfaces it as a real friction point.

## Issue 6: drift sweep + dogfooding verification log
**Status**: resolved — informational log only
**Resolution**: all 8 refusals byte-identical across PRD ↔ source ↔ chapter 11; all 24 dispatch-table cells match; CHANGELOG ↔ binary surface clean; 4 dogfooding loops traced with zero would-give-up failures.
