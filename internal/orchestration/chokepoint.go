// Package orchestration is roksbnkctl's service layer: the lifecycle /
// cluster / remote-dispatch orchestration extracted out of the
// internal/cli god-package (Sprint 15 phase-1 decomposition), plus the
// single path/env normalization chokepoint that structurally retires
// the recurring "a value correct in the invocation context is wrong
// once it crosses a boundary" bug class (Sprint 12 Issues 1/2 +
// Sprint 13 Issue 1).
//
// internal/cli is a thin cobra adapter on top of this package: it binds
// flags, builds a ResolvedFlags exactly once at command entry (the root
// PersistentPreRunE), and delegates here. Nothing in this package may
// import internal/cli (the boundary is one-directional, asserted by the
// validator's import audit and a guard test).
//
// # The chokepoint
//
// Every path-valued flag (--var-file, --tf-source, and — by
// construction — any future one) is normalized against the invocation
// CWD exactly once, in ResolveInvocationContext. Process env is
// classified exactly once into a machine-portable core (safe to cross
// the --on SSH boundary) versus local-only (KUBECONFIG and any future
// local-path-valued var). Downstream code consumes the resolved struct;
// no RunE and no remote-dispatch caller re-derives a path or env. The
// chokepoint-invariant guard test (internal/cli) fails loudly if a
// future contributor reopens the class.
package orchestration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ── path normalization ──────────────────────────────────────────────

// expandTilde performs `~` / `~/...` expansion via os.UserHomeDir,
// matching the project convention (install.go's --dir handling). A
// home-dir lookup failure leaves the input unchanged — the caller's
// subsequent absolute/relative handling still applies.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// NormalizeVarFiles normalizes --var-file entries to absolute paths
// against the *invocation* CWD. Terraform runs with CWD = the per-phase
// state directory (~/.roksbnkctl/<workspace>/state[-cluster]/), so a
// user's `--var-file=./terraform.tfvars` would otherwise resolve there
// instead of in the shell directory they typed it from (Sprint 12
// Issue 1, "v1.4.1 --var-file relative-path resolution").
//
// Order:
//  1. `~` / `~/...` expansion via os.UserHomeDir.
//  2. Absolute paths pass through unchanged (just cleaned).
//  3. Relative paths join against os.Getwd().
//  4. os.Stat against the resolved absolute, so a typo or wrong-CWD
//     surfaces *before* terraform runs with a clearer message that
//     names *both* the user-supplied input and the resolved absolute.
//
// Idempotent on already-absolute slices, so the chokepoint resolving it
// once at command entry is safe even though the value flows into nested
// composite/leaf flows.
func NormalizeVarFiles(vfs []string) ([]string, error) {
	if len(vfs) == 0 {
		return vfs, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("resolve --var-file: %w", err)
	}
	out := make([]string, len(vfs))
	for i, vf := range vfs {
		expanded := expandTilde(vf)
		if filepath.IsAbs(expanded) {
			out[i] = filepath.Clean(expanded)
			continue
		}
		abs := filepath.Join(cwd, expanded)
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("--var-file %q (resolved to %q): %w", vf, abs, err)
		}
		out[i] = abs
	}
	return out, nil
}

// NormalizeLocalPath normalizes a local-type --tf-source path to an
// absolute path before it is pinned into config.yaml.
//
// A relative --tf-source (e.g. `./mytf`) is resolved by init /
// upgrade-tf against the *shell* CWD, but the path is then persisted
// verbatim into config.yaml and later handed to terraform via
// tf.FetchSource, whose effective CWD is the per-phase state dir. That
// is the same shell-CWD-vs-state-dir trap NormalizeVarFiles fixes for
// --var-file, but worse: it survives into config.yaml and detonates on
// a *later* up/plan/apply (Sprint 12 Issue 2). Pinning the absolute
// path at init time keeps the source stable.
//
// Only reached for the local TF source form — the embedded/github
// branches never build a local Path. `~`/`~/` expansion, absolute
// pass-through cleaned, relative resolved via filepath.Abs.
func NormalizeLocalPath(path string) (string, error) {
	if path == "" {
		return path, nil
	}
	expanded := expandTilde(path)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded), nil
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("resolve --tf-source %q: %w", path, err)
	}
	return abs, nil
}

// ── env classification ──────────────────────────────────────────────

// LocalOnlyEnvKeys is the canonical, single classification of
// workspace-emitted env vars whose VALUE is a local filesystem path —
// meaningless (and harmful) once it crosses the --on SSH boundary. It
// is consumed by BOTH the local exec path and the remote dispatch path
// so the core-vs-local-only split has exactly one definition.
//
// KUBECONFIG is the only such var today; a future local-path-valued var
// is one entry here, not a new code path (Sprint 13 Issue 1, the "a
// path correct locally is wrong across a boundary" class).
var LocalOnlyEnvKeys = map[string]struct{}{
	"KUBECONFIG": {},
}

// IsLocalOnlyEnv reports whether a KEY=VALUE env entry is local-only
// (its key is in LocalOnlyEnvKeys). Malformed entries (no '=', leading
// '=') are treated as not-local-only and pass through unmangled.
func IsLocalOnlyEnv(kv string) bool {
	idx := strings.IndexByte(kv, '=')
	if idx <= 0 {
		return false
	}
	_, bad := LocalOnlyEnvKeys[kv[:idx]]
	return bad
}

// ScrubLocalOnly returns a copy of env with every local-path-valued var
// (LocalOnlyEnvKeys) removed. Correctness of the --on path comes from
// NEVER sending a local path across the boundary — not from hoping the
// target sshd's AcceptEnv drops it. nil/empty input round-trips
// unchanged (no allocation).
func ScrubLocalOnly(env []string) []string {
	if len(env) == 0 {
		return env
	}
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if IsLocalOnlyEnv(kv) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ── the resolved invocation context ─────────────────────────────────

// ResolvedFlags is the single resolved-invocation context, computed
// exactly once at command entry (the root PersistentPreRunE). Every
// path-valued flag is already normalized against the invocation CWD;
// the env classification policy is fixed. Downstream orchestration
// consumes this — it never re-derives a path or env.
//
// A future path-valued flag is added by giving it a field here and one
// normalization line in Resolve — not a new per-RunE resolution site.
type ResolvedFlags struct {
	// VarFiles is the --var-file slice normalized to absolute paths
	// against the invocation CWD (NormalizeVarFiles already applied,
	// including the os.Stat existence check).
	VarFiles []string

	// TFSource is the --tf-source value: a local relative/`~` path
	// resolved to absolute (NormalizeLocalPath applied), or the
	// embedded/github form passed through untouched by the caller
	// before Resolve (only the local form reaches normalization).
	TFSource string
}

// Resolve builds the ResolvedFlags from the raw flag values, applying
// every path normalization exactly once. Called once per invocation
// from the cobra adapter's PersistentPreRunE.
//
// rawVarFiles is the raw --var-file slice; rawTFSource is the raw
// --tf-source value (already known by the caller to be the local form,
// or "" when unset / embedded / github — NormalizeLocalPath no-ops on
// "" and the caller does not pass a github/embedded form here).
func Resolve(rawVarFiles []string, rawTFSource string) (*ResolvedFlags, error) {
	vf, err := NormalizeVarFiles(rawVarFiles)
	if err != nil {
		return nil, err
	}
	src, err := NormalizeLocalPath(rawTFSource)
	if err != nil {
		return nil, err
	}
	return &ResolvedFlags{VarFiles: vf, TFSource: src}, nil
}
