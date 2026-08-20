package test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// #115 / #99. The iperf3 L4 leg of the perf matrix never ran, for months,
// because a fixture emitted `gateway.networking.k8s.io/v1alpha2 TCPRoute` —
// a CRD BNK does not install. Two things kept it invisible:
//
//   - The apply is best-effort, so a kind the cluster has never heard of
//     produces no result rather than an error.
//   - A test existed and was GREEN. It asserted `kind: TCPRoute` and the
//     v1alpha2 group, because it was written from the same wrong assumption as
//     the code. It could only ever confirm what the author already believed.
//
// The fix for the fixture was one line. The fix for the second problem is this:
// the contract lives in gatewayapi.go, and anything spelling a kind outside it
// is found mechanically rather than by someone happening to know.
//
// This is deliberately a REPO scan, not a package test. #99's own fix corrected
// the emitter and left `tcproutes.gateway.networking.k8s.io` in the matrix
// teardown, one package away, for another two releases — a package-scoped test
// would not have looked there.

// repoRoot is this package's directory, two levels down from the repository.
func repoRoot() string { return filepath.Join("..", "..") }

// absentKindExemptions are the files allowed to name a route kind BNK does not
// install, and why each one is prose rather than a manifest. Shared by the two
// tests below so the list cannot be right in one and stale in the other.
var absentKindExemptions = map[string]string{
	"internal/test/gatewayapi.go":               "declares AbsentRouteKinds — the list these tests check against",
	"internal/test/gatewayapi_contract_test.go": "this file",
	"internal/test/fixtures.go":                 "renderL4Route's comment explains why it is NOT a TCPRoute",
	"internal/test/fixtures_test.go":            "asserts the wrong kind is absent, which requires naming it",
	"internal/config/workspace.go":              "RouteExamples' doc states which kinds the channel lacks",

	// The terraform validation REJECTS these kinds, so its error messages have
	// to name them — that rejection is the thing stopping an operator asking
	// for an object no controller will claim.
	"terraform/modules/gateway/main.tf":      "comments contrasting L4Route with the kinds the standard channel lacks",
	"terraform/modules/gateway/variables.tf": "gateway_route_examples validation message names the rejected kinds",
	"terraform/variables.tf":                 "same validation message at the root",
	"scripts/tf-variable-validation-test.sh": "asserts TCPRoute is rejected, which requires passing it",
}

// absentKindRE matches a route kind BNK does not install, either as a manifest
// kind ("TCPRoute") or as a CRD plural ("tcproutes").
//
// Both forms are DERIVED from AbsentRouteKinds rather than listed alongside it.
// A hard-coded plural list is the same defect this whole file exists to catch:
// add a kind to AbsentRouteKinds, and a second list somewhere else silently
// stops covering it — which is precisely how the matrix teardown kept naming
// tcproutes after the fixture stopped emitting TCPRoute.
func absentKindRE() *regexp.Regexp {
	alts := make([]string, 0, len(AbsentRouteKinds)*2)
	for _, k := range AbsentRouteKinds {
		alts = append(alts, regexp.QuoteMeta(k), regexp.QuoteMeta(strings.ToLower(k)+"s"))
	}
	// (?i) is deliberately NOT used: "tcproute" in prose is not a reference to
	// the kind, and matching it would make the exemption list unmanageable.
	return regexp.MustCompile(`\b(` + strings.Join(alts, "|") + `)\b`)
}

// scannedDirs are the trees that can name a Kubernetes kind.
var scannedDirs = []string{"internal", "cmd", "terraform", "scripts"}

var scannedExts = map[string]bool{
	".go": true, ".yaml": true, ".yml": true, ".tf": true, ".tftpl": true, ".sh": true,
}

// TestNoReferenceToARouteKindBNKDoesNotInstall fails on any mention of a
// Gateway API route kind outside the standard channel BNK pins.
//
// The exemption list is the point of the design: gatewayapi.go NAMES the absent
// kinds so they can be checked for, and this file explains why each remaining
// mention is prose rather than a manifest. A new one has to justify itself.
func TestNoReferenceToARouteKindBNKDoesNotInstall(t *testing.T) {
	absent := absentKindRE()

	var offenders []string
	for _, dir := range scannedDirs {
		root := filepath.Join(repoRoot(), dir)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			if !scannedExts[strings.ToLower(filepath.Ext(path))] {
				return nil
			}
			// repoRoot() is "../.." so filepath.Walk hands back paths prefixed
			// with it; strip that to get the repo-relative key the exemption
			// map is written in.
			rel := strings.TrimPrefix(filepath.ToSlash(path), "../../")
			if _, ok := absentKindExemptions[rel]; ok {
				return nil
			}
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for i, line := range strings.Split(string(body), "\n") {
				if absent.MatchString(line) {
					offenders = append(offenders, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}

	if len(offenders) > 0 {
		t.Errorf("reference to a Gateway API route kind BNK does not install:\n  %s\n\n"+
			"BNK 2.3 pins Gateway API 1.4.1 STANDARD, which has no TCPRoute/TLSRoute/UDPRoute — "+
			"BNK ships L4Route (gateway.k8s.f5net.com/v1) for TCP. An object of an absent kind is "+
			"accepted by a cluster carrying the experimental channel and silently ignored by one that "+
			"is not, which is how #99 hid for months.\n"+
			"Use internal/test.InstalledRouteKinds, or add an exemption here with the reason.",
			strings.Join(offenders, "\n  "))
	}
}

// An exemption for a file that no longer mentions an absent kind is a line the
// next person inherits without noticing. Drop it, so the next addition has to
// argue for itself.
func TestAbsentRouteKindExemptionsAreAllStillNeeded(t *testing.T) {
	absent := absentKindRE()
	for rel := range absentKindExemptions {
		body, err := os.ReadFile(filepath.Join(repoRoot(), rel))
		if err != nil {
			t.Errorf("exempt file %s does not exist", rel)
			continue
		}
		if !absent.Match(body) {
			t.Errorf("%s no longer mentions an absent route kind — drop its exemption", rel)
		}
	}
}

// Fixture teardown must delete exactly what the fixtures create. These were
// written apart — the emitter and the delete list — and drifted: the list named
// tcproutes long after the emitter stopped producing them.
func TestFixtureTeardownCoversEveryInstalledRouteKind(t *testing.T) {
	crds := FixtureRouteCRDs()
	if len(crds) != len(InstalledRouteKinds) {
		t.Fatalf("FixtureRouteCRDs returned %d entries for %d kinds", len(crds), len(InstalledRouteKinds))
	}
	for _, r := range InstalledRouteKinds {
		if !slices.Contains(crds, r.CRD()) {
			t.Errorf("%s (%s) is created but never torn down", r.Kind, r.CRD())
		}
	}

	body, err := os.ReadFile(filepath.Join(repoRoot(), "internal/cli/test_matrix_fixtures.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "test.FixtureRouteCRDs()") {
		t.Error("the matrix teardown does not derive its CRD list from test.FixtureRouteCRDs — " +
			"a hand-written list is what drifted from the fixtures in the first place")
	}
}

// The rendered manifests must carry the API versions the contract declares.
// Asserting against RouteKind.APIVersion rather than a literal means a change to
// the pinned channel moves both the fixture and its test together — a literal in
// each is two places to get right, and #99 got one of them wrong.
func TestRenderedRoutesUseTheContractsAPIVersions(t *testing.T) {
	http := renderHTTPRoute("r", "ns", "gw", "", "svc", 80)
	if !strings.Contains(http, "apiVersion: "+HTTPRouteKind.APIVersion()) {
		t.Errorf("HTTPRoute fixture does not use %s:\n%s", HTTPRouteKind.APIVersion(), http)
	}
	l4 := renderL4Route("r", "ns", "gw", "", "svc", 8080)
	if !strings.Contains(l4, "apiVersion: "+L4RouteKind.APIVersion()) {
		t.Errorf("L4Route fixture does not use %s:\n%s", L4RouteKind.APIVersion(), l4)
	}
	// And the L4 fixture must not be a Gateway API route at all — the whole
	// point of #99 is that the group was wrong, not just the version.
	if strings.Contains(l4, GatewayAPIGroup) {
		t.Errorf("the L4 fixture references %s; BNK's L4Route is its own CRD:\n%s", GatewayAPIGroup, l4)
	}
}

// The regex has to cover BOTH spellings of every absent kind, derived from the
// one list. A plural that is listed separately drifts the moment a kind is
// added — which is the failure this file exists to prevent, reproduced inside
// the guard itself.
func TestAbsentKindRegexCoversBothSpellingsOfEveryKind(t *testing.T) {
	re := absentKindRE()
	for _, kind := range AbsentRouteKinds {
		if !re.MatchString(kind) {
			t.Errorf("the guard does not match the manifest kind %q", kind)
		}
		plural := strings.ToLower(kind) + "s"
		if !re.MatchString(plural) {
			t.Errorf("the guard does not match the CRD plural %q — a delete list naming it would pass", plural)
		}
		crd := plural + "." + GatewayAPIGroup
		if !re.MatchString(crd) {
			t.Errorf("the guard does not match the CRD %q, which is the exact shape #99 left behind", crd)
		}
	}
	// Kinds BNK DOES install must not trip it, or the guard is unusable.
	for _, r := range InstalledRouteKinds {
		if re.MatchString(r.Kind) || re.MatchString(r.CRD()) {
			t.Errorf("the guard matches %s, which BNK installs", r.Kind)
		}
	}
}
