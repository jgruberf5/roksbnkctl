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

	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
)

// Why is the human-readable "why awsbnkctl cares" clause that the
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
// The slice returned is the public API used by `awsbnkctl doctor`'s
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
// Doctor green-by-default contract: there are NO required host
// binaries. Terraform is gone (post-Terraform direction — see
// docs/ARCHITECTURE.md). Helm is internalised via the
// helm.sh/helm/v3 Go SDK in phase14_flo_helm.go — no host `helm`
// binary is invoked. All previously-required-or-warned tools
// (kubectl, iperf3, dig) are INFORMATIONAL — the binary internalises
// each surface:
//
//   - kubectl: internalised via client-go in `awsbnkctl k *`
//   - iperf3: in-cluster fixture runnable via `--backend k8s`
//   - dig: miekg/dns probe library compiled into the binary
//   - helm: Helm 3 SDK (helm.sh/helm/v3) embedded directly; no host
//     `helm` binary needed.
//
// A stock dev box with nothing extra installed now produces zero
// warnings and exit 0 from `awsbnkctl doctor`. Backend-conditional
// checks (`doctor --backend k8s`) still surface their own failures
// separately.
func runWithWhy(ctx context.Context, cctx *config.Context) []withWhy {
	var out []withWhy

	// INFORMATIONAL: every other tool. Missing surfaces as StatusOK
	// with a "(internalised; …)" detail explaining the alternative.
	// Present surfaces as StatusOK with the path/version.
	out = append(out, checkBinaryInformational("kubectl", "internalised in `awsbnkctl k *` via client-go; host install used only when passthrough is convenient"))
	// `aws` CLI is intentionally NOT a doctor row: the AWS SDK chain
	// in internal/aws covers every awsbnkctl call site directly. The
	// STS / EKS / EC2 / S3 / IAM rows below replace the (never-shipped)
	// `aws` passthrough.

	out = append(out, checkBinaryInformational("iperf3", "in-cluster fixture runnable via `--backend k8s`; host install used only for `--backend local` north-south tests"))
	out = append(out, checkBinaryInformational("dig", "DNS probe internalised via miekg/dns (`awsbnkctl test dns`); host install no longer required"))

	// Kubeconfig: informational. Many doctor invocations happen
	// pre-`up`, before any cluster exists; surfacing a missing
	// kubeconfig as a warning produces noise on a fresh dev box.
	out = append(out, checkKubeconfigInformational())

	// Workspace + AWS pre-flight checks.
	//
	// The AWS row block surfaces unconditionally. On a stock dev box
	// without a workspace, the `aws credentials` row degrades to a
	// Warning naming the missing env var; downstream rows (sts / eks /
	// ec2 / s3 / iam) render as Skipped. A first-time user runs
	// `awsbnkctl doctor` and sees the AWS-side gap even before
	// `awsbnkctl init`. The legacy v0.x checkAPIKey row retired with
	// the workspace schema change.
	if cctx == nil {
		out = append(out, withWhy{
			Check: Check{Name: "workspace", Status: StatusError, Detail: "no config context"},
		})
		return out
	}
	out = append(out, checkWorkspace(cctx))
	out = append(out, awsChecks(ctx, cctx)...)

	return out
}

// checkBinaryInformational is the informational variant for kubectl and
// oc: the binary is no longer needed because the relevant verbs are
// internalised via client-go. Missing → StatusOK with an explanatory
// detail (rather than StatusWarning, which would imply something to
// fix). Present → StatusOK with the path/version, same as before.
//
// The intent: a fresh dev box without kubectl/oc should produce no
// warnings for everyday awsbnkctl use.
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
	case "iperf3":
		args = []string{"--version"}
	case "kubectl":
		args = []string{"version", "--client=true", "--output=yaml"}
	case "dig":
		args = []string{"-v"}
	default:
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- name is a hard-coded probe binary ("kubectl", "aws", etc.); args are version flags
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

// checkKubeconfigInformational is the green-by-default kubeconfig
// check. A doctor run BEFORE `awsbnkctl up` happens on a host that
// hasn't yet downloaded a kubeconfig — surfacing that as a warning is
// noise. Render the absence as informational with a one-line nudge at
// how to populate it (`awsbnkctl up` does this automatically post-
// apply; `awsbnkctl kubeconfig --download` is the manual path).
// A stock dev box should produce exit 0 + zero warnings.
func checkKubeconfigInformational() withWhy {
	c := Check{Name: "kubeconfig"}
	path := k8s.DefaultKubeconfigPath()
	if path == "" {
		c.Status = StatusOK
		c.Detail = "not yet downloaded (auto-populated by `awsbnkctl up`; manual: `awsbnkctl kubeconfig --download`)"
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
		c.Detail = fmt.Sprintf("%q not initialised — run `awsbnkctl init`", cctx.WorkspaceName)
		return withWhy{Check: c, Why: "per-environment config + state"}
	}
	c.Status = StatusOK
	c.Detail = cctx.WorkspaceName
	return withWhy{Check: c, Why: "per-environment config + state"}
}

// The v0.x checkAPIKey resolver probe is retired.
// AWS credentials resolve via the SDK chain — the `aws credentials`
// row in awsChecks surfaces the equivalent signal.

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
