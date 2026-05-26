# Forge agent prompt — make bnk-forge able to see + operate awsbnkctl-provisioned EKS clusters

> Hand this to the agent that works on `bnk-forge-v2` (the localhost forge stack + MCP server).
> Context gathered live on 2026-05-26 during awsbnkctl "cycle 6". A real EKS cluster
> (`syd-tracer`, BNK 2.3.0) is UP in AWS account `292785712872`, region `ap-southeast-2`,
> and is registered into forge (project_id=40, cluster_id=24) via the now-FIXED automated
> `awsbnkctl forge register` path. Two independent issues block forge from *operating* it. Please fix on
> localhost and PR.

## Environment / live resources (do not delete — awsbnkctl owns teardown)
- Forge stack: worktree `bnk-forge-v2/.claude/worktrees/local-integration`, compose project `bnk-forge-v2`.
  Backend `:8000`, MCP `:8081/mcp`, frontend `:8080`. Admin creds `admin` / `admin123`.
- MCP→backend auth: the `bnk-forge-mcp` container authenticates with `BNK_FORGE_PASSWORD: ${MCP_PASSWORD:-changeme}`.
  It was running with the stale `changeme` today → every authenticated MCP tool 401'd until the
  container was recreated with `MCP_PASSWORD=admin123`. (Already fixed today, but see Issue 0.)
- Forge projects: `aws-syd-test` (id=9, gold-ref), `jl-gpu-lab` (id=27), `awsbnkctl-syd-tracer` (id=40, NEW).
- Forge cluster: `syd-tracer` (id=24) under project 40 — api_server + base64 kubeconfig stored, status=active.
  (NOTE: an earlier manual registration used project 39 / cluster 23; those were deleted and re-created as
  40 / 24 via the FIXED automated `awsbnkctl forge register` path. The raw-JSON evidence below still shows the
  original 39/23 ids — that's the captured shape, the live records are now 40/24.)
- Minor forge-side bug observed: MCP `system_version` (used by `awsbnkctl forge status`) hits `/api/version`
  which 404s `FILE_NOT_FOUND`. Cosmetic (status still reports reachable=true) but worth a fix.
- EKS clusters in the account: `aws-syd-test-cluster` (gold-ref, read-only) and `syd-tracer` (cycle-6, live).
  Both are `authenticationMode: CONFIG_MAP`.
- Operator / cluster-creator identity (implicit cluster-admin on syd-tracer):
  `arn:aws:sts::292785712872:assumed-role/AWSReservedSSO_Users_28497d15a8cf2cd3/J.Lucia@F5.com`
  (AWS SSO, role `AWSReservedSSO_Users_28497d15a8cf2cd3`).

---

## Issue 0 (ops, low effort): MCP container password drifts from backend
`MCP_PASSWORD` defaulting to `changeme` while the backend admin password is `admin123` silently
breaks ALL authenticated MCP tools with `401 Login failed`. Consider: (a) make the compose default
read from the same source as the backend seed, or (b) have the MCP server surface a clear startup
healthcheck that asserts it can authenticate to the backend (so `tools/list` passing ≠ false-green).
Recreate command that fixed it today:
```
WT=.../bnk-forge-v2/.claude/worktrees/local-integration
MCP_PASSWORD=admin123 docker compose -p bnk-forge-v2 --project-directory "$WT" \
  -f "$WT/docker-compose.yml" -f "$WT/docker-compose.local.yml" up -d --force-recreate --no-deps mcp
```

## Issue 1 (PRIMARY, forge-side): forge cannot authenticate to ANY EKS cluster in this account
`test_cluster_connectivity` returns `{"success":false,"message":"Kubernetes API error: Unauthorized","status_code":401}`
for **both** the new syd-tracer (id=24) **and** the gold-ref aws-syd-test (id=9). The gold-ref's cached
`version:"1.28"` is stale from an earlier scan — it 401s now too.

Root cause evidence:
- `get_project_aws_credentials` for project 9 AND project 40 both return `{"configured": false, "message":"No AWS credentials configured for this project"}`.
  So forge has **no usable AWS identity** to mint an EKS bearer token. Passing `credential_template_id=1`
  to `create_project` attached a template but did **not** result in configured project creds.
- The EKS clusters are `CONFIG_MAP` auth mode. forge's EKS-access-entry method requires the cluster to be
  `API` or `API_AND_CONFIG_MAP` (`aws eks list-access-entries` errors otherwise). With CONFIG_MAP, authorization
  is via the `aws-auth` ConfigMap; syd-tracer currently maps only `arn:aws:iam::292785712872:role/syd-tracer-eks-node-role`.

**CONFIRMED PATH (operator decision 2026-05-26): forge should reuse the operator AWS SSO identity.**
- Operator SSO permission-set role: `AWSReservedSSO_Users_28497d15a8cf2cd3`
  (full IAM ARN with SSO path: `arn:aws:iam::292785712872:role/aws-reserved/sso.amazonaws.com/AWSReservedSSO_Users_28497d15a8cf2cd3`).
- awsbnkctl created `syd-tracer` while assuming THIS exact SSO role (`AWS_PROFILE=Users-292785712872`), so it
  already holds **implicit EKS cluster-admin** (`system:masters`) — verified live today: `kubectl` calls using this
  identity succeed against syd-tracer. **No `aws-auth` edit is required** for forge to get admin, as long as forge
  presents a session of this same permission-set role.
- So the forge-side work is purely: obtain AWS SSO creds for this permission set (they're temporary — handle refresh)
  and present them when talking to the cluster. No new IAM principal needs creating for syd-tracer.

What forge still needs to settle (design questions for the forge agent):
1. **How forge obtains + refreshes the AWS SSO session** for the project (the creds expire; `aws sso login` mints them
   under the operator's profile `Users-292785712872`). Decide whether forge reads the operator's SSO cache or runs its
   own SSO flow for the same permission set.
2. Make `aws_set_project_credentials` / the credential-template flow actually leave `get_project_aws_credentials`
   reporting `configured: true`, and have `test_cluster_connectivity {cluster_id:24}` succeed using the SSO identity.
3. (Only if forge later wants its OWN dedicated IAM principal instead of reusing operator SSO): that principal must be
   authorized — an `aws-auth` mapRoles/mapUsers entry (CONFIG_MAP mode), or, if syd-tracer is flipped to
   `API_AND_CONFIG_MAP`, an EKS Access Entry. awsbnkctl can do either on request.

Acceptance for Issue 1: `test_cluster_connectivity {cluster_id:24}` returns success with version populated,
and `scan_cluster`/`get_cluster_events` work against syd-tracer.

> awsbnkctl side can help once forge's principal is known: we can add it to syd-tracer's `aws-auth`,
> or flip syd-tracer to `API_AND_CONFIG_MAP` and create an EKS Access Entry for it. Tell us the principal.

### Precise credential mechanism (pinned 2026-05-26)
The forge credential toolchain is SSO-capable end to end:
`aws_sso_initiate(start_url,region,project_id)` → `aws_sso_poll(...)` → SSO access token;
`aws_set_project_credentials(access_token, account_id, role_name, project_id, region)` exchanges it for
short-lived creds and stores them as the **project** credential record; `aws_assume_role(role_arn,...)`;
`create_credential_template(... aws_sso_enabled, aws_sso_account_id, aws_sso_role_name ...)` +
`list_credential_templates` / `test_credential_template`.

Observed gap: credential template id=1 ("AWS Production") IS sso-enabled for account 292785712872 / role
`Users` and was freshly SSO-authenticated (token valid ~1h, creds ~12h). BUT `test_cluster_connectivity`
uses the **project's** credential record, and `get_project_aws_credentials(project_id=40)` is `configured:false`
— **passing `credential_template_id` to `create_project` does NOT copy the template creds onto the project.**
So connectivity 401s even with a valid default template.

Fix options (forge agent to decide):
- Make `create_project` with a `credential_template_id` actually populate/point the project at the template's
  creds, OR have `test_cluster_connectivity` fall back to the default template when the project has none; AND/OR
- Document that the project must be credentialed via `aws_set_project_credentials` (SSO device flow) after creation.
- For role `Users` on account 292785712872, region us-east-1, start_url `https://d-906774a4a9.awsapps.com/start/#`.

## Issue 2 (API contract): create_project / create_cluster response shapes are inconsistent
This breaks awsbnkctl's automated `forge register` (phase09) — it reports "create_project succeeded but no
project ID in response" and leaves orphan projects. Raw responses captured live today:

`create_project` →
```json
{ "success": true, "project_id": 39, "name": "awsbnkctl-syd-tracer", "message": "Project created successfully" }
```
`create_cluster` →
```json
{ "id": 23, "name": "syd-tracer", "context": "...", "api_server": "...", "project_id": 39, "status": "active", ... }
```

Note the inconsistency: create_project nests the id as flat `project_id` with a `success` envelope; create_cluster
returns the bare object with top-level `id` and **no** `success` field. awsbnkctl's Go client (`internal/forge/client.go`)
expected `{"project":{"id":..}}` and `{"cluster":{"id":..}}` respectively. **awsbnkctl will fix its own client to
match forge's current responses** — but please decide + DOCUMENT the canonical MCP response contract (ideally a
consistent envelope: `{"success":bool,"project":{"id","name",...}}` / `{"success":bool,"cluster":{"id","name",...}}`,
or consistently flat). If you change the shape, coordinate so we update the awsbnkctl client to match.

## Reproduction (localhost, against the live cluster)
```
# authenticated MCP tool (proves MCP->backend auth):
curl -s -X POST http://localhost:8081/mcp -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}'

# the 401 (both clusters):
... tools/call name=test_cluster_connectivity arguments={"cluster_id":24}   # syd-tracer
... tools/call name=test_cluster_connectivity arguments={"cluster_id":9}    # gold-ref

# project creds state:
... tools/call name=get_project_aws_credentials arguments={"project_id":40}
```
