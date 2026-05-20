---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: CI `go test -race ./...` does not clear `HOME` / unset `KUBECONFIG`; the hermetic invariant is local-only'
labels: []
assignees: ''
---

## Symptom

The integrator's local pre-tag gate runs the unit suite as
`HOME=$(mktemp -d) KUBECONFIG= go test -race ./...` — the hermetic
shape pinned by every Sprint 12-16 validator closure ("**full hermetic
`go test -race ./...`** (CI's exact command, `HOME`=empty,
`KUBECONFIG` unset)"). The `ci.yml` job that actually runs on every PR
does **not** match that shape: the `go test` step is bare
`go test -race ./...` with the runner's default `HOME=/home/runner`
(macOS: `/Users/runner`) and whatever `KUBECONFIG` setup-go / the
runner image leak. The Sprint 12 Issue 3 bug class
(`KUBECONFIG` leaking into the test process and changing behavior) is
therefore catchable only by the integrator, not by CI.

The closure comments in `issues/issue_sprint16_validator.md` Issue 1
say "CI's exact command, `HOME`=empty, `KUBECONFIG` unset" — that
sentence describes what the integrator typed at the prompt, not what
`ci.yml` actually does. The repo's hermetic discipline is documented
as CI-enforced and is in fact local-enforced; a regression that only
fires when `$HOME/.kube/config` exists, or when `$HOME/.roksbnkctl/`
has stale state from a prior run, passes CI green and only blows up
on the integrator's machine pre-tag (or on a contributor's machine
that happens to have a stray `~/.kube/config`).

## Reproduction

```
# 1. on a clean checkout
cd /path/to/roksbnkctl

# 2. simulate the dirty-HOME a CI runner has (or a contributor laptop):
mkdir -p $HOME/.roksbnkctl/e2e/state
echo 'workspace: e2e' > $HOME/.roksbnkctl/e2e/config.yaml
export KUBECONFIG=$HOME/.kube/config        # may or may not exist

# 3. the CI-shaped command, verbatim from .github/workflows/ci.yml:
go test -race ./...

# 4. compare with the hermetic shape:
HOME=$(mktemp -d) KUBECONFIG= go test -race ./...

# Step 3 picks up the real HOME / real KUBECONFIG. Step 4 does not.
# A test that reads $HOME/.roksbnkctl/ or $KUBECONFIG can pass in (3)
# and fail in (4), or vice versa — the two are not the same gate.
```

The relevant `ci.yml` lines today (commit on `main`):

```
- name: go test
  # -race catches data races; cheap to run on the small unit suite
  # we have today. [...]
  run: go test -race ./...
```

There is no `env:` block on this step. The runner's `$HOME` and
`$KUBECONFIG` are inherited.

## Expected behavior

The CI step matches the hermetic shape every validator closure
documents: `HOME` pointed at a runner-local tempdir, `KUBECONFIG`
explicitly empty. A test that depends on a real `$HOME/.kube/config`
to pass either fails CI or self-skips with a yellow message — it
never passes CI by accident and fails the integrator later.

## Actual behavior

`go test -race ./...` runs with the GitHub-hosted-runner's defaults.
On Linux runners `$HOME=/home/runner` (no `.kube/config` today, but
nothing prevents a future workflow step from creating one before the
test step); on macOS runners `$HOME=/Users/runner` (same caveat).
`KUBECONFIG` is unset on stock runners but set on any future image
that pre-installs a kube toolchain — there is no positive assertion.
The integrator's local gate and CI are not running the same command.

## Environment

- `roksbnkctl version`: N/A — this is a CI / repo-infrastructure bug.
- OS / arch: GitHub-hosted `ubuntu-latest` + `macos-latest`.
- IBM Cloud region: N/A.
- Backend: N/A.

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** the original `ci.yml` was written before the
   Sprint 12-13 `KUBECONFIG`-leak class was discovered and the
   hermetic shape became the gate; the env hygiene shipped as a local
   integrator habit and was never propagated into the YAML.
2. **Second:** the integrator's closures parrot the same sentence
   ("CI's exact command, `HOME`=empty, `KUBECONFIG` unset") run after
   run, so the gap was visually invisible at review time — the prose
   asserts an invariant the YAML doesn't hold.

## Acceptance criteria

1. `.github/workflows/ci.yml`'s `go test` step gains an `env:` block
   (or per-line `HOME=... KUBECONFIG=` prefix) that sets `HOME` to a
   per-job tempdir created at step time and explicitly clears
   `KUBECONFIG` (`KUBECONFIG: ""`). The step's resolved command in
   the runner log reads `HOME=<tmp>` and `KUBECONFIG=` (empty), not
   `/home/runner` / inherited.
2. A new file `internal/test/hermetic_env_test.go` (new test file —
   no edit to any existing `_test.go`) asserts at process start that
   `os.Getenv("KUBECONFIG") == ""` and that `os.Getenv("HOME")` does
   not equal `/home/runner` / `/Users/runner` / the path returned by
   `os/user.Current().HomeDir` on the CI runner; the test self-skips
   when not running under `CI=true` so local laptops aren't punished.
3. CI runs the new test in both the Linux and macOS matrix legs and
   it passes. A throwaway PR that *removes* the `env:` block fails
   the new test in both legs — the gate is grep-asserted, not just
   prose-claimed.
4. The CHANGELOG `### Changed` entry for this fix names the gap by
   its full shape: "CI `go test -race` step now matches the hermetic
   shape (`HOME=<tmp> KUBECONFIG=`) the validator closures document;
   previously the env-hygiene was local-only."
5. Regression: the next Sprint 17+ validator closure that quotes
   "CI's exact command, `HOME`=empty, `KUBECONFIG` unset" is
   factually correct, not aspirational.

## Out of scope (deliberately)

- The `integration` / `docker-backend` / `k8s-backend` jobs in
  `ci.yml` (those legitimately need a real `$HOME` for docker /
  kind config; they aren't the hermetic-unit-gate).
- Removing `KUBECONFIG` from the test process at the Go level
  (covered by Sprint 13 `internal/cli/env_split_test.go` for the
  remote-dispatch path; this issue is about the test runner's
  environment, not the binary's).
- Changing the `go test` command itself (`-race`, package set). Just
  the env it runs under.
- Windows runner: `windows-build` only does `go build`, not
  `go test`. If a future PR adds tests on Windows the same env shape
  applies — out of scope here.

## Notes

- Sprint 12 Issue 3 (`KUBECONFIG` leak into `--on` remote env) is
  the canonical reason this hygiene exists. That fix is in
  `internal/cli/env_split_test.go` but the test that proves it was
  always run with the integrator's hermetic env; rerunning it under
  the CI-default env is not currently exercised.
- The integrator's commit-message footer "full hermetic
  `go test -race ./...`" is the prose this issue makes true.
- The `pre-commit.sh` hook (line 29) runs `go test -short
  ./internal/...` with whatever env the developer has — not the same
  gate; out of scope here but worth a follow-up if developers start
  hitting "passed pre-commit, failed CI" on this class.
