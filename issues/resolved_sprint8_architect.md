# Sprint 8 — architect issues (resolved)

## Issue 1: chapter-10 `bnk down` sample's reassurance footer missing from `runBnkDown`
**Status**: resolved — staff fix applied during integration
**Resolution**: integrator added a two-line success footer to `runBnkDown` after `runTrialDown` returns nil:

```go
clusterDir, err := config.WorkspaceClusterStateDir(cctx.WorkspaceName)
if err == nil {
    fmt.Fprintf(os.Stderr, "\n✓ Trial phase destroyed. Cluster phase %s/ is intact.\n", clusterDir)
    fmt.Fprintln(os.Stderr, "  Run `roksbnkctl bnk up` to deploy another trial against the same cluster.")
}
```

Chapter 10's sample (line 255) matches verbatim. The path interpolation uses the workspace's actual `state-cluster/` directory rather than the chapter's hard-coded `~/.roksbnkctl/default/state-cluster/`, so a non-`default` workspace will show its own path — which is more honest than the chapter's example and reads naturally. Integration commit lands the code; chapter prose unchanged.

## Issue 2: PRD 06 nested-backtick rendering
**Status**: resolved — no edit applied
**Resolution**: closed during sprint; chapter 11 catalogue table uses double-backtick fencing where inline backticks appear, PRD table left alone. Already documented in the issue body.

## Issue 3: empty-workspace `down` refusal text matching
**Status**: resolved — staff emitted the canonical literal
**Resolution**: validator confirmed verbatim match against `~/.roksbnkctl/<empty-ws>/` (Issue 5 evidence in validator issue file). Staff's `runDown` composite emits `errors.New("nothing to destroy in this workspace")` exactly as the catalogue quotes it.

## Issue 4: PLAN.md Sprint 8 deliverables
**Status**: resolved — no change required (closed during sprint)
