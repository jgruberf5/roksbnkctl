package config

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
		// #234 added 40 overrides so every settable field has one. The blueprint
		// demo builds one standard cluster, so most describe a phase or topology
		// it does not exercise. Listed by REASON rather than as one bulk
		// exemption: a name here is a decision, and a shared sentence still says
		// which decision.
		"ROKSBNKCTL_BNK_CREATE":                     "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_BNK_EXISTING":                   "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_CERT_MANAGER_EXISTING":          "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_CLIENT_REGION":                  "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_CLUSTER_JUMPHOSTS_EXISTING":     "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_CLUSTER_VPC_CREATE":             "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_COPIED_SSH_KEY_FILES":           "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_REGISTRY_COS_EXISTING":          "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_TGW_JUMPHOST_EXISTING":          "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_TRANSIT_GATEWAY_CREATE":         "the blueprint demo builds ONE cluster with the standard shape; this knob varies a topology it does not exercise",
		"ROKSBNKCTL_TESTING_JUMPHOST_PROFILE":       "the blueprint demo does not run the testing phase",
		"ROKSBNKCTL_TESTING_MIN_MEMORY_GB":          "the blueprint demo does not run the testing phase",
		"ROKSBNKCTL_TESTING_MIN_VCPU_COUNT":         "the blueprint demo does not run the testing phase",
		"ROKSBNKCTL_GATEWAY_APP_NAMESPACE":          "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_BACKEND_PORT":           "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_BACKEND_SERVICE":        "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_LOCAL":    "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_REMOTE":   "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_EGRESS_MODE":            "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_GATEWAY_VXLAN_PORT":             "the blueprint demo does not run the gateway phase",
		"ROKSBNKCTL_FLP_CHART_VERSION":              "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_NODE_PORT_ACCESS":           "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_NODE_PORT_SOURCE_CIDRS":     "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_STORAGE_CLASS":              "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_HOST":     "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PORT":     "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PROTOCOL": "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_CERT_MANAGER_NAMESPACE":         "pins a component version; the demo takes what the manifest specifies, which is the point of the manifest",
		"ROKSBNKCTL_CERT_MANAGER_VERSION":           "pins a component version; the demo takes what the manifest specifies, which is the point of the manifest",
		"ROKSBNKCTL_FAR_REPO_URL":                   "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_ICR_HOST":                       "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_ICR_NAMESPACE":                  "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_REGISTRY_NAMESPACE":             "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_SOURCE_SERVICE_ACCOUNT_B64":     "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_GSLB_DATACENTER_NAME":           "GSLB and TCP tuning are not part of the blueprint demo's traffic path",
		"ROKSBNKCTL_TCP_SETTINGS_NAME":              "GSLB and TCP tuning are not part of the blueprint demo's traffic path",
		"ROKSBNKCTL_TMM_K8S_ROUTES":                 "GSLB and TCP tuning are not part of the blueprint demo's traffic path",
		"ROKSBNKCTL_HUGEPAGES_NODE_ROLE":            "hugepages are refused on ROKS (#203), so the demo never sets them",
		"ROKSBNKCTL_HUGEPAGES_PROFILE_NAME":         "hugepages are refused on ROKS (#203), so the demo never sets them",
		"ROKSBNKCTL_MIN_WORKER_MEMORY_GB":           "worker sizing is fixed by the demo's own cluster shape",
		"ROKSBNKCTL_MIN_WORKER_VCPU_COUNT":          "worker sizing is fixed by the demo's own cluster shape",
		"ROKSBNKCTL_BNKFORGE_INSECURE":              "the demo passes BNK_FORGE_* at register time; these seed the workspace instead",
		"ROKSBNKCTL_BNKFORGE_PROJECT":               "the demo passes BNK_FORGE_* at register time; these seed the workspace instead",
		"ROKSBNKCTL_BNKFORGE_REGISTER":              "the demo passes BNK_FORGE_* at register time; these seed the workspace instead",
		"ROKSBNKCTL_BNKFORGE_URL":                   "the demo passes BNK_FORGE_* at register time; these seed the workspace instead",
		"ROKSBNKCTL_BNKFORGE_USERNAME":              "the demo passes BNK_FORGE_* at register time; these seed the workspace instead",
		"ROKSBNKCTL_API_KEY_SOURCE":                 "selects where the API key is resolved FROM; the demo passes IBMCLOUD_API_KEY directly",

		"ROKSBNKCTL_FLP_VSI_ALLOWED_CIDRS": "the blueprint demo does not deploy FLP",
		"ROKSBNKCTL_REGISTRY_INCLUDE_DEPS": "the demo mirrors from the manifest defaults; overriding the source registry is an air-gap concern it does not model",
		"ROKSBNKCTL_API_KEY_B64":           "redundant: IBMCLOUD_API_KEY carries the key, and it is routed to the Secret",
		// Same reasoning as API_KEY_B64, one layer along. The demo already passes
		// BNK_FORGE_PASSWORD, which is read at register time and never written
		// down; ROKSBNKCTL_BNKFORGE_PASSWORD is the SEEDING path, which stores the
		// password into the workspace's config.yaml. Seeding it here would put a
		// credential on disk in a demo that does not need one.
		"ROKSBNKCTL_BNKFORGE_PASSWORD":           "redundant: the demo passes BNK_FORGE_PASSWORD at register time; seeding password_b64 would write a credential into the demo workspace",
		"ROKSBNKCTL_FAR_AUTH_LOCAL_FILE":         "the demo resolves the supply chain from COS, never local files",
		"ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE": "as above",
		"ROKSBNKCTL_CLIENT_VPC_CREATE":           "the blueprint demo does not run the testing phase",
		"ROKSBNKCTL_CLIENT_VPC_NAME":             "as above",
		"ROKSBNKCTL_TESTING_SSH_KEY_NAME":        "as above",
		"ROKSBNKCTL_TESTING_VPC_NAME":            "as above",
		"ROKSBNKCTL_TGW_JUMPHOST_CREATE":         "as above",
	}

	supported := SupportedOverrideNames()
	allow := declaredInEnvExample(t, envExample)
	inWorkflow := namesUsedUnder(t, wfDir)

	var missing []string
	for _, name := range supported {
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

func declaredInEnvExample(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Skipf(".env.example unreadable: %v", err)
	}
	out := map[string]bool{}
	// Prefix-agnostic: the supported surface is no longer all ROKSBNKCTL_* —
	// IBMCLOUD_API_KEY is an override too, and the old prefix-bound regex
	// excluded it by construction, so the guard could never have reported it
	// missing.
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]+)=`).FindAllStringSubmatch(string(b), -1) {
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
	re := regexp.MustCompile(`\b[A-Z][A-Z0-9_]{3,}\b`)
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

// The companion check, in the other direction.
//
// TestDemoEnvAllowlistCoversEveryOverride asks whether every supported override
// appears in .env.example. It cannot see the opposite drift: an entry naming an
// override that no longer EXISTS. That happens whenever a setting is removed —
// the file keeps exporting a variable nothing reads, and the failure is silent
// in the same way, because an unrecognised ROKSBNKCTL_* is simply ignored.
//
// It happened immediately: removing bnk.flp.vsi.reach (#210) left
// ROKSBNKCTL_FLP_VSI_REACH in the demo's allowlist, and the whole suite stayed
// green. A reader of that file would reasonably conclude the setting still does
// something.
func TestDemoEnvAllowlistHasNoEntriesForOverridesThatNoLongerExist(t *testing.T) {
	root := repoRootForDemoTest(t)
	envExample := filepath.Join(root, "scripts/demos/blueprint-workflows-ci-demo/.env.example")

	supported := map[string]bool{}
	for _, n := range SupportedOverrideNames() {
		supported[n] = true
	}

	// Only the ROKSBNKCTL_* namespace is ours. The file also carries the demo's
	// own driver variables (ARGO_NAMESPACE, DRY_RUN, KUBECONFIG, ...), which the
	// tool never reads and which are none of this test's business.
	//
	// ROKSBNKCTL_VERSION is the exception inside our own prefix: it selects which
	// release the demo installs, and is consumed by the demo script rather than
	// by the tool's override machinery.
	demoOwn := map[string]bool{
		"ROKSBNKCTL_VERSION": true,
	}

	var stale []string
	for name := range declaredInEnvExample(t, envExample) {
		if !strings.HasPrefix(name, "ROKSBNKCTL_") {
			continue
		}
		if supported[name] || demoOwn[name] {
			continue
		}
		stale = append(stale, name)
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("the demo's .env.example declares %d ROKSBNKCTL_* name(s) that are not "+
			"supported overrides, so setting them does nothing and the file advertises "+
			"settings the tool does not honour:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}
