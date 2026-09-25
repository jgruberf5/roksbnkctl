package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Issue #294. A ROKS-on-VPC cluster requires a Standard COS CRN to back its
// internal registry unconditionally — there is no "no registry backing" mode. So
// `resources.registry_cos.create: false` does not mean "skip the COS", it means
// "use that one", and the adopted instance has to be looked up and its CRN passed
// exactly as a created one is.
//
// It was not. The cluster module read
//
//	cos_instance_crn = var.create_cos_instance ? ibm_resource_instance.cos_instance[0].crn : null
//
// so the adopt path sent null and IBM rejected the create with E7278. The adopted
// name was rendered into tfvars (pinned by TestRenderTFVars… in vars_test.go, the
// `create_roks_registry_cos_instance = false` case) and then dropped on the floor.
//
// These assertions scan the SHIPPED terraform because the alternative — a real
// plan — needs IBM credentials. Each one names a distinct way the fix can be
// undone, and each is mutation-checked in the PR.
func clusterModuleFile(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join("..", "..", "terraform", "modules", "roks_cluster", "modules", "cluster", name)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func TestAdoptedRegistryCOSIsLookedUp(t *testing.T) {
	main := clusterModuleFile(t, "main.tf")

	// The data source must exist and be guarded on the adopt path, or the
	// lookup either never runs or runs when it must not.
	if !regexp.MustCompile(`data\s+"ibm_resource_instance"\s+"existing_cos_instance"`).MatchString(main) {
		t.Error("no data source looks up the adopted registry COS — the adopt path cannot produce a CRN")
	}
	if !regexp.MustCompile(`count\s*=\s*var\.create_cluster\s*&&\s*!var\.create_cos_instance\s*&&\s*var\.cos_instance_name\s*!=\s*""`).MatchString(main) {
		t.Error("the adopted-COS data source is not guarded on (create_cluster && !create_cos_instance && a name was given)")
	}
	if !regexp.MustCompile(`service\s*=\s*"cloud-object-storage"`).MatchString(main) {
		t.Error("the adopted-COS lookup does not filter on the cloud-object-storage service")
	}
}

// TestClusterTakesItsCRNFromEitherPath is the assertion that fails against the
// bug itself. Asserting only that the data source EXISTS would pass on a tree
// where it exists and nothing reads it.
func TestClusterTakesItsCRNFromEitherPath(t *testing.T) {
	main := clusterModuleFile(t, "main.tf")

	if !regexp.MustCompile(`cos_instance_crn\s*=\s*local\.registry_cos_crn`).MatchString(main) {
		t.Error("the cluster does not take cos_instance_crn from the shared local, so one of the two paths is unreachable")
	}
	// The exact shape of the defect, spelled out so a revert is caught by name.
	if regexp.MustCompile(`cos_instance_crn\s*=\s*var\.create_cos_instance\s*\?[^\n]*:\s*null`).MatchString(main) {
		t.Error("cos_instance_crn is back to `create ? created.crn : null` — the adopt path sends null and IBM rejects with E7278 (#294)")
	}
}

// A registry-less create fails with E7278, which names no variable and no
// instance. The precondition turns that into something actionable at plan time.
func TestAdoptWithNoNamedInstanceFailsAtPlanTime(t *testing.T) {
	main := clusterModuleFile(t, "main.tf")
	if !regexp.MustCompile(`condition\s*=\s*var\.create_cos_instance\s*\|\|\s*var\.cos_instance_name\s*!=\s*""`).MatchString(main) {
		t.Error("nothing catches registry_cos.create=false with no instance named; the run reaches IBM and returns E7278")
	}
}

// TestRegistryCOSOutputsCoverBothPaths: the outputs previously reported only the
// created instance, so an adopted COS left them empty and the CLI fell back to
// guessing "<cluster>-cos-instance" / "<cluster>-cos" — names an adopted instance
// has no reason to match, so the workspace recorded no registry COS at all.
func TestRegistryCOSOutputsCoverBothPaths(t *testing.T) {
	out := clusterModuleFile(t, "outputs.tf")
	for _, want := range []string{
		`value\s*=\s*local\.registry_cos_crn`,
		`value\s*=\s*local\.registry_cos_name`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("registry COS output does not use the shared local (%s); the adopted instance is not reported", want)
		}
	}
	if regexp.MustCompile(`length\(ibm_resource_instance\.cos_instance\)\s*>\s*0\s*\?`).MatchString(out) {
		t.Error("an output is back to reporting only the CREATED instance, so an adopted COS is invisible to the CLI (#294)")
	}
}
