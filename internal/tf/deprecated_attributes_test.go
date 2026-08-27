package tf

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// deprecatedAttributes are provider attributes the shipped terraform must not
// use. Each entry says what to use instead, because a failure that only names the
// banned thing leaves the reader to go and find the replacement.
//
// A deprecated attribute is not a style question. It emits a Warning on every
// plan AND every apply, and #242's four of them were the ONLY warnings a clean
// run produced -- which makes them noise for anyone grepping hook logs for real
// problems, and trains people to ignore the one category of output that exists to
// be read. Then the provider removes the attribute and the same code stops
// working.
var deprecatedAttributes = []struct{ banned, use, why string }{
	{
		banned: "installCRDs",
		use:    "crds.enabled",
		why:    "cert-manager >= 1.15: \"installCRDs is deprecated, use crds.enabled instead\" (#243)",
	},
	{
		banned: "primary_ipv4_address",
		use:    "primary_network_interface[0].primary_ip[0].address",
		why:    "ibm-cloud/ibm: \"primary_ipv4_address is deprecated and support will be removed. Use primary_ip instead\" (#242)",
	},
}

// The check is a source scan, which is usually the weak form -- but here the
// source IS the artefact: terraform is shipped as text and the warning is emitted
// by the provider reading it. There is no behaviour to exercise instead.
//
// Verified to fail against the defect it names: reintroducing the attribute at
// any one of the five sites #242 fixed makes this test report that file and line.
func TestTheShippedTerraformUsesNoDeprecatedProviderAttributes(t *testing.T) {
	root := "../../terraform"
	if _, err := os.Stat(root); err != nil {
		t.Skipf("terraform tree not present: %v", err)
	}

	var findings []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// .terraform holds the provider's own cached schema, which DOCUMENTS
			// the deprecation and therefore contains the string legitimately. A
			// first pass at locating these matched 100+ lines, every one of them
			// in that cache.
			if d.Name() == ".terraform" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".tf") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(b), "\n") {
			// Comments are skipped. A comment explaining WHY an attribute was
			// replaced necessarily names it, and flagging that is a false positive
			// that pushes people to delete the explanation to satisfy the check.
			// This test caught its own fix's comment block the first time it ran.
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") {
				continue
			}
			for _, dep := range deprecatedAttributes {
				if strings.Contains(line, dep.banned) {
					findings = append(findings, "  "+p+":"+strconv.Itoa(i+1)+"\n"+
						"    uses the deprecated "+dep.banned+"\n"+
						"    use instead: "+dep.use+"\n"+
						"    "+dep.why)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	if len(findings) > 0 {
		t.Errorf("the shipped terraform uses %d deprecated provider attribute(s).\n"+
			"Each emits a Warning on every plan and every apply, and stops working when the "+
			"provider drops it:\n%s", len(findings), strings.Join(findings, "\n"))
	}
}
