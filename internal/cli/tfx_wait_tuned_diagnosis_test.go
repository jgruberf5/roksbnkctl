package cli

import (
	"strings"
	"testing"
)

// #203. bnk.hugepages applies a Tuned profile that ROKS deletes immediately, and
// the resulting message blames the terraform provider — "Root object was present,
// but now absent" — while saying nothing about hugepages, ROKS, or what to do.
//
// The wait on the CR turns that into something actionable. This asserts the
// diagnosis fires for a Tuned wait and, more importantly, that it does NOT fire
// for anything else: a diagnosis attached to the wrong timeout is worse than
// none, because it sends the reader somewhere unrelated with confidence.
func TestVanishedTunedDiagnosisFiresOnlyForTuned(t *testing.T) {
	// Both resources must produce it. The MachineConfigPool wait is the one that
	// actually fires on ROKS -- it runs BEFORE the Tuned apply, which errors and
	// would skip anything ordered after it.
	for _, gvr := range []string{
		"tuned.openshift.io/v1/tuneds",
		"machineconfiguration.openshift.io/v1/machineconfigpools",
	} {
		if _, ok := vanishedTunedDiagnosis(gvr); !ok {
			t.Fatalf("no diagnosis for %s; that is the wait this exists to explain", gvr)
		}
	}
	got, ok := vanishedTunedDiagnosis("machineconfiguration.openshift.io/v1/machineconfigpools")
	if !ok {
		t.Fatal("unreachable")
	}
	for _, want := range []string{
		"ZERO MachineConfigPools",
		"DELETES user-created",
		"ZERO MachineConfigPools",
		"bnk.tmm_replicas",
		"#203",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnosis missing %q, so the reader cannot act on it:\n%s", want, got)
		}
	}
	// It must name the alternative, not merely refuse.
	if !strings.Contains(got, "Tiny") {
		t.Error("diagnosis does not name Tiny as the size that works")
	}
}

func TestVanishedTunedDiagnosisIgnoresEverythingElse(t *testing.T) {
	for _, gvr := range []string{
		"k8s.f5.com/v1/cneinstances",
		"k8s.f5.com/v1/f5tmms",
		"apps/v1/deployments",
		"", // a wait with no gvr must not trip it either
	} {
		if _, ok := vanishedTunedDiagnosis(gvr); ok {
			t.Errorf("gvr %q produced the hugepages diagnosis; it would explain a Tuned "+
				"problem for a timeout that has nothing to do with hugepages", gvr)
		}
	}
}
