# Sprint 9 — tech-writer issues (resolved)

Status: applied 4 HIGH-severity fixes in integration round. **Not yet committed** — sending to tech-writer for re-verification via SendMessage before the integration commit lands. The 6 medium/low issues are deferred to v1.2.1 polish or Sprint 10.

## Issue 1: chapter 19 §"Trusted-profile flow (v1.2+)" claims complete closure
**Status**: resolved — Sprint 10 partial-closure admonition added
**Resolution**: chapter 19 §"Trusted-profile flow (v1.2+)" now opens with a callout that names the partial closure: provisioning side ships in v1.2.0; in-pod `ibmcloud login` wrap still uses `--apikey "$IBMCLOUD_API_KEY"` (Sprint 10 work). The callout cross-links to staff Issue 2 and tells security-minded readers to use `--trusted-profile=off` for v1.2.0 if they need the runtime wrap to actually work. Closes the "documented closure but partial reality" honesty gap.

## Issue 2: CHANGELOG `### Deferred` doesn't mention in-pod login-wrap
**Status**: resolved — bullet added
**Resolution**: added a bullet at the top of `### Deferred (v1.x roadmap, post-v1.2.0)` (above the existing three) documenting the in-pod login-wrap as Sprint 10 work, including the file:line reference to `internal/exec/k8s.go`, the failure mode (`missing API key` under auto-success), and the workaround (`--trusted-profile=off`). Mirrors the chapter 19 callout for users who read CHANGELOG first.

## Issue 3: chapter 19 sample output drift across four blocks
**Status**: resolved — all four samples rewritten to match `ops.go` verbatim
**Resolution**: replaced the four sample blocks (`--trusted-profile=auto` success, `auto` fallback, `--trusted-profile=off`, `ops uninstall --confirm`) with the actual binary emissions. Vocabulary change mechanical (`applied` → `created`/`updated`/`exists`); dropped the six prescriptive trusted-profile-provisioning lines the binary doesn't emit; single line `✓ Provisioned IAM trusted profile <name> (<id>)` replaces the prescriptive breakdown. Success summary changed from `✓ pod Ready (3.4s)` to `✓ Ops pod is Ready (trusted profile <name>)` / `(static-key Secret)` per branch. Uninstall reordered: trusted-profile delete first (before cluster-side), Secret delete included (was omitted).

Step-3 prose ("Policy attachment") also corrected — v1.2 ships with **no** default policies attached (per Issue 5; not a separate fix but corrected inline during the Issue 3 rewrite since the prose lived in the same section).

## Issue 10: chapter 19 smoke test broken by Issue 1
**Status**: resolved — smoke test guarded
**Resolution**: the `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` smoke test at lines 217-219 is now framed as the **Sprint 10 target**, not the v1.2.0 shipped behaviour. Replaced the prescriptive "fresh OAuth token returns" sample with a `> Heads up — Sprint 10 carry-over` admonition naming the `missing API key` failure mode and the `--trusted-profile=off` workaround. Once Sprint 10 ships, the auto-mode smoke test will return the token directly; the chapter prose says so explicitly.

## Issue 6: chapter 19 stderr warning text drift (HIGH, validator's Issue 7)
**Status**: already resolved (integration round before tech-writer dispatch)
**Resolution**: as filed by tech-writer.

## Issue 11: dogfooding loop — `--trusted-profile=off` documented as first-class
**Status**: closed — positive finding, no action needed
**Resolution**: as filed.

## Issue 12: dogfooding loop — `docker inspect` no-leak discoverability
**Status**: closed — positive finding, no action needed
**Resolution**: as filed.

---

## Deferred to v1.2.1 polish or Sprint 10 (no v1.2.0 tag blocker)

### Issue 4: chapter 19 `ops show` shape under `--trusted-profile=auto` (medium)
**Status**: deferred to v1.2.1 polish
**Reason**: Issue 3's success-sample rewrite already removed the prescriptive `secret: <none — trusted profile X in use>` block that was the worst part of this. Remaining drift is in §"`roksbnkctl ops show`" (the show section near the end of the chapter), low-stakes given that `ops show`'s overall format is unchanged and the trusted-profile case is mentioned in §"Trusted-profile flow" already. Tracked for v1.2.1 polish.

### Issue 5: chapter 19 claims policy attachment that doesn't happen (medium)
**Status**: resolved inline during Issue 3 rewrite
**Resolution**: the step-3 "Policy attachment" prose was rewritten alongside the success-sample to acknowledge that v1.2 ships with no default policies attached, with a pointer to the v1.x deferred customisation work.

### Issue 7: chapter 14 wording drift "warning" vs "warning block" (low)
**Status**: deferred to v1.2.1 polish
**Reason**: minor inconsistency between "single line" framing and "block" framing in chapter 14 §"Compatibility note". Not worth a tag-blocking edit cycle.

### Issue 8: chapter 14 §"What's new in v1.2" section position (low)
**Status**: deferred to v1.2.1 polish
**Reason**: tech-writer's recommendation was structural (move the section earlier in chapter 14). Not a correctness issue.

### Issue 9: chapter 19 §"Credential propagation" v1.2 callout placement (medium)
**Status**: deferred to v1.2.1 polish
**Reason**: discoverability nit — the v1.2 callout exists, just in a less-prominent position than ideal. Not a correctness issue.

### Issue 13: chapter 19 `<workspace>` vs `sandbox-roks` placeholder consistency (low)
**Status**: deferred to v1.2.1 polish
**Reason**: stylistic. The chapter mixes a real-looking name (`sandbox-roks`) in samples with the abstract `<workspace>` in prose. Both work but consistency would be cleaner.
