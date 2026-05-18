# Sprint 1 — tech writer issues, resolution notes

15 issues filed by the tech-writer review. **All 15 fixed in this integration pass.** Tech-writer caught real drift between PRD 01's developer-facing prose and the code the staff agent actually shipped, plus build-prerequisite drift from Sprint 1's Go-version bump that went undocumented in user-facing docs.

## Issue 1 (jumphost user `root` vs `ubuntu`) — fixed by aligning chapters with implementation

PRD 01's pseudocode said `User: "root"`; staff's `tryAutoJumphost` in `lifecycle.go` writes `"ubuntu"` because the upstream HCL provisions an Ubuntu cloud image whose default user is `ubuntu`. Implementation is right; PRD pseudocode and chapters were wrong.

Fixed: chapter 16 (lines 27, 56, 86, 193) and chapter 7 (line 95) updated to `ubuntu`. PRD 01's pseudocode left as-is for now — it's marked as "pseudocode" so it's not directly authoritative; the chapter changes are user-visible.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`, `book/src/07-quick-start.md`

## Issue 2 (`--tty` flag documented but doesn't exist) — fixed by removing the mention

Chapter 16 told readers to pass `--tty` for non-shell verbs; no such flag exists. Removed and replaced with the workaround: fall back to `roksbnkctl shell --on jumphost` for interactive PTY.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 3 (`targets show` documents missing fields) — fixed by trimming sample

Sample showed `host_key:` and `last_seen:` lines that the actual command never prints. Trimmed sample to match real output (`name`/`host`/`port`/`user`/`key_source`).

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 4 (KEY column missing `file:` prefix) — fixed

Sample showed `~/.ssh/id_ed25519`; binary actually emits `file:~/.ssh/id_ed25519` to disambiguate from `tf-output:` and `agent` sources.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 5 (TOFU prompt text drift) — fixed by replacing with implementation-actual text

Chapter showed an OpenSSH-style multi-line prompt; implementation prints a single-line prompt. Replaced verbatim with what `internal/remote/hostkeys.go:86` actually emits.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 6 (`roksbnkctl --version` doesn't exist) — fixed

Only the `version` subcommand exists; `--version` flag is unbound. Chapter 4 updated to use `roksbnkctl version`.

**Status**: ✅ resolved (option chosen: chapter fix, not adding the flag)
**Files**: `book/src/04-installation.md`

## Issue 7 (doctor sample output drift) — fixed by re-capturing live

Re-ran `roksbnkctl doctor` against a real workspace, captured actual output, replaced sample. Dropped the literal "8 checks" count (binary emits 9 rows; count would still drift). Added a one-sentence explainer of the row format.

**Status**: ✅ resolved
**Files**: `book/src/04-installation.md`

## Issue 8 (Go 1.23 vs go.mod 1.25) — fixed (HIGH severity)

Sprint 1's go.mod toolchain bump (1.23 → 1.25, forced by testcontainers-go and gliderlabs/ssh transitives) wasn't reflected in any user-facing doc. A user with Go 1.23 hitting `make build` would get a confusing "module requires Go 1.25" error and the chapter's diagnosis ("Go too old, declares 1.23") would be self-confirming-but-wrong.

Bulk fix: every `1.23` reference in chapter 4 and README replaced with `1.25`, including `golang:1.23-alpine` Docker image tags. Added a sentence to chapter 4 explaining why the bump happened (testcontainers + gliderlabs SSH transitives).

**Status**: ✅ resolved
**Files**: `book/src/04-installation.md`, `README.md`

## Issue 9 (PRD 01 link 404 in published book) — fixed by switching to GitHub canonical URL

Chapter 16's two `../../docs/prd/01-...md` links resolve on the filesystem but break in the published book (mdBook only publishes `book/src/`). Switched both to `https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md`.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 10 (README + CONTRIBUTING don't mention `--on`) — fixed

Added a new bullet to README "Highlights" describing `--on jumphost` and linking to chapter 16. Added a "Running integration tests" subsection to CONTRIBUTING.md documenting `make test-integration` + the Docker prerequisite + the `-tags integration` build-tag pattern.

**Status**: ✅ resolved
**Files**: `README.md`, `CONTRIBUTING.md`

## Issue 11 (chapter 7 docker `golang:1.23-alpine`) — fixed alongside Issue 8

Same root cause as Issue 8; the bulk `1.23 → 1.25` replace covered chapter 4's docker image tags. Chapter 7 doesn't actually have a docker invocation; this issue was filed against chapter 4's content that the validator misread as chapter 7. No-op as filed; the docker image fix is in Issue 8.

**Status**: ✅ resolved (already covered by Issue 8 fix)

## Issue 12 (stale `book/src/ssh.md` xref in `remote.go`) — fixed

Code comment in `internal/cli/remote.go:36` pointed at a nonexistent file. Updated to point at `book/src/16-on-flag-ssh-jumphosts.md` "Behaviour details" section (the relevant chapter).

**Status**: ✅ resolved
**Files**: `internal/cli/remote.go`

## Issue 13 (chapter 7 sample output drift) — fixed by replacing with implementation-actual log line

Chapter 7's sample `up` output showed a two-line `Auto-populating ... / Wrote target ...` block that doesn't match what the binary emits. Replaced with the actual single-line `✓ Auto-registered target jumphost (...)` log message from `tryAutoJumphost`.

**Status**: ✅ resolved
**Files**: `book/src/07-quick-start.md`

## Issue 14 (chapter 16 missing Windows ssh-agent note) — fixed

Added a Windows ssh-agent bullet to chapter 16's "What `--on` doesn't do (yet)" section so a Windows reader who skipped to that section doesn't miss the limitation. Cross-references the Key sources section above.

**Status**: ✅ resolved
**Files**: `book/src/16-on-flag-ssh-jumphosts.md`

## Issue 15 (chapter 3 v0.9 forward-look missing PRD 03 link) — fixed

Added a PRD 03 GitHub-canonical URL alongside the chapter 17 link in chapter 3's v0.9 forward-look paragraph. Pattern matches Issue 9's GitHub-canonical URL approach.

**Status**: ✅ resolved
**Files**: `book/src/03-what-roksbnkctl-does.md`

## Verification post-fix

- `go build ./...` clean
- `go test ./...` clean
- `go vet ./...` clean
- `gofmt -d -l .` clean
- All 15 issues addressed (12 fixed in source, 3 resolved as "covered by another issue's fix" / "implementation-aligned")
