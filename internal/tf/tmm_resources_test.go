package tf

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #203. TMM's resources come from deploymentSize, and every size above Tiny asks
// for hugepages that a ROKS worker cannot provide. bnk.tmm_resources overrides
// the derived block, which is what drops the hugepages request.
//
// This asserts on the rendered tfvars rather than on the struct, because the
// defect this setting exists to avoid is a setting that is declared, documented
// and read by nothing. The paired terraform-side check is
// TestEveryRootVariableIsRead, which fails if no .tf reads the variable.
func TestTMMResourcesReachesTheTfvars(t *testing.T) {
	var ws config.Workspace
	ws.BNK.TMMResources = map[string]map[string]string{
		"requests": {"cpu": "1000m", "memory": "1536Mi"},
		"limits":   {"cpu": "1000m", "memory": "1536Mi"},
	}

	out := renderToString(t, &ws)
	for _, want := range []string{
		`cneinstance_tmm_resources = {`,
		`"requests" = {`,
		`"cpu" = "1000m"`,
		`"memory" = "1536Mi"`,
		`"limits" = {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered tfvars missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// Unset must render NOTHING, not an empty block: an empty map would still
// replace the controller's deploymentSize-derived resources, silently stripping
// the very hugepages request a Tiny workspace legitimately does not have and a
// larger one may need. A workspace that does not set this must plan exactly as
// it did before the setting existed.
func TestTMMResourcesUnsetRendersNothing(t *testing.T) {
	var ws config.Workspace
	if got := renderToString(t, &ws); strings.Contains(got, "cneinstance_tmm_resources") {
		t.Errorf("unset tmm_resources rendered a tfvar; want none\n--- got ---\n%s", got)
	}

	// Present but empty is the same case: nothing to say.
	ws.BNK.TMMResources = map[string]map[string]string{"requests": {}}
	if got := renderToString(t, &ws); strings.Contains(got, "cneinstance_tmm_resources") {
		t.Errorf("empty tmm_resources rendered a tfvar; want none\n--- got ---\n%s", got)
	}
}

// The render must be byte-stable: Go map iteration is randomised, and an
// unstable tfvars defeats the byte-identical-plan comparison the 2.4 work is
// verified against.
func TestTMMResourcesRenderIsDeterministic(t *testing.T) {
	var ws config.Workspace
	ws.BNK.TMMResources = map[string]map[string]string{
		"requests": {"cpu": "1000m", "memory": "1536Mi", "ephemeral-storage": "1Gi"},
		"limits":   {"memory": "1536Mi", "cpu": "1000m"},
	}
	first := renderToString(t, &ws)
	for i := 0; i < 20; i++ {
		if got := renderToString(t, &ws); got != first {
			t.Fatalf("render %d differs from the first; map ordering leaked into the tfvars", i)
		}
	}
}

func renderToString(t *testing.T, ws *config.Workspace) string {
	t.Helper()
	var b strings.Builder
	renderBNKTMMResources(&b, ws)
	return b.String()
}
