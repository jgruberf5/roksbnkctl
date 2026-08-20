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
