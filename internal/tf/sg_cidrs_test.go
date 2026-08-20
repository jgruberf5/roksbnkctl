package tf

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #122. Three security-group rules took their source from a hard-coded
// 0.0.0.0/0 with no way to narrow it — including one with no protocol or port
// at all, on a VPC default security group, which inverts IBM's safe default for
// every resource later placed in that VPC.
//
// The FLP module already had the right pattern (an allowlist variable, an open
// default for continuity, a description stating the exposure); these did not.
func TestSecurityGroupCIDRsRenderIntoTFVars(t *testing.T) {
	ws := &config.Workspace{Prefix: "sg", Resources: config.DefaultResources()}
	ws.Resources.TestingJumphostAllowedCIDRs = []string{"203.0.113.7/32"}
	ws.Resources.TestingClientVPCInboundCIDRs = []string{"10.243.0.0/16"}
	ws.Resources.ClusterHTTPAllowedCIDRs = []string{"198.51.100.0/24"}
	ws.Resources.ClusterVPCDefaultSGInboundCIDRs = []string{"10.0.0.0/8", "172.16.0.0/12"}

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	body := buf.String()

	for _, want := range []string{
		`testing_jumphost_allowed_cidrs = ["203.0.113.7/32"]`,
		`testing_client_vpc_inbound_cidrs = ["10.243.0.0/16"]`,
		`cluster_http_allowed_cidrs = ["198.51.100.0/24"]`,
		`cluster_vpc_default_sg_inbound_cidrs = ["10.0.0.0/8", "172.16.0.0/12"]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing from the render:\n  %s\n--- got ---\n%s", want, body)
		}
	}
}

// An unset list must emit nothing, so the module's own default stands. Emitting
// an empty list would be indistinguishable in HCL from "the operator asked for
// no sources", and each module's default differs per plane on purpose.
func TestUnsetSecurityGroupCIDRsAreNotRendered(t *testing.T) {
	ws := &config.Workspace{Prefix: "sg", Resources: config.DefaultResources()}

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	for _, unwanted := range []string{
		"testing_jumphost_allowed_cidrs",
		"testing_client_vpc_inbound_cidrs",
		"cluster_http_allowed_cidrs",
		"cluster_vpc_default_sg_inbound_cidrs",
	} {
		if strings.Contains(buf.String(), unwanted) {
			t.Errorf("%s was rendered while unset — that overrides the module default", unwanted)
		}
	}
}

// The rules themselves have to consume the variables. A variable that is
// declared, plumbed and rendered but never reaches a `remote =` is the shape
// that reads as fixed while changing nothing — and terraform validate passes
// either way, because an unused variable is legal HCL.
func TestSecurityGroupRulesConsumeTheCIDRVariables(t *testing.T) {
	repo := filepath.Join("..", "..")

	for _, tc := range []struct {
		file     string
		resource string
		local    string
	}{
		{"terraform/modules/testing/main.tf", "tgw_jumphost_ssh_inbound", "local.testing_ssh_cidrs"},
		{"terraform/modules/testing/main.tf", "cluster_jumphost_ssh_inbound", "local.testing_ssh_cidrs"},
		{"terraform/modules/testing/main.tf", "tgw_vpc_default_sg_inbound_all", "local.testing_client_vpc_in"},
		{"terraform/modules/roks_cluster/modules/cluster/main.tf", "cluster_tcp_80", "local.cluster_http_cidrs"},
		{"terraform/modules/roks_cluster/modules/cluster/main.tf", "cluster_sg_inbound_all", "local.cluster_vpc_default_sg_cidrs"},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(repo, tc.file))
			if err != nil {
				t.Fatalf("reading %s: %v", tc.file, err)
			}
			block := hclResourceBlock(t, string(body), tc.resource)

			if strings.Contains(block, `remote    = "0.0.0.0/0"`) {
				t.Errorf("%s still hard-codes 0.0.0.0/0 — it cannot be narrowed:\n%s", tc.resource, block)
			}
			if !strings.Contains(block, "remote    = each.value") {
				t.Errorf("%s does not take its source from the for_each:\n%s", tc.resource, block)
			}
			if !strings.Contains(block, tc.local) {
				t.Errorf("%s does not iterate %s, so the variable never reaches it:\n%s", tc.resource, tc.local, block)
			}
		})
	}
}

// hclResourceBlock returns the body of `resource "ibm_is_security_group_rule"
// "<name>" { ... }`, delimited by the closing brace in column 0.
func hclResourceBlock(t *testing.T, body, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^resource "ibm_is_security_group_rule" "` + regexp.QuoteMeta(name) + `" \{`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		t.Fatalf("resource %q not found — this test can no longer detect the gap", name)
	}
	rest := body[loc[0]:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("could not delimit resource %q", name)
	}
	return rest[:end]
}

// The sparse (legacy empty-Prefix) render body can still create a cluster via
// create_roks_cluster, so it must emit the CIDR fields too. Before this test,
// only renderFullBody emitted them: a legacy no-prefix workspace exporting
// ROKSBNKCTL_CLUSTER_HTTP_ALLOWED_CIDRS got the override logged as applied
// while the rendered tfvars silently left the fail-open module default standing.
func TestSecurityGroupCIDRsRenderOnTheSparsePathToo(t *testing.T) {
	ws := &config.Workspace{Resources: config.DefaultResources()} // no Prefix → sparse
	ws.Resources.ClusterHTTPAllowedCIDRs = []string{"198.51.100.0/24"}
	ws.Resources.ClusterVPCDefaultSGInboundCIDRs = []string{"10.0.0.0/8"}

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	for _, want := range []string{
		`cluster_http_allowed_cidrs = ["198.51.100.0/24"]`,
		`cluster_vpc_default_sg_inbound_cidrs = ["10.0.0.0/8"]`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("sparse render missing:\n  %s\n--- got ---\n%s", want, buf.String())
		}
	}
}

// The plumbing has five hops (workspace → tfvars → root variable → module arg →
// inner module arg → rule), and terraform accepts an unused root variable
// without a warning — which is exactly how the root→module hop shipped missing
// while the leaf test above stayed green. Pin every pass-through hop.
func TestSecurityGroupCIDRVariablesAreWiredThroughEveryHop(t *testing.T) {
	repo := filepath.Join("..", "..")
	for _, tc := range []struct{ file, arg string }{
		// root → testing module
		{"terraform/main.tf", "testing_jumphost_allowed_cidrs"},
		{"terraform/main.tf", "testing_client_vpc_inbound_cidrs"},
		// root → roks_cluster module
		{"terraform/main.tf", "cluster_http_allowed_cidrs"},
		{"terraform/main.tf", "cluster_vpc_default_sg_inbound_cidrs"},
		// roks_cluster → its inner cluster module
		{"terraform/modules/roks_cluster/main.tf", "cluster_http_allowed_cidrs"},
		{"terraform/modules/roks_cluster/main.tf", "cluster_vpc_default_sg_inbound_cidrs"},
	} {
		t.Run(tc.file+"/"+tc.arg, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(repo, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			// Whitespace-insensitive: alignment inside module blocks shifts.
			want := tc.arg + " = var." + tc.arg
			found := false
			for _, line := range strings.Split(string(body), "\n") {
				if strings.Join(strings.Fields(line), " ") == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s does not pass %q through (want a line `%s`) — the variable is accepted and silently unused", tc.file, tc.arg, want)
			}
		})
	}
}

// count→for_each renames a rule's state address, and destroy/create are
// independent graph nodes — without these moved blocks every existing
// deployment would drop its :80/:22/data-path rules for the window between
// them. The default-changed tgw_vpc_default_sg_inbound_all rule is deliberately
// absent: replacing it IS the #122 fix.
func TestUnchangedDefaultRulesHaveMovedBlocks(t *testing.T) {
	repo := filepath.Join("..", "..")
	for _, tc := range []struct{ file, resource string }{
		{"terraform/modules/roks_cluster/modules/cluster/main.tf", "cluster_tcp_80"},
		{"terraform/modules/roks_cluster/modules/cluster/main.tf", "cluster_sg_inbound_all"},
		{"terraform/modules/testing/main.tf", "tgw_jumphost_ssh_inbound"},
		{"terraform/modules/testing/main.tf", "cluster_jumphost_ssh_inbound"},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(repo, tc.file))
			if err != nil {
				t.Fatal(err)
			}
			flat := strings.Join(strings.Fields(string(body)), " ")
			want := `moved { from = ibm_is_security_group_rule.` + tc.resource + `[0] to = ibm_is_security_group_rule.` + tc.resource + `["0.0.0.0/0"] }`
			if !strings.Contains(flat, want) {
				t.Errorf("%s: no moved block renaming %s[0] → [\"0.0.0.0/0\"] — existing deployments would destroy-and-recreate the rule", tc.file, tc.resource)
			}
		})
	}
}
