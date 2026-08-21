package config

import (
	"strings"
	"testing"
)

// applied builds an applied-tfvars map the way the snapshot parser hands one
// over: values keep their quotes.
func applied(flo, utils string) map[string]string {
	m := map[string]string{}
	if flo != "" {
		m["flo_namespace"] = `"` + flo + `"`
	}
	if utils != "" {
		m["flo_utils_namespace"] = `"` + utils + `"`
	}
	return m
}

func wsWithNamespaces(flo, utils string) *Workspace {
	ws := &Workspace{}
	ws.BNK.FLONamespace = flo
	ws.BNK.FLOUtilsNamespace = utils
	return ws
}

func TestBNKNamespacesResolvesEmptyToTerraformDefaults(t *testing.T) {
	flo, utils := wsWithNamespaces("", "").BNKNamespaces()
	if flo != DefaultFLONamespace || utils != DefaultFLOUtilsNamespace {
		t.Fatalf("empty config should resolve to the terraform defaults, got %s/%s", flo, utils)
	}
	flo, utils = wsWithNamespaces("a", "b").BNKNamespaces()
	if flo != "a" || utils != "b" {
		t.Fatalf("explicit values should win, got %s/%s", flo, utils)
	}
	// A nil workspace still answers, so callers need no nil dance.
	if flo, utils = (*Workspace)(nil).BNKNamespaces(); flo != DefaultFLONamespace || utils != DefaultFLOUtilsNamespace {
		t.Fatalf("nil workspace should resolve to defaults, got %s/%s", flo, utils)
	}
}

// The case this guard exists for: a two-namespace install being collapsed into
// one, which deletes the utils namespace and everything in it.
func TestCollapsingAnExistingTwoNamespaceInstallIsRefused(t *testing.T) {
	err := CheckNamespaceTopology(
		wsWithNamespaces("f5-bnk", "f5-bnk"),
		applied("f5-bnk", "f5-utils"),
	)
	if err == nil {
		t.Fatal("collapsing an installed two-namespace deployment must be refused")
	}
	// The message has to name what would be destroyed — a refusal that does not
	// say "this deletes f5-utils" gets worked around with --force-equivalents.
	for _, want := range []string{"f5-utils", "DELETE", "bnk-license", "create-time"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q; got:\n%s", want, err)
		}
	}
}

// The collapse is legitimate on a workspace that has not installed yet — that is
// the supported way to get one namespace, so it must not be refused.
func TestCollapseIsAllowedOnAFreshWorkspace(t *testing.T) {
	for name, prior := range map[string]map[string]string{
		"no snapshot":    nil,
		"empty snapshot": {},
		"snapshot without namespaces": {
			"cluster_name": `"c"`,
		},
	} {
		if err := CheckNamespaceTopology(wsWithNamespaces("f5-bnk", "f5-bnk"), prior); err != nil {
			t.Errorf("%s: a first install may choose one namespace: %v", name, err)
		}
	}
}

// Already installed into one namespace and asking for the same thing again is
// the steady state for a single-namespace customer. It must converge, not refuse.
func TestReapplyingTheSameSingleNamespaceIsAllowed(t *testing.T) {
	if err := CheckNamespaceTopology(
		wsWithNamespaces("f5-bnk", "f5-bnk"),
		applied("f5-bnk", "f5-bnk"),
	); err != nil {
		t.Fatalf("re-applying an unchanged single-namespace install must converge: %v", err)
	}
}

// An unset config field means "the terraform default", so a workspace that never
// wrote the namespaces must not look like a change from one that did.
func TestUnsetConfigMatchesADefaultInstall(t *testing.T) {
	if err := CheckNamespaceTopology(
		wsWithNamespaces("", ""),
		applied(DefaultFLONamespace, DefaultFLOUtilsNamespace),
	); err != nil {
		t.Fatalf("unset config should match a default install: %v", err)
	}
}

func TestNamespaceChangesAreRefused(t *testing.T) {
	cases := []struct {
		name              string
		priorFLO, priorUt string
		wantFLO, wantUt   string
	}{
		{"expanding one namespace back into two", "f5-bnk", "f5-bnk", "f5-bnk", "f5-utils"},
		{"renaming the utils namespace", "f5-bnk", "f5-utils", "f5-bnk", "f5-other"},
		{"renaming the flo namespace", "f5-bnk", "f5-utils", "f5-other", "f5-utils"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckNamespaceTopology(
				wsWithNamespaces(tc.wantFLO, tc.wantUt),
				applied(tc.priorFLO, tc.priorUt),
			)
			if err == nil {
				t.Fatal("changing an installed namespace must be refused")
			}
			if !strings.Contains(err.Error(), "CREATE-time") {
				t.Errorf("refusal should say why it is fixed; got:\n%s", err)
			}
		})
	}
}

// A snapshot that recorded only one of the pair still pins that one, and the
// other resolves the way terraform would have.
func TestPartialSnapshotResolvesTheMissingHalfToItsDefault(t *testing.T) {
	// Recorded flo only; utils was therefore the default. Asking for the default
	// pair is not a change.
	if err := CheckNamespaceTopology(
		wsWithNamespaces("f5-bnk", "f5-utils"),
		map[string]string{"flo_namespace": `"f5-bnk"`},
	); err != nil {
		t.Fatalf("missing half should resolve to its default: %v", err)
	}
	// Same snapshot, but now collapsing — still a collapse, still refused.
	if err := CheckNamespaceTopology(
		wsWithNamespaces("f5-bnk", "f5-bnk"),
		map[string]string{"flo_namespace": `"f5-bnk"`},
	); err == nil {
		t.Fatal("collapse against a partial snapshot must still be refused")
	}
}

func TestTFVarStringUnquotes(t *testing.T) {
	for in, want := range map[string]string{
		`"f5-bnk"`:   "f5-bnk",
		` "f5-bnk" `: "f5-bnk",
		`f5-bnk`:     "f5-bnk",
		`""`:         "",
		``:           "",
	} {
		if got := tfvarString(in); got != want {
			t.Errorf("tfvarString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNilWorkspaceIsSilent(t *testing.T) {
	if err := CheckNamespaceTopology(nil, applied("f5-bnk", "f5-utils")); err != nil {
		t.Fatalf("nil workspace must be silent: %v", err)
	}
}
