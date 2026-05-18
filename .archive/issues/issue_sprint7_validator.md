# Sprint 7 — validator issue log

Sprint 7 is **the launch sprint** — the final gate before the `v1.0` tag.
Issues filed by the validator agent during dispatch — the integrator
(or the parallel architect agent, where the file is under `book/src/`)
triages and resolves; resolutions land in
`resolved_sprint7_validator.md`.

Format matches prior sprints. One issue per chapter divergence (don't
batch). `Severity: roadmap` for non-blocking forward-looking
observations; `low/medium/high/blocker` for actionable findings.

Verification baseline (all green at filing time):

- `go build ./...` clean
- `go test ./...` clean (all packages — incl. Sprint 6's `TestProbe_TruncatedFlag`)
- `go vet ./...` clean
- `gofmt -d -l .` clean
- `DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true ROKSBNKCTL_E2E_SSH_TARGET=jumphost ./scripts/e2e-test-backends.sh` — clean (all phases I-N + L-DNS emit; green final line)
- `DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true TFVARS=<stub> ./scripts/e2e-test-full.sh` — clean (baseline + backends drivers chain green)
- `e2e-full.yml` preflight fail-fast polish landed (this sprint — Sprint 6 Issue 5 carry-over)
- `docs/E2E_TEST.md` §"CI preflight" added to document the new behaviour

The 8 issues below are all **chapter code-example divergences from the
shipped binary surface**. Architect folds (chapters are read-only for
validator). None are build/test regressions; none block the `v1.0` tag
on technical grounds — they're first-impression failures for a
dogfooding user. Severity is `high` when the chapter documents a flag
or command that doesn't exist; `medium` for stale file paths or
broken cross-link anchors.

## Issue 1 (HIGH — chapter 11 documents non-existent `--keep-cluster` flag)

**Severity**: high (chapter documents a flag the binary doesn't expose)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: `book/src/11-tearing-down.md:237`

**Description**: The chapter's "destroy only the BNK overlay" paragraph reads:

> If you want to destroy *only* the BNK overlay but leave the cluster up
> (because somebody else is going to keep using it), use `roksbnkctl
> down --keep-cluster --auto` instead. The `--keep-cluster` flag skips
> the `roks_cluster` module during destroy, leaving the cluster, VPC,
> and jumphost intact.

`--keep-cluster` is not a flag on `roksbnkctl down`. The actual `down`
flags (per chapter 27 and `roksbnkctl down --help`) are only `--auto`
and `--var-file`. Running the documented command fails with
`Error: unknown flag: --keep-cluster`.

The cluster-vs-trial separation does exist (Sprint 3 `cluster up` / `cluster
down` split), but it's enforced by **using a different command**
(`roksbnkctl cluster down` for the cluster phase, `roksbnkctl down` for
trial-only), not a flag on `down`.

**Fix**: rewrite the paragraph to point at the `cluster up`/`cluster down`
split — `roksbnkctl down` already only destroys the trial; the cluster
stays up by default. The "leave the cluster up" outcome is the **default
behaviour** when you've used `cluster up` separately, not a flag toggle.

## Issue 2 (HIGH — chapter 12 documents non-existent `--api-key-stdin` flag)

**Severity**: high (chapter documents a flag the binary doesn't expose)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: `book/src/12-workspace-config.md:333`

**Description**: The "Worked example: bootstrap a workspace from scratch"
walkthrough includes:

```
op read 'op://Private/IBM Cloud/api-key' | roksbnkctl init -w dev --api-key-stdin
```

`--api-key-stdin` is not a flag on `roksbnkctl init`. The actual init
flags are `--tf-source` and `--upgrade-tf`. The API-key resolution chain
documented in chapter 14 (env → keychain → workspace `api_key_b64` →
TTY prompt) is the supported surface — there's no stdin source.

**Fix**: either rewrite the example to use the `IBMCLOUD_API_KEY` env-var
path (which is the closest practical equivalent: `IBMCLOUD_API_KEY=$(op
read 'op://...') roksbnkctl init -w dev --auto`), or file the stdin
flag as a v1.x roadmap item. Recommend: rewrite to env-var.

## Issue 3 (HIGH — chapter 17 documents non-existent `roksbnkctl destroy` command and `--auto-approve` flag)

**Severity**: high (chapter documents a command + flag the binary doesn't expose)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: `book/src/17-execution-backends.md:329-336`

**Description**: The chapter's "Supported commands" block under
"terraform docker backend" reads:

```
roksbnkctl up      --backend docker  [--var-file <path>] [--auto-approve]
roksbnkctl plan    --backend docker  [--var-file <path>]
roksbnkctl apply   --backend docker  [--var-file <path>] [--auto-approve]
roksbnkctl destroy --backend docker  [--var-file <path>] [--auto-approve]
```

Two divergences:

1. **`roksbnkctl destroy` is not a command.** The destroy verb is `roksbnkctl down`. Running the documented command fails with `Error: unknown command "destroy"`.
2. **`--auto-approve` is not a flag.** The actual flag (per chapter 27 and `roksbnkctl up --help`) is `--auto`. The longer name comes from terraform's own CLI; roksbnkctl wraps it but renames to `--auto` for terseness and consistency across `up` / `apply` / `down`.

The follow-on prose ("Flags that the local terraform backend honours
(`--var-file`, `--auto-approve`, plus the `-w/--workspace` selector)…")
repeats `--auto-approve`. Same fix.

**Fix**: replace `destroy` with `down` and `--auto-approve` with `--auto`
in lines 329-336. Verify other instances of `auto-approve` in the same
chapter (if any) and across other chapters with the same prose pattern.

## Issue 4 (HIGH — chapter 28 documents non-existent `--refresh-kubeconfig` flag)

**Severity**: high (chapter documents a flag the binary doesn't expose)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: `book/src/28-configuration-reference.md:16`

**Description**: The "Workspace config — top-of-chapter metadata" table reads:

| Updated by | `roksbnkctl init --upgrade-tf`, `roksbnkctl init --refresh-kubeconfig`, hand-editing |

`--refresh-kubeconfig` is not a flag on `roksbnkctl init`. The actual
init flags are `--tf-source` and `--upgrade-tf`. The kubeconfig-refresh
operation is exposed via a separate command: `roksbnkctl kubeconfig
--download` (per chapter 27).

Sprint 6 caught this exact drift (per `resolved_sprint6_validator.md`)
and supposedly resolved it. It's resurfaced — either the resolution
didn't land here or a subsequent edit reintroduced it.

**Fix**: replace `roksbnkctl init --refresh-kubeconfig` with `roksbnkctl
kubeconfig --download` in the metadata table.

## Issue 5 (MEDIUM — stale file-path drift: `state/terraform.tfvars.user` in chapters 10/12/14)

**Severity**: medium (file-path divergence; the wrong path doesn't exist on disk so users following the chapter would get "No such file or directory")

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**Files**:
- `book/src/10-deploying-bnk-trials.md:153`
- `book/src/12-workspace-config.md:267`
- `book/src/14-credentials-resolver.md:210`

**Description**: All three chapters reference the optional manual-override
tfvars file at `~/.roksbnkctl/<ws>/state/terraform.tfvars.user`. The
actual path (per `internal/tf/terraform.go::UserTFVarsPath`) is one
level up — the file is a **sibling of `config.yaml`**, not inside
`state/`:

```go
// internal/tf/terraform.go:163-171
// UserTFVarsPath: <workspace-dir>/terraform.tfvars.user (sibling to
// config.yaml). Optional — if present, roksbnkctl passes it to terraform
// as a second -var-file after the auto-rendered one, so values in the
// user file override values from config.yaml.
func (w *Workspace) UserTFVarsPath() string {
    return filepath.Join(filepath.Dir(w.stateDir), "terraform.tfvars.user")
}
```

So the correct path is `~/.roksbnkctl/<ws>/terraform.tfvars.user`. Sprint
6 caught the same drift in a different chapter; it's resurfaced (or
never fully swept) in 10, 12, and 14.

**Fix**: search-and-replace `state/terraform.tfvars.user` →
`terraform.tfvars.user` across all three chapters. Verify no other
chapter has the same drift (Issue 5 is the third instance Sprint 6+7
have caught; a final repo-wide grep would confirm).

## Issue 6 (MEDIUM — broken cross-link anchors — 5 instances)

**Severity**: medium (broken anchors render as no-op clicks; the page loads but the in-page jump fails)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**Files** (and the offending anchor):

| File | Line | Link |
|---|---|---|
| `17-execution-backends.md` | 243 | `[…](./14-credentials-resolver.md#backend-specific-cred-propagation-forward-look)` — chapter 14's actual heading is `## Backend-specific cred propagation` (no `-forward-look` suffix) |
| `26-troubleshooting.md` | 196 | `[…](./21-dns-testing-gslb.md#the-gslb-compare-workflow)` — chapter 21's actual slug is `#the---gslb-compare-workflow` (3 dashes, from `## The \`--gslb-compare\` workflow`) |
| `26-troubleshooting.md` | 241 | `[…](./25-cos-supply-chain.md#worked-example-upload-a-new-far-image)` — chapter 25's worked example is `## Worked example: rotating COS supply-chain assets`, so the slug is `#worked-example-rotating-cos-supply-chain-assets` |
| `30-glossary.md` | 208 | `[…](./26-troubleshooting.md#symptom-terraform-destroy-leaves-orphan-ibm-cloud-resources)` — chapter 26's actual heading slug ends `…-resources-lbs-security-groups-vpes` (the link truncates the parenthetical suffix; either extend the link or shorten the heading) |
| `preface.md` | 48 | `[…](./NN-slug.md)` — boilerplate placeholder string `NN-slug.md` was left in the rendered prose by mistake |

Verification: built the book locally with mdbook 0.4.40, extracted the
generated `id="…"` attributes from each chapter's HTML, and confirmed
the slugger I wrote (which produces matching ground-truth slugs for
each of chapters 12/14/22's headings) flags exactly these 5 as
unresolved. Slug algorithm verified: lowercase, vanish-class drops
backticks/quotes/parens/colons/dots/commas/em-dashes/en-dashes,
whitespace-char → `-` (whitespace runs are NOT collapsed —
`<space><em-dash><space>` produces `--`), literal `-` passes through,
trailing `-` trimmed, leading `-` preserved.

**Fix**: for the 4 anchor-mismatch cases, either rename the target
heading to match the link OR update the link to match the target. For
the `preface.md` boilerplate, replace with a real chapter link.

## Issue 7 (MEDIUM — search-index canonical-query top-hit miss-routes)

**Severity**: medium (the top hit for several canonical queries is the
auto-generated reference chapter 27 or the terraform-variable-reference
chapter 29, not the chapter that has the *prose* explanation)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: applies across multiple chapters via prose distribution

**Description**: Built the book locally with `mdbook build book/` and ran
the canonical queries from `prompts/sprint7/validator.md` against the
generated `searchindex.json` (heuristic: full-token match per doc, scored
by token-occurrence count — proxy for lunr's actual ranking, which
weights titles + breadcrumbs more heavily; the actual top hits will
shift somewhat but the prose-vs-reference pattern below holds).

| Query | Heuristic top hit | Expected top hit (per prompt) |
|---|---|---|
| `GSLB` | 21-dns-testing-gslb.html | 21 ✓ |
| `jumphost` | 29-terraform-variable-reference.html | 16 |
| `kubeconfig` | 05-doctor.html | 14 / 11 / 6 (any of the prose chapters) |
| `backend k8s` | 32-extending-roksbnkctl.html | 17 |
| `on jumphost` | 29-terraform-variable-reference.html | 16 |
| `cred resolver` | 14-credentials-resolver.html | 14 ✓ |
| `ops pod` | 19-in-cluster-ops-pod.html | 19 ✓ |
| `terraform via docker` | 18-choosing-backend.html | 17 §"terraform via docker" |
| `iperf3 north-south` | 17-execution-backends.html | 22 §"north-south" |
| `OpenShift SCC` | 02-why-roks.html | 22 or 26 |
| `cluster register` | 08-cluster-phase.html | 9 |
| `cos object put` | 29-terraform-variable-reference.html | 25 |
| `init --upgrade-tf` | 27-command-reference.html | 12 or 4 |
| `TOFU host key` | 16-on-flag-ssh-jumphosts.html | 16 ✓ |
| `TOFU` (bare) | 05-doctor.html | 30 glossary |

Of the 15 canonical queries: 4 match expected; 11 miss-route. The
miss-routes cluster around chapter 29 (terraform variable reference)
and chapter 27 (command reference) — both are auto-generated reference
dumps that mention every keyword in passing without the prose context
a dogfooding user needs.

**Fix recommendation**: this is a search-relevance issue, not a broken-link
issue. The mdbook search weights heading occurrence and early-chapter
prose more heavily than mid-paragraph mentions; surfacing the canonical
query terms in the **first 200 characters** of each prose chapter
(e.g., chapter 22's lead-in could mention `iperf3 north-south`; chapter
25's lead-in could mention `cos object put`) would tilt the ranking.
The architect's polish pass is the right vehicle.

Note: lunr's actual ranking is title-weighted; the title strings (e.g.
"Throughput testing" vs the prose-mentioned "iperf3 north-south") are
the dominant signal. Embedding the search-canonical terms in chapter
titles is a heavier lift — for v1.0 ship as-is, file as a polish item
for v1.x. The prompt explicitly anticipated this trade-off ("if a query
returns the wrong chapter, file an issue").

## Issue 8 (LOW — chapter 22 references CLI flag `--streams` that doesn't exist on `test throughput`)

**Severity**: low (the prose is interpretable as referring to iperf3's
own knob rather than a roksbnkctl flag, but the formatting suggests a
roksbnkctl CLI flag)

**Status**: resolved by architect fold (see `resolved_sprint7_validator.md`)

**File**: `book/src/22-throughput-testing.md:186`

**Description**: The "Reading the output" table row for
`end.cpu_utilization_percent.host_total` reads:

> Whether the client CPU was the bottleneck. >80% suggests the iperf3
> client maxed out CPU before the network did — increase `--streams` to
> spread load, or run on a beefier client.

The `` `--streams` `` formatting reads as a roksbnkctl CLI flag, but
`roksbnkctl test throughput` only exposes `--cross-node`, `--keep`, and
`--mode`. No `--streams` flag is plumbed through to iperf3 from
roksbnkctl in v1.0.

**Fix**: either (a) reword to make clear this is an iperf3-server-side
knob the user would set on the deployed server pod (not on the
roksbnkctl CLI), or (b) file an enhancement to plumb `--streams`
through `test throughput` as a v1.x feature and remove the prose
reference for v1.0. Recommend (a) — the reword is cheap and the v1.x
plumbing isn't on the roadmap yet.

## Summary

8 issues filed: 4 high (non-existent flag / command), 3 medium
(stale file path; broken anchors x5; search-index miss-routes), 1 low
(flag-formatting ambiguity).

**Zero build / test / vet / gofmt regressions.** **Zero DRY_RUN walkthrough
regressions.** All 8 are chapter-prose divergences from the shipped
binary surface — first-impression failures for a dogfooding user, but
none block the `v1.0` tag on technical grounds.

The optional `e2e-full.yml` preflight fail-fast polish (Sprint 6 Issue
5 carry-over) landed in this sprint; documented in `docs/E2E_TEST.md`
§"CI preflight". Both DRY_RUN walkthroughs green at validator-run time
(no integrator defer needed this sprint).
