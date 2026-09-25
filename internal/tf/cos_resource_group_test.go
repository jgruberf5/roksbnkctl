package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Issue #295. A supply chain is naturally central — one `bnk-supply-chain` in the
// `default` group, read by every workspace — while a workspace may sit in another
// group for reasons that have nothing to do with it, typically because `default`
// hit its service-instance quota. Every supply-chain COS lookup pinned the
// WORKSPACE's group, so such a workspace failed its first BNK read with
//
//	No resource instance found with name [bnk-supply-chain]
//
// about an instance that plainly exists. `cos.resource_group` names the group;
// empty keeps the old behaviour exactly.

func tfFile(t *testing.T, rel ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{"..", "..", "terraform"}, rel...)...)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// All four supply-chain COS lookups must resolve through the dedicated group, or
// the fix reaches some phases and not others — and a BNK install that reads the
// FAR credential in flo but the licence JWT in license would half-work, which is
// worse to diagnose than failing outright.
func TestEverySupplyChainCOSLookupUsesTheCOSResourceGroup(t *testing.T) {
	for _, tc := range []struct{ rel []string }{
		{[]string{"modules", "flo", "modules", "flo", "main.tf"}},
		{[]string{"modules", "license", "modules", "license", "main.tf"}},
		{[]string{"modules", "flp", "main.tf"}},
		{[]string{"modules", "flp_vsi", "main.tf"}},
	} {
		name := filepath.Join(tc.rel...)
		src := tfFile(t, tc.rel...)

		if !regexp.MustCompile(`data\s+"ibm_resource_group"\s+"cos_resource_group"`).MatchString(src) {
			t.Errorf("%s: no dedicated COS resource-group lookup", name)
			continue
		}
		// The COS instance lookup itself must consume it.
		cos := regexp.MustCompile(`(?s)data\s+"ibm_resource_instance"\s+"cos(_instance)?"\s*\{.*?\}`).FindString(src)
		if cos == "" {
			t.Errorf("%s: could not find the COS instance data source", name)
			continue
		}
		if !strings.Contains(cos, "data.ibm_resource_group.cos_resource_group[0].id") {
			t.Errorf("%s: the COS instance lookup still pins a different resource group:\n%s", name, cos)
		}
	}
}

// The override must FALL BACK to the workspace group, or every existing config
// that does not set cos.resource_group breaks.
func TestCOSResourceGroupFallsBackToTheWorkspaceGroup(t *testing.T) {
	for _, tc := range []struct {
		rel []string
		rg  string
	}{
		{[]string{"modules", "flo", "modules", "flo", "main.tf"}, "resource_group"},
		{[]string{"modules", "license", "modules", "license", "main.tf"}, "resource_group"},
		{[]string{"modules", "flp", "main.tf"}, "rg"},
		{[]string{"modules", "flp_vsi", "main.tf"}, "rg"},
	} {
		name := filepath.Join(tc.rel...)
		src := tfFile(t, tc.rel...)
		want := `var\.ibmcloud_cos_resource_group\s*!=\s*""\s*\?\s*var\.ibmcloud_cos_resource_group\s*:\s*data\.ibm_resource_group\.` + tc.rg + `\[0\]\.name`
		if !regexp.MustCompile(want).MatchString(src) {
			t.Errorf("%s: cos_resource_group does not fall back to the workspace group; "+
				"an unset cos.resource_group would change where the COS is looked up", name)
		}
	}
}

// TestFLPVSIKeepsItsOwnResourcesInTheWorkspaceGroup is the guard against the
// tempting wrong fix. flp_vsi's `rg` lookup also places the VSI, its floating IP
// and its security groups — repointing it instead of adding a separate lookup
// would move all of those into the COS's resource group, which is a far worse bug
// than the one being fixed.
func TestFLPVSIKeepsItsOwnResourcesInTheWorkspaceGroup(t *testing.T) {
	src := tfFile(t, "modules", "flp_vsi", "main.tf")
	// Every `resource_group = ` assignment (the VSI/network ones) must still use
	// the workspace group; only the COS `resource_group_id` may differ.
	for _, m := range regexp.MustCompile(`(?m)^\s*resource_group\s*=\s*(\S+)`).FindAllStringSubmatch(src, -1) {
		if !strings.Contains(m[1], "ibm_resource_group.rg[0].id") {
			t.Errorf("an flp_vsi resource was moved out of the workspace resource group: %q", m[0])
		}
	}
	if n := strings.Count(src, "data.ibm_resource_group.cos_resource_group[0].id"); n != 1 {
		t.Errorf("cos_resource_group is used %d times in flp_vsi; only the COS lookup should use it", n)
	}
}

// The tfvar must reach terraform, and an unset field must emit NOTHING — an empty
// string would still be a value and could mask the terraform default.
func TestCOSResourceGroupRendersOnlyWhenSet(t *testing.T) {
	var set strings.Builder
	renderBNKCOS(&set, &config.Workspace{COS: &config.COSCfg{
		Instance: "bnk-supply-chain", ResourceGroup: "default",
	}})
	if !strings.Contains(set.String(), `ibmcloud_cos_resource_group = "default"`) {
		t.Errorf("configured resource group not rendered:\n%s", set.String())
	}

	var unset strings.Builder
	renderBNKCOS(&unset, &config.Workspace{COS: &config.COSCfg{Instance: "bnk-supply-chain"}})
	if strings.Contains(unset.String(), "ibmcloud_cos_resource_group") {
		t.Errorf("unset resource group still emitted a tfvar, overriding the terraform default:\n%s", unset.String())
	}
}

// The variable has to exist in every module it is passed to, or terraform fails
// with "An argument named ... is not expected here" at plan time.
func TestCOSResourceGroupIsDeclaredEverywhereItIsPassed(t *testing.T) {
	for _, rel := range [][]string{
		{"variables.tf"},
		{"modules", "flo", "variables.tf"},
		{"modules", "flo", "modules", "flo", "variables.tf"},
		{"modules", "license", "variables.tf"},
		{"modules", "license", "modules", "license", "variables.tf"},
		{"modules", "flp", "variables.tf"},
		{"modules", "flp_vsi", "variables.tf"},
	} {
		if !strings.Contains(tfFile(t, rel...), `variable "ibmcloud_cos_resource_group"`) {
			t.Errorf("%s does not declare ibmcloud_cos_resource_group", filepath.Join(rel...))
		}
	}
}
