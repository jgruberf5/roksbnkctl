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
func TestTheReadinessConditionIsLineSelected(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t),
		filepath.FromSlash("terraform/modules/cne_instance/modules/cneinstance/main.tf")))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)

	if !strings.Contains(src, `cneinstance_ready_condition = local.line_pre_24 ? "CNEControllerAvailable" : "Available"`) {
		t.Error("the readiness condition is not line-selected. 2.4 must wait on the aggregate " +
			"Available — CNEControllerAvailable is True on an install where TMM is 0/3 and " +
			"nothing passes traffic — while 2.3 must keep CNEControllerAvailable or it " +
			"deadlocks against its own licensing step.")
	}
	// And the gate must actually USE it rather than keep a hardcoded condition.
	if !strings.Contains(src, "--for condition=${local.cneinstance_ready_condition}=True") {
		t.Error("the tfx wait still hardcodes a condition; the line-selected local is unused")
	}
	if strings.Contains(src, "--for condition=CNEControllerAvailable=True") {
		t.Error("a hardcoded CNEControllerAvailable wait remains — on 2.4 it reports success " +
			"on a broken install")
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
