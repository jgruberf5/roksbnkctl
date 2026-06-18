package cli

import (
	"bytes"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestInitExample_PrintsConfigYAML pins that `init example` writes the embedded
// config.yaml template (non-empty, recognisably config.yaml).
func TestInitExample_PrintsConfigYAML(t *testing.T) {
	var out bytes.Buffer
	initExampleCmd.SetOut(&out)
	t.Cleanup(func() { initExampleCmd.SetOut(nil) })

	if err := runInitExample(initExampleCmd, nil); err != nil {
		t.Fatalf("runInitExample: %v", err)
	}
	got := out.String()
	if len(got) < 200 {
		t.Fatalf("init example output too short (%d bytes) — embedded example not found?", len(got))
	}
	for _, want := range []string{"ibmcloud:", "tf_source:", "prefix:"} {
		if !bytes.Contains([]byte(got), []byte(want)) {
			t.Errorf("output does not look like config.yaml (missing %q):\n%.160s", want, got)
		}
	}
}

// TestInitExampleParsesStrictly pins that the embedded config.example.yaml is a
// VALID config.yaml: it strict-decodes into a Workspace with unknown fields
// rejected — exactly how `init --config-file` parses a seed — so the template
// can never drift out of the schema, and its required fields are populated.
func TestInitExampleParsesStrictly(t *testing.T) {
	if len(exampleConfigYAML) == 0 {
		t.Fatal("exampleConfigYAML embed is empty")
	}

	var ws config.Workspace
	dec := yaml.NewDecoder(bytes.NewReader(exampleConfigYAML))
	dec.KnownFields(true) // same strictness as init --config-file
	if err := dec.Decode(&ws); err != nil {
		t.Fatalf("config.example.yaml does not strict-parse into Workspace: %v", err)
	}

	if ws.IBMCloud.Region == "" || ws.IBMCloud.ResourceGroup == "" {
		t.Errorf("example missing ibmcloud region/resource_group: %+v", ws.IBMCloud)
	}
	if ws.Prefix == "" {
		t.Error("example missing prefix")
	}
	if ws.TFSource.Type == "" {
		t.Error("example missing tf_source.type")
	}
	if ws.Resources == nil || !ws.Resources.ClusterVPC.Create {
		t.Errorf("example should surface resources.cluster_vpc: %+v", ws.Resources)
	}
	if ws.BNK.ManifestVersion == "" {
		t.Error("example should surface bnk.manifest_version")
	}
}
