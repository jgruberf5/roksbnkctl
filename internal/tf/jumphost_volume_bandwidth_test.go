package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #316: total_volume_bandwidth is optional+computed on ibm_is_instance. Left
// unset, IBM fills it from the profile at CREATION and terraform records it in
// state; a later profile change carries the stored value into the update and
// the API rejects the whole thing —
//
//	instance's total volume bandwidth 20000Mbps (specified by
//	total_volume_bandwidth) must not be greater than max volume bandwidth 3500Mbps
//
// — AFTER stopping the VM, leaving it stopped on the old profile.
//
// Pinning a modest value at creation is what makes a downsize possible later.
// It has to be on BOTH jumphost resources: missing it on either reintroduces
// the trap for that path, and the TGW jumphost is the one that is easy to
// forget because most workspaces only create the cluster ones.
//
// These read the shipped HCL rather than evaluating it, because the value sits
// on a resource whose plan needs credentials. The check is narrow — a specific
// attribute on two named resources — and it fails against the bug it names.

func testingModuleSource(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "terraform", "modules", "testing", "main.tf"))
	if err != nil {
		t.Fatalf("resolve module: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read testing/main.tf: %v", err)
	}
	return string(b)
}

// resourceBody returns the text of `resource "<type>" "<name>" { ... }`.
func resourceBody(t *testing.T, src, typ, name string) string {
	t.Helper()
	head := `resource "` + typ + `" "` + name + `" {`
	i := strings.Index(src, head)
	if i < 0 {
		t.Fatalf("resource %s.%s not found in testing/main.tf — this guard cannot check what it cannot find", typ, name)
	}
	rest := src[i:]
	if end := strings.Index(rest, "\n}\n"); end > 0 {
		return rest[:end]
	}
	return rest
}

func TestBothJumphostsPinVolumeBandwidth(t *testing.T) {
	src := testingModuleSource(t)

	for _, name := range []string{"tgw_jumphost", "cluster_jumphost"} {
		body := resourceBody(t, src, "ibm_is_instance", name)
		if !strings.Contains(body, "total_volume_bandwidth = var.testing_jumphost_total_volume_bandwidth") {
			t.Errorf("%s does not pin total_volume_bandwidth (#316).\n"+
				"Unpinned, IBM computes it from the profile at creation and the stored value "+
				"makes a later downsize fail with the VM left stopped.", name)
		}
	}
}

// The default has to be low enough that every profile the auto-select can pick
// can carry it. 1000 is the value verified to work by hand on bx2-2x8, whose
// ceiling is 3500.
func TestVolumeBandwidthDefaultIsConservative(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("..", "..", "terraform", "variables.tf"),
		filepath.Join("..", "..", "terraform", "modules", "testing", "variables.tf"),
	} {
		b, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		block := regexp.MustCompile(`(?s)variable "testing_jumphost_total_volume_bandwidth" \{.*?\n\}`).FindString(string(b))
		if block == "" {
			t.Fatalf("testing_jumphost_total_volume_bandwidth not declared in %s", rel)
		}
		m := regexp.MustCompile(`default\s*=\s*(\d+)`).FindStringSubmatch(block)
		if m == nil {
			t.Fatalf("no default in %s:\n%s", rel, block)
		}
		if m[1] != "1000" {
			t.Errorf("%s default is %s, want 1000 — the value verified by hand against bx2-2x8, "+
				"whose ceiling is 3500Mbps. A higher default reintroduces #316 for smaller profiles.", rel, m[1])
		}
	}
}

// The root must actually pass it through, or the module default silently wins
// and the root variable is inert — the class of defect #175 and #228 both were.
func TestRootPassesVolumeBandwidthToTheModule(t *testing.T) {
	p, err := filepath.Abs(filepath.Join("..", "..", "terraform", "main.tf"))
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read root main.tf: %v", err)
	}
	if !regexp.MustCompile(`testing_jumphost_total_volume_bandwidth\s*=\s*var\.testing_jumphost_total_volume_bandwidth`).Match(b) {
		t.Error("the root does not pass testing_jumphost_total_volume_bandwidth to the testing module; " +
			"the root variable would be inert and the module default would silently apply")
	}
}
