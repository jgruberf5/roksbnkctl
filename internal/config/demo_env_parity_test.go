package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The blueprint-workflows demo builds its bnk-env ConfigMap from the keys
// declared in .env.example — not from whatever happens to be exported. That file
// is an allowlist, so a setting missing from it is unreachable from every Argo
// workflow no matter how it is set.
//
// That has now drifted three times: the BYO-network fields (#64), the FLO
// namespaces, and nine settings from v1.43/v1.44 that were only noticed when
// someone asked. Each time the symptom was silence — the variable is simply
// ignored — which is why a review never catches it.
//
// This test fails when a supported ROKSBNKCTL_* override is neither in the
// allowlist, nor set by a workflow template, nor explicitly recorded below as
// deliberately excluded. Adding an override now forces a decision about the
// demo instead of leaving one to be discovered.
func TestDemoEnvAllowlistCoversEveryOverride(t *testing.T) {
	root := repoRootForDemoTest(t)
	envExample := filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/.env.example")
	wfDir := filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/workflows")

	// Deliberately absent, with the reason. A name here is a decision, not an
	// oversight — that distinction is the whole point of the list.
	excluded := map[string]string{
		"ROKSBNKCTL_API_KEY_B64":                 "redundant: IBMCLOUD_API_KEY carries the key, and it is routed to the Secret",
		"ROKSBNKCTL_FAR_AUTH_LOCAL_FILE":         "the demo resolves the supply chain from COS, never local files",
		"ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE": "as above",
		"ROKSBNKCTL_CLIENT_VPC_CREATE":           "the blueprint demo does not run the testing phase",
		"ROKSBNKCTL_CLIENT_VPC_NAME":             "as above",
		"ROKSBNKCTL_TESTING_SSH_KEY_NAME":        "as above",
		"ROKSBNKCTL_TESTING_VPC_NAME":            "as above",
		"ROKSBNKCTL_TGW_JUMPHOST_CREATE":         "as above",
	}

	supported := supportedOverrideNames(t, root)
	allow := declaredInEnvExample(t, envExample)
	inWorkflow := namesUsedUnder(t, wfDir)

	var missing []string
	for _, name := range supported {
		if strings.HasPrefix(name, "ROKSBNKCTL_ZONE") {
			continue // indexed per-zone family, declared as ZONE1_/ZONE2_/ZONE3_
		}
		if allow[name] || inWorkflow[name] {
			continue
		}
		if _, ok := excluded[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	if len(missing) > 0 {
		t.Errorf("these overrides cannot reach a blueprint — add them to %s, "+
			"set them in a workflow template, or record why they are excluded in this test:\n  %s",
			"scripts/demos/blueprint-workflows-ci-demo/.env.example", strings.Join(missing, "\n  "))
	}

	// The reverse: an exclusion for something that no longer exists is stale
	// documentation that will mislead the next person reading the list.
	sup := map[string]bool{}
	for _, n := range supported {
		sup[n] = true
	}
	for name := range excluded {
		if !sup[name] {
			t.Errorf("%s is recorded as deliberately excluded but is no longer a supported override — drop the entry", name)
		}
	}
}

// A credential must never land in the ConfigMap: the demo renders it into the
// Argo UI and prints it to the terminal on purpose.
func TestDemoRoutesCredentialsToTheSecret(t *testing.T) {
	root := repoRootForDemoTest(t)
	b, err := os.ReadFile(filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/blueprint-workflows-ci-demo.sh"))
	if err != nil {
		t.Skipf("demo script unreadable: %v", err)
	}
	script := string(b)
	for _, secret := range []string{
		"IBMCLOUD_API_KEY",
		"ROKSBNKCTL_GENERIC_PASSWORD",
		"ROKSBNKCTL_BIGIP_PASSWORD",
		"ROKSBNKCTL_GTM_PASSWORD",
	} {
		if !regexp.MustCompile(`SECRET_KEYS=\([^)]*` + regexp.QuoteMeta(secret)).MatchString(script) {
			t.Errorf("%s is credential-grade but is not in SECRET_KEYS — it would be written to the ConfigMap", secret)
		}
	}
}

func repoRootForDemoTest(t *testing.T) string {
	t.Helper()
	// internal/config -> repo root
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "scripts/demos")); err != nil {
		t.Skipf("demos not present: %v", err)
	}
	return root
}

func supportedOverrideNames(t *testing.T, root string) []string {
	t.Helper()
	re := regexp.MustCompile(`envValue\("(ROKSBNKCTL_[A-Z0-9_]+)"\)|\{"(ROKSBNKCTL_[A-Z0-9_]+)",`)
	seen := map[string]bool{}
	var out []string
	for _, f := range []string{"internal/config/envoverride.go", "internal/config/envoverride_flp.go"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			name := m[1]
			if name == "" {
				name = m[2]
			}
			if name != "" && !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no overrides to check — the extraction regex has drifted")
	}
	return out
}

func declaredInEnvExample(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf(".env.example unreadable: %v", err)
	}
	out := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^(ROKSBNKCTL_[A-Z0-9_]+)=`).FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	return out
}

func namesUsedUnder(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	re := regexp.MustCompile(`ROKSBNKCTL_[A-Z0-9_]+`)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, m := range re.FindAllString(string(b), -1) {
			out[m] = true
		}
	}
	return out
}
