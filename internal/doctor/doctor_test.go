package doctor

// Doctor green-by-default refresh tests.
//
// Contract: `awsbnkctl doctor` exits 0 with zero warnings on a stock
// dev box with NO extra tools installed. Terraform is removed
// (post-Terraform direction). Helm is internalised via the
// helm.sh/helm/v3 Go SDK (phase14_flo_helm.go) — no host binary
// required. Every previously-required-or-warned tool (kubectl,
// iperf3, dig) is internalised in the binary (kubectl via client-go;
// iperf3 via the in-cluster fixture; dig via miekg/dns).
//
// These tests pin the contract so a future refactor can't silently
// regress.

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/config"
)

// TestRunWithWhy_StockDevBox_NoWorkspace asserts: on a host with
// (potentially) no kubectl/iperf3/dig and no workspace initialised,
// the doctor emits no StatusError rows. There are no required host
// binaries (terraform removed; helm internalised via Go SDK).
//
// AWS-row block (PRD 04): the `aws credentials` row is StatusWarning
// (naming the missing AWS_PROFILE / AWS_ACCESS_KEY_ID env var) and
// every downstream AWS row (sts / eks / ec2 / s3 / iam) is
// StatusSkipped.
//
// The test passes a nil-Workspace config.Context so the workspace +
// IAM-auth checks degrade to "no workspace" / "no credentials"
// warnings, not errors. This is the "fresh dev box, before
// `awsbnkctl init`" shape.
func TestRunWithWhy_StockDevBox_NoWorkspace(t *testing.T) {
	cctx := &config.Context{WorkspaceName: "test-stock-dev"}
	pairs := runWithWhy(context.Background(), cctx)

	for _, p := range pairs {
		switch p.Check.Status {
		case StatusError:
			// There are no required binaries — every tool is now
			// informational. A StatusError row from any host-binary
			// check indicates a green-by-default regression.
			t.Errorf("check %q is StatusError: %s — green-by-default contract violated (no required binaries)",
				p.Check.Name, p.Check.Detail)
		case StatusWarning:
			// Allowed warnings: `workspace` (unchanged) +
			// `aws credentials` (Sprint 3 visibility relaxation —
			// closes Sprint 2 tech-writer Issue 4).
			switch p.Check.Name {
			case "workspace", "aws credentials":
				// expected
			default:
				t.Errorf("unexpected StatusWarning on %q: %s — Sprint 3 contract allows 'workspace' + 'aws credentials' warnings only",
					p.Check.Name, p.Check.Detail)
			}
		case StatusSkipped:
			// Downstream AWS rows (sts / eks / ec2 / s3 / iam) are
			// Skipped when `aws credentials` failed; that's the
			// documented degrade. Any other Skipped row is a
			// regression.
			if !strings.HasPrefix(p.Check.Name, "aws ") {
				t.Errorf("unexpected StatusSkipped on %q: %s — only `aws *` rows may be Skipped on a stock dev box",
					p.Check.Name, p.Check.Detail)
			}
		}
	}
}

// TestRunWithWhy_InformationalTools_OK pins that every previously-
// optional tool (kubectl / iperf3 / dig) renders as StatusOK
// regardless of whether it's installed. A missing informational tool
// produces StatusOK with an explanatory detail; a present tool
// produces StatusOK with the path.
func TestRunWithWhy_InformationalTools_OK(t *testing.T) {
	cctx := &config.Context{WorkspaceName: "test-info"}
	pairs := runWithWhy(context.Background(), cctx)

	informationalNames := map[string]bool{
		"kubectl": true, "iperf3": true, "dig": true,
	}
	for _, p := range pairs {
		if !informationalNames[p.Check.Name] {
			continue
		}
		if p.Check.Status != StatusOK {
			t.Errorf("informational tool %q: got Status=%s (detail=%q), want StatusOK",
				p.Check.Name, p.Check.Status, p.Check.Detail)
		}
		if !p.Check.Optional {
			t.Errorf("informational tool %q: Optional=%v, want true", p.Check.Name, p.Check.Optional)
		}
	}
}

// TestHasFailures_StockDevBoxGreen asserts the exit-code semantic:
// a stock dev box with no extra tools produces no HasFailures-reported
// failures, so `awsbnkctl doctor` exits 0 even without terraform or
// helm on PATH (both are no longer required host binaries).
func TestHasFailures_StockDevBoxGreen(t *testing.T) {
	cctx := &config.Context{WorkspaceName: "test-green"}
	pairs := runWithWhy(context.Background(), cctx)
	checks := make([]Check, 0, len(pairs))
	for _, p := range pairs {
		checks = append(checks, p.Check)
	}
	if HasFailures(checks) {
		var failing []string
		for _, c := range checks {
			if c.Status == StatusError {
				failing = append(failing, c.Name+": "+c.Detail)
			}
		}
		t.Errorf("stock dev box (no workspace) should have no failures; failing: %v", failing)
	}
}
