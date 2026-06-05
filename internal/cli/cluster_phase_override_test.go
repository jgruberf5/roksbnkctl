package cli

// Sprint 28 — additive byte-exact pin for the cluster-phase override now that
// the testing jumphosts left the cluster phase. The architect's §2b requires
// the cluster-phase override to ALSO force testing_create_* = false (so the
// jumphosts — and the shared SSH key, count-gated on those toggles — drop to
// count=0 in state-cluster/ and live only in state-testing/). The pre-existing
// deploy_bnk=false / deploy_cert_manager=false gates are unchanged.
//
// This complements the orchestration-side byte tests for the testing-phase and
// bnk-phase overrides; the cluster override is a cli-package const, so its
// pin lives here.

import (
	"strings"
	"testing"
)

func TestClusterPhaseOverrideContent_Sprint28TestingGates(t *testing.T) {
	got := clusterPhaseOverrideContent

	// The forced VALUE block: the BNK gates (pre-existing) + the Sprint 28
	// testing gates (new), in the exact order + adjacency the source emits.
	const wantBlock = "deploy_bnk = false\n" +
		"deploy_cert_manager = false\n" +
		"testing_create_tgw_jumphost = false\n" +
		"testing_create_cluster_jumphosts = false\n" +
		"testing_create_client_vpc = false\n"
	if !strings.Contains(got, wantBlock) {
		t.Fatalf("cluster-phase override missing the Sprint 28 testing-gate block.\n--- want ---\n%s\n--- got ---\n%s", wantBlock, got)
	}

	// Each testing gate individually present and OFF (defence against a
	// reorder that keeps the block string but flips a value).
	for _, want := range []string{
		"testing_create_tgw_jumphost = false",
		"testing_create_cluster_jumphosts = false",
		"testing_create_client_vpc = false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cluster-phase override missing forced gate %q\n--- override ---\n%s", want, got)
		}
	}

	// And it must NOT re-enable any of them (the jumphosts must not be
	// re-created in the cluster phase).
	for _, bad := range []string{
		"testing_create_tgw_jumphost = true",
		"testing_create_cluster_jumphosts = true",
		"testing_create_client_vpc = true",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("cluster-phase override re-enables a jumphost toggle %q — jumphosts must live only in the Testing phase\n--- override ---\n%s", bad, got)
		}
	}
}
