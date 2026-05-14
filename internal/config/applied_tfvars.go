package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// WriteAppliedTFVars writes terraform.applied.tfvars — a snapshot of the
// var-file inputs terraform actually consumed during a successful apply.
// See docs/prd/07-DEPLOYED-TFVARS.md for the design.
//
// Arguments:
//
//   - workspace — the roksbnkctl workspace name. Used to resolve the
//     per-phase state dir where the snapshot lands.
//   - phase     — one of "cluster", "trial", or "legacy-single". Picks
//     the target state dir and is recorded in the header comment so the
//     reader can disambiguate which phase produced the file.
//   - sources   — ordered slice of var-file paths exactly as passed to
//     `terraform apply -var-file=...`. Each file is read in order; the
//     output section for source[i] preserves terraform's "later wins"
//     semantics implicitly (the reader can grep top-to-bottom and the
//     last occurrence is the value terraform used).
//
// Output file path:
//
//   - phase "cluster"        → <WorkspaceClusterStateDir>/terraform.applied.tfvars
//   - phase "trial"          → <WorkspaceStateDir>/terraform.applied.tfvars
//   - phase "legacy-single"  → <WorkspaceStateDir>/terraform.applied.tfvars
//
// Returns nil on success. Callers log-and-continue on error per PRD 07
// §"Anti-patterns to avoid" #4 — the apply succeeded, the snapshot is a
// nice-to-have output.
func WriteAppliedTFVars(workspace, phase string, sources []string) error {
	target, err := appliedTFVarsPath(workspace, phase)
	if err != nil {
		return err
	}

	body, err := renderAppliedTFVars(phase, sources, time.Now().UTC(), appliedTFVarsVersion())
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("creating state dir for applied tfvars: %w", err)
	}

	// Atomic-rename pattern: write to a tempfile in the same dir, then
	// rename. Avoids leaving a half-written snapshot if the process is
	// killed mid-write.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".terraform.applied.tfvars.*")
	if err != nil {
		return fmt.Errorf("creating temp file for applied tfvars: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing applied tfvars: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing applied tfvars temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("chmod applied tfvars: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming applied tfvars into place: %w", err)
	}
	return nil
}

// AppliedTFVarsPath returns the snapshot path for (workspace, phase)
// without writing anything. Exposed so callers (or tests) can locate the
// file the same way WriteAppliedTFVars would.
func AppliedTFVarsPath(workspace, phase string) (string, error) {
	return appliedTFVarsPath(workspace, phase)
}

func appliedTFVarsPath(workspace, phase string) (string, error) {
	switch phase {
	case "cluster":
		dir, err := WorkspaceClusterStateDir(workspace)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "terraform.applied.tfvars"), nil
	case "trial", "legacy-single":
		dir, err := WorkspaceStateDir(workspace)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "terraform.applied.tfvars"), nil
	default:
		// Fallback: treat unknown phases as trial — keeps the snapshot
		// from being lost on unexpected call paths. Matches the defensive
		// posture spelled out in PRD 07 §"Anti-patterns to avoid" #4.
		dir, err := WorkspaceStateDir(workspace)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "terraform.applied.tfvars"), nil
	}
}

// redactedVarNames lists every variable whose value must be replaced with
// "<redacted>" in the snapshot. Per PRD 07 §"Resolved design decisions"
// #4 this is intentionally a single literal entry — `ibmcloud_api_key`,
// the only variable today sourced from the cred resolver rather than
// authored by the user in config.yaml / a tfvars file. Future credential-
// grade variables extend the list here (one-line change), no config knob.
var redactedVarNames = map[string]struct{}{
	"ibmcloud_api_key": {},
}

// tfvarsAssignmentRE matches one HCL-tfvars assignment per line. The
// snapshot only consumes what roksbnkctl writes (terraform.tfvars,
// terraform.tfvars.user, cluster-phase-override.tfvars) so the surface is
// constrained: identifier `=` value, where value is one of:
//
//   - a double-quoted string (no embedded newlines, no fancy escapes
//     beyond the standard HCL set — roksbnkctl never emits any)
//   - a bare bool / number (true|false|123|1.5)
//
// Anything more exotic (HCL heredocs, multi-line lists, object literals)
// is out of scope — roksbnkctl doesn't emit them, and the user's
// terraform.tfvars.user is documented as line-oriented. Lines that don't
// match are dropped from the snapshot with a "# (skipped: …)" comment so
// the user can see what was ignored.
var tfvarsAssignmentRE = regexp.MustCompile(
	`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+?)\s*$`,
)

// tfvarsCommentRE strips trailing `# ...` comments off the value portion
// of an assignment so a `foo = "bar" # note` line round-trips as `foo = "bar"`.
var tfvarsCommentRE = regexp.MustCompile(`\s+#.*$`)

// appliedTFVarsVersion returns the roksbnkctl version string for the
// header comment. Wired by the CLI layer at init via SetAppliedTFVarsVersion
// to avoid an import cycle (config <- cli). Falls back to "dev" when
// unset — tests get "dev" without further setup.
func appliedTFVarsVersion() string {
	if appliedTFVarsVersionFn != nil {
		if v := appliedTFVarsVersionFn(); v != "" {
			return v
		}
	}
	return "dev"
}

// appliedTFVarsVersionFn is set by the CLI layer's init() to return its
// build-time Version. Left nil in test binaries that don't import the
// CLI package — those get the "dev" fallback.
var appliedTFVarsVersionFn func() string

// SetAppliedTFVarsVersion wires the CLI's Version through to the
// snapshot header. Called from internal/cli/root.go's init(). Same seam
// pattern as exec.SetToolImageTag.
func SetAppliedTFVarsVersion(fn func() string) {
	appliedTFVarsVersionFn = fn
}

// renderAppliedTFVars builds the snapshot body. Exposed (lower-case but
// callable from the test file in the same package) so tests can pin a
// fixed timestamp + version without touching the filesystem.
func renderAppliedTFVars(phase string, sources []string, now time.Time, version string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by roksbnkctl %s at %s after terraform apply on phase=%s.\n",
		version, now.Format(time.RFC3339), phase)
	fmt.Fprintln(&b, "# Re-generated each apply. Do not edit by hand — your changes will be overwritten.")
	fmt.Fprintln(&b)

	for _, src := range sources {
		label := sourceLabel(src)
		assigns, missing, err := readTFVarsAssignments(src)
		if err != nil {
			return "", err
		}
		if missing {
			fmt.Fprintf(&b, "# === from %s (missing) ===\n", label)
			fmt.Fprintln(&b)
			continue
		}
		fmt.Fprintf(&b, "# === from %s ===\n", label)

		keys := make([]string, 0, len(assigns))
		for k := range assigns {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if _, redact := redactedVarNames[k]; redact {
				fmt.Fprintf(&b, "%s = \"<redacted>\"  # source: cred resolver, not persisted\n", k)
				continue
			}
			fmt.Fprintf(&b, "%s = %s\n", k, assigns[k])
		}
		fmt.Fprintln(&b)
	}
	return b.String(), nil
}

// readTFVarsAssignments reads a tfvars file and returns the assignments
// as name → raw-value strings (the value half is kept verbatim from the
// source — quoted strings retain their quotes, bare bools/numbers stay
// bare). The boolean second return is true when the file was missing
// (not an error — PRD 07 says best-effort; the caller emits a "missing"
// section marker so the reader sees that source was unavailable).
func readTFVarsAssignments(path string) (map[string]string, bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warning: tfvars source %q is missing — skipping in applied snapshot\n", path)
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("reading tfvars source %s: %w", path, err)
	}

	out := make(map[string]string)
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		m := tfvarsAssignmentRE.FindStringSubmatch(line)
		if m == nil {
			// Line doesn't match the supported "name = value" shape
			// (HCL heredoc, multi-line list, etc.). Skip silently — the
			// snapshot is best-effort and roksbnkctl never emits these
			// shapes itself.
			continue
		}
		name := m[1]
		value := tfvarsCommentRE.ReplaceAllString(m[2], "")
		out[name] = value
	}
	return out, false, nil
}

// sourceLabel maps a var-file path to a human-friendly label used in
// the snapshot's section header comments. The mapping is intentionally
// keyed on the basename (and on a substring match for the user override)
// so the same label survives whether the path is absolute or relative.
func sourceLabel(path string) string {
	base := filepath.Base(path)
	switch base {
	case "terraform.tfvars":
		return "config.yaml"
	case "terraform.tfvars.user":
		return "terraform.tfvars.user"
	case "cluster-phase-override.tfvars":
		return "cluster-phase override"
	default:
		return base
	}
}
