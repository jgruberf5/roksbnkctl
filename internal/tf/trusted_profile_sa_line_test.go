package tf

import (
	"encoding/json"
	"testing"
)

// #313: the Trusted Profile link's service-account name is a MATCHER — IBM IAM
// compares crn/namespace/name with EQUALS — and the name the CNE controller
// actually runs as differs by BNK line:
//
//	2.3: f5-cne-controller-<ns>-f5-cne-controller-serviceaccount
//	2.4: f5-cne-controller
//
// Both were read off live installs. The 2.4 name was confirmed on a 2.4.0 GA
// cluster: spec.serviceAccountName on the f5-cne-controller pod is the short
// name, and no account carrying the long suffix exists in any namespace.
//
// A single hard-coded default made every 2.4 install link a profile to an
// account that does not exist, so the controller could not assume it and
// skipped VPC address-prefix and route programming entirely — with Infra,
// GatewaySettings and Gateway all still reporting Programmed=True, because
// those conditions never call the cloud.
//
// These assert on the EVALUATED local rather than on source text, for the
// reason recorded in line_gating_eval_test.go: a commented-out line is still
// text, and a text scan reads text.
const (
	sa23 = "f5-cne-controller-f5-bnk-f5-cne-controller-serviceaccount"
	sa24 = "f5-cne-controller"
)

func consoleString(t *testing.T, module []string, tfvars, expr string) string {
	t.Helper()
	encoded := consoleJSON(t, module, tfvars, expr)
	var s string
	if err := json.Unmarshal([]byte(encoded), &s); err != nil {
		t.Fatalf("decode %q as a string: %v", encoded, err)
	}
	if s == "" {
		t.Fatal("evaluated to an empty string; an empty SA name matches nothing and would pass some assertions vacuously")
	}
	return s
}

// The module paths are the same two the fix touches. Naming them here rather
// than reusing lineGatedModules keeps this test honest if that map changes for
// an unrelated reason.
var trustedProfileModules = map[string][]string{
	"flo":         {"flo", "modules", "flo"},
	"cneinstance": {"cne_instance", "modules", "cneinstance"},
}

func TestTrustedProfileSAFollowsTheLine(t *testing.T) {
	const expr = "local.trusted_profile_sa"

	cases := []struct {
		line string
		want string
		why  string
	}{
		{"2.3", sa23, "2.3 runs as FLO's helm-derived long name"},
		{"2.4", sa24, "2.4 runs as the short name; the long one exists nowhere on a 2.4 cluster"},
		// An unrecognised line must keep the 2.3 name, matching every other
		// gate in these modules. 2.3 is the behaviour that has shipped.
		{"9.9", sa23, "an unknown line keeps the shipped 2.3 name"},
	}

	for name, module := range trustedProfileModules {
		for _, tc := range cases {
			t.Run(name+"/"+tc.line, func(t *testing.T) {
				tfvars := "bnk_line = \"" + tc.line + "\"\nflo_namespace = \"f5-bnk\"\n"
				got := consoleString(t, module, tfvars, expr)
				if got != tc.want {
					t.Errorf("%s on line %s derived %q, want %q — %s",
						name, tc.line, got, tc.want, tc.why)
				}
			})
		}
	}
}

// The SCC ClusterRoleBinding in cneinstance and the Trusted Profile link in flo
// must name ONE account. If they drift, a pod may assume the profile but not
// use the SCC (or the reverse), and only one of the two failures is visible.
func TestTrustedProfileSAAgreesAcrossModules(t *testing.T) {
	const expr = "local.trusted_profile_sa"

	for _, line := range []string{"2.3", "2.4"} {
		t.Run(line, func(t *testing.T) {
			tfvars := "bnk_line = \"" + line + "\"\nflo_namespace = \"f5-bnk\"\n"
			flo := consoleString(t, trustedProfileModules["flo"], tfvars, expr)
			cne := consoleString(t, trustedProfileModules["cneinstance"], tfvars, expr)
			if flo != cne {
				t.Errorf("line %s: flo links %q but cneinstance binds the SCC to %q; "+
					"a profile the pod may assume and an SCC it may use have to name one account",
					line, flo, cne)
			}
		})
	}
}

// An explicit override must still win on both lines — the derivation is a
// default, not a policy. Without this, a fix that hard-codes per line would
// pass everything above while silently ignoring the variable.
func TestTrustedProfileSAOverrideStillWins(t *testing.T) {
	const expr = "local.trusted_profile_sa"
	const custom = "my-own-controller-sa"

	for name, module := range trustedProfileModules {
		for _, line := range []string{"2.3", "2.4"} {
			t.Run(name+"/"+line, func(t *testing.T) {
				tfvars := "bnk_line = \"" + line + "\"\nflo_namespace = \"f5-bnk\"\n" +
					"trusted_profile_sa_name = \"" + custom + "\"\n"
				if got := consoleString(t, module, tfvars, expr); got != custom {
					t.Errorf("%s on line %s: override ignored, got %q want %q", name, line, got, custom)
				}
			})
		}
	}
}

// The 2.3 name is namespace-derived; the 2.4 name is not. A fix that pinned a
// literal for 2.3 would break any non-default flo_namespace, which is exactly
// the class of bug #65 recorded in cneinstance/main.tf.
func TestTrustedProfileSA23FollowsTheNamespace(t *testing.T) {
	const expr = "local.trusted_profile_sa"
	const ns = "bnk-custom"

	for name, module := range trustedProfileModules {
		t.Run(name, func(t *testing.T) {
			tfvars := "bnk_line = \"2.3\"\nflo_namespace = \"" + ns + "\"\n"
			want := "f5-cne-controller-" + ns + "-f5-cne-controller-serviceaccount"
			if got := consoleString(t, module, tfvars, expr); got != want {
				t.Errorf("%s: 2.3 name did not follow flo_namespace, got %q want %q", name, got, want)
			}
		})
	}
}
