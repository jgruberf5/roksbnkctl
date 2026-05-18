# Sprint 1 — tech writer issues

Format matches Sprint 0. Findings cover the 6 new chapters
(1, 2, 3, 4, 7, 16), the staff agent's SSH/--on/targets implementation,
README/CONTRIBUTING drift, and PRD/PLAN cross-document drift. All
findings are doc/example-correctness only — no code changes proposed.

## Issue 1: chapter 16 documents auto-discovered jumphost user as `root` but implementation writes `ubuntu`

**Severity**: medium
**Status**: open
**Description**: Chapter 16 § "Auto-discovery from `roksbnkctl up`"
shows the auto-population output as `(user: root, ...)` and the
preceding `targets:` example block lists `user: root` for the
`jumphost` entry (lines 28, 56). PRD 01 § "Auto-discovery from TF
output" pseudocode also uses `User: "root"`. The staff agent's
implementation in `internal/cli/lifecycle.go::tryAutoJumphost` (line
348) writes `User: "ubuntu"` instead, with the inline comment
"upstream HCL provisions Ubuntu cloud-init users". A reader who
copies the chapter's `roksbnkctl exec --on jumphost -- whoami`
example expects `root`; they will get `ubuntu`. Chapter 7 step 3
also shows `(user: root, key: tf-output:jumphost_shared_key)` in the
sample `up` output (line 95) — same drift.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(lines 28, 56, ~80–87 sample output);
`/mnt/d/project/roksbnkctl/book/src/07-quick-start.md` (line 95).
**Proposed fix**: either chapter prose updates to `ubuntu` (the
likely-correct path — the upstream HCL really does provision an
Ubuntu jumphost), OR staff agent realigns
`tryAutoJumphost` and PRD 01 to write `root`. PRD 01 alignment is
the cleaner outcome. File a matching staff follow-up to pick which
side moves.

## Issue 2: chapter 16 documents `--tty` flag that does not exist

**Severity**: medium
**Status**: open
**Description**: Chapter 16 § "Working examples → Behaviour details"
(line 218) says: "For other verbs, pass `--tty` if you need a PTY
(e.g. for `top` or any command that checks `isatty()`)". No `--tty`
flag is registered anywhere in `internal/cli/*.go` or on
`internal/remote.RunOpts` plumbed through from a flag. PRD 01 §
"Scope/in scope" lists `--tty` as a deliverable but the staff agent
did not implement it (the `RunOpts.TTY` field exists internally but
no CLI flag binds it; `dispatchRemote` is called with `tty: false`
for every non-shell verb). A reader who follows the chapter and
runs `roksbnkctl exec --on jumphost --tty -- top` will get
`unknown flag: --tty`.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(line 218).
**Proposed fix**: either the chapter is corrected to remove the
`--tty` mention (and explain that PTY is auto-on for `shell --on`
only in v0.7), OR a follow-up staff issue adds the flag. Removing
the mention is the lighter touch given the rest of the chapter's
"What --on doesn't do (yet)" section is already there to absorb
deferrals.

## Issue 3: chapter 16 `roksbnkctl targets show` output documents fields the implementation does not print

**Severity**: medium
**Status**: open
**Description**: Chapter 16 § "`targets show <name>`" (lines 142–150)
shows sample output containing two fields that the implementation
never prints: `host_key:` and `last_seen:`. The actual
`runTargetsShow` in `internal/cli/targets.go` (lines 97–117) prints
only `name`, `host`, `port`, `user`, and one of `key_path` /
`key_source` — there is no host-key fingerprint lookup against
`~/.roksbnkctl/known_hosts` and no last-seen tracking. A reader
trusting the chapter would expect `targets show jumphost` to surface
the recorded fingerprint before they connect; the actual command
won't show that information. The chapter's prose immediately after
the sample even leans on the missing field: "Useful for confirming
what you're trusting before running a command."
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(lines 141–152).
**Proposed fix**: trim the sample output and the trailing prose to
match what the binary actually prints, OR file a small staff
follow-up to add `host_key` lookup (the data is in
`~/.roksbnkctl/known_hosts` already; rendering it is a few lines).
The sample should also drop `last_seen` unless someone wants to
build the recording infrastructure for it.

## Issue 4: chapter 16 `targets list` KEY column omits the `file:` prefix the binary actually emits

**Severity**: low
**Status**: open
**Description**: Chapter 16 § "`targets list`" (lines 130–135) shows
the KEY column with values `tf-output:jumphost_shared_key` and
`~/.ssh/id_ed25519`. The implementation
(`internal/remote/targets.go::KeySourceDescription`, lines 143–155)
returns `file:~/.ssh/id_ed25519` for KeyPath-backed targets — the
`file:` prefix is intentional, see `targets_test.go::TestKeySourceDescription`
lines 122–125. So the bastion row in chapter 16's sample should read
`file:~/.ssh/id_ed25519`. Minor cosmetic drift; doesn't break a
reader's command but does mismatch what they will see.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(line 134).
**Proposed fix**: change the sample row to
`bastion    ops.example.com:22  jgruber  file:~/.ssh/id_ed25519`.

## Issue 5: chapter 16 first-connect TOFU prompt sample does not match the implementation's actual prompt text

**Severity**: low
**Status**: open
**Description**: Chapter 16 § "Host-key TOFU on first connect" (lines
80–87) shows a sample TOFU prompt borrowed from OpenSSH's wording:

```
The authenticity of host '169.45.91.177:22' can't be established.
ED25519 key fingerprint is SHA256:abc123def456ghi789jkl0mnopqrstuvwxyz/+=.
Add this key to ~/.roksbnkctl/known_hosts? [y/N]: y
```

The implementation's actual prompt (`internal/remote/hostkeys.go`
line 86) is a single line:

```
Add 169.45.91.177:22's key (SHA256:...) to ~/.roksbnkctl/known_hosts? [y/N]:
```

No "authenticity can't be established" line, no "ED25519 key
fingerprint is" line. The mismatch error sample (lines 95–102) also
diverges from the real `ErrHostKeyMismatch`-wrapped message
(`hostkeys.go` line 67–68): the chapter shows a multi-line block
including `exit code: 126`, but the binary produces a single-line
`error: host key mismatch: <host> known with <fp1> but server
presented <fp2>; if the host was rebuilt, edit
~/.roksbnkctl/known_hosts`. Readers may try to grep for the
chapter's exact wording in their logs and not find it.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(lines ~82–87, ~95–102).
**Proposed fix**: replace the sample blocks with the implementation's
actual single-line prompt text (run the binary against the
testcontainers fixture to capture the real string verbatim).
Alternatively, leave the chapter prose as a sketch but add an "actual
output may vary in formatting" disclaimer; the latter is weaker but
defensible given the existing "output blocks are illustrative" note
in chapter 7.

## Issue 6: chapter 4 documents `roksbnkctl --version` but only `roksbnkctl version` (subcommand) exists

**Severity**: medium
**Status**: open
**Description**: Chapter 4 § "Verifying the install → `roksbnkctl
--version`" (lines 124–136) shows `roksbnkctl --version` as a
working command. Running it returns `Error: unknown flag: --version`.
There is no `--version` flag binding; only the `version` subcommand
exists (`internal/cli/meta.go` lines 17–19). A reader following the
"first thing after install" verification step will hit an error and
think the binary is broken. Chapter 7 sidesteps this by using
`roksbnkctl status` instead, but a fresh install reader is most
likely to type `roksbnkctl --version` (kubectl/helm/terraform muscle
memory) and see the failure.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/04-installation.md`
(lines 124–136).
**Proposed fix**: change the heading and command from
`roksbnkctl --version` to `roksbnkctl version` throughout the
section. Alternatively, file a tiny staff follow-up to add a
`--version` persistent flag alias that prints the same string and
exits — five lines of cobra wiring; keeps both forms working and
matches user expectations from peer tools.

## Issue 7: chapter 4 `roksbnkctl doctor` sample output does not match the implementation's format or check count

**Severity**: medium
**Status**: open
**Description**: Chapter 4 § "Verifying the install → `roksbnkctl
doctor`" (lines 138–161) describes "an eight-check prereq +
credentials report" and shows formatted output like:

```
  ✓ terraform on PATH (terraform 1.7.5)
  ✓ kubectl on PATH (v1.29.2)        [optional — passthrough only]
  ...
8 checks, 7 passed, 1 warning, 0 failed.
```

The actual binary produces nine lines (terraform, iperf3, kubectl,
oc, ibmcloud, kubeconfig, workspace, ibmcloud api key, ibm cloud
auth) in a different format:

```
✓  terraform         /usr/bin/terraform (Terraform v1.15.2)  (required for `roksbnkctl up`)
⚠  iperf3            not on PATH  (needed for `roksbnkctl test throughput`)
...
```

No trailing summary line ("N checks, P passed, ..."), no `[optional
— passthrough only]` annotation, the path is shown rather than just
"on PATH (version)". The numeric "eight-check" count is also wrong —
nine checks run. Readers who interpret the chapter literally and
grep for "8 checks" or the trailing summary line will get an empty
match.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/04-installation.md`
(lines 138–161).
**Proposed fix**: re-capture the doctor sample by running
`/tmp/roksbnkctl-s1 doctor` (or `make build && ./bin/roksbnkctl
doctor`) into the chapter; update the count from "eight" to "nine"
or — better — drop the literal count and say "the doctor run". The
prose's "terraform is the only check that's hard-required" claim is
still true.

## Issue 8: chapter 4 + README declare Go 1.23+ but go.mod requires Go 1.25.0

**Severity**: high
**Status**: open
**Description**: Sprint 1's staff/validator agents bumped the `go`
directive in `go.mod` from `1.23` → `1.25.0` (forced by
`gliderlabs/ssh` and `testcontainers-go` transitive deps; documented
in `issues/resolved_sprint1_staff.md` Issue 5 and
`issues/issue_sprint1_validator.md` Issue 6). Three docs still
declare 1.23+ as the build prerequisite:

- `book/src/04-installation.md` line 11: "Go 1.23 or newer if you
  want a native build"
- `book/src/04-installation.md` line 18: "If `go version` reports
  `1.23` or newer"
- `book/src/04-installation.md` lines 36–43, 55, 68, 76, 79, 84:
  every `golang:1.23-alpine` invocation and the `make build` failure
  diagnosis says "Go too old. The module declares `go 1.23` in
  `go.mod`"
- `README.md` line 30: "Build requires Go 1.23 or newer"
- `README.md` line 35: "go version go1.23.x or newer"
- `README.md` Docker invocations: `golang:1.23-alpine`

A user with Go 1.23 on PATH will hit a `go: module requires Go 1.25`
error from `make build`, and the chapter's diagnosis ("most likely
cause is **Go too old**. The module declares `go 1.23`") will be
self-confirming but version-incorrect. Worse, the suggested Docker
fallback (`golang:1.23-alpine`) will fail the same way because
`go.mod` requires 1.25.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/04-installation.md` (multiple
lines, see above);
`/mnt/d/project/roksbnkctl/README.md` (lines 30, 35, plus docker
invocations).
**Proposed fix**: replace every `1.23` reference with `1.25` in
chapter 4 and README, including the `golang:1.25-alpine` image tag.
This is a mechanical search-and-replace; no prose restructuring
needed. The validator's resolved issue 5 already flagged the bump
"for v0.7 release notes" — these doc updates are the user-facing
half of that note.

## Issue 9: chapter 16's PRD 01 link path will 404 in the published book

**Severity**: medium
**Status**: open
**Description**: Chapter 16 references PRD 01 with a relative link
written as `[PRD 01](../../docs/prd/01-SSH-AND-ON-FLAG.md)` (lines 7
and 235). The link resolves correctly on the filesystem — `book/src/`
plus `../../docs/prd/...` lands at the repo's `docs/prd/...` — but
mdBook only publishes the contents of `book/src/` rendered into
`book/book/`. The rendered HTML
(`book/book/16-on-flag-ssh-jumphosts.html`) rewrites both occurrences
to `<a href="../../docs/prd/01-SSH-AND-ON-FLAG.html">PRD 01</a>`,
which from the published `https://jgruberf5.github.io/roksbnkctl/`
URL points outside the book and 404s. PRDs are not part of the
published book tree; they live alongside the source.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(lines 7, 235);
`/mnt/d/project/roksbnkctl/book/book/16-on-flag-ssh-jumphosts.html`
(rendered output).
**Proposed fix**: rewrite the two links to point at the GitHub
canonical: `[PRD 01](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md)`.
Alternative: copy PRD 01 into `book/src/` as e.g. `33-prd-01-ssh.md`
and link locally. The first option is lighter and matches how
peer projects (kubebuilder, etc.) link to design docs from rendered
books.

## Issue 10: README + CONTRIBUTING.md not updated for Sprint 1's flagship `--on` feature

**Severity**: medium
**Status**: open
**Description**: M1 / v0.7 ships the `--on` flag and SSH targets as
the headline feature (PLAN.md line 9 milestone description; chapter
3 line 92 calls it "the v0.7-flagship feature"). Neither
`README.md` nor `CONTRIBUTING.md` mention `--on`, SSH, jumphost, or
`targets`. A casual visitor scanning the README's "Highlights"
section will not learn that this release exists for SSH/jumphost
support, even though it's the entire point of the release. Similarly,
`CONTRIBUTING.md` has no mention of the new
`scripts/e2e-test.sh` Phase B7-B9 steps, the new
`internal/remote/integration_test.go`, or the `-tags integration`
build-tag pattern the validator agent introduced. A new contributor
who lands in `internal/remote/` or who runs the e2e script won't know
the integration tests exist or how to run them.
**Files affected**:
`/mnt/d/project/roksbnkctl/README.md`,
`/mnt/d/project/roksbnkctl/CONTRIBUTING.md`.
**Proposed fix**:
- Add one bullet to README "Highlights": "**`--on jumphost`** — run
  any passthrough (`exec`, `shell`, `kubectl`, `oc`, `ibmcloud`)
  against an auto-discovered SSH jumphost; useful in
  customer-firewalled or air-gapped environments. See
  [chapter 16](https://...book/.../16-on-flag-ssh-jumphosts.html)."
- Add a "Running integration tests" subsection to CONTRIBUTING.md
  under "Running tests", documenting `go test -tags integration
  ./internal/remote/...` (or `make test-integration`) and the Docker
  prerequisite. Tie it back to validator's Issue 6 about the tag-gated
  `go mod tidy` invocation.

## Issue 11: chapter 7 docker example uses outdated `golang:1.23-alpine` image

**Severity**: low
**Status**: open
**Description**: Same root cause as Issue 8 — chapter 4's docker-build
section uses `golang:1.23-alpine` everywhere, including the
cross-compile examples. Because the alpine image's bundled Go matches
the tag, Go 1.25 is required by `go.mod` post-Sprint-1 and the
1.23 image will fail the build. Filing as a separate issue from the
prose-version-string fix because the affected line is structurally
distinct (image tag, not prose) and may be missed by a single-pass
edit.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/04-installation.md` (lines 55,
68, 79, 85);
`/mnt/d/project/roksbnkctl/README.md` (Docker build section).
**Proposed fix**: bump every `golang:1.23-alpine` to
`golang:1.25-alpine` in the same pass as Issue 8.

## Issue 12: chapter 16 cross-reference to non-existent `book/src/ssh.md` in `internal/cli/remote.go`

**Severity**: low
**Status**: open
**Description**: Found while reviewing staff's code: comment in
`internal/cli/remote.go` line 36–37 says "users who hit 'ibmcloud
not logged in' on the remote should configure AcceptEnv on the
jumphost (documented in book/src/ssh.md, owned by the architect
agent)". No file `book/src/ssh.md` exists; the relevant chapter is
`16-on-flag-ssh-jumphosts.md` (and chapter 16's "Behaviour details"
already does cover `AcceptEnv IBMCLOUD_*`). This is a stale
codebase-comment pointer rather than a published-doc issue, but it
will mislead the next person who greps for "ssh.md" expecting to find
docs.
**Files affected**:
`/mnt/d/project/roksbnkctl/internal/cli/remote.go` (lines 36–37).
**Proposed fix**: either change the comment to point at
`book/src/16-on-flag-ssh-jumphosts.md`, or drop the path and say
"see chapter 16". Pure code-comment cleanup; no chapter content
changes.

## Issue 13: chapter 7 sample `up` output lists 77 resources but the testing module emits a separate jumphost instance — count is plausible but one section header inconsistency

**Severity**: low
**Status**: open
**Description**: Chapter 7 step 3 sample (lines 70–96) shows the
`Apply complete! Resources: 77 added, 0 changed, 0 destroyed.` line
followed by "→ Auto-populating targets.jumphost from terraform
outputs" and "✓ Wrote target 'jumphost' → 169.45.91.177 (user: root,
key: tf-output:jumphost_shared_key)". Beyond the user mismatch
already filed (Issue 1), the implementation's actual log line for
that step (`internal/cli/lifecycle.go::tryAutoJumphost` line 355)
reads:

```
✓ Auto-registered target jumphost (<ip>); use `roksbnkctl --on jumphost ...`
```

— different verb ("Auto-registered" vs "Wrote") and a different
trailing hint. Not a correctness break (it still describes the same
behavior) but readers won't see the chapter's exact strings on their
own runs. Combined with the fact that there's no "→ Auto-populating
targets.jumphost from terraform outputs" line in the binary's output
at all (the implementation goes straight to the ✓ line), the chapter
is two lines too long for a real session.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/07-quick-start.md`
(lines 94–96).
**Proposed fix**: replace the two-line `Auto-populating ... / Wrote
target ...` block with a single line matching the implementation:

```
✓ Auto-registered target jumphost (169.45.91.177); use `roksbnkctl --on jumphost ...`
```

## Issue 14: chapter 16 omits PRD 01's deferred items list (silent gap rather than "What --on doesn't do" mention)

**Severity**: low
**Status**: open
**Description**: PRD 01 § "Out of scope" enumerates 5 deliberately
deferred items: ProxyJump/multi-hop, `~/.ssh/config` parsing,
password auth, Windows agent, SCP/SFTP. Chapter 16's "What `--on`
doesn't do (yet)" section covers 4 of those (lifecycle commands,
ProxyJump, `~/.ssh/config`, password auth, SCP/SFTP) — but it
specifies "Lifecycle commands ... reject `--on` with a clear error
in v0.7" as one of the deferred items, which is technically a
**runtime behaviour** not a Phase 1 scope decision (PRD 01 lists
this under "in scope" as part of the dispatch surface, just with the
clear-error gate). The section also doesn't explicitly say
"Windows ssh-agent named-pipe protocol is not supported in v0.7"
even though chapter 4 line 173 mentions it in passing as a Windows
limitation. A reader on Windows reading chapter 16 alone will not
learn that the agent path won't work for them; they need to
back-reference chapter 4's compile-only line.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/16-on-flag-ssh-jumphosts.md`
(§ "What `--on` doesn't do (yet)", lines 222–230).
**Proposed fix**: add one bullet to the section: "**Windows
ssh-agent.** The `key_source: agent` path is Linux/macOS only in
v0.7 — Windows users must use `key_path` to a file. (Already noted
in the Key sources section, but called out here explicitly so a
Windows reader who only skimmed §`--on doesn't do` doesn't miss it.)"
The chapter already mentions this in the Key sources section line
69, but explicit cross-mention is worth the two lines.

## Issue 15: chapter 3's "v0.9" forward-look paragraph mentions `--backend` but no PRD is cross-linked

**Severity**: low
**Status**: open
**Description**: Chapter 3 § "What's coming in future releases" line
82 describes "v0.9 — four execution backends" with a `--backend`
flag selectable across `local | docker | k8s | ssh`. The chapter
links forward to chapter 17 ("Execution backends") but not to PRD
03 (`docs/prd/03-EXECUTION-BACKENDS.md`) where the design lives.
Chapter 16, by contrast, links forward to PRD 01 for the SSH design
rationale (line 7). Inconsistent treatment between sister chapters.
A reader who cares about the v0.9 design rationale won't find it
because chapter 17 is a stub for now.
**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/03-what-roksbnkctl-does.md`
(line 82).
**Proposed fix**: add a parenthetical link to PRD 03 alongside the
chapter 17 link, e.g. "[Chapter 17](./17-execution-backends.md)
covers the design (full design rationale in
[PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md))."
Use the GitHub canonical URL per Issue 9's pattern to avoid the
publishing-path 404.

---

*Total filed: 15 issues — 1 high (Go 1.25 doc drift, blocks
build-from-source), 6 medium (chapter 16 implementation drift on
user / `--tty` / `targets show` / TOFU prompt; chapter 4 doctor
output + `--version`; PRD 01 link 404; README/CONTRIBUTING SSH
mention), 8 low (cosmetic + cross-reference + tone).*
