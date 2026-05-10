package test

// Sprint 5 / PRD 03 §"GSLB use case" — multi-vantage comparison logic
// unit tests.
//
// CompareDNSVantages stitches a slice of per-vantage probe results into
// a single DNSCompareResult and computes:
//
//   - GSLBDivergence (bool) — true when two or more vantages return
//     different answer sets (sorted {type, rdata} tuples).
//   - GSLBDivergenceSummary (string) — human-readable rendering of which
//     vantages disagreed; empty when no divergence.
//
// These tests pin the comparison logic at the unit tier — the CLI
// surface (--gslb-compare / --require-divergence flags) is exercised
// end-to-end in scripts/e2e-test-backends.sh Phase L-DNS step LD7-LD8.
//
// Run with:
//
//	go test -run CompareDNSVantages ./internal/test/...

import (
	"strings"
	"testing"

	"github.com/miekg/dns"
)

// TestCompareDNSVantages_DivergenceTrue: two vantages with different
// answer sets → GSLBDivergence=true + a populated summary.
func TestCompareDNSVantages_DivergenceTrue(t *testing.T) {
	vantages := []DNSProbeResult{
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "local",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{
				{Name: "gslb.example.com.", Type: "A", TTL: 60, RData: "169.45.91.10"},
			},
		},
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "k8s",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{
				{Name: "gslb.example.com.", Type: "A", TTL: 60, RData: "10.20.30.40"},
			},
		},
	}
	got := CompareDNSVantages("gslb.example.com.", dns.TypeA, vantages)
	if got.Schema != DNSSchemaVersion {
		t.Errorf("Schema: got %q, want %q", got.Schema, DNSSchemaVersion)
	}
	if !got.GSLBDivergence {
		t.Errorf("GSLBDivergence: got false, want true (vantages returned different rdata)")
	}
	if got.GSLBDivergenceSummary == "" {
		t.Errorf("GSLBDivergenceSummary: empty; expected a populated summary")
	}
	if !strings.Contains(got.GSLBDivergenceSummary, "169.45.91.10") {
		t.Errorf("GSLBDivergenceSummary should mention 169.45.91.10: got %q", got.GSLBDivergenceSummary)
	}
	if !strings.Contains(got.GSLBDivergenceSummary, "10.20.30.40") {
		t.Errorf("GSLBDivergenceSummary should mention 10.20.30.40: got %q", got.GSLBDivergenceSummary)
	}
	if len(got.Vantages) != 2 {
		t.Errorf("Vantages: got %d, want 2", len(got.Vantages))
	}
}

// TestCompareDNSVantages_DivergenceFalse_AllAgree: all vantages return
// the same answer → GSLBDivergence=false, no summary.
func TestCompareDNSVantages_DivergenceFalse_AllAgree(t *testing.T) {
	answer := []DNSAnswer{
		{Name: "anycast.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"},
	}
	vantages := []DNSProbeResult{
		{Schema: DNSVantageSchemaVersion, Backend: "local", Rcode: "NOERROR", Answers: answer},
		{Schema: DNSVantageSchemaVersion, Backend: "k8s", Rcode: "NOERROR", Answers: answer},
	}
	got := CompareDNSVantages("anycast.example.com.", dns.TypeA, vantages)
	if got.GSLBDivergence {
		t.Errorf("GSLBDivergence: got true, want false (all vantages agreed)")
	}
	if got.GSLBDivergenceSummary != "" {
		t.Errorf("GSLBDivergenceSummary: got %q, want empty (no divergence)", got.GSLBDivergenceSummary)
	}
}

// TestCompareDNSVantages_TTLIgnoredInComparison: vantages return the
// same rdata but different TTLs → still NOT a divergence (TTL drift
// across resolvers is normal even for identical GSLB answers; the
// PRD 03 §"GSLB use case" definition of divergence is the rdata, not
// the TTL).
func TestCompareDNSVantages_TTLIgnoredInComparison(t *testing.T) {
	vantages := []DNSProbeResult{
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "local",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{{Name: "x.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"}},
		},
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "k8s",
			Rcode:   "NOERROR",
			// Different TTL, same rdata.
			Answers: []DNSAnswer{{Name: "x.example.com.", Type: "A", TTL: 300, RData: "1.1.1.1"}},
		},
	}
	got := CompareDNSVantages("x.example.com.", dns.TypeA, vantages)
	if got.GSLBDivergence {
		t.Errorf("GSLBDivergence: TTL-only difference flagged as divergence")
	}
}

// TestCompareDNSVantages_MultipleAnswersOrderInsensitive: vantages
// return the same set of rdata in different orders → not a divergence.
// The comparison's fingerprint sorts the {type, rdata} tuples so order
// doesn't matter.
func TestCompareDNSVantages_MultipleAnswersOrderInsensitive(t *testing.T) {
	vantages := []DNSProbeResult{
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "local",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{
				{Name: "x.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"},
				{Name: "x.example.com.", Type: "A", TTL: 60, RData: "2.2.2.2"},
			},
		},
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "k8s",
			Rcode:   "NOERROR",
			// Same answers, reverse order.
			Answers: []DNSAnswer{
				{Name: "x.example.com.", Type: "A", TTL: 60, RData: "2.2.2.2"},
				{Name: "x.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"},
			},
		},
	}
	got := CompareDNSVantages("x.example.com.", dns.TypeA, vantages)
	if got.GSLBDivergence {
		t.Errorf("GSLBDivergence: order-only difference flagged as divergence")
	}
}

// TestCompareDNSVantages_SingleVantage_NoDivergence: one vantage means
// nothing to compare; result is non-divergent regardless of content.
func TestCompareDNSVantages_SingleVantage_NoDivergence(t *testing.T) {
	vantages := []DNSProbeResult{
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "local",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{{Name: "x.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"}},
		},
	}
	got := CompareDNSVantages("x.example.com.", dns.TypeA, vantages)
	if got.GSLBDivergence {
		t.Errorf("GSLBDivergence: single vantage flagged as divergent")
	}
	if len(got.Vantages) != 1 {
		t.Errorf("Vantages: got %d, want 1", len(got.Vantages))
	}
}

// TestCompareDNSVantages_ErroredVantageSkipped: one vantage errored
// (Rcode/transport failure); the other has answers. Divergence is
// determined ONLY by vantages with answers — an errored vantage is
// skipped (the GSLB might be working; one network path was just
// broken).
func TestCompareDNSVantages_ErroredVantageSkipped(t *testing.T) {
	vantages := []DNSProbeResult{
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "local",
			Rcode:   "NOERROR",
			Answers: []DNSAnswer{{Name: "x.example.com.", Type: "A", TTL: 60, RData: "1.1.1.1"}},
		},
		{
			Schema:  DNSVantageSchemaVersion,
			Backend: "k8s",
			Rcode:   "ERROR",
			Err:     "no kubeconfig reachable",
		},
	}
	got := CompareDNSVantages("x.example.com.", dns.TypeA, vantages)
	if got.GSLBDivergence {
		t.Errorf("GSLBDivergence: errored vantage was counted as different (should be skipped)")
	}
	// Both vantages still appear in the output — the consumer sees
	// the errored one and can decide what to do.
	if len(got.Vantages) != 2 {
		t.Errorf("Vantages: got %d, want 2 (errored vantage should still be in output)", len(got.Vantages))
	}
}
