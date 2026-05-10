You are the architect agent for Sprint 5 of the roksbnkctl project. Your scope is **book chapter authoring** for the 3 testing chapters that land this sprint, plus a terraform-docker-backend section appended to the existing chapter 17.

Project location: `/mnt/d/project/roksbnkctl/`. The book is _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_, served at `https://jgruberf5.github.io/roksbnkctl/book/`.

Sprint 5 is the **v0.9 release gate sprint** — at sprint-end the integrator tags `v0.9` and cuts a GitHub release. Anything that lands in your chapters should be true of the v0.9 binary, not aspirational.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` §"DNS probe (GSLB-aware)" — the design spec for chapter 21. Covers per-vantage probing, the `--server`/`--type`/`--iterations`/`--gslb-compare` flag surface, the `roksbnkctl.dns.v1` JSON schema, and the GSLB-divergence rationale. Authoritative for the chapter's technical details.
- `docs/prd/03-EXECUTION-BACKENDS.md` §"terraform" §"State concerns" — the design background for chapter 17's new terraform docker-backend section.
- `docs/prd/05-E2E-TEST-PLAN.md` §"Phase L-DNS" — the test surface for the DNS probe; chapter 21's "validating your GSLB setup" section can reference the e2e steps.
- `docs/PLAN.md` Sprint 5 section, especially "Documentation deliverables" — confirms the 3 chapters + chapter-17 update land this sprint.
- `book/src/SUMMARY.md` — existing TOC; do not change.
- The existing chapter stubs at `book/src/20-connectivity-testing.md`, `21-dns-testing-gslb.md`, `22-throughput-testing.md` — replace stubs with real content.
- The existing chapter 17 (`book/src/17-execution-backends.md`) — Sprint 4 landed the local/docker/k8s/ssh deep-dives; Sprint 5 adds a terraform-via-docker subsection per PLAN.md.
- Sprint 4 chapters 17, 18, 19 for tone reference; Sprint 3 chapter 14 (credentials) for the cred-audit framing chapter 21's GSLB JSON section may need.
- `prompts/sprint4/architect.md` for prompt-structure reference.

## Coordinate with parallel agents

A staff-engineer agent is implementing the DNS probe (`miekg/dns` integration in `internal/test/dns.go`, `internal/cli/test.go` flag extensions, the `dns-probe` Job mode in `internal/exec/k8s.go`), the workspace-config additions (`test.dns.resolvers`, `test.dns.default_target`), the terraform docker backend wiring (bind-mount workspace state, `hashicorp/terraform:<v>` image, `--backend docker` for `up/plan/apply/destroy`), doctor extensions for the DNS probe + ops-pod health, and three Sprint 4 polish carry-overs (k8s argv-entrypoint fix per validator Issue 7; SSH wrapper-script test seam per validator Issue 3; minor `:dev`-on-main polish coordinated with the validator). A validator agent is adding miekg-based probe unit tests with a mocked DNS server, an integration tier against `8.8.8.8` + a local stub, Phase L-DNS in `scripts/e2e-test-backends.sh`, terraform-via-docker tests, k8s test-name + comment polish (tech-writer Issue 14), and CONTRIBUTING.md updates.

**Do not touch their files.** Your scope is `book/src/<chapter>.md` only.

## Tasks

For each chapter below, replace the stub content (or for chapter 17, append a new subsection) with real prose. Aim for 250-450 lines per chapter (21 will be longest — it's the flagship; 22 may be shorter since the heavy lifting is in chapter 17 §K8s backend). Use relative markdown links for in-book cross-references and GitHub-canonical URLs for PRD links (per Sprint 1 Issue 9 fix pattern).

### Chapter 20 — `book/src/20-connectivity-testing.md` — "Connectivity testing"

`roksbnkctl test connectivity`. The workspace's `extra_hosts` config block (already wired pre-Sprint-5; this chapter documents it user-facing). Sections:

- What the connectivity suite is — HTTP/HTTPS reachability of configured hosts; pass = 2xx-3xx, fail = anything else or timeout
- The `extra_hosts:` config block schema in `~/.roksbnkctl/<workspace>/config.yaml` (read `internal/config/workspace.go::Workspace.ExtraHosts` for the actual field shape); how to add a host (URL, optional method, optional expected status, optional `insecure_tls: true` for self-signed)
- The `--insecure-tls` flag — what it does, when to use it (dev envs with self-signed certs), what it doesn't do (it's per-host in config; the flag is a session-wide override; both paths skip cert verification only on that host/run)
- Pass/fail interpretation — exit code 0 on all-pass, 1 on any-fail; JSON output with `-o json` per the standard test-suite schema
- A worked example: probing the BNK CIS controller, the F5 BIG-IP Next admin endpoint, and a GSLB VIP from a workspace's `extra_hosts`
- When `roksbnkctl test connectivity` is the wrong tool: deep network debugging (use `curl -v`), TLS handshake debugging (use `openssl s_client`), L4 reachability (use `nc -vz`); connectivity is just "does HTTP work"

Cross-link forward to chapter 21 (DNS validation) and back to chapter 12 (workspace config) for `extra_hosts:`.

### Chapter 21 — `book/src/21-dns-testing-gslb.md` — "DNS testing for GSLB" (flagship)

The Sprint 5 flagship chapter. The GSLB problem statement, the per-vantage probing rationale, the full `roksbnkctl test dns` flag surface, the JSON output schema, sample GSLB scenarios. Read PRD 03 §"DNS probe (GSLB-aware)" in full before drafting.

Sections (suggested ordering — adjust if a better flow emerges):

1. **The GSLB problem** — F5 BIG-IP Next's GSLB returns different answers depending on the requesting resolver's IP (geographic affinity, datacenter routing, health-check state). Validating that GSLB is doing what you configured it to do requires querying the same name from multiple network vantages and comparing.
2. **Why per-vantage matters** — concrete example: a customer's GSLB rule says "users in the US get DC1, users in EU get DC2". From your laptop in the US, you see DC1. To verify the EU rule, you need to query from the EU. That's what `--backend k8s` (cluster's egress IP) and `--backend ssh:eu-bastion` give you.
3. **The `roksbnkctl test dns` flag surface** — `--target`, `--type` (A/AAAA/CNAME/MX/NS/TXT/SRV/SOA/PTR/CAA/DS/DNSKEY/ANY — anything `miekg/dns` exposes), `--server` (IP, hostname:port, `system` for the host's `/etc/resolv.conf`, `cluster` for in-pod CoreDNS, named resolver from workspace config), `--iterations N` for RTT distribution, `--backend local|k8s|ssh:<target>`, `--gslb-compare` for multi-vantage mode, `-o json` for structured output. Cross-link to chapter 17 §K8s backend for the dns-probe Job mode (the binary self-execs in-cluster — no separate image).
4. **Server resolution** — the `--server` argument values: literal IP, `system`, `cluster`, named-resolver-from-config. The workspace config additions are `test.dns.resolvers` (map of name→`<ip>:<port>`) and `test.dns.default_target` — document the YAML shape.
5. **The `--gslb-compare` workflow** — what fans out, what gets collected, when `gslb_divergence` flips to `true`. The `--require-divergence` flag for CI assertions.
6. **JSON output schema** — the `roksbnkctl.dns.v1` schema verbatim from PRD 03; explain each field; show a sample for both single-vantage and multi-vantage runs.
7. **RTT measurement** — per-query RTT extracted from `miekg/dns`'s `Exchange()`; `--iterations N` reports p50/p95/p99; useful for detecting health-check flapping or anycast routing changes; for k8s/ssh backends, measured **inside** the remote vantage point.
8. **Sample F5 BIG-IP Next GSLB scenarios** — three or four worked examples: (a) geographic affinity working as expected (different answers from US-laptop vs EU-bastion); (b) health-check-driven failover (probe before and after manually taking a pool member offline); (c) anycast vs unicast detection. Each scenario shows the command, the JSON output, the interpretation.
9. **Why `--backend docker` is rejected** — same network identity as the host, no vantage benefit. The error message the user sees verbatim.
10. **Integration with `extra_hosts`** — if you've configured `extra_hosts` in workspace config, `roksbnkctl test dns` (no args) probes those hosts. Cross-link to chapter 20.

This is the chapter people will land on when their GSLB isn't behaving — make it concrete, make it diagnostic, make the JSON examples copy-pasteable into a CI assertion.

### Chapter 22 — `book/src/22-throughput-testing.md` — "Throughput testing"

`roksbnkctl test throughput`. The iperf3 internalization story. Sections:

- What the throughput suite measures — TCP bandwidth between a client and a server, both running iperf3. Returns Mbps + jitter + retransmits in JSON.
- The two modes: `--mode east-west` (server runs as a ClusterIP-backed Pod inside the cluster; client runs adjacent in the same cluster — measures intra-cluster fabric) vs `--mode north-south` (server runs as a LoadBalancer-backed Pod; client runs on the laptop / SSH bastion — measures the inbound path). Cross-link to chapter 17 §iperf3 server side for the cluster-side mechanics.
- The default backend (`k8s` per Sprint 4's per-tool default map); when `local` or `ssh:<target>` makes sense (measuring laptop uplink, measuring bastion-to-cluster).
- The bundled iperf3 image and the `USER 1000` non-root constraint; what to do if a custom workspace overrides `test.throughput.image` (it must respect `runAsNonRoot`).
- Sample output — the iperf3 `-J` JSON pruned to the fields roksbnkctl surfaces; interpretation of `sum_received` vs `sum_sent`; how to tell jitter / retransmits from raw throughput.
- The OpenShift SCC story — chapter 17 already covers the technical fix; this chapter says "if your throughput pod fails to start with an SCC error, see chapter 17 §iperf3 server side for the manifest's securityContext".
- Cross-link to chapter 18's "iperf3 throughput → k8s default" decision tree row.

### Chapter 17 update — add `### terraform docker backend` section

The terraform docker backend lands in Sprint 5 per PLAN.md Sprint 5 row 6. Chapter 17's docker-backend deep-dive doesn't currently document terraform-specific shape (it's mostly ibmcloud + iperf3). Add a new subsection under `### docker backend` (or after, if structure permits) covering:

- The bind-mount: `~/.roksbnkctl/<ws>/state/` → container path so terraform's local state lives on the host across runs
- The image: `hashicorp/terraform:<v>` (`<v>` pinned literal — the staff agent picks the value)
- The UID/GID gotcha — Linux containers run as root by default; bind-mount-owned-by-user causes permission collisions. The docker backend passes `--user $(id -u):$(id -g)` to keep state file ownership consistent with the host user.
- The supported commands: `roksbnkctl up --backend docker` (apply + auto-approve), `roksbnkctl plan --backend docker`, `roksbnkctl apply --backend docker`, `roksbnkctl destroy --backend docker`. Plumb through the same flags the local terraform backend honors (`--var-file`, `--auto-approve`, etc.).
- Deferred: k8s + ssh terraform backends. State-handling design is still open; deferred to v1.x. Cross-link to PRD 03 §"State concerns".

The chapter 17 update may also need a sentence in the §"per-tool defaults" table noting `terraform` adds `docker` as a supported backend this sprint while keeping `local` as default.

## Style guidance

- Lower-case prose; sentence-case section headers
- Code blocks for any command; inline code for filenames and identifiers
- Cross-reference other chapters with relative links
- Short paragraphs; one idea per paragraph
- Examples should be runnable as written
- When citing PRDs, link as `[PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md)` — GitHub canonical URL avoids the published-book 404 issue Sprint 1 surfaced
- Sample JSON should validate against the schema name documented in PRD 03 (`roksbnkctl.dns.v1` for DNS, the existing throughput schema for chapter 22)

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint5_architect.md`:

```markdown
# Sprint 5 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 3 chapter files (20, 21, 22) have replaced their stubs with real content
- Chapter 17 has gained a terraform docker-backend subsection
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on book CI
- Internal links resolve
- No "Coming in Sprint 5" placeholder text left in chapters 20/21/22
- Chapter 21's flag surface matches the staff agent's actual `internal/cli/test.go::dnsCmd` flag definitions — coordinate with staff if there's drift, or note as an issue for the integrator
- Chapter 21's JSON sample validates against `roksbnkctl.dns.v1` schema per PRD 03
- Chapter 17's terraform docker-backend subsection matches the staff agent's actual implementation in `internal/exec/docker.go::DockerBackend` + the `up/plan/apply/destroy` wiring

## Final report (under 200 words)

- Per-chapter line count
- Whether mdbook was available locally
- Issues filed (counts by severity)
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
