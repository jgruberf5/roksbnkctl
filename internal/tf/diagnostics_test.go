package tf

import (
	"strings"
	"testing"
)

func TestSummarizeTerraformDiagnostics_IBMBlocksDeduped(t *testing.T) {
	// The exact shape a user hit: the same IBM-provider block printed 3x plus a
	// bare "exit status 1", interleaved with a deprecation Warning.
	raw := `module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc[0]: Destroying...

Warning: Argument is deprecated

  with module.flp_vsi.ibm_is_security_group_rule.flp_in,
  tcp is deprecated, use 'protocol'.

Error: ---
id: terraform-510d59a0
summary: 'DeleteVPCWithContext failed: The VPC is in use and cannot be deleted. Subnets [0717-85fe, 0727-320e] and 1 additional Subnets'
severity: error
resource: ibm_is_vpc
operation: delete
---

Error: exit status 1

Error: ---
id: terraform-510d59a0
summary: 'DeleteVPCWithContext failed: The VPC is in use and cannot be deleted. Subnets [0717-85fe, 0727-320e] and 1 additional Subnets'
severity: error
resource: ibm_is_vpc
operation: delete
---

Error: ---
id: terraform-510d59a0
summary: 'DeleteVPCWithContext failed: The VPC is in use and cannot be deleted. Subnets [0717-85fe, 0727-320e] and 1 additional Subnets'
severity: error
resource: ibm_is_vpc
operation: delete
---`

	got := summarizeTerraformDiagnostics(raw)
	if got == "" {
		t.Fatal("expected a non-empty summary")
	}
	// One distinct real error (×3), not three separate ones. The bare
	// "exit status 1" is its own title-only block, so 2 distinct total.
	if !strings.Contains(got, "distinct error") {
		t.Errorf("missing the distinct-error header:\n%s", got)
	}
	if !strings.Contains(got, "x3") {
		t.Errorf("the repeated VPC error should be collapsed to x3:\n%s", got)
	}
	if !strings.Contains(got, "ibm_is_vpc delete") || !strings.Contains(got, "The VPC is in use") {
		t.Errorf("summary should carry resource+operation+message:\n%s", got)
	}
	// The single-quotes around the YAML summary must be stripped.
	if strings.Contains(got, "'DeleteVPCWithContext") {
		t.Errorf("YAML single-quotes should be stripped:\n%s", got)
	}
	// Warnings must not appear as errors.
	if strings.Contains(got, "deprecated") {
		t.Errorf("warnings must not be summarized as errors:\n%s", got)
	}
}

func TestSummarizeTerraformDiagnostics_PlainError(t *testing.T) {
	raw := `Error: Invalid count argument

  on main.tf line 5, in resource "x":
   5:   count = length(...)`
	got := summarizeTerraformDiagnostics(raw)
	if !strings.Contains(got, "Invalid count argument") || !strings.Contains(got, "x1") {
		t.Errorf("plain terraform error not summarized:\n%s", got)
	}
}

func TestSummarizeTerraformDiagnostics_NoErrors(t *testing.T) {
	if got := summarizeTerraformDiagnostics("Apply complete! Resources: 3 added.\n"); got != "" {
		t.Errorf("clean output should yield no summary, got: %q", got)
	}
}

func TestDiagCaptureTeesAndBounds(t *testing.T) {
	var live strings.Builder
	d := newDiagCapture(&live, 16)
	_, _ = d.Write([]byte("hello "))
	_, _ = d.Write([]byte("world, this is long"))
	// live gets everything...
	if live.String() != "hello world, this is long" {
		t.Errorf("live passthrough wrong: %q", live.String())
	}
	// ...but the capture keeps only the last 16 bytes.
	if cap := d.String(); len(cap) != 16 || !strings.HasSuffix("hello world, this is long", cap) {
		t.Errorf("capture not bounded to tail: %q", cap)
	}
	d.Reset()
	if d.String() != "" {
		t.Errorf("reset should clear the capture")
	}
}
