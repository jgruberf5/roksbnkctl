package intent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	return p
}

const minimalYAML = `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
    - ap-southeast-2b
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
`

func TestLoad_HappyPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Metadata.Name != "my-cluster" {
		t.Errorf("name: got %q, want %q", c.Metadata.Name, "my-cluster")
	}
	if c.Metadata.Region != "ap-southeast-2" {
		t.Errorf("region: got %q", c.Metadata.Region)
	}
	if len(c.Network.AZs) != 2 {
		t.Errorf("azs len: got %d, want 2", len(c.Network.AZs))
	}
	if c.Network.VPCCidr != "10.0.0.0/16" {
		t.Errorf("vpcCidr: got %q", c.Network.VPCCidr)
	}
}

func TestLoad_OmitsForgeWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Forge != nil {
		t.Errorf("Forge: got %+v, want nil for cluster.yaml without forge block", c.Forge)
	}
}

func TestLoad_ForgeBlockEnabled(t *testing.T) {
	dir := t.TempDir()
	withForge := minimalYAML + `
forge:
  enabled: true
  url: http://localhost:8000
  mcpUrl: http://localhost:8081/mcp/
`
	p := writeFile(t, dir, "cluster.yaml", withForge)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with forge: %v", err)
	}
	if c.Forge == nil {
		t.Fatal("Forge: nil, want populated struct")
	}
	if !c.Forge.Enabled {
		t.Errorf("Forge.Enabled: got false, want true")
	}
	if c.Forge.URL != "http://localhost:8000" {
		t.Errorf("Forge.URL: got %q", c.Forge.URL)
	}
	if c.Forge.MCPURL != "http://localhost:8081/mcp/" {
		t.Errorf("Forge.MCPURL: got %q", c.Forge.MCPURL)
	}
}

func TestLoad_RejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	bad := minimalYAML + "\nunknownField: boom\n"
	p := writeFile(t, dir, "cluster.yaml", bad)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
}

func TestLoad_RejectsInvalidName(t *testing.T) {
	cases := []struct {
		name string
		desc string
	}{
		{"UPPER", "uppercase not allowed"},
		{"a", "too short (single char)"},
		{"-starts-with-dash", "starts with dash"},
		{"ends-with-dash-", "ends with dash"},
		{"this-name-is-way-too-long-to-be-valid-for-eks-cluster-rules-x", "too long"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: ` + tc.name + `
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
`
			dir := t.TempDir()
			p := writeFile(t, dir, "cluster.yaml", yaml)
			_, err := Load(p)
			if err == nil {
				t.Fatalf("expected error for name %q (%s), got nil", tc.name, tc.desc)
			}
		})
	}
}

func TestLoad_ValidatesAZsNonEmpty(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs: []
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty azs, got nil")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/cluster.yaml")
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestStateDir(t *testing.T) {
	c := &Cluster{Metadata: Metadata{Name: "tracer"}}
	want := ".awsbnkctl/tracer"
	if got := c.StateDir(); got != want {
		t.Errorf("StateDir: got %q, want %q", got, want)
	}
}

const clusterWithEKSYAML = `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
    - ap-southeast-2b
  subnets:
    public:
      - cidr: 10.0.1.0/24
        az: ap-southeast-2a
    private:
      - cidr: 10.0.11.0/24
        az: ap-southeast-2a
  natGateways: 1
cluster:
  kubernetesVersion: "1.30"
  nodeGroups:
    - name: default
      instanceType: t3.medium
      desiredSize: 1
      minSize: 1
      maxSize: 2
      diskSize: 50
`

func TestLoad_ClusterSpecParsed(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", clusterWithEKSYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ClusterSpec == nil {
		t.Fatal("ClusterSpec: nil, want populated struct")
	}
	if c.ClusterSpec.KubernetesVersion != "1.30" {
		t.Errorf("KubernetesVersion: got %q, want 1.30", c.ClusterSpec.KubernetesVersion)
	}
	if len(c.ClusterSpec.NodeGroups) != 1 {
		t.Fatalf("NodeGroups len: got %d, want 1", len(c.ClusterSpec.NodeGroups))
	}
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.Name != "default" {
		t.Errorf("NodeGroup.Name: got %q, want default", ng.Name)
	}
	if ng.InstanceType != "t3.medium" {
		t.Errorf("NodeGroup.InstanceType: got %q, want t3.medium", ng.InstanceType)
	}
	if ng.DesiredSize != 1 {
		t.Errorf("NodeGroup.DesiredSize: got %d, want 1", ng.DesiredSize)
	}
	if ng.DiskSize != 50 {
		t.Errorf("NodeGroup.DiskSize: got %d, want 50", ng.DiskSize)
	}
}

func TestLoad_ClusterSpecDefaults(t *testing.T) {
	yaml := minimalYAML + `
cluster:
  nodeGroups:
    - name: ng
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ClusterSpec.KubernetesVersion != "1.30" {
		t.Errorf("default KubernetesVersion: got %q, want 1.30", c.ClusterSpec.KubernetesVersion)
	}
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.InstanceType != "t3.medium" {
		t.Errorf("default InstanceType: got %q, want t3.medium", ng.InstanceType)
	}
	if ng.DesiredSize != 1 {
		t.Errorf("default DesiredSize: got %d, want 1", ng.DesiredSize)
	}
	if ng.MinSize != 1 {
		t.Errorf("default MinSize: got %d, want 1", ng.MinSize)
	}
	if ng.MaxSize != 2 {
		t.Errorf("default MaxSize: got %d, want 2", ng.MaxSize)
	}
	if ng.DiskSize != 50 {
		t.Errorf("default DiskSize: got %d, want 50", ng.DiskSize)
	}
}

func TestLoad_ClusterSpecRejectsEmptyNodeGroups(t *testing.T) {
	yaml := minimalYAML + `
cluster:
  kubernetesVersion: "1.30"
  nodeGroups: []
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty nodeGroups with cluster block, got nil")
	}
}

func TestLoad_ClusterSpecRejectsInvalidNodeGroupName(t *testing.T) {
	cases := []struct {
		name string
		desc string
	}{
		{"UPPER", "uppercase not allowed"},
		{"-starts-dash", "starts with dash"},
		{"ends-dash-", "ends with dash"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			yaml := minimalYAML + `
cluster:
  nodeGroups:
    - name: ` + tc.name + `
`
			dir := t.TempDir()
			p := writeFile(t, dir, "cluster.yaml", yaml)
			_, err := Load(p)
			if err == nil {
				t.Fatalf("expected error for node group name %q (%s), got nil", tc.name, tc.desc)
			}
		})
	}
}

func TestLoad_ClusterSpecOmittedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ClusterSpec != nil {
		t.Errorf("ClusterSpec: got %+v, want nil when cluster block absent", c.ClusterSpec)
	}
}

// ─── BnkSpec tests (slice 5) ──────────────────────────────────────────────────

func TestLoad_BnkBlockOmittedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Bnk != nil {
		t.Errorf("Bnk: got %+v, want nil when bnk block absent", c.Bnk)
	}
}

func TestLoad_BnkBlockParsed(t *testing.T) {
	dir := t.TempDir()
	// Write placeholder files so path-existence validation passes.
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ` + jwtPath + `
  certManagerVersion: "1.16.1"
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with bnk block: %v", err)
	}
	if c.Bnk == nil {
		t.Fatal("Bnk: nil, want populated struct")
	}
	if c.Bnk.FARArchive != farPath {
		t.Errorf("Bnk.FARArchive: got %q, want %q", c.Bnk.FARArchive, farPath)
	}
	if c.Bnk.JWT != jwtPath {
		t.Errorf("Bnk.JWT: got %q, want %q", c.Bnk.JWT, jwtPath)
	}
	if c.Bnk.CertManagerVersion != "1.16.1" {
		t.Errorf("Bnk.CertManagerVersion: got %q, want 1.16.1", c.Bnk.CertManagerVersion)
	}
}

func TestLoad_BnkBlockDefaultCertManagerVersion(t *testing.T) {
	dir := t.TempDir()
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	// certManagerVersion omitted — should default to 1.16.1.
	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ` + jwtPath + `
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with bnk (no version): %v", err)
	}
	if c.Bnk.CertManagerVersion != EmbeddedCertManagerVersion {
		t.Errorf("default CertManagerVersion: got %q, want %q", c.Bnk.CertManagerVersion, EmbeddedCertManagerVersion)
	}
}

func TestLoad_BnkBlockRejectsMismatchedVersion(t *testing.T) {
	dir := t.TempDir()
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ` + jwtPath + `
  certManagerVersion: "1.15.0"
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for mismatched certManagerVersion, got nil")
	}
	if !containsStr(err.Error(), "certManagerVersion") {
		t.Errorf("error should mention 'certManagerVersion': %v", err)
	}
}

func TestLoad_BnkBlockRejectsMissingFARArchive(t *testing.T) {
	dir := t.TempDir()
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	yaml := minimalYAML + `
bnk:
  farArchive: ""
  jwt: ` + jwtPath + `
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty farArchive, got nil")
	}
}

func TestLoad_BnkBlockRejectsMissingJWT(t *testing.T) {
	dir := t.TempDir()
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)

	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ""
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for empty jwt, got nil")
	}
}

// ─── AddonsSpec + FloSpec tests (slice 6) ─────────────────────────────────────

func TestLoad_AddonsBlockOmittedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Addons != nil {
		t.Errorf("Addons: got %+v, want nil when addons block absent", c.Addons)
	}
}

func TestLoad_AddonsFloBlock(t *testing.T) {
	dir := t.TempDir()
	withAddons := minimalYAML + `
addons:
  flo:
    version: "v2.21.13-0.0.28"
`
	p := writeFile(t, dir, "cluster.yaml", withAddons)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with addons: %v", err)
	}
	if c.Addons == nil {
		t.Fatal("Addons: nil, want populated struct")
	}
	if c.Addons.Flo == nil {
		t.Fatal("Addons.Flo: nil, want populated struct")
	}
	if c.Addons.Flo.Version != "v2.21.13-0.0.28" {
		t.Errorf("Addons.Flo.Version: got %q", c.Addons.Flo.Version)
	}
}

func TestLoad_AddonsFloEnabled_ExplicitlyFalse(t *testing.T) {
	dir := t.TempDir()
	withDisabledFLO := minimalYAML + `
addons:
  flo:
    enabled: false
`
	p := writeFile(t, dir, "cluster.yaml", withDisabledFLO)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with flo.enabled=false: %v", err)
	}
	if c.Addons == nil || c.Addons.Flo == nil {
		t.Fatal("expected Addons.Flo to be populated")
	}
	if c.Addons.Flo.FloEnabled() {
		t.Error("FloEnabled() should return false when enabled: false")
	}
}

func TestFloSpec_FloEnabled_NilSpec(t *testing.T) {
	var f *FloSpec
	if !f.FloEnabled() {
		t.Error("FloEnabled() on nil FloSpec should return true (default enabled)")
	}
}

func TestFloSpec_FloEnabled_NilEnabled(t *testing.T) {
	f := &FloSpec{} // Enabled field is nil
	if !f.FloEnabled() {
		t.Error("FloEnabled() with nil Enabled field should return true")
	}
}

func TestFloSpec_FLOVersion_Default(t *testing.T) {
	var f *FloSpec
	if got := f.FLOVersion(); got != DefaultFLOVersion {
		t.Errorf("FLOVersion() on nil spec = %q, want %q", got, DefaultFLOVersion)
	}
}

func TestFloSpec_FLOVersion_Override(t *testing.T) {
	f := &FloSpec{Version: "v2.99.0"}
	if got := f.FLOVersion(); got != "v2.99.0" {
		t.Errorf("FLOVersion() = %q, want v2.99.0", got)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsRune(s, sub))
}

func containsRune(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─── Pattern + DataPath tests (slice 7) ──────────────────────────────────────

const hostDeviceMinimalYAML = `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
  region: ap-southeast-2
network:
  vpcCidr: 10.0.0.0/16
  azs:
    - ap-southeast-2a
    - ap-southeast-2b
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
pattern: host-device
cluster:
  nodeGroups:
    - name: ng
`

// TestLoad_HostDevicePattern_RequiresDataPath verifies that host-device without
// network.dataPath returns an error.
func TestLoad_HostDevicePattern_RequiresDataPath(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
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
pattern: host-device
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error when pattern=host-device and dataPath is absent, got nil")
	}
	if !containsStr(err.Error(), "dataPath") {
		t.Errorf("error should mention 'dataPath': %v", err)
	}
}

// TestLoad_HostDevicePattern_AZMismatch verifies that a dataPath AZ not in
// network.azs returns an error.
func TestLoad_HostDevicePattern_AZMismatch(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
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
      az: ap-southeast-2b
    internal:
      cidr: 10.0.20.0/24
      az: ap-southeast-2a
pattern: host-device
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error when dataPath.external.az is not in network.azs, got nil")
	}
	if !containsStr(err.Error(), "ap-southeast-2b") {
		t.Errorf("error should mention the mismatched AZ: %v", err)
	}
}

// TestLoad_HostDevicePattern_AutoInjectsRoleBnk verifies that when pattern is
// host-device and no role label is set, role=bnk is auto-injected.
func TestLoad_HostDevicePattern_AutoInjectsRoleBnk(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", hostDeviceMinimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ClusterSpec == nil || len(c.ClusterSpec.NodeGroups) == 0 {
		t.Fatal("expected ClusterSpec with at least one node group")
	}
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.Labels["role"] != "bnk" {
		t.Errorf("expected role=bnk auto-injected, got labels=%v", ng.Labels)
	}
}

// TestLoad_HostDevicePattern_PreservesExplicitRoleBnk verifies that an
// explicitly-set role label is preserved (not overwritten).
func TestLoad_HostDevicePattern_PreservesExplicitRoleBnk(t *testing.T) {
	yaml := `
apiVersion: awsbnkctl/v1
kind: Cluster
metadata:
  name: my-cluster
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
pattern: host-device
cluster:
  nodeGroups:
    - name: ng
      labels:
        role: bnk
        custom: value
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.Labels["role"] != "bnk" {
		t.Errorf("role label overwritten: %v", ng.Labels)
	}
	if ng.Labels["custom"] != "value" {
		t.Errorf("custom label lost: %v", ng.Labels)
	}
}

// TestLoad_HostDevicePattern_DataPathParsed verifies the dataPath block is
// parsed correctly.
func TestLoad_HostDevicePattern_DataPathParsed(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", hostDeviceMinimalYAML)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Network.DataPath == nil {
		t.Fatal("DataPath: nil, want populated struct")
	}
	if c.Network.DataPath.External.CIDR != "10.0.10.0/24" {
		t.Errorf("External.CIDR: got %q, want 10.0.10.0/24", c.Network.DataPath.External.CIDR)
	}
	if c.Network.DataPath.External.AZ != "ap-southeast-2a" {
		t.Errorf("External.AZ: got %q, want ap-southeast-2a", c.Network.DataPath.External.AZ)
	}
	if c.Network.DataPath.Internal.CIDR != "10.0.20.0/24" {
		t.Errorf("Internal.CIDR: got %q, want 10.0.20.0/24", c.Network.DataPath.Internal.CIDR)
	}
}

// TestLoad_BnkSpec_Defaults verifies that BnkSpec slice-7 fields get defaults
// applied when omitted.
func TestLoad_BnkSpec_Defaults(t *testing.T) {
	dir := t.TempDir()
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ` + jwtPath + `
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Bnk.DeploymentSize != "Small" {
		t.Errorf("DeploymentSize: got %q, want Small", c.Bnk.DeploymentSize)
	}
	if c.Bnk.StorageClassName != "gp2" {
		t.Errorf("StorageClassName: got %q, want gp2", c.Bnk.StorageClassName)
	}
	if c.Bnk.ManifestVersion != "2.3.0-3.2598.3-0.0.170" {
		t.Errorf("ManifestVersion: got %q, want 2.3.0-3.2598.3-0.0.170", c.Bnk.ManifestVersion)
	}
	if c.Bnk.TmmMtu != 9000 {
		t.Errorf("TmmMtu: got %d, want 9000", c.Bnk.TmmMtu)
	}
	if c.Bnk.TmmCpu != "2" {
		t.Errorf("TmmCpu: got %q, want 2", c.Bnk.TmmCpu)
	}
	if c.Bnk.TmmMemory != "8Gi" {
		t.Errorf("TmmMemory: got %q, want 8Gi", c.Bnk.TmmMemory)
	}
	if c.Bnk.TmmHugepages != "4Gi" {
		t.Errorf("TmmHugepages: got %q, want 4Gi", c.Bnk.TmmHugepages)
	}
	if c.Bnk.PalCpuSet != "0,2" {
		t.Errorf("PalCpuSet: got %q, want 0,2", c.Bnk.PalCpuSet)
	}
}

// TestLoad_BnkSpec_ExplicitValuesPreserved verifies that explicitly-set
// slice-7 BnkSpec values are not overwritten by defaults.
func TestLoad_BnkSpec_ExplicitValuesPreserved(t *testing.T) {
	dir := t.TempDir()
	farPath := writeFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeFile(t, dir, "license.jwt", "jwt-token")

	yaml := minimalYAML + `
bnk:
  farArchive: ` + farPath + `
  jwt: ` + jwtPath + `
  deploymentSize: Medium
  storageClassName: gp2
  manifestVersion: "2.20.0"
  tmmMtu: 1500
  tmmCpu: "8"
  tmmMemory: "32Gi"
  tmmHugepages: "16Gi"
  palCpuSet: "0-7"
`
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Bnk.DeploymentSize != "Medium" {
		t.Errorf("DeploymentSize: got %q, want Medium", c.Bnk.DeploymentSize)
	}
	if c.Bnk.StorageClassName != "gp2" {
		t.Errorf("StorageClassName: got %q, want gp2", c.Bnk.StorageClassName)
	}
	if c.Bnk.ManifestVersion != "2.20.0" {
		t.Errorf("ManifestVersion: got %q, want 2.20.0", c.Bnk.ManifestVersion)
	}
	if c.Bnk.TmmMtu != 1500 {
		t.Errorf("TmmMtu: got %d, want 1500", c.Bnk.TmmMtu)
	}
	if c.Bnk.TmmCpu != "8" {
		t.Errorf("TmmCpu: got %q, want 8", c.Bnk.TmmCpu)
	}
	if c.Bnk.PalCpuSet != "0-7" {
		t.Errorf("PalCpuSet: got %q, want 0-7", c.Bnk.PalCpuSet)
	}
}

// ─── slice-10: SelfIP derivation + host-device default desiredSize=3 ─────

func TestDeriveSelfIP(t *testing.T) {
	cases := []struct {
		cidr   string
		offset int
		wantIP string
		wantP  int
	}{
		{"10.0.10.0/24", 240, "10.0.10.240", 24},
		{"10.0.20.0/24", 240, "10.0.20.240", 24},
		{"192.168.1.0/24", 100, "192.168.1.100", 24},
		{"10.0.0.0/16", 240, "", 16},  // unsupported prefix
		{"not-a-cidr", 240, "", 0},    // parse error
		{"10.0.10.0/24", 256, "", 24}, // offset out of byte range
		{"10.0.10.0/24", 0, "", 24},   // offset zero rejected
	}
	for _, tc := range cases {
		gotIP, gotP := DeriveSelfIP(tc.cidr, tc.offset)
		if gotIP != tc.wantIP || gotP != tc.wantP {
			t.Errorf("DeriveSelfIP(%q,%d) = (%q,%d), want (%q,%d)",
				tc.cidr, tc.offset, gotIP, gotP, tc.wantIP, tc.wantP)
		}
	}
}

// TestApplyDefaults_HostDevice_DesiredSize3 confirms host-device pattern auto-
// bumps DesiredSize/MinSize from 1 to 3 (dSSM quorum + TMM headroom).
// Per aws-gpu-setup vars.env:110 (≥3 for dSSM quorum).
func TestApplyDefaults_HostDevice_DesiredSize3(t *testing.T) {
	c := &Cluster{
		Pattern: "host-device",
		Network: Network{
			DataPath: &DataPathSpec{
				External: SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
				Internal: SubnetSpec{CIDR: "10.0.20.0/24", AZ: "ap-southeast-2a"},
			},
		},
		ClusterSpec: &ClusterSpec{
			NodeGroups: []NodeGroupSpec{{Name: "default"}},
		},
	}
	applyDefaults(c)
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.DesiredSize != 3 || ng.MinSize != 3 || ng.MaxSize < 3 {
		t.Errorf("host-device defaults: got desired=%d min=%d max=%d, want desired=3 min=3 max>=3",
			ng.DesiredSize, ng.MinSize, ng.MaxSize)
	}
}

// TestApplyDefaults_HostDevice_PreservesExplicitSize confirms explicit operator
// overrides (e.g. desiredSize=1 for cost-sensitive labs that accept reduced HA)
// are preserved when the operator set a value other than the default 1.
func TestApplyDefaults_HostDevice_PreservesExplicitSize(t *testing.T) {
	c := &Cluster{
		Pattern: "host-device",
		Network: Network{
			DataPath: &DataPathSpec{
				External: SubnetSpec{CIDR: "10.0.10.0/24", AZ: "a"},
				Internal: SubnetSpec{CIDR: "10.0.20.0/24", AZ: "a"},
			},
		},
		ClusterSpec: &ClusterSpec{
			NodeGroups: []NodeGroupSpec{{Name: "lab", DesiredSize: 2, MinSize: 2, MaxSize: 5}},
		},
	}
	applyDefaults(c)
	ng := c.ClusterSpec.NodeGroups[0]
	if ng.DesiredSize != 2 || ng.MinSize != 2 || ng.MaxSize != 5 {
		t.Errorf("explicit override stripped: got desired=%d min=%d max=%d, want 2/2/5",
			ng.DesiredSize, ng.MinSize, ng.MaxSize)
	}
}

// ─── Testing + JumphostSpec tests (slice 12) ─────────────────────────────────

// TestLoad_TestingBlock_Defaults verifies that testing.jumphost gets instanceType
// defaulted to "t3.small" and mgmtSubnetIndex stays 0.
func TestLoad_TestingBlock_Defaults(t *testing.T) {
	yaml := hostDeviceMinimalYAML + `
testing:
  jumphost:
    enabled: true
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Testing == nil || c.Testing.Jumphost == nil {
		t.Fatal("Testing.Jumphost: nil, want populated")
	}
	if !c.Testing.Jumphost.Enabled {
		t.Error("Jumphost.Enabled: got false, want true")
	}
	if c.Testing.Jumphost.InstanceType != "t3.small" {
		t.Errorf("default InstanceType: got %q, want t3.small", c.Testing.Jumphost.InstanceType)
	}
	if c.Testing.Jumphost.MgmtSubnetIndex != 0 {
		t.Errorf("default MgmtSubnetIndex: got %d, want 0", c.Testing.Jumphost.MgmtSubnetIndex)
	}
}

// TestLoad_TestingBlock_ValidationSuccess verifies a fully valid testing block loads.
func TestLoad_TestingBlock_ValidationSuccess(t *testing.T) {
	yaml := hostDeviceMinimalYAML + `
testing:
  jumphost:
    enabled: true
    instanceType: m5.large
    mgmtSubnetIndex: 0
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with testing block: %v", err)
	}
	if c.Testing.Jumphost.InstanceType != "m5.large" {
		t.Errorf("InstanceType: got %q, want m5.large", c.Testing.Jumphost.InstanceType)
	}
}

// TestLoad_TestingBlock_ValidationFailure_NoDataPath verifies that enabling the
// jumphost without pattern:host-device (and thus without dataPath) returns the
// documented "BNK_EXT subnet required" error.
func TestLoad_TestingBlock_ValidationFailure_NoDataPath(t *testing.T) {
	// minimalYAML has no pattern: host-device and no network.dataPath.
	yaml := minimalYAML + `
testing:
  jumphost:
    enabled: true
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for testing.jumphost.enabled without dataPath, got nil")
	}
	if !containsStr(err.Error(), "BNK_EXT subnet") {
		t.Errorf("error should mention 'BNK_EXT subnet': %v", err)
	}
}

// TestApplyDefaults_HostDevice_AutoDerivesSelfIPs confirms SelfIPs auto-derive
// to <subnet>.240 when not explicitly set in cluster.yaml.
func TestApplyDefaults_HostDevice_AutoDerivesSelfIPs(t *testing.T) {
	c := &Cluster{
		Pattern: "host-device",
		Network: Network{
			DataPath: &DataPathSpec{
				External: SubnetSpec{CIDR: "10.0.10.0/24", AZ: "a"},
				Internal: SubnetSpec{CIDR: "10.0.20.0/24", AZ: "a"},
			},
		},
	}
	applyDefaults(c)
	s := c.Network.DataPath.SelfIPs
	if s == nil {
		t.Fatal("SelfIPs not auto-created")
	}
	if s.External != "10.0.10.240" {
		t.Errorf("ext SelfIP = %q, want 10.0.10.240", s.External)
	}
	if s.Internal != "10.0.20.240" {
		t.Errorf("int SelfIP = %q, want 10.0.20.240", s.Internal)
	}
	if s.PrefixLen != 24 {
		t.Errorf("PrefixLen = %d, want 24", s.PrefixLen)
	}
}
