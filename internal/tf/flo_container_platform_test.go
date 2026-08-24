package tf

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #189. containerPlatform was hard-coded to "Generic" in the FLO helm values.
// ROKS is OpenShift, and under "Generic" the CNE controller looks for the
// kubeadm-config ConfigMap that only kubeadm-built clusters have, aborts at
// Reconciled=False, and never creates TMM's internal macvlan NAD — while the
// install still reports every CNEInstance condition True and all pods Running.
//
// Verified on a live cluster: two clean installs differing only in this value
// gave Reconciled=False (Generic) and Reconciled=True (OCP).
//
// Asserted on the RENDERED tfvars through BOTH render bodies. A setting that
// reaches only one of them is the trap this renderer already has a history of —
// and a setting that reaches neither is the inert-feature defect this repo has
// shipped twice.
func TestFLOContainerPlatformReachesTerraform(t *testing.T) {
	for name, render := range map[string]func(io.Writer, *config.Workspace, *config.RegistryMirror) error{
		"full":   renderFullBody,
		"sparse": renderSparseBody,
	} {
		t.Run(name, func(t *testing.T) {
			for _, want := range []string{"OCP", "Generic"} {
				ws := fullyPopulatedWorkspace(t)
				if name == "sparse" {
					ws.Prefix = ""
				}
				ws.BNK.FLOContainerPlatform = want

				var buf bytes.Buffer
				if err := render(&buf, ws, nil); err != nil {
					t.Fatal(err)
				}
				exp := `flo_container_platform = "` + want + `"`
				if !strings.Contains(buf.String(), exp) {
					t.Errorf("%s body: missing %s.\nA setting that does not reach terraform "+
						"cannot change the install, however well it is documented.", name, exp)
				}
			}

			// Unset must render nothing at all, so terraform's own default applies
			// rather than an empty string overriding it.
			ws := fullyPopulatedWorkspace(t)
			if name == "sparse" {
				ws.Prefix = ""
			}
			ws.BNK.FLOContainerPlatform = ""
			var buf bytes.Buffer
			if err := render(&buf, ws, nil); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), "flo_container_platform") {
				t.Errorf("%s body: unset FLOContainerPlatform still rendered the tfvar; "+
					"an empty value would override terraform's default", name)
			}
		})
	}
}

// Threading a variable through the module chain is not the same as USING it.
// Reverting the leaf to a hard-coded "Generic" leaves `var.flo_container_platform`
// referenced in the wrapper module, so both the tfvars render test above and
// TestEveryRootVariableIsRead still pass while the setting does nothing — which
// is the exact inert-setting shape this repo has shipped twice.
//
// So assert the CONSUMPTION, at the leaf, on the expression itself.
func TestFLOContainerPlatformIsConsumedByTheHelmValues(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "terraform", "modules", "flo", "modules", "flo", "main.tf"))
	if err != nil {
		t.Fatalf("read flo main.tf: %v", err)
	}
	src := string(b)

	if !strings.Contains(src, "containerPlatform        = var.flo_container_platform") {
		t.Error("the FLO helm values do not set containerPlatform from var.flo_container_platform.\n" +
			"  Hard-coding it makes bnk.flo_container_platform inert: the variable still\n" +
			"  threads through the module chain, so the orphan-variable guard and the tfvars\n" +
			"  render test both stay green while the setting changes nothing.")
	}
	if strings.Contains(src, `containerPlatform        = "Generic"`) || strings.Contains(src, `containerPlatform        = "OCP"`) {
		t.Error("containerPlatform is hard-coded in the FLO helm values; it must come from the variable")
	}
}
