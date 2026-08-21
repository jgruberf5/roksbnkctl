package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #167. The BNK phase's readiness gate waited on a single CNEInstance condition,
// CNEControllerAvailable. On a live 2.4 cluster, while unlicensed:
//
//	CNEControllerAvailable=True      <- what the gate saw
//	Available=False                  <- the truth
//	F5TmmAvailable=False
//
// TMM was 0/3 Ready and nothing could pass traffic. A 2.3-style wait declares
// that install successful — a false green, which is worse than a timeout: it
// sends the operator looking at their own configuration for a fault that is not
// there.
//
// 2.3 must KEEP waiting on CNEControllerAvailable. That is not an oversight
// there: it flips before TMM needs its license, so the License CR — gated on
// this same id — can proceed and TMM reaches Available afterwards. Waiting on
// the aggregate on 2.3 would deadlock against its own licensing step.
// The GATE stays CNEControllerAvailable on BOTH lines, and the aggregate is
// checked AFTER licensing. This test asserted the opposite until a live 2.4
// install disproved it.
//
// Waiting on Available at the gate deadlocks: the License CR is gated on that
// same readiness id, so Available cannot go True until TMM is licensed, TMM
// cannot be licensed until the License CR applies, and the License CR waits on
// the gate. Observed — 15 minutes, then 16 conditions True, F5TmmAvailable
// Pending, and no License CR at all, because the wait had blocked the very step
// that would have cleared it.
func TestTheReadinessGateDoesNotWaitOnTheAggregate(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t),
		filepath.FromSlash("terraform/modules/cne_instance/modules/cneinstance/main.tf")))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, `cneinstance_ready_condition = "CNEControllerAvailable"`) {
		t.Error("the readiness gate must wait on CNEControllerAvailable on both lines. It answers " +
			"\"can licensing proceed\", which is the question it is asked; waiting on the " +
			"aggregate Available there deadlocks against the License CR that is gated on it.")
	}
	if strings.Contains(src, `? "CNEControllerAvailable" : "Available"`) {
		t.Error("the gate is line-selected again — that is the deadlock, not a fix for it")
	}
}

// And the aggregate IS still checked, after licensing, on 2.4. Without this the
// original defect returns: a 2.3-style wait declares a 2.4 install successful
// while TMM is Pending and nothing passes traffic.
func TestTheAggregateIsCheckedAfterLicensing(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t),
		filepath.FromSlash("terraform/modules/license/modules/license/main.tf")))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, "cneinstance_available_24") {
		t.Fatal("nothing checks the CNEInstance's aggregate Available after licensing, so a 2.4 " +
			"install with TMM Pending would still be reported as successful")
	}
	if !strings.Contains(src, "condition=Available=True") {
		t.Error("the post-licensing check should wait on the aggregate Available")
	}
	// 2.4 only: 2.3's aggregate is not a reliable signal the same way, and adding
	// a new failure mode to the line that ships today is not worth the symmetry.
	if !strings.Contains(src, `var.bnk_line == "2.4"`) {
		t.Error("the post-licensing check must be gated to 2.4")
	}
	// It must depend on the License CR, or it is just the gate again under
	// another name.
	if !strings.Contains(src, "depends_on = [kubectl_manifest.bnk_license]") {
		t.Error("the check must run AFTER the License CR applies; otherwise it deadlocks exactly " +
			"as the gate did")
	}
}

// The second half of #167: a failure should name the component rather than
// leaving the operator with a timeout.
func TestStatusReportsTheCNEInstanceConditions(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t),
		filepath.FromSlash("internal/cli/phase_status.go")))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, "func cneInstanceConditions(") {
		t.Fatal("bnk status does not read the CNEInstance's conditions; pod counts alone cannot " +
			"show CNEControllerAvailable=True next to Available=False")
	}
	// The aggregate is the headline; the not-True components are what turn a
	// timeout into a diagnosis.
	if !strings.Contains(src, `res["cneinstance_not_ready"]`) {
		t.Error("status reports the aggregate but not which components are not ready, which is " +
			"the half that names the fault")
	}
	// It must not fail the command. This is a report, not a gate — a cluster
	// mid-install, or without the CRD, must not make `bnk status` an error.
	if !strings.Contains(src, "best-effort") && !strings.Contains(src, "Best-effort") {
		t.Error("the condition read should be documented as best-effort; inventing an error here " +
			"would make bnk status fail on a cluster that is merely mid-install")
	}
}
