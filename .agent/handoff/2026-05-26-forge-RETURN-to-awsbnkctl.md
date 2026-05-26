# RETURN handoff: forge → awsbnkctl — Issue 1 FIXED + verified; ONE awsbnkctl-side action remains

> Reply to `2026-05-26-forge-agent-prompt.md`. Written by the bnk-forge-v2 agent after fixing + live-verifying Issue 1.
> Backlog item `forge-allocate-project-credentials` in this repo's BACKLOG.md is satisfied on the FORGE side; the
> remaining piece is an awsbnkctl/IdC-side SSO bootstrap (below).

## TL;DR
syd-tracer now authenticates from forge. `test_cluster_connectivity {cluster_id:24}` → **success, version 1.30**;
`scan_cluster {24}` + `get_cluster_events {24}` work. Fixed on forge branch `fix/forge-eks-creds-allocation`
(3 commits, stacks on PR #124; PR to staging pending). Verified live on the localhost local-integration stack.

## Correction to the original diagnosis (important)
The original prompt said the gold-ref cluster also 401s and that template-cred resolution was broken. A live A/B test
disproved both: gold-ref **cluster 9 was succeeding** the whole time (it's bound to the is_default template, which had
valid keys), and forge's credential resolver + EKS-token mint were never broken. The real causes were TWO independent
defects on syd-tracer, both now fixed in forge:

1. **No credential binding.** `forge register`'s create_project left project 40 with `credential_template_id = NULL`
   and no project cred record, so the resolver had no AWS identity → 401. (The MCP `create_project` tool only persists
   `credential_template_id` when it's truthy; the automated call passed 0/none.)
2. **Wrong `x-k8s-aws-id`.** Forge does NOT use the stored kubeconfig's auth — it mints a fresh SigV4 STS token at
   connect time and overwrites the kubeconfig user block. The `x-k8s-aws-id` header must be the BARE cluster name.
   create_cluster stored syd-tracer's `context` as the FULL ARN (`arn:aws:eks:...:cluster/syd-tracer`) with an empty
   `meta_data.cluster_arn`, so forge signed the token with the ARN → EKS rejected it → 401 **even with valid creds**.

### Forge-side fixes (no action needed from you)
- Allocation ("Both"): create_project now defaults `credential_template_id` to the is_default template; the resolver
  falls back to the is_default template for an unbound project; MCP `update_project` can (re)bind an existing project.
- EKS token: forge now derives the bare cluster name from an ARN-shaped `context`/`name` (not only `meta_data.cluster_arn`).
- Honest `get_project_aws_credentials` (now reports `configured:true, source:"default_template"` for project 40) and a
  structured credential-unavailable signal scoped to the connectivity path (success:false with a credential message,
  not a bare 401).

## THE ONE THING awsbnkctl/IdC must do — unattended SSO refresh
forge verified live that the is_default template (id=1) has **`can_refresh: false` — NO refresh token**, and its
exchanged AWS keys expire (observed `aws_credentials_expiry` 2026-05-26 17:07 UTC). **Forge cannot re-mint creds
non-interactively without a refresh token.** After expiry, syd-tracer (and gold-ref) will 401 until an interactive
SSO device-flow re-auth. For `up --auto` to stay non-interactive across the credential lifetime:

- The one-time SSO bootstrap must capture a **refresh token** (request `offline_access` scope / ensure the IdC permission
  set issues refresh tokens), AND it must target the **credential TEMPLATE**, not the project record.
- WHY the template, not the project: the MCP tools `aws_sso_poll` / `aws_set_project_credentials` write the *project*
  `cloud_credentials_encrypted` record — but a template-backed project is authenticated via the *template's* exchanged
  keys, and forge's background/lazy refresh operates on the template. Bootstrapping the project record for a
  template-backed project is the mismatch that produced the original confusion. Use the template SSO flow
  (`POST /api/credential-templates/{id}/authenticate-sso` + poll) once, so the template holds a refresh token.
- If the IdC permission set does NOT issue refresh tokens for this account/role, unattended is impossible and `up --auto`
  must either prompt for SSO re-auth or pre-flight a fresh token each run. forge can then auto-refresh from the template's
  refresh token on subsequent expiries.

## Optional awsbnkctl cleanups (forge now tolerates both, so not required)
- `forge register` could pass `credential_template_id` to create_project (forge defaults/falls back if you don't).
- create_cluster could store the bare cluster name as `context` and/or populate `meta_data.cluster_arn` (forge now
  parses an ARN-shaped context, so either is fine).

## Issues 0 and 2 (forge fixes — Issue 2 is a BREAKING change you must adopt)
- **Issue 0** (MCP `MCP_PASSWORD` drift): FIXED (forge branch `fix/mcp-password-healthcheck`). The MCP container
  healthcheck now runs an auth-probe (`python -m bnk_forge_mcp.healthcheck`) that logs in to the backend and exits
  non-zero on 401 — so a password drift flips the container UNHEALTHY instead of silently 401-ing every tool. Verified
  live: stale `changeme` → unhealthy; correct password → healthy. No action needed on your side.
- **Issue 2** (response-envelope inconsistency): **NORMALIZED — this is a deliberate breaking change** (operator chose
  "normalize now" over document-only). Forge branch `fix/mcp-response-envelope`. **ACTION REQUIRED: update the awsbnkctl
  Go client to the new envelopes** (`internal/forge/client.go`):
  - `create_project` now returns: `{"success": true, "project": {"id": <int>, "name": "...", ...}, "message": "..."}`
    (was flat `{"success":true,"project_id":<int>,"name":...}` — `project_id` is now `project.id`).
  - `create_cluster` now returns: `{"success": true, "cluster": {"id": <int>, "name": "...", "project_id": <int>, "status": "...", ...}}`
    (was a bare object with top-level `id` and no `success`).
  - Both pass structured error envelopes (`{"ok": false, "error": ...}`) through unchanged.
  Coordinate merge order so the client update lands together with forge's PR. Backend REST routes are UNCHANGED — this is
  the MCP tool layer only.

## Live records (unchanged — you own teardown)
project 40 / cluster 24 still present; project 40's `credential_template_id` is still NULL in the DB (forge resolves it
at runtime via the is_default fallback — per operator instruction we did NOT hand-edit the DB).
