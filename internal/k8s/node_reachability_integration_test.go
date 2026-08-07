//go:build integration

package k8s

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestNodeProbeFailurePath is the half a passing run cannot demonstrate: that an
// UNREACHABLE registry actually fails, from every node, with an error that stops
// `bnk up`.
//
// It calls the same two functions bnk up calls, in the same order, so what is proven
// is the real path rather than a reimplementation of it. It also passes an EMPTY CA,
// which exercises the "no root CA supplied" case — a registry already trusted by the
// node bundle must still be reachability-checked.
//
// Needs a live cluster. Run it deliberately:
//
//	KUBECONFIG_FILE=/path/to/admin.kubeconfig \
//	  go test -tags integration ./internal/k8s/ -run TestNodeProbeFailurePath -v
//
// It deploys the trust/probe DaemonSet, so do not run it against a cluster mid-install:
// it shares a name and namespace with the one `bnk up` uses.
func TestNodeProbeFailurePath(t *testing.T) {
	path := os.Getenv("KUBECONFIG_FILE")
	if path == "" {
		t.Skip("set KUBECONFIG_FILE to a cluster admin kubeconfig to run the failure-path check")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading kubeconfig: %v", err)
	}
	kc, err := NewFromKubeconfigBytes(body)
	if err != nil {
		t.Fatalf("building client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// 10.99.99.99 looks routable but is not on the transit gateway, so no node can
	// reach it — the shape of a mistyped mirror address or a missing TGW attachment.
	// The control target is the real mirror, so a total network outage cannot be
	// mistaken for the gate working.
	unreachable := os.Getenv("UNREACHABLE_HOST")
	if unreachable == "" {
		unreachable = "10.99.99.99"
	}
	control := os.Getenv("CONTROL_HOST")

	targets := []ProbeTarget{{Label: "registry", Host: unreachable, Port: "443", Required: true}}
	if control != "" {
		targets = append(targets, ProbeTarget{Label: "control", Host: control, Port: "443"})
	}

	// Empty CA on purpose: the probe must still run.
	if err := kc.EnsureRegistryCATrust(ctx, unreachable, "", "", true, targets...); err != nil {
		t.Logf("DaemonSet step returned (may be expected): %v", err)
	}
	results, err := kc.CollectNodeProbeResults(ctx)
	if err != nil {
		t.Fatalf("collecting probe results: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("no probe results came back — the DaemonSet did not report, so the gate would pass blind")
	}

	summary, probeErr := SummariseProbeResults(results, targets)
	t.Logf("\n%s", summary)

	if probeErr == nil {
		t.Fatal("an unreachable REQUIRED registry must fail the install; the gate returned no error")
	}
	t.Logf("bnk up would fail with:\n%v", probeErr)

	// Every node must have reported the unreachable target as failed — a gate that
	// only catches it on some nodes would let a per-AZ break through.
	var regResults, regFailed int
	for _, r := range results {
		if r.Label == "registry" {
			regResults++
			if !r.OK() {
				regFailed++
			}
		}
	}
	if regResults == 0 {
		t.Fatal("no node reported on the registry target")
	}
	// Coverage: every node the DaemonSet scheduled must have reported. A missing node
	// is indistinguishable from one that would have failed, and silently shrinking the
	// denominator is how "2/2 reachable" gets printed for a three-node cluster.
	if want, err := kc.expectedProbeNodes(ctx); err == nil && want > 0 && regResults != want {
		t.Errorf("the registry verdict covers only %d of %d scheduled nodes -- an unreported node could be the broken one", regResults, want)
	}
	if regFailed != regResults {
		t.Errorf("all %d nodes should report the unreachable registry as failed, got %d", regResults, regFailed)
	}
	if control != "" {
		for _, r := range results {
			if r.Label == "control" && !r.OK() {
				t.Errorf("the control target (%s) should be reachable; if it is not, this test proved nothing about the gate: %s", control, r)
			}
		}
	}
	if !strings.Contains(probeErr.Error(), "registry") {
		t.Errorf("the error must name which target failed; got: %v", probeErr)
	}
}
