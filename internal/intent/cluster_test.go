package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestLoad_ForgeCredentialTemplateID(t *testing.T) {
	dir := t.TempDir()
	withForge := minimalYAML + `
forge:
  enabled: true
  credentialTemplateId: 5
`
	p := writeFile(t, dir, "cluster.yaml", withForge)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with forge credentialTemplateId: %v", err)
	}
	if c.Forge == nil {
		t.Fatal("Forge: nil, want populated struct")
	}
	if c.Forge.CredentialTemplateID != 5 {
		t.Errorf("Forge.CredentialTemplateID = %d, want 5", c.Forge.CredentialTemplateID)
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

// TestDefaultVIP_HappyPath confirms DefaultVIP derives <network>.100.
func TestDefaultVIP_HappyPath(t *testing.T) {
	cases := []struct {
		cidr string
		want string
	}{
		{"10.0.10.0/24", "10.0.10.100"},
		{"172.16.0.0/24", "172.16.0.100"},
		{"192.168.5.0/24", "192.168.5.100"},
	}
	for _, tc := range cases {
		c := &Cluster{
			Network: Network{
				DataPath: &DataPathSpec{
					External: SubnetSpec{CIDR: tc.cidr},
				},
			},
		}
		got, err := c.DefaultVIP()
		if err != nil {
			t.Errorf("DefaultVIP(%q): unexpected error %v", tc.cidr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("DefaultVIP(%q) = %q, want %q", tc.cidr, got, tc.want)
		}
	}
}

// TestValidatePattern_HostDeviceRejectsDesiredSize2 verifies that an explicit
// desiredSize: 2 on a host-device cluster is rejected at load time (dSSM
// quorum requires ≥3). desiredSize: 0 (default) and ≥3 must still pass.
func TestValidatePattern_HostDeviceRejectsDesiredSize2(t *testing.T) {
	// desiredSize: 2 is explicitly set and must be rejected.
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
      desiredSize: 2
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)

	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for host-device with desiredSize=2, got nil")
	}
	if !containsStr(err.Error(), "desiredSize") {
		t.Errorf("error should mention 'desiredSize': %v", err)
	}
	if !containsStr(err.Error(), "3") {
		t.Errorf("error should mention required size '3': %v", err)
	}
}

// TestDefaultVIP_NoDataPath confirms DefaultVIP returns an error when
// network.dataPath is nil.
func TestDefaultVIP_NoDataPath(t *testing.T) {
	c := &Cluster{Network: Network{DataPath: nil}}
	_, err := c.DefaultVIP()
	if err == nil {
		t.Fatal("expected error when dataPath is nil, got nil")
	}
}

// TestDefaultVIP_EmptyCIDR confirms DefaultVIP returns an error when the
// external CIDR is empty.
func TestDefaultVIP_EmptyCIDR(t *testing.T) {
	c := &Cluster{
		Network: Network{
			DataPath: &DataPathSpec{
				External: SubnetSpec{CIDR: ""},
			},
		},
	}
	_, err := c.DefaultVIP()
	if err == nil {
		t.Fatal("expected error for empty CIDR, got nil")
	}
}

// ─── ForgeSpec resolver tests ─────────────────────────────────────────────────

// TestForgeSpec_ResolveUsername_Default verifies nil and zero ForgeSpec both
// return "admin".
func TestForgeSpec_ResolveUsername_Default(t *testing.T) {
	var nilSpec *ForgeSpec
	if got := nilSpec.ResolveUsername(); got != "admin" {
		t.Errorf("nil.ResolveUsername() = %q, want \"admin\"", got)
	}
	emptySpec := &ForgeSpec{}
	if got := emptySpec.ResolveUsername(); got != "admin" {
		t.Errorf("empty.ResolveUsername() = %q, want \"admin\"", got)
	}
}

// TestForgeSpec_ResolveUsername_YAMLField verifies that forge.username in the
// spec is returned when set.
func TestForgeSpec_ResolveUsername_YAMLField(t *testing.T) {
	spec := &ForgeSpec{Username: "operator"}
	if got := spec.ResolveUsername(); got != "operator" {
		t.Errorf("ResolveUsername() = %q, want \"operator\"", got)
	}
}

// TestForgeSpec_ResolvePassword_Default verifies the built-in default is
// returned (with usingDefault=true) when nothing is configured.
func TestForgeSpec_ResolvePassword_Default(t *testing.T) {
	// Clear env so we don't accidentally pick up a real value.
	t.Setenv("AWSBNKCTL_FORGE_PASSWORD", "")
	var nilSpec *ForgeSpec
	pwd, usingDefault := nilSpec.ResolvePassword()
	if pwd != "changeme" {
		t.Errorf("password = %q, want \"changeme\"", pwd)
	}
	if !usingDefault {
		t.Error("usingDefault should be true when no env/yaml is set")
	}
}

// TestForgeSpec_ResolvePassword_EnvOverridesAll verifies AWSBNKCTL_FORGE_PASSWORD
// takes precedence over both the yaml field and the built-in default.
func TestForgeSpec_ResolvePassword_EnvOverridesAll(t *testing.T) {
	t.Setenv("AWSBNKCTL_FORGE_PASSWORD", "envpass")
	// Even with a yaml password set, env wins.
	spec := &ForgeSpec{Password: "yamlpass"}
	pwd, usingDefault := spec.ResolvePassword()
	if pwd != "envpass" {
		t.Errorf("password = %q, want \"envpass\" (env should win)", pwd)
	}
	if usingDefault {
		t.Error("usingDefault should be false when env is set")
	}
}

// TestForgeSpec_ResolvePassword_YAMLFallback verifies forge.password in the
// spec is used when the env is unset.
func TestForgeSpec_ResolvePassword_YAMLFallback(t *testing.T) {
	t.Setenv("AWSBNKCTL_FORGE_PASSWORD", "")
	spec := &ForgeSpec{Password: "yamlpass"}
	pwd, usingDefault := spec.ResolvePassword()
	if pwd != "yamlpass" {
		t.Errorf("password = %q, want \"yamlpass\"", pwd)
	}
	if usingDefault {
		t.Error("usingDefault should be false when forge.password is set")
	}
}

// TestForgeSpec_ResolveURL_Default verifies that a nil / zero ForgeSpec returns
// DefaultForgeRESTURL when no env override is set.
func TestForgeSpec_ResolveURL_Default(t *testing.T) {
	t.Setenv("AWSBNKCTL_FORGE_URL", "")
	var nilSpec *ForgeSpec
	if got := nilSpec.ResolveURL(); got != DefaultForgeRESTURL {
		t.Errorf("nil.ResolveURL() = %q, want %q", got, DefaultForgeRESTURL)
	}
}

// TestForgeSpec_ResolveURL_EnvOverride verifies AWSBNKCTL_FORGE_URL takes
// precedence over both the yaml field and the default.
func TestForgeSpec_ResolveURL_EnvOverride(t *testing.T) {
	t.Setenv("AWSBNKCTL_FORGE_URL", "http://my-forge:9000")
	spec := &ForgeSpec{URL: "http://yaml-forge:8000"}
	if got := spec.ResolveURL(); got != "http://my-forge:9000" {
		t.Errorf("ResolveURL() = %q, want env value", got)
	}
}

// TestForgeSpec_ResolveURL_YAMLFallback verifies forge.url is used when the
// env is unset.
func TestForgeSpec_ResolveURL_YAMLFallback(t *testing.T) {
	t.Setenv("AWSBNKCTL_FORGE_URL", "")
	spec := &ForgeSpec{URL: "http://yaml-forge:8000"}
	if got := spec.ResolveURL(); got != "http://yaml-forge:8000" {
		t.Errorf("ResolveURL() = %q, want \"http://yaml-forge:8000\"", got)
	}
}

// TestLoad_ForgeBlockWithCredentials verifies that the new username + password
// fields round-trip through cluster.yaml load.
func TestLoad_ForgeBlockWithCredentials(t *testing.T) {
	dir := t.TempDir()
	withForgeCreds := minimalYAML + `
forge:
  enabled: true
  url: http://localhost:8000
  username: myoperator
  password: s3cr3t
`
	p := writeFile(t, dir, "cluster.yaml", withForgeCreds)

	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with forge creds: %v", err)
	}
	if c.Forge == nil {
		t.Fatal("Forge: nil, want populated struct")
	}
	if c.Forge.Username != "myoperator" {
		t.Errorf("Forge.Username = %q, want \"myoperator\"", c.Forge.Username)
	}
	if c.Forge.Password != "s3cr3t" {
		t.Errorf("Forge.Password = %q, want \"s3cr3t\"", c.Forge.Password)
	}
}

// ─── DemoSpec tests (PRD 10, Slice A1) ───────────────────────────────────────

// hostDeviceWithJumphostYAML is a valid host-device cluster with jumphost enabled —
// the minimum required for demo mode.
const hostDeviceWithJumphostYAML = hostDeviceMinimalYAML + `
testing:
  jumphost:
    enabled: true
`

// TestLoad_DemoBlock_Absent verifies that omitting the demo: block keeps Demo nil.
func TestLoad_DemoBlock_Absent(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", minimalYAML)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Demo != nil {
		t.Errorf("Demo: got %+v, want nil when demo block absent", c.Demo)
	}
}

// TestLoad_DemoBlock_EnabledWithJumphost verifies the happy path: demo: enabled: true
// with testing.jumphost.enabled: true loads successfully and TTL defaults to "24h".
func TestLoad_DemoBlock_EnabledWithJumphost(t *testing.T) {
	dir := t.TempDir()
	yaml := hostDeviceWithJumphostYAML + `
demo:
  enabled: true
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with demo block: %v", err)
	}
	if c.Demo == nil {
		t.Fatal("Demo: nil, want populated struct")
	}
	if !c.Demo.Enabled {
		t.Error("Demo.Enabled: got false, want true")
	}
	if c.Demo.TTL != "24h" {
		t.Errorf("Demo.TTL: got %q, want \"24h\" (default)", c.Demo.TTL)
	}
}

// TestLoad_DemoBlock_ExplicitTTL verifies a custom TTL is accepted and preserved.
func TestLoad_DemoBlock_ExplicitTTL(t *testing.T) {
	dir := t.TempDir()
	yaml := hostDeviceWithJumphostYAML + `
demo:
  enabled: true
  ttl: 48h
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with demo ttl=48h: %v", err)
	}
	if c.Demo.TTL != "48h" {
		t.Errorf("Demo.TTL: got %q, want \"48h\"", c.Demo.TTL)
	}
}

// TestDemoEnabled_Helper verifies DemoEnabled() returns the correct boolean.
func TestDemoEnabled_Helper(t *testing.T) {
	cases := []struct {
		desc string
		c    *Cluster
		want bool
	}{
		{"nil Demo", &Cluster{}, false},
		{"Demo.Enabled=false", &Cluster{Demo: &DemoSpec{Enabled: false}}, false},
		{"Demo.Enabled=true", &Cluster{Demo: &DemoSpec{Enabled: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if got := tc.c.DemoEnabled(); got != tc.want {
				t.Errorf("DemoEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestEnableDemo verifies the EnableDemo method across three cases:
//  1. nil Demo block → creates block, sets Enabled=true, defaults TTL.
//  2. explicit TTL already set → Enabled=true, TTL preserved.
//  3. already enabled → idempotent (no double-application).
func TestEnableDemo(t *testing.T) {
	t.Run("creates Demo block and defaults TTL", func(t *testing.T) {
		c := &Cluster{}
		c.EnableDemo()
		if !c.DemoEnabled() {
			t.Fatal("DemoEnabled() = false after EnableDemo on nil Demo")
		}
		if c.Demo.TTL != DefaultDemoTTL {
			t.Errorf("TTL = %q, want %q", c.Demo.TTL, DefaultDemoTTL)
		}
	})

	t.Run("preserves explicit TTL", func(t *testing.T) {
		c := &Cluster{Demo: &DemoSpec{TTL: "48h"}}
		c.EnableDemo()
		if !c.DemoEnabled() {
			t.Fatal("DemoEnabled() = false after EnableDemo")
		}
		if c.Demo.TTL != "48h" {
			t.Errorf("TTL = %q, want \"48h\" (explicit value should be preserved)", c.Demo.TTL)
		}
	})

	t.Run("idempotent on already-enabled spec", func(t *testing.T) {
		c := &Cluster{Demo: &DemoSpec{Enabled: true, TTL: "12h"}}
		c.EnableDemo()
		if !c.DemoEnabled() {
			t.Fatal("DemoEnabled() = false after second EnableDemo call")
		}
		if c.Demo.TTL != "12h" {
			t.Errorf("TTL = %q after second EnableDemo, want \"12h\" (should not change)", c.Demo.TTL)
		}
	})
}

// TestApplyDefaults_Demo_TTLDefault verifies that applyDefaults fills in DefaultDemoTTL
// when demo is enabled but TTL is empty.
func TestApplyDefaults_Demo_TTLDefault(t *testing.T) {
	c := &Cluster{Demo: &DemoSpec{Enabled: true}}
	applyDefaults(c)
	if c.Demo.TTL != DefaultDemoTTL {
		t.Errorf("applyDefaults: Demo.TTL = %q, want %q", c.Demo.TTL, DefaultDemoTTL)
	}
}

// TestApplyDefaults_Demo_TTLPreserved verifies that an explicitly-set TTL is
// not overwritten by applyDefaults.
func TestApplyDefaults_Demo_TTLPreserved(t *testing.T) {
	c := &Cluster{Demo: &DemoSpec{Enabled: true, TTL: "48h"}}
	applyDefaults(c)
	if c.Demo.TTL != "48h" {
		t.Errorf("applyDefaults: Demo.TTL = %q, want \"48h\"", c.Demo.TTL)
	}
}

// TestApplyDefaults_Demo_DisabledNoTTL verifies that applyDefaults does NOT
// set a TTL when demo is disabled (the block is present but Enabled=false).
func TestApplyDefaults_Demo_DisabledNoTTL(t *testing.T) {
	c := &Cluster{Demo: &DemoSpec{Enabled: false}}
	applyDefaults(c)
	if c.Demo.TTL != "" {
		t.Errorf("applyDefaults: Demo.TTL = %q on disabled demo, want \"\"", c.Demo.TTL)
	}
}

// TestValidateDemo_HappyPath verifies that a cluster with jumphost enabled and a
// valid TTL passes ValidateDemo.
func TestValidateDemo_HappyPath(t *testing.T) {
	c := &Cluster{
		Demo: &DemoSpec{Enabled: true, TTL: "24h"},
		Testing: &TestingSpec{
			Jumphost: &JumphostSpec{Enabled: true},
		},
	}
	if err := ValidateDemo(c); err != nil {
		t.Errorf("ValidateDemo happy path: unexpected error: %v", err)
	}
}

// TestValidateDemo_Disabled verifies that ValidateDemo is a no-op when demo is disabled.
func TestValidateDemo_Disabled(t *testing.T) {
	c := &Cluster{Demo: &DemoSpec{Enabled: false}}
	if err := ValidateDemo(c); err != nil {
		t.Errorf("ValidateDemo disabled: unexpected error: %v", err)
	}
}

// TestValidateDemo_BadTTL verifies that malformed TTL values are rejected with
// a clear error message.
func TestValidateDemo_BadTTL(t *testing.T) {
	cases := []struct {
		ttl  string
		desc string
	}{
		{"banana", "non-duration string"},
		{"-5h", "negative duration"},
		{"0", "zero duration"},
		{"0s", "zero duration as 0s"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			c := &Cluster{
				Demo: &DemoSpec{Enabled: true, TTL: tc.ttl},
				Testing: &TestingSpec{
					Jumphost: &JumphostSpec{Enabled: true},
				},
			}
			err := ValidateDemo(c)
			if err == nil {
				t.Fatalf("ValidateDemo(%q): expected error, got nil", tc.ttl)
			}
			if !strings.Contains(err.Error(), "demo.ttl") {
				t.Errorf("error should mention 'demo.ttl': %v", err)
			}
		})
	}
}

// TestValidateDemo_JumphostRequired verifies that demo mode requires
// testing.jumphost.enabled: true across all absence variants.
func TestValidateDemo_JumphostRequired(t *testing.T) {
	cases := []struct {
		desc    string
		testing *TestingSpec
	}{
		{"testing block absent", nil},
		{"jumphost block absent", &TestingSpec{}},
		{"jumphost.enabled=false", &TestingSpec{Jumphost: &JumphostSpec{Enabled: false}}},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			c := &Cluster{
				Demo:    &DemoSpec{Enabled: true, TTL: "24h"},
				Testing: tc.testing,
			}
			err := ValidateDemo(c)
			if err == nil {
				t.Fatal("ValidateDemo: expected error when jumphost not enabled, got nil")
			}
			if !strings.Contains(err.Error(), "testing.jumphost.enabled") {
				t.Errorf("error should mention 'testing.jumphost.enabled': %v", err)
			}
		})
	}
}

// TestLoad_DemoBlock_RejectsNoJumphost verifies that demo: enabled: true without
// testing.jumphost.enabled fails with a clear error at Load time.
func TestLoad_DemoBlock_RejectsNoJumphost(t *testing.T) {
	dir := t.TempDir()
	// hostDeviceMinimalYAML has no testing block.
	yaml := hostDeviceMinimalYAML + `
demo:
  enabled: true
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for demo without jumphost, got nil")
	}
	if !strings.Contains(err.Error(), "testing.jumphost.enabled") {
		t.Errorf("error should mention 'testing.jumphost.enabled': %v", err)
	}
}

// TestLoad_DemoBlock_RejectsMalformedTTL verifies that a bad TTL in cluster.yaml
// fails Load with a clear error.
func TestLoad_DemoBlock_RejectsMalformedTTL(t *testing.T) {
	dir := t.TempDir()
	yaml := hostDeviceWithJumphostYAML + `
demo:
  enabled: true
  ttl: banana
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for malformed demo.ttl, got nil")
	}
	if !strings.Contains(err.Error(), "demo.ttl") {
		t.Errorf("error should mention 'demo.ttl': %v", err)
	}
}

// ─── SetDemoTags tests (PRD 10, Slice A2) ────────────────────────────────────

// TestSetDemoTags_NilMap verifies that SetDemoTags nil-inits c.Tags and writes
// both demo tag keys.
func TestSetDemoTags_NilMap(t *testing.T) {
	c := &Cluster{} // Tags is nil
	expiry := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c.SetDemoTags(expiry)

	if c.Tags == nil {
		t.Fatal("Tags: nil after SetDemoTags, want initialised map")
	}
	if got := c.Tags[DemoTagKey]; got != "true" {
		t.Errorf("Tags[%q] = %q, want \"true\"", DemoTagKey, got)
	}
	wantExpiry := expiry.UTC().Format(time.RFC3339)
	if got := c.Tags[DemoExpiryTagKey]; got != wantExpiry {
		t.Errorf("Tags[%q] = %q, want %q", DemoExpiryTagKey, got, wantExpiry)
	}
}

// TestSetDemoTags_ExistingMapPreservesOtherKeys verifies that SetDemoTags does
// not clobber existing keys unrelated to demo.
func TestSetDemoTags_ExistingMapPreservesOtherKeys(t *testing.T) {
	c := &Cluster{
		Tags: map[string]string{
			"env":  "staging",
			"team": "infra",
		},
	}
	expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetDemoTags(expiry)

	if c.Tags["env"] != "staging" {
		t.Errorf("Tags[\"env\"] = %q, want \"staging\" (should be preserved)", c.Tags["env"])
	}
	if c.Tags["team"] != "infra" {
		t.Errorf("Tags[\"team\"] = %q, want \"infra\" (should be preserved)", c.Tags["team"])
	}
	if c.Tags[DemoTagKey] != "true" {
		t.Errorf("Tags[%q] = %q, want \"true\"", DemoTagKey, c.Tags[DemoTagKey])
	}
}

// TestSetDemoTags_ExpiryIsRFC3339UTC verifies that the expiry tag value equals
// the passed time formatted as RFC3339 UTC regardless of the input timezone.
func TestSetDemoTags_ExpiryIsRFC3339UTC(t *testing.T) {
	// Use a fixed time in a non-UTC location to verify UTC normalisation.
	loc := time.FixedZone("AEST", 10*60*60)
	localTime := time.Date(2026, 6, 1, 22, 0, 0, 0, loc) // 22:00 AEST = 12:00 UTC
	c := &Cluster{}
	c.SetDemoTags(localTime)

	wantExpiry := localTime.UTC().Format(time.RFC3339) // "2026-06-01T12:00:00Z"
	if got := c.Tags[DemoExpiryTagKey]; got != wantExpiry {
		t.Errorf("Tags[%q] = %q, want %q (RFC3339 UTC)", DemoExpiryTagKey, got, wantExpiry)
	}
}

// TestSetDemoTags_Idempotent verifies that calling SetDemoTags twice with the
// same expiry produces the same result (no duplicates, no panic).
func TestSetDemoTags_Idempotent(t *testing.T) {
	c := &Cluster{}
	expiry := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetDemoTags(expiry)
	c.SetDemoTags(expiry) // second call

	if len(c.Tags) != 2 {
		t.Errorf("Tags len = %d after two SetDemoTags calls, want 2", len(c.Tags))
	}
	if c.Tags[DemoTagKey] != "true" {
		t.Errorf("Tags[%q] = %q, want \"true\"", DemoTagKey, c.Tags[DemoTagKey])
	}
}

// TestLoad_DemoBlock_DisabledNotValidated verifies that a disabled demo block
// (enabled: false) with a missing jumphost does NOT fail — disabled = no rules applied.
func TestLoad_DemoBlock_DisabledNotValidated(t *testing.T) {
	dir := t.TempDir()
	yaml := minimalYAML + `
demo:
  enabled: false
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err != nil {
		t.Fatalf("disabled demo block should not fail validation: %v", err)
	}
}

// ─── BigIPVESpec tests (F2, Slice A) ─────────────────────────────────────────

// bigipVEFullYAML is a valid dual-interface cluster with jumphost + demo enabled
// — the minimum required for bigipVE: enabled: true.
const bigipVEFullYAML = hostDeviceWithJumphostYAML + `
demo:
  enabled: true
bigipVE:
  enabled: true
`

// TestBigIPVEEnabled_Helper verifies BigIPVEEnabled() returns the correct bool.
func TestBigIPVEEnabled_Helper(t *testing.T) {
	cases := []struct {
		desc string
		c    *Cluster
		want bool
	}{
		{"nil BigIPVE", &Cluster{}, false},
		{"BigIPVE.Enabled=false", &Cluster{BigIPVE: &BigIPVESpec{Enabled: false}}, false},
		{"BigIPVE.Enabled=true", &Cluster{BigIPVE: &BigIPVESpec{Enabled: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			if got := tc.c.BigIPVEEnabled(); got != tc.want {
				t.Errorf("BigIPVEEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApplyDefaults_BigIPVE_FillsDefaults verifies that applyDefaults populates
// all zero fields when BigIPVE is present and Enabled.
func TestApplyDefaults_BigIPVE_FillsDefaults(t *testing.T) {
	c := &Cluster{
		BigIPVE: &BigIPVESpec{Enabled: true},
	}
	applyDefaults(c)
	ve := c.BigIPVE
	if ve.InstanceType != "c5n.2xlarge" {
		t.Errorf("InstanceType = %q, want c5n.2xlarge", ve.InstanceType)
	}
	if ve.MgmtSubnetIndex != 0 {
		t.Errorf("MgmtSubnetIndex = %d, want 0", ve.MgmtSubnetIndex)
	}
	if ve.VIP != "10.0.10.120" {
		t.Errorf("VIP = %q, want 10.0.10.120", ve.VIP)
	}
	if ve.LicenseTier != "Good" {
		t.Errorf("LicenseTier = %q, want Good", ve.LicenseTier)
	}
	if ve.Version != "" {
		t.Errorf("Version = %q, want \"\" (newest AMI default)", ve.Version)
	}
}

// TestApplyDefaults_BigIPVE_PreservesExplicitValues verifies that applyDefaults
// does not overwrite fields that the operator set explicitly.
func TestApplyDefaults_BigIPVE_PreservesExplicitValues(t *testing.T) {
	c := &Cluster{
		BigIPVE: &BigIPVESpec{
			Enabled:      true,
			InstanceType: "c5n.4xlarge",
			VIP:          "10.0.10.130",
			LicenseTier:  "Best",
			Version:      "17.5.1*",
		},
	}
	applyDefaults(c)
	ve := c.BigIPVE
	if ve.InstanceType != "c5n.4xlarge" {
		t.Errorf("InstanceType = %q, want c5n.4xlarge", ve.InstanceType)
	}
	if ve.VIP != "10.0.10.130" {
		t.Errorf("VIP = %q, want 10.0.10.130", ve.VIP)
	}
	if ve.LicenseTier != "Best" {
		t.Errorf("LicenseTier = %q, want Best", ve.LicenseTier)
	}
	if ve.Version != "17.5.1*" {
		t.Errorf("Version = %q, want 17.5.1*", ve.Version)
	}
}

// TestApplyDefaults_BigIPVE_DisabledNoDefaults verifies that applyDefaults does
// NOT fill defaults when BigIPVE is present but disabled.
func TestApplyDefaults_BigIPVE_DisabledNoDefaults(t *testing.T) {
	c := &Cluster{BigIPVE: &BigIPVESpec{Enabled: false}}
	applyDefaults(c)
	if c.BigIPVE.InstanceType != "" {
		t.Errorf("InstanceType = %q on disabled bigipVE, want \"\"", c.BigIPVE.InstanceType)
	}
}

// TestValidateBigIPVE_HappyPath exercises the full valid-block path via Load.
func TestValidateBigIPVE_HappyPath(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", bigipVEFullYAML)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("Load with valid bigipVE block: %v", err)
	}
	if c.BigIPVE == nil || !c.BigIPVE.Enabled {
		t.Fatal("BigIPVE: nil or disabled, want enabled")
	}
	// Check defaults applied.
	if c.BigIPVE.InstanceType != "c5n.2xlarge" {
		t.Errorf("InstanceType = %q, want c5n.2xlarge", c.BigIPVE.InstanceType)
	}
	if c.BigIPVE.VIP != "10.0.10.120" {
		t.Errorf("VIP = %q, want 10.0.10.120", c.BigIPVE.VIP)
	}
	if c.BigIPVE.LicenseTier != "Good" {
		t.Errorf("LicenseTier = %q, want Good", c.BigIPVE.LicenseTier)
	}
}

// TestValidateBigIPVE_RejectsExternalOnlyPattern verifies that bigipVE requires
// pattern: dual-interface and rejects external-only.
func TestValidateBigIPVE_RejectsExternalOnlyPattern(t *testing.T) {
	// Build a minimal external-only cluster with demo+jumphost then add bigipVE.
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
pattern: external-only
cluster:
  nodeGroups:
    - name: ng
testing:
  jumphost:
    enabled: true
demo:
  enabled: true
bigipVE:
  enabled: true
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bigipVE with pattern external-only, got nil")
	}
	if !strings.Contains(err.Error(), "dual-interface") {
		t.Errorf("error should mention 'dual-interface': %v", err)
	}
}

// TestValidateBigIPVE_RejectsJumphostDisabled verifies that bigipVE requires
// testing.jumphost.enabled: true.
func TestValidateBigIPVE_RejectsJumphostDisabled(t *testing.T) {
	// hostDeviceMinimalYAML has no testing block.
	yaml := hostDeviceMinimalYAML + `
demo:
  enabled: true
bigipVE:
  enabled: true
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bigipVE without jumphost, got nil")
	}
	if !strings.Contains(err.Error(), "testing.jumphost.enabled") {
		t.Errorf("error should mention 'testing.jumphost.enabled': %v", err)
	}
}

// TestValidateBigIPVE_RejectsDemoDisabled verifies that bigipVE requires demo.enabled: true.
func TestValidateBigIPVE_RejectsDemoDisabled(t *testing.T) {
	yaml := hostDeviceWithJumphostYAML + `
bigipVE:
  enabled: true
`
	dir := t.TempDir()
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bigipVE without demo.enabled, got nil")
	}
	if !strings.Contains(err.Error(), "demo.enabled") {
		t.Errorf("error should mention 'demo.enabled': %v", err)
	}
}

// TestValidateBigIPVE_RejectsBadInstanceType verifies that a malformed
// instanceType is rejected.
func TestValidateBigIPVE_RejectsBadInstanceType(t *testing.T) {
	dir := t.TempDir()
	yaml := bigipVEFullYAML + `  instanceType: "C5n.2xlarge"
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bad instanceType, got nil")
	}
	if !strings.Contains(err.Error(), "instanceType") {
		t.Errorf("error should mention 'instanceType': %v", err)
	}
}

// TestValidateBigIPVE_RejectsBadLicenseTier verifies that an invalid
// licenseTier is rejected.
func TestValidateBigIPVE_RejectsBadLicenseTier(t *testing.T) {
	dir := t.TempDir()
	yaml := bigipVEFullYAML + `  licenseTier: Premium
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for bad licenseTier, got nil")
	}
	if !strings.Contains(err.Error(), "licenseTier") {
		t.Errorf("error should mention 'licenseTier': %v", err)
	}
}

// TestValidateBigIPVE_RejectsVIPOutsideCIDR verifies that a VIP outside the
// external CIDR is rejected.
func TestValidateBigIPVE_RejectsVIPOutsideCIDR(t *testing.T) {
	dir := t.TempDir()
	yaml := bigipVEFullYAML + `  vip: 192.168.1.120
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err == nil {
		t.Fatal("expected error for VIP outside external CIDR, got nil")
	}
	if !strings.Contains(err.Error(), "not inside") {
		t.Errorf("error should mention 'not inside': %v", err)
	}
}

// TestValidateBigIPVE_RejectsVIPInReservedSet verifies that a VIP matching a
// reserved address (e.g. .100 BNK Gateway VIP) is rejected with a clear error.
func TestValidateBigIPVE_RejectsVIPInReservedSet(t *testing.T) {
	reservedVIPs := []struct {
		vip  string
		desc string
	}{
		{"10.0.10.100", "BNK default gateway VIP"},
		{"10.0.10.110", "Diameter demo VIP"},
		{"10.0.10.111", "HTTP2 demo VIP"},
		{"10.0.10.112", "gRPC demo VIP"},
		{"10.0.10.113", "additional BNK VIP"},
		{"10.0.10.200", "jumphost ENI secondary IP"},
		{"10.0.10.240", "TMM SelfIP"},
	}
	for _, tc := range reservedVIPs {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			dir := t.TempDir()
			yaml := bigipVEFullYAML + "  vip: " + tc.vip + "\n"
			p := writeFile(t, dir, "cluster.yaml", yaml)
			_, err := Load(p)
			if err == nil {
				t.Fatalf("expected error for reserved VIP %s (%s), got nil", tc.vip, tc.desc)
			}
			if !strings.Contains(err.Error(), "collides") {
				t.Errorf("error should mention 'collides': %v", err)
			}
		})
	}
}

// TestValidateBigIPVE_LicenseTierVariants verifies all three valid licenseTier values.
func TestValidateBigIPVE_LicenseTierVariants(t *testing.T) {
	for _, tier := range []string{"Good", "Better", "Best"} {
		tier := tier
		t.Run(tier, func(t *testing.T) {
			dir := t.TempDir()
			yaml := bigipVEFullYAML + "  licenseTier: " + tier + "\n"
			p := writeFile(t, dir, "cluster.yaml", yaml)
			_, err := Load(p)
			if err != nil {
				t.Errorf("licenseTier %q should be valid, got: %v", tier, err)
			}
		})
	}
}

// TestLoad_BigIPVE_DisabledNotValidated verifies that a disabled bigipVE block
// (enabled: false) with missing prerequisites does NOT fail validation.
func TestLoad_BigIPVE_DisabledNotValidated(t *testing.T) {
	dir := t.TempDir()
	// minimalYAML has no pattern/jumphost/demo — all bigipVE prerequisites absent.
	yaml := minimalYAML + `
bigipVE:
  enabled: false
`
	p := writeFile(t, dir, "cluster.yaml", yaml)
	_, err := Load(p)
	if err != nil {
		t.Fatalf("disabled bigipVE block should not fail validation: %v", err)
	}
}
