package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestRenderTFVars_NoMirror_NoRedirect pins the byte-identical-off-path
// invariant: a nil mirror record emits none of the Sprint-29 redirect
// variables, so an un-mirrored workspace renders exactly as before.
func TestRenderTFVars_NoMirror_NoRedirect(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Name: "c"},
		BNK:      config.BNKCfg{FARRepoURL: "repo.f5.com"},
	}
	var buf bytes.Buffer
	if err := renderTFVars(&buf, ws, nil, "", ""); err != nil {
		t.Fatalf("renderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `far_repo_url = "repo.f5.com"`) {
		t.Errorf("far_repo_url should still render off the mirror path:\n%s", out)
	}
	for _, forbidden := range []string{"far_chart_repo_url", "far_image_repo_url", "use_registry_mirror"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("unexpected mirror redirect %q rendered with nil mirror:\n%s", forbidden, out)
		}
	}
}

// TestRenderTFVars_Mirror_Redirect: a populated record emits the split
// hosts + use_registry_mirror, while far_repo_url remains as the modules'
// coalesce fallback.
func TestRenderTFVars_Mirror_Redirect(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		Cluster:  config.ClusterCfg{Name: "c"},
		BNK:      config.BNKCfg{FARRepoURL: "repo.f5.com"},
	}
	mirror := &config.RegistryMirror{
		ChartHost: "default-route-openshift-image-registry.apps.x/bnk-mirror",
		ImageHost: "image-registry.openshift-image-registry.svc:5000/bnk-mirror",
	}
	var buf bytes.Buffer
	if err := renderTFVars(&buf, ws, mirror, "", ""); err != nil {
		t.Fatalf("renderTFVars: %v", err)
	}
	out := buf.String()
	want := []string{
		`far_repo_url = "repo.f5.com"`,
		`far_chart_repo_url = "default-route-openshift-image-registry.apps.x/bnk-mirror"`,
		`far_image_repo_url = "image-registry.openshift-image-registry.svc:5000/bnk-mirror"`,
		`use_registry_mirror = true`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing line: %s\noutput:\n%s", w, out)
		}
	}
}

// TestRenderTFVars_IncompleteMirror_NoRedirect: a record missing one host
// never half-redirects (the modules' coalesce would then leave one side on
// far_repo_url with use_registry_mirror flipped, a broken split).
func TestRenderTFVars_IncompleteMirror_NoRedirect(t *testing.T) {
	ws := &config.Workspace{BNK: config.BNKCfg{FARRepoURL: "repo.f5.com"}}
	mirror := &config.RegistryMirror{ChartHost: "chart-only/bnk-mirror"} // ImageHost empty
	var buf bytes.Buffer
	if err := renderTFVars(&buf, ws, mirror, "", ""); err != nil {
		t.Fatalf("renderTFVars: %v", err)
	}
	if strings.Contains(buf.String(), "use_registry_mirror") {
		t.Errorf("incomplete record must not emit use_registry_mirror:\n%s", buf.String())
	}
}
