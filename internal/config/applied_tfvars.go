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

// ReadAppliedTFVarsReplayAssignments parses the workspace+phase
// snapshot at `terraform.applied.tfvars` into a flat key→raw-value map
// suitable for re-feeding to terraform as a single var-file (validator
// Issue 3 / `live-verify-high-issues`). The snapshot's on-disk shape
// (per PRD 07 / Sprint 11) is intentionally multi-section — one
// `# === from <source> ===` block per consumed var-file with sorted
// `key = value` lines — so the same key can appear in multiple sections.
// Terraform rejects intra-file duplicate keys ("Each argument may be set
// only once"), so a naive replay errors out; this helper produces the
// deduped later-source-wins shape the replay needs while leaving the
// canonical snapshot on disk unchanged.
//
// `phase` is one of "cluster" / "trial" / "legacy-single" (matching
// `AppliedTFVarsPath`). Returns (nil, nil) when the snapshot file does
// not exist (workspace never applied — caller falls back to its prior
// behaviour, which is terraform's own missing-required-var error).
//
// Redacted keys (`ibmcloud_api_key` per `redactedVarNames`) are
// **dropped**: the snapshot records them as `"<redacted>"` for audit
// visibility, but feeding that literal back into terraform would
// override the real value from `TF_VAR_*` env / explicit `--var-file`
// (terraform precedence: var-file > env) and break IAM auth. The secret
// must come from env / explicit `--var-file` exactly as before — Issue 3
// closes the missing-var gap, not the secret-handoff.
func ReadAppliedTFVarsReplayAssignments(workspace, phase string) (map[string]string, error) {
	p, err := AppliedTFVarsPath(workspace, phase)
	if err != nil {
		return nil, err
	}
	assigns, missing, err := readTFVarsAssignments(p)
	if err != nil {
		return nil, err
	}
	if missing {
		return nil, nil
	}
	for k := range redactedVarNames {
		delete(assigns, k)
	}
	return assigns, nil
}

// AppliedTFVarsPath returns the snapshot path for (workspace, phase)
// without writing anything. Exposed so callers (or tests) can locate the
// file the same way WriteAppliedTFVars would.
func AppliedTFVarsPath(workspace, phase string) (string, error) {
	return appliedTFVarsPath(workspace, phase)
}

// ReadTFVarsAssignments parses a tfvars file at path and returns the
// supported `name = value` assignments as a flat map (value strings are
// kept verbatim — quoted strings retain their quotes, bare bools/numbers
// stay bare). Sprint 19 thin export of the package-private parser so
// `roksbnkctl init --var-file <path>` can reuse the exact same tolerant
// parsing the applied-tfvars snapshot writer uses (same shape as
// `terraform.tfvars.example`, same skip-unsupported-HCL behaviour). The
// boolean return is true when the file was missing — callers that treat
// a missing var-file as a hard error (like init's `--var-file`) check
// this and surface an actionable message naming the path.
func ReadTFVarsAssignments(path string) (assigns map[string]string, missing bool, err error) {
	return readTFVarsAssignments(path)
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
	case "testing":
		dir, err := WorkspaceTestingStateDir(workspace)
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "terraform.applied.tfvars"), nil
	case "gateway":
		dir, err := WorkspaceGatewayStateDir(workspace)
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
	// Every other variable roksbnkctl renders that carries a credential. The
	// snapshot is documented as suitable for git commit once the operator has
	// checked the redaction against their threat model (Chapter 6), so a secret
	// that is not listed here is a secret that ends up in a repository.
	//
	// bigip_password and registry_mirror_password predate this list and were
	// missed; cneinstance_gtm_password arrived with the GTM work and would have
	// been missed the same way. Adding a rendered credential to vars.go means
	// adding it here — the comment above promised a one-line change, and three
	// releases went by without anyone making it.
	"bigip_password":           {},
	"registry_mirror_password": {},
	"cneinstance_gtm_password": {},
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
			// The write path is where a missing source is worth saying out loud:
			// something asked for this var-file and it was not there, so the
			// snapshot under-records what the apply actually used.
			fmt.Fprintf(os.Stderr, "warning: tfvars source %q is missing — skipping in applied snapshot\n", src)
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
			// Missing is not an error here — the caller decides what it means.
			// The warning belongs to the WRITE path only (see renderAppliedTFVars):
			// this parser is shared with the read path, where a missing snapshot is
			// the normal state of a workspace that has not applied yet. Warning
			// there fired "skipping in applied snapshot" at someone who was not
			// writing a snapshot, before every first install.
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("reading tfvars source %s: %w", path, err)
	}

	out := make(map[string]string)
	lines := strings.Split(string(b), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		m := tfvarsAssignmentRE.FindStringSubmatch(line)
		if m == nil {
			// Line doesn't match the supported "name = value" shape (an HCL
			// heredoc, or a continuation line of a block already consumed
			// below). Skip silently — the snapshot is best-effort.
			continue
		}
		name := m[1]
		value := tfvarsCommentRE.ReplaceAllString(m[2], "")

		// A value that OPENS a block or list and does not close it on this line
		// continues onto the following ones. Recording just the opening brace
		// used to be harmless because nothing emitted that shape; it is not
		// harmless now. The snapshot is re-rendered from this map and handed
		// back to terraform as a -var-file on plan, apply AND down, so a
		// truncated `name = {` is a var-file terraform refuses to parse — which
		// strands a workspace that cannot be torn down.
		if trailer, ok := unclosedBlockTrailer(value); ok {
			block, end, closed := consumeBlock(lines, i, value, trailer)
			// Where parsing RESUMES, in both outcomes. Assigning it only on the
			// success path left it dead on the other, and a mutation that made
			// the failure path skip to EOF — the original defect — changed
			// nothing observable and passed the suite.
			i = end
			if !closed {
				// UNTERMINATED. Drop the key and carry on from the line after
				// the opener (#219).
				//
				// The previous behaviour did the two worst possible things. It
				// recorded the opening fragment -- its comment claimed that
				// "at least round-trips as something parseable", and `[` does
				// not, so `bnk down` died on `Missing item separator` and the
				// workspace could not be destroyed at all. And it left the scan
				// index at EOF, so every assignment AFTER the bad block was
				// silently swallowed: a snapshot of four variables parsed to
				// two, quietly.
				//
				// Dropping is safe precisely where it happens. The replay file
				// is the LOWEST-precedence var-file in the chain, so a key
				// omitted here falls through to the config render layered after
				// it; and the variable this occurs on in practice,
				// cneinstance_network_zones, is `default = []`. An unusable
				// value helps nobody -- terraform cannot parse it, and keeping
				// it means the workspace stays stranded on every retry.
				fmt.Fprintf(os.Stderr,
					"warning: %s: %q opens a block that is never closed — dropping it from the "+
						"replay (the value comes from your config; re-run an apply to rewrite the snapshot)\n",
					path, name)
				continue
			}
			value = block
			i = end
		}
		out[name] = value
	}
	return out, false, nil
}

// HCLValueIsUnbalanced reports whether a rendered value's brackets do not
// balance, which makes it unparseable as HCL.
//
// Exported so the replay writer can refuse to emit one. It shares
// blockDepthDelta with the parser deliberately: a second bracket counter is a
// second thing to keep true, and the two disagreeing is how a value passes the
// check here and fails in terraform.
func HCLValueIsUnbalanced(v string) bool {
	return blockDepthDelta(v) != 0
}

// consumeBlock follows a multi-line value from the line that opens it until its
// bracket depth returns to zero.
//
// Returns the joined block, the index of its LAST line, and whether it actually
// closed. The third return is the point: the caller must be able to tell a
// complete block from a truncated one, because the two need opposite handling
// and the previous version of this code could not distinguish them.
func consumeBlock(lines []string, start int, value string, trailer int) (block string, end int, closed bool) {
	var b strings.Builder
	b.WriteString(value)
	depth := trailer
	for j := start + 1; j < len(lines); j++ {
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(lines[j], "\r"))
		depth += blockDepthDelta(lines[j])
		if depth <= 0 {
			return b.String(), j, true
		}
	}
	// Ran out of file with the block still open. Report the OPENER's index so
	// the caller resumes at the very next line. Reporting EOF here is the
	// original defect: it swallowed every assignment after the bad block.
	return "", start, false
}

// unclosedBlockTrailer reports whether v opens more braces/brackets than it
// closes, and by how many. Quoted sections are skipped so a brace inside a
// string does not count.
func unclosedBlockTrailer(v string) (int, bool) {
	d := blockDepthDelta(v)
	return d, d > 0
}

func blockDepthDelta(v string) int {
	depth := 0
	inStr := false
	for i := 0; i < len(v); i++ {
		c := v[i]
		if inStr {
			if c == '\\' {
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch {
		case c == '"':
			inStr = true
		case c == '#', c == '/' && i+1 < len(v) && v[i+1] == '/':
			// Comment: skip to the end of THIS LINE, not to the end of the
			// string. Returning here was correct while every caller passed a
			// single line, and wrong the moment one passed a joined multi-line
			// value — a well-formed list containing a `# zone 1` comment would
			// stop being counted at the comment and read as unbalanced (#219).
			for i < len(v) && v[i] != '\n' {
				i++
			}
		case c == '{', c == '[', c == '(':
			depth++
		case c == '}', c == ']', c == ')':
			depth--
		}
	}
	return depth
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
