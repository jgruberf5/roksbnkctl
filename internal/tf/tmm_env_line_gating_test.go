package tf

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Issue #307. F5's approved 2.4 reference carries neither PAL_CPU_SET nor
// TMM_K8S_ROUTES in advanced.tmm.env; on 2.4 the routing knob is
// ENABLE_K8S_ROUTES (a boolean) rather than a CIDR. Both were emitted
// unconditionally, so a 2.4 CNEInstance carried the 2.3 defaults AND the 2.4
// additions at once — observed on a live 2.4.0-EA cluster.
//
// PRD 18 predicted this and the terraform never acted on it, so these guards
// exist to stop it drifting back.

func cneinstanceMain(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "terraform", "modules", "cne_instance", "modules", "cneinstance", "main.tf")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

// tmmEnvBlock returns the `tmm = concat(...)` list from adv_env_defaults — the
// UNGATED defaults, not the 2.4-only additions in adv_env_line.
func tmmEnvBlock(t *testing.T, src string) string {
	t.Helper()
	i := strings.Index(src, "adv_env_defaults = {")
	if i < 0 {
		t.Fatal("adv_env_defaults not found")
	}
	j := strings.Index(src[i:], "tmm = ")
	if j < 0 {
		t.Fatal("tmm list not found inside adv_env_defaults")
	}
	start := i + j
	// to the next top-level key in the same map
	end := strings.Index(src[start:], "\n    pseudoCNI")
	if end < 0 {
		t.Fatal("could not bound the tmm list")
	}
	return src[start : start+end]
}

// Each 2.3-only entry must sit behind a line gate. Asserting merely that the
// name still appears would pass on the bug; asserting it is GONE would break
// 2.3, which still needs both.
func TestTwoThreeOnlyTMMEnvIsLineGated(t *testing.T) {
	block := tmmEnvBlock(t, cneinstanceMain(t))

	for _, name := range []string{"PAL_CPU_SET", "TMM_K8S_ROUTES"} {
		idx := strings.Index(block, `"`+name+`"`)
		if idx < 0 {
			t.Errorf("%s is absent from the tmm defaults — 2.3 needs it", name)
			continue
		}
		// The nearest preceding `local.line_pre_24 ?` must be closer than the
		// nearest preceding entry that is NOT gated.
		before := block[:idx]
		gate := strings.LastIndex(before, "local.line_pre_24 ?")
		if gate < 0 {
			t.Errorf("%s is emitted unconditionally; a 2.4 CNEInstance would carry it (#307)", name)
			continue
		}
		// and the gate must not have been closed before reaching the entry
		if closed := strings.LastIndex(before, "] : [],"); closed > gate {
			t.Errorf("%s sits AFTER a closed line gate, so it is still unconditional (#307)", name)
		}
	}
}

// The entries that belong on BOTH lines must stay ungated, or 2.3 and 2.4 both
// lose settings the reference carries for each.
func TestSharedTMMEnvStaysUngated(t *testing.T) {
	block := tmmEnvBlock(t, cneinstanceMain(t))
	for _, name := range []string{"TMM_CALICO_ROUTER", "TMM_DEFAULT_MTU", "TMM_MAPRES_ADDL_VETHS_ON_DP"} {
		idx := strings.Index(block, `"`+name+`"`)
		if idx < 0 {
			t.Errorf("%s disappeared from the tmm defaults", name)
			continue
		}
		before := block[:idx]
		gate := strings.LastIndex(before, "local.line_pre_24 ?")
		closed := strings.LastIndex(before, "] : [],")
		if gate >= 0 && gate > closed {
			t.Errorf("%s was swept into a 2.3-only gate; 2.4 would lose it", name)
		}
	}
}

// ENABLE_K8S_ROUTES is the 2.4 replacement and must remain in the 2.4-only map.
// If the gating above were ever "fixed" by deleting the 2.4 additions instead,
// 2.4 would have no routing knob at all.
func TestTwoFourKeepsItsRoutingKnob(t *testing.T) {
	src := cneinstanceMain(t)
	i := strings.Index(src, "adv_env_line = local.line_pre_24 ? {} : {")
	if i < 0 {
		t.Fatal("adv_env_line (the 2.4-only additions) not found")
	}
	if !regexp.MustCompile(`"ENABLE_K8S_ROUTES"`).MatchString(src[i:]) {
		t.Error("ENABLE_K8S_ROUTES is not in the 2.4-only additions; 2.4 would have no routing knob")
	}
}

// The tfvar is still rendered on 2.4 (so terraform.applied.tfvars records what
// the operator asked for) but the terraform gates it off, which makes the config
// field inert. Silently inert is the failure mode #279 was about, moved from
// comments into config — so the render path says so out loud.
func TestTMMK8SRoutesWarnsWhenInertOnTwoFour(t *testing.T) {
	// The line is DERIVED from the manifest version, not set directly — so the
	// fixture goes through the same derivation the product does.
	cfg := func(manifest string) *config.Workspace {
		ws := &config.Workspace{}
		ws.BNK.ManifestVersion = manifest
		ws.BNK.Network = &config.BNKNetworkCfg{TMMK8SRoutes: "10.0.0.0/16"}
		return ws
	}
	for _, tc := range []struct {
		manifest  string
		wantWarn  bool
		wantTFVar bool
	}{
		{"2.4.0-EA", true, true},
		{"2.3.0-EHF-2-3.2598.3-0.0.226", false, true},
	} {
		ws := cfg(tc.manifest)
		if got := ws.BNKLineOrEmpty(); (got == "2.4") != tc.wantWarn {
			t.Fatalf("fixture broken: %s derives line %q", tc.manifest, got)
		}
		var out strings.Builder
		stderr := captureStderr(t, func() { renderBNKNetwork(&out, ws) })

		if got := strings.Contains(out.String(), `cneinstance_tmm_k8s_routes = "10.0.0.0/16"`); got != tc.wantTFVar {
			t.Errorf("%s: tfvar rendered=%v, want %v\n%s", tc.manifest, got, tc.wantTFVar, out.String())
		}
		warned := strings.Contains(stderr, "ignored on BNK 2.4")
		if warned != tc.wantWarn {
			t.Errorf("%s: warned=%v, want %v (stderr: %q)", tc.manifest, warned, tc.wantWarn, stderr)
		}
	}
}

// An unset field must warn on neither line — a warning for something the operator
// never configured is noise that teaches people to ignore warnings.
func TestUnsetTMMK8SRoutesNeverWarns(t *testing.T) {
	var out strings.Builder
	stderr := captureStderr(t, func() {
		ws := &config.Workspace{}
		ws.BNK.ManifestVersion = "2.4.0-EA"
		ws.BNK.Network = &config.BNKNetworkCfg{}
		renderBNKNetwork(&out, ws)
	})
	if strings.Contains(stderr, "tmm_k8s_routes") {
		t.Errorf("warned about a field the operator never set: %q", stderr)
	}
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = orig
	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// The gate is spliced in POSITION, not appended, so a shipping 2.3 install sees
// no CNEInstance diff from this change — the same rule the cneController gate
// follows. Reordering would show up as a spurious diff on every 2.3 cluster and
// look like a real config change to whoever reviews the plan.
func TestTwoThreeTMMEnvOrderIsUnchanged(t *testing.T) {
	block := tmmEnvBlock(t, cneinstanceMain(t))

	want := []string{
		"TMM_CALICO_ROUTER",
		"TMM_DEFAULT_MTU",
		"PAL_CPU_SET",
		"TMM_MAPRES_ADDL_VETHS_ON_DP",
		"TMM_K8S_ROUTES",
	}
	var got []string
	for _, m := range regexp.MustCompile(`name\s*=\s*"([A-Z0-9_]+)"`).FindAllStringSubmatch(block, -1) {
		got = append(got, m[1])
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("2.3 env order changed — every shipping 2.3 cluster would show a CNEInstance diff\n got: %v\nwant: %v", got, want)
	}
}
