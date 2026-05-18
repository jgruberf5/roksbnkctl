# Sprint 13 — architect issues (carry-in from Sprint 12)

> **Sprint 13 frame.** Feature cycle, `v1.5.0`. Architect scope this
> cycle: author **PRD 08** (read-only `terraform`) + **PRD 09** (per-AZ
> jumphost auto-registration) from `issues/issue_sprint13_staff.md`
> Issues 2/3; CHANGELOG `v1.5.0` block + re-point the `v1.4.1 §Deferred`
> known-issue from `v1.4.2` to `v1.5.0`; the chapter 15/16 per-AZ
> jumphost docs (Issue 1 below); optional `--tf-source` cobra-help
> nudge (Issue 3 below). `docs/PLAN.md` §"Sprint 13" is
> integrator-authored — do not rewrite it.
>
> **Issue 1 is now written for the post-auto-registration world.** The
> carried Sprint 12 text below documented the *manual* `targets add`
> path because the auto-registration code was deferred then. In Sprint
> 13 `issues/issue_sprint13_staff.md` Issue 3 lands the
> auto-registration, so the chapter 15/16 prose must describe
> auto-registered `jumphost-<zone>` (verify with `targets list`), with
> the manual path kept only as a brief pre-`v1.5.0` aside. Ship the
> docs in lockstep with staff code deliverable 3.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: book — document reaching the per-AZ cluster jumphosts (auto-registered `jumphost-<zone>` + hop-via-`jumphost`)

**Severity**: low (documentation gap / discoverability — no defect)
**Status**: resolved — Sprint 13 / `v1.5.0`. Chapter 16 §"Working examples" gained a §"Per-AZ cluster jumphosts" subsection (auto-registered `jumphost-<zone>` headline + the zero-setup hop-via-`jumphost` pattern + orphan caveat) and the §"Environment passthrough" stale-`KUBECONFIG` claim was corrected to the post-staff-Issue-1 behaviour; §"What `--on` doesn't do" reworded (no stale "not auto-registered" claim — the per-AZ jumphosts now *are* auto-registered). Chapter 15 §"Auto-discovery from terraform outputs" gained §"Per-AZ cluster jumphosts (`jumphost-<zone>`)" (auto-registration described, orphan caveat, pre-v1.5.0 manual fallback aside) and a §"What is *not* auto-discovered" note. All five new cross-link anchors verified resolvable in the generated mdbook HTML. Written for the post-auto-registration world; lockstep status with staff code deliverable 3 recorded in Issue 4 below.

> **Scope note (read first).** This is a *documentation feature*, filed
> into the Sprint 12 ledger at user request ("add … in the next
> sprint"). Sprint 12 is a strict bugfix-only patch (`v1.4.1`) — see
> Issue 1 above and `docs/PLAN.md:854-858`. A new instructional book
> section is **not** a patch-cycle deliverable. Recommendation:
> schedule for the next *minor* (`v1.5.0` / Sprint 13). Logged here so
> it isn't lost; the integrator owns the accept/defer call. Suggested
> triaged status: `accepted` (defer to Sprint 13). Pairs naturally
> with `issues/issue_sprint12_staff.md` Issue 4 (read-only `roksbnkctl
> terraform` escape hatch) — the doc below should use that command for
> the IP lookups and ship in the **same** release.

### Motivation

Surfaced in the same user-testing thread as staff Issues 3-4. With
`testing_create_cluster_jumphosts = true` (this user's tfvars) the
deploy builds **one cluster jumphost per cluster-VPC AZ** in addition
to the single TGW jumphost (`ibm_is_instance.cluster_jumphost`,
`for_each = local.cluster_zones`, `terraform/modules/testing/main.tf:404`;
each gets its own floating IP, `ibm_is_floating_ip.cluster_jumphost_fip:430`).
`tryAutoJumphost` (`internal/cli/lifecycle.go:540-565`) only auto-seeds
the singular TGW jumphost as the `jumphost` target — the per-AZ cluster
jumphosts are reachable but **undocumented**: a user who sees "4
jumphosts created" has no instructions for the other three. The book's
jumphost chapters (15, 16) currently describe only the auto-seeded
single target.

Two complementary documentation deliverables (the user asked for
both):

#### 9a. Running commands on the in-AZ cluster jumphosts *via* the registered `jumphost` (SSH hop)

The shared key (`tls_private_key.jumphost_shared_key`) is installed on
**every** jumphost, and the private key file is present on each box at
`/home/ubuntu/.ssh/id_rsa` (`terraform/modules/testing/main.tf:108-111`).
So from the auto-seeded TGW `jumphost` a user can hop to any cluster
jumphost by its **private** IP (the TGW jumphost reaches the cluster
VPC over the Transit Gateway) with no key copying:

```bash
# private IPs of the per-AZ cluster jumphosts (one-liner once staff Issue 4 lands):
roksbnkctl terraform output testing_cluster_jumphost_private_ips
#   (pre-Issue-4 fallback: cd ~/.roksbnkctl/<ws>/state && \
#    TF_DATA_DIR=$PWD/terraform terraform output testing_cluster_jumphost_private_ips)

# run a command on the ca-tor-2 cluster jumphost, hopping through the TGW jumphost:
roksbnkctl --on jumphost ssh -o StrictHostKeyChecking=accept-new \
  ubuntu@<ca-tor-2-private-ip> kubectl get nodes
```

Document: where the private IPs come from, that the on-box
`~/.ssh/id_rsa` is the same shared key (no scp needed), the
StrictHostKeyChecking note for the inner hop, and that this is the
zero-setup path (nothing added to roksbnkctl state).

#### 9b. Registering the in-AZ cluster jumphosts directly into roksbnkctl state

For first-class `--on` access (no hop, full passthrough — `kubectl` /
`oc` / `ibmcloud` / `shell`), register each cluster jumphost as its own
target using the **same shared-key tf-output** the auto-seeded
`jumphost` already uses (`key_source: tf-output:jumphost_shared_key`):

```bash
# public (floating) IPs + ready-made ssh commands, keyed by AZ:
roksbnkctl terraform output testing_cluster_jumphost_ssh_commands
roksbnkctl terraform output testing_cluster_jumphost_public_ips

# register one target per AZ (name carries the zone for disambiguation):
roksbnkctl targets add jumphost-ca-tor-1 \
  --host <ca-tor-1-fip> --user ubuntu \
  --key-source tf-output:jumphost_shared_key
# …repeat per zone…

roksbnkctl --on jumphost-ca-tor-1 kubectl get pods
roksbnkctl targets list      # shows jumphost + jumphost-ca-tor-{1,2,3}
```

Document: the `targets add` invocation, why `key-source:
tf-output:jumphost_shared_key` is correct (one key for all jumphosts —
cross-link ch15 §"`key_source: tf-output:<output-name>`"), a naming
convention (`jumphost-<zone>`), the known-hosts TOFU implication for
each new IP (cross-link ch15 §"Host-key TOFU"), and that these entries
are **not** auto-managed — a destroy+recreate rotates the FIPs and the
user must re-`targets add` (contrast with the auto-seeded `jumphost`,
which `up` re-seeds; cross-link ch15 §"Auto-discovery from terraform
outputs").

### Where it lands (book)

- **Chapter 16** (`book/src/16-on-flag-ssh-jumphosts.md`) — new
  subsection under §"Working examples" (after line 200) for 9a (the
  hop pattern), and a bullet in §"What `--on` doesn't do (yet)" (line
  211) noting per-AZ jumphosts are not auto-registered, pointing to 9b.
- **Chapter 15** (`book/src/15-ssh-targets.md`) — extend §"Auto-discovery
  from terraform outputs" (line 265) with a "what is *not*
  auto-discovered" note, and add a worked 9b example under/after
  §"`roksbnkctl targets add <name> ...`" (line 224).
- Both subsections cross-link each other and the relevant
  `testing_cluster_jumphost_*` outputs (`terraform/outputs.tf:82-89`).

### Acceptance criteria

- A reader who sees N>1 jumphosts can, from chapter 15/16 alone:
  (a) run a command on a specific per-AZ cluster jumphost via the
  registered `jumphost` hop, and (b) register each cluster jumphost as
  its own `--on` target.
- Every command shown is copy-pasteable and uses only documented
  outputs / flags; the IP-lookup step uses `roksbnkctl terraform
  output …` (staff Issue 4) with the raw-`terraform` fallback noted
  for releases before that lands.
- The not-auto-managed caveat (FIP rotation on destroy+recreate →
  re-`targets add`) is stated where 9b is documented.
- All new cross-links resolve on the mdbook HTML backend; chapter
  15/16 prose still flows (no run-ons at the insertion points);
  `mdbook build book/` clean.
- `CHANGELOG.md` `### Added`/`### Changed` (docs) bullet **in whichever
  release ships it** — NOT the `v1.4.1` bugfix-only block (see Issue 1
  + Scope note).

### Related

- `issues/issue_sprint12_staff.md` Issue 4 (read-only `terraform`
  escape hatch) — hard dependency for the clean IP-lookup one-liners;
  ship together. Until then the doc must show the raw-`terraform`
  fallback.
- `issues/issue_sprint12_staff.md` Issue 5 — the "auto-register
  `jumphost-<zone>` from `testing_cluster_jumphost_public_ips`" code
  enhancement (now filed). **Hard doc coupling:** if Issue 5 lands,
  9b's manual `targets add` steps collapse to "verify with `targets
  list`" and this doc must be revised in lockstep — ship together or
  sequence Issue 9 to follow Issue 5 so the two don't drift.
- Code/output facts: `terraform/modules/testing/main.tf:404,430`
  (per-AZ instance + FIP), `:108-111` (shared key on-box),
  `terraform/outputs.tf:82-89` (`testing_cluster_jumphost_public_ips`
  / `_ssh_commands`), `internal/cli/lifecycle.go:540-565`
  (`tryAutoJumphost` seeds only the TGW jumphost),
  `internal/cli/targets.go` (`targets add`).

### Out of scope

- The auto-registration code change itself (potential separate
  Sprint 13 staff enhancement; this issue is docs-only and explicitly
  documents the *manual* path, with a coupling note if the code lands).
- Pulling this into `v1.4.1` absent an explicit integrator decision
  (patch scope — see Scope note).
- Any change to `tryAutoJumphost` behaviour itself (that is staff's
  `issues/issue_sprint13_staff.md` Issue 3 — this issue documents the
  resulting behaviour, in lockstep).

> **Sprint 13 rescope note.** The "Out of scope" bullets above are the
> *Sprint 12* framing (auto-registration deferred → docs document the
> manual path). In Sprint 13 the auto-registration **lands** (staff
> Issue 3 / PRD 09): rewrite the chapter 15/16 prose so the headline is
> the auto-registered `jumphost-<zone>` targets (verify with
> `roksbnkctl targets list`) and the IP-lookup one-liners use
> `roksbnkctl terraform output …` (PRD 08, shipped this cycle). The
> manual `targets add` walkthrough collapses to a short pre-`v1.5.0`
> fallback aside. The orphan caveat (option (a) upsert-only: a
> destroy/zone-removal leaves a stale `jumphost-<oldzone>` until
> `targets remove`) is documented where the auto-registration is
> described.

---

## Issue 2: cloud-init boot-timing race produces the same `localhost:8080` symptom — explicitly OUT of v1.5.0 scope

**Severity**: ~~low (cross-reference only)~~ → **HIGH** — escalated
2026-05-18 by live testing. Deterministically breaks the documented
`--on jumphost kubectl|oc` happy path; same user-visible severity the
env-leak (staff Issue 1) carried.
**Status**: RESOLVED 2026-05-18 (Sprint 14, option C; folded into the held `v1.5.0`). Part A (cloud-init bounded-retry + loud sentinel) + Part B (`--on` self-heal, extended to `ibmcloud login` the target). **Live-verified by the integrator 16:33** (user-authorized): `roksbnkctl exec --on jumphost kubectl get pods` self-healed on attempt 1 and reached the cluster API — `localhost:8080` gone, exit 0, no redeploy. The validation blind spot is also closed: `internal/cli/lifecycle_e2e_test.go` (13 e2e guards incl. the not-logged-in + bad-credentials cases) makes this defect class fail a test, not a human. Was: open — headline of the get-well sprint (Sprint 14). Out
of scope for `v1.5.0` (which correctly ships the env fix + the two
features and is not regressed by this), but the substantive remaining
blocker. Pull into Sprint 14 as deliverable 1.

### Live confirmation (2026-05-18 14:54)

Not a transient boot-*timing* race as originally framed — a **silent
permanent provisioning failure**. After the env-leak fix was
live-verified working (`KUBECONFIG=[]` crossing the boundary), the user
still hit `localhost:8080`. Diagnostic showed `/home/ubuntu/.kube/config:
No such file or directory` on the jumphost — the file was **never
created**, not "not ready yet". Re-running the explicit-path test
minutes later still failed: it is not recovering on its own.

### Root cause (grounded in `terraform/modules/testing/main.tf`)

Cloud-init does:

```sh
mkdir -p /root/.kube
ibmcloud login --apikey "…" -r "${var.ibmcloud_cluster_region}" … || true
ibmcloud ks cluster config --cluster "${var.roks_cluster_name_or_id}" --admin || true
…
if [ -f /root/.kube/config ]; then
  cp /root/.kube/config /home/ubuntu/.kube/config; chown -R ubuntu:ubuntu …
fi
```

`/home/ubuntu/.kube/config` is created **only if** `/root/.kube/config`
exists, which requires `ibmcloud ks cluster config --admin` to have
succeeded. Both that command and the preceding `ibmcloud login` are
guarded by `|| true`: any failure (cluster not Ready at boot, region /
resource-group mismatch, transient IAM/API error) is **swallowed with
no retry, no log line, no failure marker, and no remediation**. The
jumphost boots "successfully" with no kubeconfig and stays that way
until a human manually re-runs the documented commands. This is a
deterministic break of the canonical private-cluster workflow
(`book/src/16-on-flag-ssh-jumphosts.md`,
`book/src/09-registering-existing-cluster.md`), not an edge case.

### Disposition / fix options for Sprint 14 (integrator chooses scope)

- **A — harden cloud-init** (`terraform/modules/testing/main.tf`):
  replace the bare `|| true` on the login + `ks cluster config --admin`
  with a bounded retry/readiness loop; on exhaustion write a clear
  failure marker (e.g. `/var/log/jumphost-setup.log` + a sentinel file)
  instead of silently continuing. Fixes **new** deploys; does nothing
  for an already-running jumphost (like the user's current one) until
  `terraform apply` recreates it.
- **B — roksbnkctl-side self-heal**: roksbnkctl already fetches the
  admin kubeconfig locally post-`up` (`tryAutoKubeconfig`,
  `internal/cli/lifecycle.go:471`). Either (b1) the post-`up` hook also
  pushes a freshly-fetched kubeconfig to each seeded jumphost target,
  or (b2) `--on <target> kubectl|oc` self-heals: if the target has no
  kubeconfig, run `ibmcloud ks cluster config --admin` on the target
  (it is already `ibmcloud login`'d as `ubuntu` per the cloud-init
  fork) before dispatching the command. (b2) is the "layer-2 remote
  kubeconfig remap" deferred from the original Issue 1 proposal. Fixes
  the user's **existing** jumphost with no re-deploy — strictly better
  UX for the get-well goal.
- **C — both** (recommended): A for robust new deploys, B for
  self-heal of existing/already-broken hosts. Mirrors the env-fix's own
  two-layer (fix + defense-in-depth) posture.

**Integrator decision 2026-05-18: option C (both).** Sprint 14
(get-well) deliverable 1 = (A) harden the cloud-init login + `ibmcloud
ks cluster config --admin` path with bounded retry/readiness gating and
a loud failure marker replacing the silent `|| true`, **and** (B)
roksbnkctl-side self-heal so `--on <target> kubectl|oc` provisions /
repairs a missing remote kubeconfig on the fly (and/or the post-`up`
hook pushes a freshly-fetched kubeconfig to seeded jumphost targets).
Rationale: A makes new deploys robust; B unblocks already-broken
jumphosts (like the one in the 2026-05-18 live test) with no
`terraform` recreate. Mirrors the env-fix's two-layer fix +
defense-in-depth posture.

This issue **cannot be closed by bookkeeping** — option C must be
implemented in Sprint 14. Tracked as the get-well headline (deliverable
1).

**Integrator decision 2026-05-18 (supersedes the "ships as a documented
known-issue" framing above): HOLD & MERGE.** `v1.5.0` is **not** cut at
the end of Sprint 13. The `## Unreleased (v1.5.0)` CHANGELOG block stays
open; Sprint 14 lands option C **into the same `v1.5.0`** so the
release that finally ships genuinely makes `--on jumphost kubectl|oc`
work end-to-end (env-leak fix + kubeconfig provisioning together).
Rationale: the two `localhost:8080` causes are indistinguishable to a
user, so shipping `v1.5.0` with the symptom still reproducible would
make the headline fix look broken. Sprint 13's three deliverables are
complete and GREEN but remain staged in the working tree under the held
`v1.5.0`. Carried into `issues/issue_sprint14_staff.md` Issue 1 +
`issues/issue_sprint14_architect.md` Issue 1 as the Sprint 14 headline.

---

## Issue 3: `--tf-source` cobra help silent on relative-path resolution (Sprint 12 tech-writer §"Sprint 13 awareness" carry-forward)

**Severity**: low (discoverability nudge — staff-surface hand-off)
**Status**: resolved — integrator applied the proposed 2-line diff to `internal/cli/lifecycle.go:87,90` 2026-05-18; `go build ./...` + `go test ./internal/cli/` green, `gofmt` clean. Both `--tf-source` help strings now state relative-path resolution.

### Description

Sprint 12 tech-writer §"Sprint 13 awareness": now that `--var-file` and
`--tf-source` resolve relative paths against the invocation CWD
(v1.4.1), the `init` / `up --tf-source` cobra help string
(`internal/cli/lifecycle.go:86,89`, currently `"override TF source
(path or URL)"`) is silent on relative-path resolution — the same gap
the `--var-file` help had before Sprint 12 scrutiny.

### Disposition

**Judged: still mildly misleading by omission — filed as a staff-surface
hand-off (low, fold-in-if-cheap).** Current strings
(`internal/cli/lifecycle.go:87,90`):

- `init`: `"override TF source (path or URL); pinned into config.yaml"`
- `up`:   `"override TF source for this run only"`

Neither says a relative local path is resolved to absolute. The
`--var-file` help is also silent on relative resolution, so there is no
*inconsistency between the two flags* — but `--tf-source` is the more
surprising case: `init` **persists** the value into `config.yaml`, so a
relative `--tf-source=./mytf` that worked at `init` time used to
detonate on a *later* `up`/`plan`/`apply` (the v1.4.1 fix). A user who
doesn't know the fix exists can't tell from the help that
`./mytf` is now safe. One word resolves it.

**Proposed staff-surface fix** (architect does not edit `internal/`;
hand-off — fold in only if cheap and non-disruptive to the three
primary deliverables):

```diff
--- a/internal/cli/lifecycle.go
+++ b/internal/cli/lifecycle.go
-	initCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source (path or URL); pinned into config.yaml")
+	initCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source (path or URL); relative local paths are resolved to absolute before being pinned into config.yaml")
@@
-	upCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source for this run only")
+	upCmd.Flags().StringVar(&flagTFSource, "tf-source", "", "override TF source for this run only (path or URL; relative local paths resolved against the invocation CWD)")
```

Line numbers approximate (drift-tolerant — match by flag name).
Severity low; not a blocker for any primary deliverable. Staff/integrator
owns the accept/fold call.

**Status**: resolved — diff applied 2026-05-18 (see above), build/test/gofmt green.

---

## Issue 4: top-level terraform output is `testing_cluster_jumphost_ips`, not `…_public_ips`/`…_private_ips` (design-surface drift — staff + validator hand-off)

**Severity**: medium (would have caused staff code deliverable 3 to read
a non-existent top-level output and silently no-op)
**Status**: resolved — staff landed deliverable 3 reading
`testing_cluster_jumphost_ips` as the primary output name
(`internal/cli/lifecycle.go:611`, with a `…_public_ips` fallback at
:613), matching PRD 09 / CHANGELOG / chapters 15-16. Architect-side
deliverables and the as-landed binary agree on the output name. Left
here as a record; validator's doc-coupling audit should still confirm
the name once over.

### Description

`issues/issue_sprint13_staff.md` Issue 3 and this file's Issue 1
(carried from Sprint 12) instruct reading
`testing_cluster_jumphost_public_ips`, and the Issue 1 hop example
references `testing_cluster_jumphost_private_ips`. Those names exist
**only inside the `testing` module**
(`terraform/modules/testing/outputs.tf:97,102`). They are **not**
top-level terraform outputs. The top-level outputs the post-`up` hook
and `roksbnkctl terraform output` actually see
(`terraform/outputs.tf:82-90`) are:

- `testing_cluster_jumphost_ips` — `{zone => floating-IP}` map
  (forwards the module's internal `…_public_ips`; `try(…, [])` default)
- `testing_cluster_jumphost_ssh_commands` — `{zone => ssh-cmd}` map

There is **no** top-level `testing_cluster_jumphost_public_ips` and
**no** top-level `testing_cluster_jumphost_private_ips`.

### Resolution in the architect deliverables

PRD 09, the CHANGELOG `v1.5.0 §Added`, and chapters 15/16 are all
written against the **real** top-level name
`testing_cluster_jumphost_ips`, with the deviation explicitly recorded
in PRD 09 §"Design" (the boxed "Output-name deviation" note).

### Hand-off

- **Staff (code deliverable 3):** read
  `testing_cluster_jumphost_ips`, **not** `…_public_ips`. The
  `mapOutput` no-op guard must treat the `[]`-default JSON
  (HCL `try(…, [])` → JSON array, not a map) as the
  skip signal. If staff genuinely needs the `…_public_ips` /
  `…_private_ips` granularity at top level, that requires a new
  `terraform/outputs.tf` output (terraform surface — separate change,
  not this cycle's scope).
- **Validator:** the doc-coupling audit must verify chapter 15/16 use
  the as-landed output name (`testing_cluster_jumphost_ips`), not the
  `…_public_ips` name from the original (now-superseded) staff issue
  text.

---

## Issue 5: chapter 15/16 ↔ staff code deliverable 3 lockstep status

**Severity**: low (process / lockstep tracking — no defect)
**Status**: resolved — staff landed deliverables 2 + 3 in parallel
before architect return: `internal/cli/terraform.go` (cobra `tf` alias,
`tf.OpenReadOnly`/`RunReadOnly`) and `tryAutoClusterJumphosts` +
`mapOutput` in `internal/cli/lifecycle.go`, reading
`testing_cluster_jumphost_ips`. The chapter 15/16 prose and PRD 08/09
match the as-landed behaviour (command name `roksbnkctl terraform`/`tf`,
auto-registered `jumphost-<zone>`, output name per Issue 4). Lockstep
confirmed at the doc-relevant level; validator's doc-coupling audit is
the final checkpoint.

### Description

Per the prompt's lockstep requirement: chapter 15 §"Per-AZ cluster
jumphosts (`jumphost-<zone>`)" and chapter 16 §"Per-AZ cluster
jumphosts" describe binary behaviour that **staff code deliverable 3
lands in parallel**. At architect-return time, staff had **not** yet
landed deliverable 3 (`internal/cli/terraform.go` absent;
`tryAutoClusterJumphosts` / `mapOutput` not present in
`internal/cli/lifecycle.go`; `RunReadOnly` not in
`internal/tf/terraform.go`).

The chapters are written to the **intended** post-auto-registration
behaviour as specified by PRD 08/09 + `issues/issue_sprint13_staff.md`
Issues 2/3, with every pre-v1.5.0 fallback clearly marked as such. No
"TODO" markers were embedded in the prose (the pre-v1.5.0 fallback
asides already make the version boundary explicit to readers, and a raw
`TODO` in shipped book prose is worse than the version-gated phrasing).

### Hand-off / gate

This is **not** a blocker for the architect deliverables but **is** a
`v1.5.0` gate item (PLAN.md §"Sprint 13 → Gate"): the chapters must not
ship in the `v1.5.0` tag describing behaviour absent from the binary.

- If staff code deliverable 3 (and 2, for the `roksbnkctl terraform
  output` one-liner) land as specified, the chapters are correct as
  written — resolve this issue.
- If staff diverges (e.g. command name, output name per Issue 4, or
  scopes the auto-registration differently), the integrator must
  reconcile the chapters to the as-landed behaviour before the
  `v1.5.0` tag. Validator's doc-coupling audit is the checkpoint.
