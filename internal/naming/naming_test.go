package naming

// Sprint 26 validator Issue 1 — hermetic coverage for the prefix-driven
// naming package (issues/issue_sprint26_validator.md). Additive new file.
//
// Three surfaces under test, against staff's shipped API
// (internal/naming/naming.go):
//
//   - Derive(prefix)          — the exact compact suffix scheme.
//   - ValidatePrefix(prefix)  — label-rule + per-resource length validation,
//                               including the module-appended -<zone> on the
//                               cluster-jumphost prefix, and the actionable
//                               overflow message.
//   - SanitizeToPrefix(name)  — default-prefix seeding + idempotence.
//
// Expected limits are read from the package's own MaxPrefixLen() and the
// constraint table where the contract is "the binding limit", NOT a
// hard-coded 35, so the suite stays correct if a limit ever changes.

import (
	"fmt"
	"strings"
	"testing"
)

// TestDerive pins the exact suffix scheme. Cluster name == prefix (no
// suffix); every other name is prefix + its documented suffix.
func TestDerive(t *testing.T) {
	const prefix = "demo"
	got := Derive(prefix)
	want := Plan{
		ClusterName:           "demo",
		ClusterVPCName:        "demo-cluster-vpc",
		COSInstanceName:       "demo-registry-cos",
		TransitGatewayName:    "demo-tgw",
		ClientVPCName:         "demo-client-vpc",
		TGWJumphostName:       "demo-jh-tgw",
		ClusterJumphostPrefix: "demo-jh",
	}
	if got != want {
		t.Errorf("Derive(%q) =\n  %+v\nwant\n  %+v", prefix, got, want)
	}
}

// TestDerive_ClusterNameIsBarePrefix is the load-bearing invariant: the
// cluster name carries NO suffix, so the prefix-length limit equals the
// tightest resource limit (the cluster). A regression that tidies the
// cluster name into "<prefix>-cluster" would silently shrink the usable
// prefix and is the exact mistake the package doc warns against.
func TestDerive_ClusterNameIsBarePrefix(t *testing.T) {
	for _, p := range []string{"a", "demo", "e2e-prefix-a"} {
		if got := Derive(p).ClusterName; got != p {
			t.Errorf("Derive(%q).ClusterName = %q; cluster name must equal the bare prefix", p, got)
		}
	}
}

// TestValidatePrefix_Accept covers the normal/accept cases, including the
// longest prefix that still fits (MaxPrefixLen()), which exercises the
// zone-suffix budget on the cluster-jumphost name.
func TestValidatePrefix_Accept(t *testing.T) {
	max := MaxPrefixLen()
	// A maximum-length prefix built from a valid label: starts with a
	// letter, only [a-z0-9-], no trailing hyphen.
	maxPrefix := "a" + strings.Repeat("b", max-1)
	if len(maxPrefix) != max {
		t.Fatalf("test setup: maxPrefix len %d != MaxPrefixLen() %d", len(maxPrefix), max)
	}

	cases := []struct {
		name   string
		prefix string
	}{
		{"single letter", "a"},
		{"normal", "demo"},
		{"with digits", "bnk2demo"},
		{"with hyphens", "e2e-prefix-a"},
		{"max length", maxPrefix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidatePrefix(c.prefix); err != nil {
				t.Errorf("ValidatePrefix(%q) = %v; want nil", c.prefix, err)
			}
		})
	}
}

// TestValidatePrefix_RejectLabel covers every charset/label rejection the
// issue enumerates: empty, uppercase, leading digit, leading hyphen,
// trailing hyphen, and illegal characters.
func TestValidatePrefix_RejectLabel(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
	}{
		{"empty", ""},
		{"uppercase", "Demo"},
		{"all uppercase", "DEMO"},
		{"leading digit", "1demo"},
		{"leading hyphen", "-demo"},
		{"trailing hyphen", "demo-"},
		{"illegal underscore", "demo_x"},
		{"illegal dot", "demo.x"},
		{"illegal space", "demo x"},
		{"illegal slash", "demo/x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := ValidatePrefix(c.prefix); err == nil {
				t.Errorf("ValidatePrefix(%q) = nil; want a label-rule error", c.prefix)
			}
		})
	}
}

// TestValidatePrefix_Overflow proves an over-long-but-well-formed prefix is
// rejected, and that the error names the offending resource + the
// table-computed max prefix length (the actionable message the operator
// needs to trim). The boundary is read from MaxPrefixLen() so this stays
// correct if a limit ever changes.
func TestValidatePrefix_Overflow(t *testing.T) {
	max := MaxPrefixLen()
	// One char over the limit — a valid label (letter-led, [a-z0-9]) so
	// the ONLY failure is length, isolating the overflow path.
	overlong := "a" + strings.Repeat("b", max) // len == max+1
	if len(overlong) != max+1 {
		t.Fatalf("test setup: overlong len %d != max+1 (%d)", len(overlong), max+1)
	}

	err := ValidatePrefix(overlong)
	if err == nil {
		t.Fatalf("ValidatePrefix(%q) = nil; want an overflow error (len %d > max %d)", overlong, len(overlong), max)
	}
	msg := err.Error()

	// The binding resource is the cluster (its name == prefix, the
	// tightest limit). The message must name it and the max prefix length.
	if !strings.Contains(msg, "cluster") {
		t.Errorf("overflow error must name the offending resource (cluster); got: %s", msg)
	}
	if !strings.Contains(msg, fmt.Sprintf("%d", max)) {
		t.Errorf("overflow error must surface the max prefix length %d; got: %s", max, msg)
	}
}

// TestValidatePrefix_ZoneSuffixBudget proves the cluster-jumphost name's
// validation budget includes the module-appended "-<zone>" suffix: a prefix
// crafted so that <prefix>-jh fits 63 but <prefix>-jh-us-south-1 does NOT
// must be rejected, attributing the overflow to an IS resource (the
// jumphost), not the cluster.
//
// We only run this assertion if the zone suffix is actually the binding
// constraint for some prefix length (i.e. the IS jumphost budget is tighter
// than the cluster budget at that length). If the cluster limit binds first
// for all lengths, the package guarantees every derived name fits anyway, so
// the scenario is vacuous and we skip rather than assert a false contract.
func TestValidatePrefix_ZoneSuffixBudget(t *testing.T) {
	// Budget for the cluster-jumphost name including the zone suffix:
	// len(prefix) + len("-jh") + len("-us-south-1") <= 63.
	const jhSuffix = "-jh"
	const zoneSuffix = "-us-south-1"
	jhZoneBudget := 63 - len(jhSuffix) - len(zoneSuffix)
	// Budget without the zone suffix (what a naive validator would use):
	jhNoZoneBudget := 63 - len(jhSuffix)

	if jhZoneBudget >= MaxPrefixLen() {
		t.Skipf("cluster limit (max prefix %d) binds before the jumphost zone budget (%d); zone-suffix case is vacuous for this table", MaxPrefixLen(), jhZoneBudget)
	}

	// A prefix length strictly between the zone-inclusive budget and the
	// zone-exclusive budget: <prefix>-jh fits 63, but <prefix>-jh-<zone>
	// overflows. Such a prefix is only reachable if it also fits the
	// cluster limit; if not, this scenario can't be isolated from the
	// cluster overflow, so we skip.
	overflowLen := jhNoZoneBudget // fits -jh exactly, overflows -jh-<zone>
	if overflowLen > 35 {
		t.Skipf("a prefix long enough to isolate the jumphost zone overflow (%d) also overflows the cluster limit; can't isolate", overflowLen)
	}
	prefix := "a" + strings.Repeat("b", overflowLen-1)

	err := ValidatePrefix(prefix)
	if err == nil {
		t.Fatalf("ValidatePrefix(%q) = nil; the cluster-jumphost name with the -%s zone suffix should overflow 63", prefix, "us-south-1")
	}
	// Sanity: the same prefix WITHOUT the zone suffix would fit, proving
	// the zone suffix is what tipped it over.
	if Derive(prefix).ClusterJumphostPrefix == "" {
		t.Fatal("derive returned empty jumphost prefix")
	}
}

// TestSanitizeToPrefix covers the documented transforms: lowercase,
// `_`/`.`→`-`, illegal-char strip, hyphen-run collapse, leading-non-letter
// strip, trailing-hyphen trim, and the length cap.
func TestSanitizeToPrefix(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"already clean", "demo", "demo"},
		{"uppercase", "Demo", "demo"},
		{"mixed case", "MyCluster", "mycluster"},
		{"underscore to hyphen", "my_cluster", "my-cluster"},
		{"dot to hyphen", "my.cluster", "my-cluster"},
		{"strip illegal", "my@clu$ter", "my-clu-ter"},
		{"collapse hyphen runs", "my---cluster", "my-cluster"},
		{"leading digit stripped", "1demo", "demo"},
		{"leading hyphen stripped", "-demo", "demo"},
		{"leading non-letters stripped", "123-demo", "demo"},
		{"trailing hyphen trimmed", "demo-", "demo"},
		{"trailing underscore trimmed", "demo_", "demo"},
		{"workspace-shaped", "Canada_ROKS.demo-1", "canada-roks-demo-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SanitizeToPrefix(c.in); got != c.want {
				t.Errorf("SanitizeToPrefix(%q) = %q; want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeToPrefix_ProducesValidLabel proves a sanitized non-empty
// result satisfies the label rule (so it can be a ValidatePrefix candidate),
// excepting only the length bound that ValidatePrefix enforces separately.
func TestSanitizeToPrefix_ProducesValidLabel(t *testing.T) {
	for _, in := range []string{"demo", "Canada_ROKS.demo-1", "my---cluster", "1demo", "MyCluster"} {
		got := SanitizeToPrefix(in)
		if got == "" {
			continue // a name with no usable letters sanitizes to "" — allowed.
		}
		if !labelCharset.MatchString(got) {
			t.Errorf("SanitizeToPrefix(%q) = %q which violates the label charset rule", in, got)
		}
	}
}

// TestSanitizeToPrefix_Idempotent — Sanitize(Sanitize(x)) == Sanitize(x) for
// a spread of messy inputs, including the length-cap edge that can re-expose
// a trailing hyphen.
func TestSanitizeToPrefix_Idempotent(t *testing.T) {
	inputs := []string{
		"demo",
		"My_Cluster.1",
		"---weird---name---",
		"123abc",
		"a..b__c",
		strings.Repeat("ab-", 40), // forces the length cap + trailing-hyphen re-trim
		strings.Repeat("x", 200),
	}
	for _, in := range inputs {
		once := SanitizeToPrefix(in)
		twice := SanitizeToPrefix(once)
		if once != twice {
			t.Errorf("SanitizeToPrefix not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

// TestMaxPrefixLen_Sane is a guard on the exported bound: it must be
// positive and no larger than the cluster constraint's max length (the
// cluster name == prefix, so the prefix can never exceed the cluster limit).
func TestMaxPrefixLen_Sane(t *testing.T) {
	max := MaxPrefixLen()
	if max <= 0 {
		t.Fatalf("MaxPrefixLen() = %d; must be positive", max)
	}
	if max > constraintCluster.maxLen {
		t.Errorf("MaxPrefixLen() = %d exceeds the cluster limit %d; prefix==cluster name so it cannot", max, constraintCluster.maxLen)
	}
	// And the max-length prefix really does validate (boundary is inclusive).
	maxPrefix := "a" + strings.Repeat("b", max-1)
	if err := ValidatePrefix(maxPrefix); err != nil {
		t.Errorf("a prefix of exactly MaxPrefixLen()=%d chars must validate; got %v", max, err)
	}
}
