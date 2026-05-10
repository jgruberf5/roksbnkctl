package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// Why is the human-readable "why roksbnkctl cares" clause that the
// existing doctor table renders alongside each row. It's not part of the
// spec'd Check struct (see check.go) because future per-backend checks
// won't always have a why blurb — but the legacy general checks all do,
// and we keep it here so output remains byte-identical to the pre-refactor
// behaviour. A parallel slice keyed by Check index lets us extend Check
// later without breaking external callers.

// withWhy pairs a Check with its rendering blurb.
type withWhy struct {
	Check Check
	Why   string
}

// Run executes all diagnostic checks. cctx may carry a nil Workspace —
// workspace-dependent checks downgrade to a clear "no workspace" detail.
//
// The slice returned is the public API used by `roksbnkctl doctor`'s
// rendering; the rendering helper PrintResults takes the same slice so
// callers don't need to know about the internal withWhy pairing.
func Run(ctx context.Context, cctx *config.Context) []Check {
	pairs := runWithWhy(ctx, cctx)
	out := make([]Check, len(pairs))
	for i, p := range pairs {
		out[i] = p.Check
	}
	// Stash the why blurbs on a package-level map keyed by pointer to the
	// returned slice header so PrintResults can recover them without
	// changing the public Check shape. Keep this strictly local to one
	// Run/PrintResults round-trip — concurrent doctor invocations are not
	// supported (the CLI runs one).
	lastWhys = make([]string, len(pairs))
	for i, p := range pairs {
		lastWhys[i] = p.Why
	}
	return out
}

// lastWhys is the side-channel for the last Run's why blurbs. Doctor is
// not concurrent-safe; the CLI calls Run + PrintResults sequentially.
var lastWhys []string

// runWithWhy is the actual check-list builder. Split out so we can
// unit-test it without poking the lastWhys side-channel.
//
// Sprint 6 refresh (PLAN.md §"Gate to Sprint 7" line 481 — "Doctor
// green-by-default on a stock dev box"): only `terraform` is a
// REQUIRED host install. Every other previously-required-or-warned
// tool (kubectl, oc, ibmcloud, iperf3, dig) is now INFORMATIONAL —
// the binary internalises each surface:
//
//   - kubectl + oc: internalised via client-go in `roksbnkctl k *`
//     (PRD 02, Sprint 2).
//   - ibmcloud: bundled image runnable via `--backend docker` or
//     `--backend ssh:<target>` (PRD 03, Sprint 3/4).
//   - iperf3: bundled image runnable via `--backend k8s` (PRD 03,
//     Sprint 4).
//   - dig: miekg/dns probe library compiled into the binary (PRD 03
//     §"DNS probe", Sprint 5).
//
// A stock dev box with `terraform` installed and nothing else now
// produces zero warnings and exit 0 from `roksbnkctl doctor`. Backend-
// conditional checks (`doctor --backend k8s`) still surface their
// own failures separately.
func runWithWhy(ctx context.Context, cctx *config.Context) []withWhy {
	var out []withWhy

	// REQUIRED: terraform is the workhorse for `roksbnkctl up`; the
	// binary embeds the HCL but doesn't (yet) ship a terraform-go
	// runtime — `--backend docker` runs upstream `hashicorp/terraform`
	// in a container, but the local backend still needs a host
	// install. This is the ONE hard fail for the general doctor.
	out = append(out, checkBinary("terraform", true, "required for `roksbnkctl up` (local backend); `--backend docker` runs containerised but the local path needs a host install"))

	// INFORMATIONAL: every other tool. Missing surfaces as StatusOK
	// with a "(internalised; …)" detail explaining the alternative.
	// Present surfaces as StatusOK with the path/version.
	out = append(out, checkBinaryInformational("kubectl", "internalised in `roksbnkctl k *` via client-go; host install used only when passthrough is convenient"))
	out = append(out, checkBinaryInformational("oc", "internalised in `roksbnkctl k *` via client-go; host install used only when passthrough is convenient"))
	out = append(out, checkBinaryInformational("ibmcloud", "bundled image runnable via `--backend docker` or `--backend ssh:<target>`; host install used only for the default `--backend local` passthrough"))
	out = append(out, checkBinaryInformational("iperf3", "bundled image runnable via `--backend k8s`; host install used only for `--backend local` north-south tests"))
	out = append(out, checkBinaryInformational("dig", "DNS probe internalised via miekg/dns (`roksbnkctl test dns`); host install no longer required"))

	// Kubeconfig: informational. Many doctor invocations happen
	// pre-`up`, before any cluster exists; surfacing a missing
	// kubeconfig as a warning produces noise on a fresh dev box.
	out = append(out, checkKubeconfigInformational())

	// Workspace + creds.
	if cctx == nil {
		out = append(out, withWhy{
			Check: Check{Name: "workspace", Status: StatusError, Detail: "no config context"},
		})
		return out
	}
	out = append(out, checkWorkspace(cctx))
	if cctx.Workspace != nil {
		out = append(out, checkAPIKey(cctx))
		out = append(out, checkIBMAuth(ctx, cctx))
	}

	return out
}

// checkBinary reports whether name is on PATH and (best-effort) which version.
func checkBinary(name string, required bool, w string) withWhy {
	c := Check{Name: name, Optional: !required}
	path, err := exec.LookPath(name)
	if err != nil {
		if required {
			c.Status = StatusError
		} else {
			c.Status = StatusWarning
		}
		c.Detail = "not on PATH"
		return withWhy{Check: c, Why: w}
	}
	c.Status = StatusOK
	c.Detail = path
	if v := versionLine(name); v != "" {
		c.Detail = fmt.Sprintf("%s (%s)", path, v)
	}
	return withWhy{Check: c, Why: w}
}

// checkBinaryInformational is the post-Sprint-2 variant for kubectl and
// oc: the binary is no longer needed because the relevant verbs are
// internalised via client-go. Missing → StatusOK with an explanatory
// detail (rather than StatusWarning, which would imply something to
// fix). Present → StatusOK with the path/version, same as before.
//
// The intent: a fresh dev box without kubectl/oc should produce no
// warnings for everyday roksbnkctl use post-Sprint-2.
func checkBinaryInformational(name, w string) withWhy {
	c := Check{Name: name, Optional: true}
	path, err := exec.LookPath(name)
	if err != nil {
		c.Status = StatusOK
		c.Detail = "not on PATH (internalised; passthrough still works if installed)"
		return withWhy{Check: c, Why: w}
	}
	c.Status = StatusOK
	c.Detail = path
	if v := versionLine(name); v != "" {
		c.Detail = fmt.Sprintf("%s (%s)", path, v)
	}
	return withWhy{Check: c, Why: w}
}

// versionLine runs the binary's --version-equivalent and returns the
// first non-empty line, trimmed. Best-effort — empty on any error.
func versionLine(name string) string {
	var args []string
	switch name {
	case "terraform":
		args = []string{"version"}
	case "iperf3":
		args = []string{"--version"}
	case "kubectl":
		args = []string{"version", "--client=true", "--output=yaml"}
	case "oc":
		args = []string{"version", "--client=true"}
	case "ibmcloud":
		args = []string{"--version"}
	case "dig":
		args = []string{"-v"}
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// checkKubeconfigInformational is the Sprint 6 green-by-default
// variant. A doctor run BEFORE `roksbnkctl up` happens on a host that
// hasn't yet downloaded a kubeconfig — surfacing that as a warning is
// noise. Render the absence as informational with a one-line nudge at
// how to populate it (`roksbnkctl up` does this automatically post-
// apply; `roksbnkctl kubeconfig --download` is the manual path).
//
// PLAN.md §"Gate to Sprint 7": stock dev box should produce exit 0 +
// zero warnings.
func checkKubeconfigInformational() withWhy {
	c := Check{Name: "kubeconfig"}
	path := k8s.DefaultKubeconfigPath()
	if path == "" {
		c.Status = StatusOK
		c.Detail = "not yet downloaded (auto-populated by `roksbnkctl up`; manual: `roksbnkctl kubeconfig --download`)"
		return withWhy{Check: c, Why: "needed for cluster-side ops"}
	}
	c.Status = StatusOK
	c.Detail = path
	return withWhy{Check: c, Why: "needed for cluster-side ops"}
}

func checkWorkspace(cctx *config.Context) withWhy {
	c := Check{Name: "workspace"}
	if cctx.Workspace == nil {
		c.Status = StatusWarning
		c.Detail = fmt.Sprintf("%q not initialised — run `roksbnkctl init`", cctx.WorkspaceName)
		return withWhy{Check: c, Why: "per-environment config + state"}
	}
	c.Status = StatusOK
	c.Detail = cctx.WorkspaceName
	return withWhy{Check: c, Why: "per-environment config + state"}
}

func checkAPIKey(cctx *config.Context) withWhy {
	c := Check{Name: "ibmcloud api key"}
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
		NonInteractive: true,
	}
	_, err := resolver.IBMCloudAPIKey(context.Background())
	if err != nil {
		c.Status = StatusError
		c.Detail = err.Error()
		return withWhy{Check: c, Why: "auth for terraform + IBM SDK calls"}
	}
	c.Status = StatusOK
	c.Detail = "resolved"
	return withWhy{Check: c, Why: "auth for terraform + IBM SDK calls"}
}

func checkIBMAuth(ctx context.Context, cctx *config.Context) withWhy {
	c := Check{Name: "ibm cloud auth"}
	resolver := &cred.Resolver{
		Workspace:      cctx.WorkspaceName,
		Source:         cctx.Workspace.IBMCloud.APIKeySource,
		NonInteractive: true,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		c.Status = StatusError
		c.Detail = "no api key: " + err.Error()
		return withWhy{Check: c, Why: "verifies API key works against IBM IAM"}
	}
	cl, err := ibm.New(apiKey, cctx.Workspace.IBMCloud.Region)
	if err != nil {
		c.Status = StatusError
		c.Detail = err.Error()
		return withWhy{Check: c, Why: "verifies API key works against IBM IAM"}
	}
	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id, err := cl.Verify(tctx)
	if err != nil {
		c.Status = StatusError
		c.Detail = err.Error()
		return withWhy{Check: c, Why: "verifies API key works against IBM IAM"}
	}
	c.Status = StatusOK
	c.Detail = id.String()
	return withWhy{Check: c, Why: "verifies API key works against IBM IAM"}
}

// PrintResults writes a tabular human-readable rendering to w.
//
// Format and column widths are intentionally identical to the pre-refactor
// output: "<sym>\t<name>\t<detail>\t(<why>)\n", flushed via tabwriter so
// columns line up regardless of detail length.
func PrintResults(w io.Writer, results []Check) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for i, r := range results {
		sym := symbolFor(r.Status)
		var why string
		if i < len(lastWhys) {
			why = lastWhys[i]
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sym, r.Name, r.Detail, dim(why))
	}
	return tw.Flush()
}

// symbolFor maps a CheckStatus to the printed glyph. StatusSkipped renders
// as ⚠ for now (no skipped checks exist yet); revisit when Phase 3
// per-backend checks land.
func symbolFor(s CheckStatus) string {
	switch s {
	case StatusOK:
		return "✓"
	case StatusWarning, StatusSkipped:
		return "⚠"
	default:
		return "✗"
	}
}

// HasFailures reports whether any check failed (exit-code-worthy).
// Warnings and skipped checks don't count — they're informational.
func HasFailures(results []Check) bool {
	for _, r := range results {
		if r.Status == StatusError {
			return true
		}
	}
	return false
}

// AsError returns a single error summarising the first failure, or nil.
func AsError(results []Check) error {
	for _, r := range results {
		if r.Status == StatusError {
			return errors.New(r.Name + ": " + r.Detail)
		}
	}
	return nil
}

// dim wraps text in a parenthetical for the "why" column. Kept simple
// (no ANSI) so output is grep-friendly and works on Windows terminals.
func dim(s string) string {
	if s == "" {
		return ""
	}
	return "(" + s + ")"
}
