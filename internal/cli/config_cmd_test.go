package cli

import (
	"bytes"
	"encoding/base64"

	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Drives the run functions directly. Calling Execute() on a SUBcommand does not
// do what it looks like: cobra walks up to the root and dispatches from there,
// so the assertions would run against the root's help output instead.
func runConfigCmd(t *testing.T, args ...string) string {
	t.Helper()
	// The --from flags are package-level, so a previous case would otherwise leak
	// into this one and quietly change what is being asserted.
	flagConfigFromYAML, flagConfigFromEnv = "", ""
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--from-yaml":
			flagConfigFromYAML = args[i+1]
		case "--from-env":
			flagConfigFromEnv = args[i+1]
		}
	}
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	var err error
	switch args[0] {
	case "yaml":
		err = runConfigYAML(cmd, nil)
	case "env":
		err = runConfigEnv(cmd, nil)
	default:
		t.Fatalf("unknown form %q", args[0])
	}
	if err != nil {
		t.Fatalf("config %v: %v", args, err)
	}
	return out.String()
}

// The templates exist to be piped to a file, so the thing worth asserting is
// that they ARE a file's worth of content and carry their comments.
func TestConfigPrintsAnnotatedTemplates(t *testing.T) {
	y := runConfigCmd(t, "yaml")
	if !strings.Contains(y, "ibmcloud:") || !strings.Contains(y, "#") {
		t.Errorf("config yaml is not an annotated config.yaml:\n%.200s", y)
	}
	e := runConfigCmd(t, "env")
	for _, want := range []string{"ROKSBNKCTL_", "# ---- bnk ----", "config env --from-yaml"} {
		if !strings.Contains(e, want) {
			t.Errorf("config env template missing %q", want)
		}
	}
	// Every line that assigns must be commented out. An uncommented empty
	// assignment is not "leave this alone" -- it sets the variable to empty, and
	// sourcing the template would then override real settings with nothing.
	for _, line := range strings.Split(e, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "ROKSBNKCTL_") {
			t.Errorf("template line is not commented out, so sourcing it would set an "+
				"empty value: %q", l)
		}
	}
}

// The conversion is the half most likely to be subtly wrong, so it is checked by
// round-tripping real settings rather than by inspecting either format alone.
func TestConfigConvertsBothDirections(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(in, []byte(`ibmcloud:
  region: us-east
  resource_group: default
prefix: demo
cluster:
  create: true
  name: demo
  workers_per_zone: 2
  worker_flavor: bx2.8x32
bnk:
  manifest_version: 2.4.0-EA
  tmm_replicas: 3
`), 0o600); err != nil {
		t.Fatal(err)
	}

	env := runConfigCmd(t, "env", "--from-yaml", in)
	for _, want := range []string{
		"ROKSBNKCTL_REGION=us-east",
		"ROKSBNKCTL_WORKER_FLAVOR=bx2.8x32",
		"ROKSBNKCTL_WORKERS_PER_ZONE=2",
		"ROKSBNKCTL_MANIFEST_VERSION=2.4.0-EA",
		"ROKSBNKCTL_TMM_REPLICAS=3",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("yaml->env lost %q\n--- got ---\n%s", want, env)
		}
	}
	// A setting the input never mentioned must not appear with an empty value.
	if strings.Contains(env, "ROKSBNKCTL_CNEINSTANCE_SIZE=") {
		t.Error("yaml->env emitted a setting the input did not set")
	}

	envFile := filepath.Join(dir, "out.env")
	if err := os.WriteFile(envFile, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	back := runConfigCmd(t, "yaml", "--from-env", envFile)
	for _, want := range []string{
		"region: us-east", "worker_flavor: bx2.8x32",
		"workers_per_zone: 2", "manifest_version: 2.4.0-EA", "tmm_replicas: 3",
	} {
		if !strings.Contains(back, want) {
			t.Errorf("env->yaml lost %q\n--- got ---\n%s", want, back)
		}
	}
}

// A .env written by hand carries comments, `export`, quotes and trailing
// comments. Dropping any of those into a value is how a setting ends up as
// `bx2.8x32 # the flavour`.
func TestConfigFromEnvToleratesRealDotEnvSyntax(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	if err := os.WriteFile(f, []byte(`# a comment
export ROKSBNKCTL_WORKER_FLAVOR="bx2.8x32"   # the flavour

ROKSBNKCTL_TMM_REPLICAS='3'
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "yaml", "--from-env", f)
	if !strings.Contains(got, "worker_flavor: bx2.8x32") {
		t.Errorf("quotes/export/trailing comment leaked into the value:\n%s", got)
	}
	if !strings.Contains(got, "tmm_replicas: 3") {
		t.Errorf("single-quoted int not parsed:\n%s", got)
	}
}

func TestConfigRejectsBothFromFlags(t *testing.T) {
	flagConfigFromYAML, flagConfigFromEnv = "a", "b"
	defer func() { flagConfigFromYAML, flagConfigFromEnv = "", "" }()
	if _, _, err := loadConfigSource(); err == nil {
		t.Error("passing both --from-yaml and --from-env was accepted; they are alternatives")
	}
}

// The output is piped to a file, so a secret in it is a credential written to
// disk by a command whose own template says to keep them out of the file.
func TestConfigEnvNeverPrintsASecretValue(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "secret.yaml")
	// COMPUTED, not written as a literal. A base64 string beside a name like
	// apiKey trips the secret scanner's generic-api-key rule, and the fix for
	// that is to stop the fixture looking like a credential -- not to teach the
	// repo to ignore api-key patterns, which is the one rule least worth
	// weakening.
	apiKey := base64.StdEncoding.EncodeToString([]byte("fixture-not-a-credential"))
	bigipPw := base64.StdEncoding.EncodeToString([]byte("fixture-not-a-password"))
	if err := os.WriteFile(in, []byte(`ibmcloud:
  region: us-east
  resource_group: default
  api_key_b64: `+apiKey+`
prefix: demo
bnk:
  cis:
    bigip_password_b64: `+bigipPw+`
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := runConfigCmd(t, "env", "--from-yaml", in)
	for _, secret := range []string{apiKey, bigipPw} {
		if strings.Contains(got, secret) {
			t.Errorf("a secret VALUE reached stdout; piping this writes a credential to disk:\n%s", got)
		}
	}
	// The variable must still be NAMED, or the reader never learns it exists.
	for _, name := range []string{"IBMCLOUD_API_KEY", "ROKSBNKCTL_BIGIP_PASSWORD"} {
		if !strings.Contains(got, name) {
			t.Errorf("%s is not mentioned at all; the reader cannot know to set it", name)
		}
	}
}

// Several config fields are not omitempty and so marshal at their zero value
// whether or not the input mentioned them. Emitting those turns a conversion
// into an assertion the user never made.
func TestConfigEnvOmitsSettingsTheInputNeverSet(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "min.yaml")
	if err := os.WriteFile(in, []byte("ibmcloud: {region: us-east, resource_group: default}\nprefix: demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "env", "--from-yaml", in)
	if strings.Contains(got, "ROKSBNKCTL_CLUSTER_CREATE") {
		t.Errorf("emitted cluster.create though the input has no cluster block:\n%s", got)
	}
}

// `ROKSBNKCTL_X=a b` assigns `a` and then tries to run `b`.
func TestConfigEnvQuotesValuesTheShellWouldResplit(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "spaces.yaml")
	if err := os.WriteFile(in, []byte(`ibmcloud: {region: us-east, resource_group: default}
prefix: demo
bnk: {cluster_identifier: my cluster with spaces}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "env", "--from-yaml", in)
	if !strings.Contains(got, `ROKSBNKCTL_CLUSTER_IDENTIFIER='my cluster with spaces'`) {
		t.Errorf("value with spaces was not quoted:\n%s", got)
	}
}

// A list arrives from the probe as `path[0]`. Emitting the first element alone,
// or dropping the setting, both lose configuration silently.
func TestConfigEnvKeepsWholeLists(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "list.yaml")
	if err := os.WriteFile(in, []byte(`ibmcloud: {region: us-east, resource_group: default}
prefix: demo
bnk:
  flp:
    vsi:
      management_allowed_cidrs: ["10.0.0.0/8", "192.168.0.0/16"]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "env", "--from-yaml", in)
	if !strings.Contains(got, "10.0.0.0/8,192.168.0.0/16") {
		t.Errorf("list was truncated or dropped:\n%s", got)
	}
}

// Setting a nested field materialises its enclosing block, so a naive probe
// reports two paths for one variable and the caller cannot tell which to use.
func TestConfigEnvKeepsNestedBlockSettings(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "hp.yaml")
	if err := os.WriteFile(in, []byte(`ibmcloud: {region: us-east, resource_group: default}
prefix: demo
bnk:
  hugepages: {enabled: true, count: 2048}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "env", "--from-yaml", in)
	for _, want := range []string{"ROKSBNKCTL_HUGEPAGES=true", "ROKSBNKCTL_HUGEPAGES_COUNT=2048"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q — a side-effect path shadowed the real one:\n%s", want, got)
		}
	}
}

// Multi-zone is the ONLY shape roksbnkctl deploys, so losing the zone family
// from a conversion loses the network configuration entirely.
//
// The subtle half is the INDEX. Each zone's variables must carry that zone's
// values: an earlier probe filled one zone at a time, which made it the only
// entry in the list, so ZONE2 and ZONE3 both resolved to zones[0] and a
// three-zone config emitted zone 1's addresses three times. That is wrong in a
// way the count of mapped variables cannot show.
func TestConfigEnvKeepsEveryZoneWithItsOwnValues(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "zones.yaml")
	if err := os.WriteFile(in, []byte(`ibmcloud: {region: us-east, resource_group: default}
prefix: demo
bnk:
  network:
    zones:
      - {ext_vlan_cidr: 10.11.1.0/24, int_vlan_cidr: 10.21.1.0/24, int_snat_cidr: 10.31.1.0/24, int_vip_cidr: 10.41.1.0/24, external_selfip: 10.11.1.101, internal_selfip: 10.21.1.101}
      - {ext_vlan_cidr: 10.12.2.0/24, int_vlan_cidr: 10.22.2.0/24, int_snat_cidr: 10.32.2.0/24, int_vip_cidr: 10.42.2.0/24, external_selfip: 10.12.2.101, internal_selfip: 10.22.2.101}
      - {ext_vlan_cidr: 10.13.3.0/24, int_vlan_cidr: 10.23.3.0/24, int_snat_cidr: 10.33.3.0/24, int_vip_cidr: 10.43.3.0/24, external_selfip: 10.13.3.101, internal_selfip: 10.23.3.101}
`), 0o600); err != nil {
		t.Fatal(err)
	}

	got := runConfigCmd(t, "env", "--from-yaml", in)

	// Every zone, every field — 18 in total.
	for _, want := range []string{
		"ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR=10.11.1.0/24",
		"ROKSBNKCTL_ZONE1_INTERNAL_SELFIP=10.21.1.101",
		"ROKSBNKCTL_ZONE2_EXT_VLAN_CIDR=10.12.2.0/24",
		"ROKSBNKCTL_ZONE2_INT_SNAT_CIDR=10.32.2.0/24",
		"ROKSBNKCTL_ZONE3_EXT_VLAN_CIDR=10.13.3.0/24",
		"ROKSBNKCTL_ZONE3_INT_VIP_CIDR=10.43.3.0/24",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n--- got ---\n%s", want, got)
		}
	}
	if n := strings.Count(got, "ROKSBNKCTL_ZONE"); n != 18 {
		t.Errorf("emitted %d zone variables; want 18 (3 zones x 6 fields)", n)
	}
	// The index-collapse bug: zone 1's address appearing under another zone.
	if strings.Contains(got, "ROKSBNKCTL_ZONE2_EXT_VLAN_CIDR=10.11.1.0/24") ||
		strings.Contains(got, "ROKSBNKCTL_ZONE3_EXT_VLAN_CIDR=10.11.1.0/24") {
		t.Error("a later zone carries zone 1's value — the list index was collapsed")
	}
}

// The Forge CA is a CERTIFICATE, which is public data. The config field says so:
// "unlike the `_b64` credential fields — this is encoded only for single-line
// YAML safety". Suppressing it as though it were a credential drops a working
// setting to protect nothing.
func TestConfigEnvEmitsTheForgeCAWhichIsPublic(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "forge.yaml")
	ca := base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----\n"))
	if err := os.WriteFile(in, []byte(`ibmcloud: {region: us-east, resource_group: default}
prefix: demo
bnkforge:
  url: https://forge.example.com
  ca_b64: `+ca+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := runConfigCmd(t, "env", "--from-yaml", in)
	if !strings.Contains(got, "ROKSBNKCTL_BNKFORGE_CA_B64="+ca) {
		t.Errorf("the Forge CA was withheld as if it were a secret; it is a public "+
			"certificate:\n%s", got)
	}
}
