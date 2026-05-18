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
**Status**: open — Sprint 13 / `v1.5.0` (carried from Sprint 12 architect Issue 9; rescope to the post-auto-registration world per the frame note above; lockstep with `issues/issue_sprint13_staff.md` Issue 3)

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

**Severity**: low (cross-reference only — not a Sprint 13 deliverable)
**Status**: accepted — out of scope for `v1.5.0`, filed so it is not conflated with the Issue-1 / staff-Issue-1 env-leak verify

### Description

`issues/issue_sprint13_staff.md` Issue 1 §"Related" notes a second,
independent cause of the identical `connection to the server
localhost:8080 was refused` symptom: `terraform/modules/testing/main.tf:80-104`
writes `/home/ubuntu/.kube/config` via `ibmcloud ks cluster config
--admin` guarded by `|| true`, asynchronously during cloud-init. A
freshly-booted jumphost can transiently lack a usable kubeconfig — the
KUBECONFIG-leak fix (staff Issue 1) is necessary but, on its own, still
subject to this boot-timing race.

### Why it's filed here

So the validator's env-leak verify and the tech-writer's dogfooding
loop do not mistake a transient boot-race failure for a regression of
the env-leak fix, and so a future cycle has a written pointer.
Hardening the cloud-init kubeconfig provisioning (retry / readiness
gating around `ibmcloud ks cluster config --admin`) is its own
architect/infra change in `terraform/` — **out of scope for `v1.5.0`**
(the Sprint 13 fix is the env leak only).

### Disposition

No action this cycle beyond this cross-reference. File a dedicated
follow-up against `terraform/modules/testing/main.tf` if the boot-race
is observed in live testing after the env-leak fix lands.

---

## Issue 3: `--tf-source` cobra help silent on relative-path resolution (Sprint 12 tech-writer §"Sprint 13 awareness" carry-forward)

**Severity**: low (discoverability nudge — staff-surface hand-off)
**Status**: open — judge during the cycle; resolve as a staff-surface diff or `accepted` if the help text is fine post-v1.4.1

### Description

Sprint 12 tech-writer §"Sprint 13 awareness": now that `--var-file` and
`--tf-source` resolve relative paths against the invocation CWD
(v1.4.1), the `init` / `up --tf-source` cobra help string
(`internal/cli/lifecycle.go:86,89`, currently `"override TF source
(path or URL)"`) is silent on relative-path resolution — the same gap
the `--var-file` help had before Sprint 12 scrutiny.

### Disposition

The help string lives in `internal/` (staff write surface) — architect
does **not** edit it. Judge whether it still misleads a user who passes
a relative `--tf-source=./mytf`. If yes, file the one-line
proposed-fix diff here as a staff-surface hand-off (low severity, fold
in only if cheap and non-disruptive to the three primary deliverables).
If the post-v1.4.1 behaviour + existing help is discoverable enough,
mark `accepted` and record the rationale.
