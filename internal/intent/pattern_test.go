package intent

import "testing"

// ─── capability helpers ──────────────────────────────────────────────────────

func TestCapabilityHelpers_Matrix(t *testing.T) {
	tests := []struct {
		pattern      string
		wantBNK      bool
		wantInternal bool
		wantBinding  string
	}{
		{"", false, false, ""},
		{PatternExternalOnly, true, false, "host-device"},
		{PatternDualInterface, true, true, "host-device"},
		{PatternHostDevice, true, true, "host-device"}, // legacy alias → dual
		{PatternSRIOVExternal, true, false, "sriov"},
		{"bogus", false, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			c := &Cluster{Pattern: tc.pattern}
			if got := c.IsBNKPattern(); got != tc.wantBNK {
				t.Errorf("IsBNKPattern()=%v want %v", got, tc.wantBNK)
			}
			if got := c.HasInternalInterface(); got != tc.wantInternal {
				t.Errorf("HasInternalInterface()=%v want %v", got, tc.wantInternal)
			}
			if got := c.DataplaneBinding(); got != tc.wantBinding {
				t.Errorf("DataplaneBinding()=%q want %q", got, tc.wantBinding)
			}
		})
	}
}

// ─── host-device alias normalization ─────────────────────────────────────────

// TestPattern_HostDeviceAlias_NormalizesToDual verifies the legacy host-device
// value is rewritten to dual-interface on Load so downstream code sees one value.
func TestPattern_HostDeviceAlias_NormalizesToDual(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", hostDeviceMinimalYAML)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Pattern != PatternDualInterface {
		t.Errorf("Pattern = %q, want %q (host-device must normalize to dual-interface)", c.Pattern, PatternDualInterface)
	}
	if !c.HasInternalInterface() {
		t.Error("host-device alias must report HasInternalInterface()=true")
	}
}

// ─── external-only validation ────────────────────────────────────────────────

const externalOnlyYAML = `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: ext-only
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
  dataPath:
    external:
      cidr: 10.0.10.0/24
      az: ap-southeast-2a
pattern: external-only
cluster:
  nodeGroups:
    - name: ng
`

func TestLoad_ExternalOnly_HappyPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", externalOnlyYAML)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load external-only: %v", err)
	}
	if c.HasInternalInterface() {
		t.Error("external-only must not report an internal interface")
	}
	if c.DataplaneBinding() != "host-device" {
		t.Errorf("external-only binding = %q, want host-device", c.DataplaneBinding())
	}
	// External SelfIP derived; internal must stay empty.
	sip := c.Network.DataPath.SelfIPs
	if sip == nil || sip.External == "" {
		t.Fatalf("expected external SelfIP derived, got %+v", sip)
	}
	if sip.Internal != "" {
		t.Errorf("external-only must not derive an internal SelfIP, got %q", sip.Internal)
	}
	// BNK sizing defaults still apply (role=bnk, desiredSize 3).
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.Labels["role"] != "bnk" {
		t.Errorf("expected role=bnk on external-only, got %v", ng.Labels)
	}
	if ng.DesiredSize != 3 {
		t.Errorf("expected desiredSize default 3 for BNK pattern, got %d", ng.DesiredSize)
	}
}

func TestLoad_ExternalOnly_ForbidsInternalBlock(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: ext-only
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
  dataPath:
    external:
      cidr: 10.0.10.0/24
      az: ap-southeast-2a
    internal:
      cidr: 10.0.20.0/24
      az: ap-southeast-2a
pattern: external-only
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error when external-only sets an internal block, got nil")
	}
	if !containsStr(err.Error(), "single-interface") {
		t.Errorf("error should explain single-interface: %v", err)
	}
}

func TestLoad_ExternalOnly_RequiresExternal(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: ext-only
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
pattern: external-only
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error when external-only omits dataPath, got nil")
	}
	if !containsStr(err.Error(), "dataPath") {
		t.Errorf("error should mention dataPath: %v", err)
	}
}

// ─── sriov-external gate ─────────────────────────────────────────────────────

func TestLoad_SRIOVExternal_BlockedPendingSpike(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: sriov
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
  dataPath:
    external:
      cidr: 10.0.10.0/24
      az: ap-southeast-2a
pattern: sriov-external
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected sriov-external to be blocked pending spike, got nil")
	}
	if !containsStr(err.Error(), "experimental") {
		t.Errorf("error should flag sriov-external as experimental: %v", err)
	}
}

// TestValidatePattern_UnknownValue rejects an unrecognised pattern string.
func TestValidatePattern_UnknownValue(t *testing.T) {
	c := &Cluster{Pattern: "bogus", Network: Network{AZs: []string{"a"}}}
	if err := validatePattern(c); err == nil {
		t.Fatal("expected error for unknown pattern, got nil")
	}
}
