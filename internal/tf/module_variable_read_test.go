package tf

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A variable can be declared at the root, rendered into tfvars, documented in the
// book, passed down through every module boundary — and read by NOTHING in the
// module that is supposed to act on it. The whole feature is then a no-op, and
// every text search for the name finds it, because the name exists in the
// declaration, the renderer, the docs and the plumbing. That is precisely why it
// survives: a grep proves the name exists, never that the two halves are joined.
//
// This has shipped three times now — cneinstance_advanced_env, install_cert_manager,
// and it was reproducible again for cneinstance_tmm_resources (#203): unwiring the
// leaf module left the ENTIRE suite green.
//
// TestEveryRootVariableIsRead guards the root. It cannot see this case, because the
// root variable IS read there — by the module block that passes it down. The gap is
// one level lower: a variable declared in a module and never referenced in that
// module's own body.
func TestEveryModuleVariableIsReadInsideItsModule(t *testing.T) {
	// Modules that legitimately only forward a variable to a nested module: the
	// forwarding IS the read, and it appears as `x = var.x` in a module block.
	// Those are matched by the passthrough check below rather than exempted.
	declRe := regexp.MustCompile(`(?m)^\s*variable\s+"([a-z0-9_]+)"\s*\{`)

	dirs := map[string]bool{}
	err := filepath.WalkDir("../../terraform", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".tf" {
			return err
		}
		dirs[filepath.Dir(p)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walking terraform: %v", err)
	}

	// PRE-EXISTING, NOT BLESSED. Each of these was already unread when this guard
	// was written; the list exists so the guard can start catching NEW ones today
	// instead of waiting for 21 separate triages. Some are probably legitimate --
	// a variable passed purely to create a dependency edge does its work in the
	// PARENT's module block and needs no reference in the child -- and some are
	// probably the same no-op defect this test exists to stop. They are tracked
	// for triage in #204.
	//
	// The list is self-cleaning: an entry that becomes read is reported as stale,
	// so it cannot outlive the problem it records.
	knownUnread := map[string]bool{
		"terraform/modules/cert_manager/modules/cert-manager: kube_host":               true,
		"terraform/modules/cert_manager/modules/cert-manager: kube_token":              true,
		"terraform/modules/cert_manager/modules/cert-manager: post_deployment_delay":   true,
		"terraform/modules/cert_manager/modules/cert-manager: wait_for_deployment":     true,
		"terraform/modules/cne_instance/modules/cneinstance: cneinstance_spec":         true,
		"terraform/modules/cne_instance/modules/cneinstance: flo_deployment_id":        true,
		"terraform/modules/cne_instance/modules/cneinstance: registry_mirror_username": true,
		"terraform/modules/flo/modules/flo: cert_manager_crd_ready":                    true,
		"terraform/modules/flo/modules/flo: f5_cne_subscription_jwt_file":              true,
		"terraform/modules/flo/modules/flo: helm_registry_config":                      true,
		"terraform/modules/flo/modules/flo: jwt_token":                                 true,
		"terraform/modules/flo/modules/flo: kube_host":                                 true,
		"terraform/modules/flp: helm_registry_config":                                  true,
		"terraform/modules/flp: roks_cluster_dependency_id":                            true,
		"terraform/modules/flp_vsi: flp_vsi_reach":                                     true,
		"terraform/modules/gateway: ibmcloud_resource_group":                           true,
		"terraform/modules/gateway: roks_cluster_dependency_id":                        true,
		"terraform/modules/roks_cluster/modules/cluster: ibmcloud_api_key":             true,
		"terraform/modules/roks_cluster/modules/cluster: worker_pool_name":             true,
		"terraform: cluster_network_mode":                                              true,
		"terraform: gateway_api_bundle_url":                                            true,
	}
	stillUnread := map[string]bool{}

	var unread []string
	for dir := range dirs {
		decls, body := map[string]bool{}, strings.Builder{}
		entries, err := filepath.Glob(filepath.Join(dir, "*.tf"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, f := range entries {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			src := string(b)
			if filepath.Base(f) == "variables.tf" {
				for _, m := range declRe.FindAllStringSubmatch(src, -1) {
					decls[m[1]] = true
				}
				// A variables.tf can still carry validation blocks that read the
				// variable; those are not a consumer, so its body is skipped.
				continue
			}
			body.WriteString(src)
			body.WriteString("\n")
		}
		if len(decls) == 0 {
			continue
		}
		text := body.String()
		for name := range decls {
			if strings.Contains(text, "var."+name) {
				continue
			}
			key := filepath.ToSlash(strings.TrimPrefix(dir, "../../")) + ": " + name
			if knownUnread[key] {
				stillUnread[key] = true
				continue
			}
			unread = append(unread, key)
		}
	}

	// An allowlisted entry that is now read must leave the list, or the list
	// silently grants an exemption to a variable that no longer needs one.
	var stale []string
	for k := range knownUnread {
		if !stillUnread[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("these are allowlisted as unread but are now read (or gone); "+
			"remove them from knownUnread:\n  %s", strings.Join(stale, "\n  "))
	}

	sort.Strings(unread)
	if len(unread) > 0 {
		t.Errorf("these module variables are declared but never read inside their own module,\n"+
			"so whatever they configure is a no-op no matter what the operator sets:\n  %s",
			strings.Join(unread, "\n  "))
	}
}
