package tf

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The advanced.<component>.env plumbing (#175) and the 2.4 USE_GATEWAY_SETTINGS
// default are both things a source scan cannot check honestly: what matters is
// the list terraform actually evaluates, not the text that produces it. Both
// defects these tests cover were found by evaluating, and neither would have
// shown up in a grep:
//
//   - cneinstance_advanced_env was declared at the root, rendered as a tfvar,
//     documented and tested on the Go side, and read by no terraform anywhere.
//     Every text search for the name found it. The override was a no-op.
//   - USE_GATEWAY_SETTINGS was absent on 2.4, so `gateway up` applied Infra and
//     GatewaySettings that no controller ever reconciled. They sat at
//     Accepted=Unknown / "Waiting for controller" indefinitely.
//
// So these run `terraform console` against the real cneinstance module and read
// the evaluated list. No credentials: console never reaches a provider for a
// local, and -backend=false keeps init offline apart from the provider mirror.
//
// A first attempt at this even wrote `lookup(m, k, [])` against an
// object-typed collection — `terraform validate` accepted it and evaluation
// rejected it, which is the other reason this has to evaluate rather than
// validate.

func consoleEnvNames(t *testing.T, tfvars, expr string) []string {
	t.Helper()
	return consoleStrings(t, []string{"cne_instance", "modules", "cneinstance"}, tfvars, expr)
}

// consoleJSON evaluates expr against a copy of the named module and returns the
// JSON terraform produced. Module path is relative to terraform/modules.
func consoleJSON(t *testing.T, module []string, tfvars, expr string) string {
	t.Helper()

	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not on PATH")
	}

	parts := append([]string{"..", "..", "terraform", "modules"}, module...)
	src, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("resolve module: %v", err)
	}

	dir := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}

	// far_repo_url is required only because an unrelated local coalesces it.
	vars := "far_repo_url = \"https://repo.f5.com\"\n" + tfvars
	if err := os.WriteFile(filepath.Join(dir, "zz_test.auto.tfvars"), []byte(vars), 0o644); err != nil {
		t.Fatalf("write tfvars: %v", err)
	}

	init := exec.Command(tf, "init", "-backend=false", "-input=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Skipf("terraform init unavailable offline: %v\n%s", err, out)
	}

	cmd := exec.Command(tf, "console")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("jsonencode(" + expr + ")\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform console failed: %v\n%s", err, out)
	}

	// console echoes the value as a quoted JSON string; take the last non-empty
	// line so any warning banner ahead of it is ignored.
	var line string
	for _, l := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			line = s
		}
	}
	var encoded string
	if err := json.Unmarshal([]byte(line), &encoded); err != nil {
		t.Fatalf("console output was not a JSON string: %q\nfull output:\n%s", line, out)
	}
	return encoded
}

// consoleStrings is consoleJSON for an expression that evaluates to a list of
// strings, with an explicit guard against an empty result — an empty list would
// let every assertion built on it pass without checking anything.
func consoleStrings(t *testing.T, module []string, tfvars, expr string) []string {
	t.Helper()
	encoded := consoleJSON(t, module, tfvars, expr)
	var names []string
	if err := json.Unmarshal([]byte(encoded), &names); err != nil {
		t.Fatalf("decode %q: %v", encoded, err)
	}
	if len(names) == 0 {
		t.Fatalf("evaluated to an empty list, which would let every assertion below pass vacuously")
	}
	return names
}

func countName(names []string, want string) int {
	n := 0
	for _, s := range names {
		if s == want {
			n++
		}
	}
	return n
}

func TestUseGatewaySettingsIsOnFor24AndAbsentOn23(t *testing.T) {
	const expr = `local.adv_env["cneController"][*].name`

	on24 := consoleEnvNames(t, "bnk_line = \"2.4\"\n", expr)
	if countName(on24, "USE_GATEWAY_SETTINGS") != 1 {
		t.Errorf("2.4 must set USE_GATEWAY_SETTINGS exactly once; the controller reads Infra and "+
			"GatewaySettings only under that flag, and without it `gateway up` applies CRs that stay "+
			"at Accepted=Unknown forever. got %v", on24)
	}

	on23 := consoleEnvNames(t, "bnk_line = \"2.3\"\n", expr)
	if countName(on23, "USE_GATEWAY_SETTINGS") != 0 {
		t.Errorf("2.3 has no Infra/GatewaySettings CRDs, so the flag must not appear there: %v", on23)
	}

	// The 2.3 list is also the regression guard for the hoist that made the
	// override reachable: it has to render what it rendered before, in order.
	want := []string{
		"TMM_DEFAULT_MTU", "CLOUD_ENV", "CLOUD_PROVIDER", "CLOUD_NETWORK_CONFIGMAP",
		"VPC_NAME", "CLOUD_REGION", "IBM_TRUSTED_PROFILE_ID", "GSLB_DATACENTER_NAME",
		"CLOUD_VPC", "CLOUD_TRUSTED_PROFILE",
	}
	if strings.Join(on23, ",") != strings.Join(want, ",") {
		t.Errorf("2.3 cneController env changed\n got: %v\nwant: %v", on23, want)
	}
}

func TestAdvancedEnvOverrideReplacesRatherThanDuplicates(t *testing.T) {
	names := consoleEnvNames(t,
		"bnk_line = \"2.4\"\ncneinstance_advanced_env = { cneController = { USE_GATEWAY_SETTINGS = \"false\", TMM_DEFAULT_MTU = \"1500\" } }\n",
		`local.adv_env["cneController"][*].name`)

	for _, n := range []string{"USE_GATEWAY_SETTINGS", "TMM_DEFAULT_MTU"} {
		if got := countName(names, n); got != 1 {
			t.Errorf("%s appears %d times; an override must replace the default, not append a "+
				"duplicate — the CNEInstance spec is read by the lifecycle operator, not kubelet, "+
				"so last-one-wins is not a rule we get to rely on. got %v", n, got, names)
		}
	}

	values := consoleEnvNames(t,
		"bnk_line = \"2.4\"\ncneinstance_advanced_env = { cneController = { USE_GATEWAY_SETTINGS = \"false\" } }\n",
		`[for e in local.adv_env["cneController"] : e.value if e.name == "USE_GATEWAY_SETTINGS"]`)
	if len(values) != 1 || values[0] != "false" {
		t.Errorf("the override's value must win over the 2.4 default, got %v", values)
	}
}

func TestAdvancedEnvReachesAComponentThatHasNoDefaults(t *testing.T) {
	// A component named only in the user map has no static attribute in the
	// spec, so it has to arrive through the merge() of adv_env_extra. This is
	// the case that decides whether the variable's documented promise — that
	// components F5 adds between releases work without a code change here — is
	// true or just written down.
	names := consoleEnvNames(t,
		"bnk_line = \"2.4\"\ncneinstance_advanced_env = { externalBigip = { CLUSTER_IDENTIFIER = \"c1\" } }\n",
		`local.adv_env_extra["externalBigip"].env[*].name`)
	if len(names) != 1 || names[0] != "CLUSTER_IDENTIFIER" {
		t.Errorf("a components-only-in-the-user-map entry must render its env, got %v", names)
	}
}

// The Go renderer and the terraform variable were developed apart, and the
// defect that hid for a whole feature was exactly a join that nobody crossed:
// the name matched on both sides and the two halves were never connected. So
// this feeds the renderer's own bytes to terraform rather than asserting on
// their text.
func TestRenderedAdvancedEnvTfvarsAreAcceptedByTheModule(t *testing.T) {
	ws := &config.Workspace{}
	ws.BNK.Advanced = map[string]config.AdvancedComponentCfg{
		"cneController": {Env: map[string]string{"USE_GATEWAY_SETTINGS": "false"}},
		"tmm":           {Env: map[string]string{"TMM_DEFAULT_MTU": "1500"}},
	}

	var buf strings.Builder
	renderBNKAdvancedEnv(&buf, ws)
	rendered := buf.String()
	if !strings.Contains(rendered, "cneinstance_advanced_env") {
		t.Fatalf("renderer produced nothing to test: %q", rendered)
	}

	names := consoleEnvNames(t, "bnk_line = \"2.4\"\n"+rendered,
		`[for e in local.adv_env["cneController"] : "${e.name}=${e.value}" if e.name == "USE_GATEWAY_SETTINGS"]`)
	if len(names) != 1 || names[0] != "USE_GATEWAY_SETTINGS=false" {
		t.Errorf("the rendered tfvars did not reach the CR spec: %v", names)
	}

	tmm := consoleEnvNames(t, "bnk_line = \"2.4\"\n"+rendered,
		`[for e in local.adv_env["tmm"] : "${e.name}=${e.value}" if e.name == "TMM_DEFAULT_MTU"]`)
	if len(tmm) != 1 || tmm[0] != "TMM_DEFAULT_MTU=1500" {
		t.Errorf("tmm override did not reach the CR spec: %v", tmm)
	}
}
