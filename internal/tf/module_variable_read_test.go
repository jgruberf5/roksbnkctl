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

	// #204 is FINISHED. What remains is not a backlog -- every entry below is a
	// permanent exemption with a reason, and the guard exists to catch the next
	// variable that is declared, documented and read by nothing.
	//
	//   flo_deployment_id            dependency EDGES. The ordering is created in
	//   roks_cluster_dependency_id   the PARENT by `x = module.foo.id`, so the
	//                                child never mentions them. Deleting these
	//                                compiles, validates, and silently drops an
	//                                ordering constraint.
	//
	//   cluster_network_mode         exists only for its validation block.
	//
	//   gateway_api_bundle_url       consumed by the GO side (internal/bnkbom),
	//                                not by terraform. Its own description says
	//                                "Terraform installs nothing from this."
	//
	// Sixteen were removed across #207, #209, #211 and #210. Two constraints
	// cost a full revert and are worth knowing before automating any of it: a
	// bulk regex on `^\s*name\s*=` also matches `name = var.name` INSIDE a
	// provider block, which is provider configuration and not a module pass; and
	// the root passes the same argument name into several modules, so a removal
	// has to be scoped to the specific module block.
	//
	// The list is self-cleaning: an entry that becomes read (or is deleted) is
	// reported as stale, so it cannot outlive the problem it records.
	knownUnread := map[string]bool{
		"terraform/modules/cne_instance/modules/cneinstance: flo_deployment_id": true,
		"terraform/modules/flp: roks_cluster_dependency_id":                     true,
		"terraform/modules/gateway: roks_cluster_dependency_id":                 true,
		"terraform: cluster_network_mode":                                       true,
		"terraform: gateway_api_bundle_url":                                     true,
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
