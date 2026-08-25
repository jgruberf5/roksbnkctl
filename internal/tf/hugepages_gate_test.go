package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #203. bnk.hugepages applies a Tuned profile that ROKS deletes between the write
// and the provider's read-back, which terraform reports as "Provider produced
// inconsistent result after apply ... Root object was present, but now absent" —
// a message blaming the provider and naming neither hugepages nor the platform.
//
// ORDER IS THE WHOLE FIX. Checking the CR afterwards cannot work: the manifest
// apply itself ERRORS, so anything ordered after it is skipped, and the check
// would be dead code in precisely the case it exists for. The gate has to run
// BEFORE the manifest.
//
// That ordering lives in a single depends_on, and deleting it leaves terraform
// valid, the suite green, and the feature silently back to blaming the provider.
// This is what makes the edge non-optional.
func TestHugepagesGateRunsBeforeTheTunedManifest(t *testing.T) {
	src := readCNEInstanceModule(t)

	gate := regexp.MustCompile(`(?s)resource\s+"null_resource"\s+"hugepages_supported"\s*\{.*?\n\}`)
	if !gate.MatchString(src) {
		t.Fatal("null_resource.hugepages_supported is gone; nothing checks whether this " +
			"platform can apply a bootloader Tuned profile before one is written")
	}

	// The gate must interrogate MachineConfigPools — the thing that makes a
	// [bootloader] argument mean anything. Without a pool the argument is written
	// and never applied.
	g := gate.FindString(src)
	if !strings.Contains(g, "machineconfiguration.openshift.io") {
		t.Error("the gate no longer waits on machineconfiguration.openshift.io; it would " +
			"stop detecting the platform limitation it exists to report")
	}

	// And the manifest must be ordered after it.
	manifest := regexp.MustCompile(`(?s)resource\s+"kubectl_manifest"\s+"hugepages_tuned"\s*\{.*?\n\}`)
	m := manifest.FindString(src)
	if m == "" {
		t.Fatal("kubectl_manifest.hugepages_tuned not found")
	}
	if !strings.Contains(m, "null_resource.hugepages_supported") {
		t.Error("kubectl_manifest.hugepages_tuned no longer depends on the gate, so the " +
			"Tuned CR is applied first. On ROKS that apply errors and the gate never runs, " +
			"putting the operator back to reading a provider bug report.")
	}
}

func readCNEInstanceModule(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "terraform", "modules",
		"cne_instance", "modules", "cneinstance", "main.tf"))
	if err != nil {
		t.Fatalf("read cneinstance main.tf: %v", err)
	}
	return string(b)
}
