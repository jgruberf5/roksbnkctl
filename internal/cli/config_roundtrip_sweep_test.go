package cli

import (
	"sort"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"gopkg.in/yaml.v3"
)

// A SYSTEMATIC round trip over every override, rather than the handful anyone
// thought to check by hand.
//
// `roksbnkctl config env --from-yaml` and `config yaml --from-env` are advertised
// as two spellings of the same settings, and env.example's own header tells
// operators to convert a config.yaml and feed the result to
// `init --non-interactive --override-from-env`. That is only true if every
// setting survives the trip. #246 was one that did not, and it was found by
// accident -- converting a real workspace and noticing a line was missing.
//
// This drives each override through env -> workspace -> env and reports every one
// whose value does not come back. Starting from the ENV side rather than a
// hand-built Workspace is deliberate: the env form is the one with a finite,
// enumerable surface, so "every override" is a claim this test can actually make.
func TestEveryOverrideSurvivesAnEnvRoundTrip(t *testing.T) {
	paths := config.OverridePaths()

	// A value of the right shape for the field, so the parser accepts it.
	value := func(name, path string) string {
		switch {
		case strings.HasSuffix(name, "_B64"), strings.Contains(name, "PASSWORD"),
			strings.Contains(name, "API_KEY"), strings.Contains(name, "TOKEN"),
			strings.Contains(name, "SECRET"), strings.Contains(name, "JWT"):
			return "" // secrets are deliberately not carried; excluded below
		case strings.Contains(path, "cidr"), strings.Contains(name, "CIDRS"):
			return "10.99.0.0/16"
		case strings.Contains(name, "REPLICAS"), strings.Contains(name, "COUNT"),
			strings.Contains(name, "WORKERS"), strings.Contains(name, "PORT"):
			return "7"
		default:
			return "roundtrip-probe"
		}
	}

	var names []string
	for n, p := range paths {
		if p == "" || strings.Contains(p, ",") {
			continue // no single field to compare; covered by the #246 guards
		}
		if isSecretPath(p) {
			continue // deliberately not written to .env
		}
		names = append(names, n)
	}
	sort.Strings(names)

	var lost []string
	for _, name := range names {
		path := paths[name]
		in := value(name, path)
		if in == "" {
			continue
		}

		// env -> workspace, via the same machinery a real run uses.
		var ws config.Workspace
		applied := config.OverrideFromMap(&ws, map[string]string{name: in})
		if len(applied) == 0 {
			continue // the probe shape was rejected; not a round-trip failure
		}

		// workspace -> env.
		lines, err := envLinesFor(&ws)
		if err != nil {
			t.Fatalf("%s: envLinesFor: %v", name, err)
		}
		if !containsAssignment(lines, name) {
			lost = append(lost, name+" -> "+path)
		}
	}

	if len(lost) > 0 {
		t.Errorf("%d override(s) do not survive env -> config -> env.\n"+
			"Each was applied to a workspace, took effect, and then was absent from the .env "+
			"rendering of that same workspace — so converting a config.yaml silently drops the "+
			"setting, and a round-trip returns the field to its default:\n  %s",
			len(lost), strings.Join(lost, "\n  "))
	}
}

func containsAssignment(lines []string, name string) bool {
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), name+"=") {
			return true
		}
	}
	return false
}

// The other direction, and the one an operator actually runs: a populated
// config.yaml converted to .env and back must still be the same workspace.
//
// tf_source.type is the interesting case. It is a REQUIRED field with no
// environment override at all, so the .env form cannot carry it and the trip
// necessarily loses it. That is worth failing on rather than tolerating: a
// workspace pinned to a specific terraform source silently becomes an
// embedded-source workspace.
func TestAPopulatedConfigSurvivesTheYAMLEnvYAMLTrip(t *testing.T) {
	src := []byte(`
prefix: rt
ibmcloud:
    region: us-east
    resource_group: default
tf_source:
    type: local
    path: /opt/roksbnkctl/terraform
cluster:
    create: false
    name: adopted-cluster
    openshift_version: "4.21"
resources:
    transit_gateway:
        create: false
        existing: bnkci-testing
`)
	var in config.Workspace
	if err := yaml.Unmarshal(src, &in); err != nil {
		t.Fatalf("parsing the source config: %v", err)
	}

	lines, err := envLinesFor(&in)
	if err != nil {
		t.Fatalf("envLinesFor: %v", err)
	}
	env := map[string]string{}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		env[k] = strings.Trim(v, `"'`)
	}

	var out config.Workspace
	config.OverrideFromMap(&out, env)

	for _, tc := range []struct{ what, got, want string }{
		{"prefix", out.Prefix, in.Prefix},
		{"ibmcloud.region", out.IBMCloud.Region, in.IBMCloud.Region},
		{"ibmcloud.resource_group", out.IBMCloud.ResourceGroup, in.IBMCloud.ResourceGroup},
		{"tf_source.type", string(out.TFSource.Type), string(in.TFSource.Type)},
		{"cluster.name", out.Cluster.Name, in.Cluster.Name},
		{"resources.transit_gateway.existing", out.Resources.TransitGateway.Existing, in.Resources.TransitGateway.Existing},
	} {
		if tc.got != tc.want {
			t.Errorf("%s did not survive yaml -> env -> yaml: got %q, want %q", tc.what, tc.got, tc.want)
		}
	}
	if out.Resources == nil || out.Resources.TransitGateway.Create {
		t.Error("resources.transit_gateway.create=false did not survive: the workspace would " +
			"PROVISION a transit gateway instead of adopting bnkci-testing")
	}
	if out.Cluster.Create {
		t.Error("cluster.create=false did not survive: the workspace would create a cluster " +
			"instead of adopting the existing one")
	}
}

// Every REQUIRED field must have an environment override.
//
// tf_source.type did not (#248), which made `config yaml --from-env`
// structurally unable to emit a complete config: the one field the loader
// insists on was the one the env form could not carry. A github-pinned workspace
// came back as type:"" and init then defaulted it to "embedded", silently
// swapping a tree pinned to a tag for the one compiled into the binary.
//
// This is the general form rather than a check for tf_source, because the same
// gap on any future required field has the same consequence.
func TestEveryRequiredFieldHasAnEnvironmentOverride(t *testing.T) {
	covered := map[string]bool{}
	for _, path := range config.OverridePaths() {
		for _, p := range strings.Split(path, ",") {
			if p != "" {
				covered[p] = true
			}
		}
	}

	for _, required := range config.RequiredConfigFields {
		if !covered[required] {
			t.Errorf("%s is a required config field with NO ROKSBNKCTL_* override.\n"+
				"`config yaml --from-env` therefore cannot produce a valid config, and a "+
				"config.yaml -> .env -> config.yaml round-trip silently drops the value.",
				required)
		}
	}
}
