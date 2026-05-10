package test

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// DNSSchemaVersion is the schema string emitted by the new
// `roksbnkctl test dns` flag-driven path. Distinct from SchemaVersion
// (the umbrella `roksbnkctl.v1`) because PRD 03 §"DNS probe" specifies
// a richer per-vantage shape with RTT distribution + GSLB divergence.
const (
	DNSSchemaVersion        = "roksbnkctl.dns.v1"
	DNSVantageSchemaVersion = "roksbnkctl.dns.v1.vantage"
)

// Probe is a single-vantage DNS probe (PRD 03 §"DNS probe (GSLB-aware)"
// §"CLI surface" + §"Server resolution"). One Probe instance issues
// `Iterations` queries against `Server` for `Target`/`Type` and returns
// a ProbeResult with RTT distribution + answer set.
//
// Server resolution semantics:
//
//   - "system"            — resolve from /etc/resolv.conf (first nameserver).
//   - "cluster"           — k8s-backend-only sentinel; the local backend
//     errors clearly. Resolved to the pod's resolv.conf at run time.
//   - "<ip>" / "<ip>:<port>" — used verbatim (default port 53 if omitted).
//
// Named-from-config resolvers (workspace `test.dns.resolvers`) are
// resolved by the CLI layer **before** invoking Run — Probe sees the
// concrete `<ip>:<port>` string.
type Probe struct {
	Target     string        // DNS name to query (FQDN; missing trailing dot is added)
	Type       uint16        // dns.Type* constant (A=1, AAAA=28, CNAME=5, …)
	Server     string        // "<ip>[:<port>]" or "system"
	Iterations int           // number of repeated queries (default 1)
	Timeout    time.Duration // per-query timeout (default 2s)
	// Backend is the label stamped onto ProbeResult.Backend (e.g.
	// "local", "k8s", "ssh:jumphost"). The probe doesn't dispatch by
	// backend itself — the CLI layer composes vantages by running this
	// Probe via each backend.
	Backend string
}

// DNSProbeResult is the per-vantage result document. JSON-serialised as
// part of the multi-vantage comparison (DNSCompareResult) or alone for
// the single-vantage path. Schema string is set explicitly so a JSON
// consumer parsing one shape can reject the other (a vantage entry
// embedded in a comparison still carries the same schema string —
// callers can union the documents safely).
type DNSProbeResult struct {
	Schema        string          `json:"schema"`
	Backend       string          `json:"backend"`
	Server        string          `json:"server"`
	Iterations    int             `json:"iterations"`
	RTTMs         RTTDistribution `json:"rtt_ms"`
	Answers       []DNSAnswer     `json:"answers"`
	Rcode         string          `json:"rcode"`
	Authoritative bool            `json:"authoritative"`
	Truncated     bool            `json:"truncated"`
	Err           string          `json:"error,omitempty"`
}

// RTTDistribution captures p50/p95/p99 query latency in milliseconds.
// For Iterations==1, all three fields equal the single observation.
type RTTDistribution struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

// DNSAnswer is one resource record from the response section. The
// `RData` field is the textual rendering miekg/dns produces (e.g.
// "169.45.91.10" for an A, "host.example.com." for a CNAME) — minus
// the leading "NAME TTL CLASS TYPE" preamble miekg/dns prepends in its
// `RR.String()` output.
type DNSAnswer struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	TTL   uint32 `json:"ttl"`
	RData string `json:"rdata"`
}

// DNSCompareResult is the multi-vantage comparison document
// (`--gslb-compare`). Schema is `roksbnkctl.dns.v1`.
type DNSCompareResult struct {
	Schema                string           `json:"schema"`
	Target                string           `json:"target"`
	Type                  string           `json:"type"`
	Vantages              []DNSProbeResult `json:"vantages"`
	GSLBDivergence        bool             `json:"gslb_divergence"`
	GSLBDivergenceSummary string           `json:"gslb_divergence_summary,omitempty"`
}

// Run executes the DNS probe synchronously and returns the per-vantage
// result. ctx cancellation aborts the in-flight query budget.
//
// Errors at the transport layer (connect refused, timeout) populate
// ProbeResult.Err + Rcode (which becomes "TIMEOUT" or the literal
// transport-error message) and return a *successful* (*ProbeResult, nil)
// — the CLI surfaces the failure via the rendering, not as a Go error.
// Programmer-error inputs (zero target, unsupported "cluster" on the
// local backend) return (nil, error) so the CLI can short-circuit.
func (p *Probe) Run(ctx context.Context) (*DNSProbeResult, error) {
	if p.Target == "" {
		return nil, fmt.Errorf("dns probe: target is empty")
	}
	if p.Type == 0 {
		p.Type = dns.TypeA
	}
	iterations := p.Iterations
	if iterations < 1 {
		iterations = 1
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	// Resolve "system" at run time. "cluster" is rejected here — the k8s
	// backend's job runs the same Probe inside the pod, where its
	// /etc/resolv.conf (CoreDNS) gives the same effective semantics as
	// "system" — so the k8s backend rewrites "cluster" → "system" before
	// re-dispatching. The local backend never sees "cluster".
	server, err := resolveServerAddr(p.Server)
	if err != nil {
		return nil, err
	}

	target := dns.Fqdn(p.Target)
	client := &dns.Client{
		Net:     "udp",
		Timeout: timeout,
	}

	result := &DNSProbeResult{
		Schema:     DNSVantageSchemaVersion,
		Backend:    p.Backend,
		Server:     server,
		Iterations: iterations,
	}

	rtts := make([]time.Duration, 0, iterations)
	var lastResp *dns.Msg
	for i := 0; i < iterations; i++ {
		select {
		case <-ctx.Done():
			result.Err = ctx.Err().Error()
			result.Rcode = "TIMEOUT"
			return result, nil
		default:
		}
		msg := new(dns.Msg)
		msg.SetQuestion(target, p.Type)
		msg.RecursionDesired = true

		resp, rtt, err := client.ExchangeContext(ctx, msg, server)
		if err != nil {
			// Transport / DNS-level failure. Capture into the result and
			// keep the per-vantage shape stable so the JSON consumer
			// always gets a well-formed document.
			result.Err = err.Error()
			if isTimeoutErr(err) {
				result.Rcode = "TIMEOUT"
			} else {
				result.Rcode = "ERROR"
			}
			return result, nil
		}
		rtts = append(rtts, rtt)
		lastResp = resp
		// If the message was truncated under UDP, retry the SAME
		// iteration over TCP. Per RFC 1035 §4.2.2 the truncated bit
		// signals "switch to TCP and try again".
		if resp.Truncated {
			tcpClient := &dns.Client{Net: "tcp", Timeout: timeout}
			if tresp, trtt, terr := tcpClient.ExchangeContext(ctx, msg, server); terr == nil {
				lastResp = tresp
				// Replace the UDP RTT with the TCP one — closer to the
				// real-world latency the resolver experiences when the
				// answer set is large enough to matter.
				rtts[len(rtts)-1] = trtt
			}
		}
	}

	if lastResp != nil {
		result.Rcode = dns.RcodeToString[lastResp.Rcode]
		if result.Rcode == "" {
			result.Rcode = fmt.Sprintf("RCODE_%d", lastResp.Rcode)
		}
		result.Authoritative = lastResp.Authoritative
		result.Truncated = lastResp.Truncated
		result.Answers = answersFromMsg(lastResp)
	}
	result.RTTMs = computeRTT(rtts)
	return result, nil
}

// resolveServerAddr maps the user-facing --server value to a concrete
// "<ip>:<port>" miekg/dns can dial. Empty / "system" reads the host's
// /etc/resolv.conf and uses the first nameserver. "cluster" is rejected
// (k8s-backend-only — the cli layer rewrites it before reaching here).
// Otherwise the value is normalised: bare IP gets ":53" appended; an
// existing port is preserved.
func resolveServerAddr(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "system") {
		conf, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return "", fmt.Errorf("reading /etc/resolv.conf: %w", err)
		}
		if len(conf.Servers) == 0 {
			return "", fmt.Errorf("no nameservers in /etc/resolv.conf")
		}
		port := conf.Port
		if port == "" {
			port = "53"
		}
		return net.JoinHostPort(conf.Servers[0], port), nil
	}
	if strings.EqualFold(s, "cluster") {
		return "", fmt.Errorf("--server cluster is only valid with --backend k8s; for local probes use --server system or an explicit address")
	}
	// Add :53 if no port present. net.SplitHostPort errors when the
	// input is just an IP / hostname; that's our cue.
	if _, _, err := net.SplitHostPort(s); err != nil {
		return net.JoinHostPort(s, "53"), nil
	}
	return s, nil
}

// answersFromMsg extracts the answer-section RRs from msg. The RData
// is the textual remainder of `RR.String()` past the header (TTL +
// class + type tokens) so consumers see "169.45.91.10" rather than
// "www.example.com. 60 IN A 169.45.91.10".
func answersFromMsg(msg *dns.Msg) []DNSAnswer {
	if msg == nil || len(msg.Answer) == 0 {
		return nil
	}
	out := make([]DNSAnswer, 0, len(msg.Answer))
	for _, rr := range msg.Answer {
		hdr := rr.Header()
		// RR.String() yields "name TTL CLASS TYPE rdata"; strip the
		// header to get just rdata. We can't rely on field count
		// because some RR types have multi-token rdata, so trim by
		// finding the type token. miekg/dns doesn't expose a "rdata
		// only" formatter, so we cut after the type-string.
		full := rr.String()
		typeStr := dns.TypeToString[hdr.Rrtype]
		if typeStr == "" {
			typeStr = fmt.Sprintf("TYPE%d", hdr.Rrtype)
		}
		rdata := full
		if idx := strings.Index(full, typeStr); idx >= 0 {
			rest := full[idx+len(typeStr):]
			rdata = strings.TrimLeft(rest, " \t")
		}
		out = append(out, DNSAnswer{
			Name:  hdr.Name,
			Type:  typeStr,
			TTL:   hdr.Ttl,
			RData: rdata,
		})
	}
	return out
}

// computeRTT computes p50/p95/p99 from a slice of durations. Empty
// input returns the zero distribution. Single-sample input returns
// the same value for all three percentiles. Implementation is the
// nearest-rank method (RFC 9330 §4.4 reference) — adequate for our N
// (typically ≤ 100 iterations).
func computeRTT(rtts []time.Duration) RTTDistribution {
	if len(rtts) == 0 {
		return RTTDistribution{}
	}
	sorted := append([]time.Duration(nil), rtts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	pct := func(p float64) float64 {
		// nearest-rank: index = ceil(p * N) - 1 (clamped to last entry)
		idx := int(p*float64(len(sorted))+0.999999) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return float64(sorted[idx].Microseconds()) / 1000.0
	}
	return RTTDistribution{
		P50: pct(0.50),
		P95: pct(0.95),
		P99: pct(0.99),
	}
}

// isTimeoutErr reports whether err is a net-stack timeout. miekg/dns's
// Exchange wraps the underlying net.Error; this peeks for the Timeout()
// interface method without dragging the errors.As ergonomics into the
// hot path.
func isTimeoutErr(err error) bool {
	type timeouter interface{ Timeout() bool }
	if te, ok := err.(timeouter); ok && te.Timeout() {
		return true
	}
	return strings.Contains(err.Error(), "timeout") || strings.Contains(err.Error(), "deadline exceeded")
}

// ParseDNSType maps a user-facing record-type name to a miekg/dns
// type constant. Accepts the full PRD 03 §"Record types supported"
// surface plus any other type miekg/dns knows about (lookup is via
// dns.StringToType so future RFC adds come along for free).
//
// Returns (0, error) on unknown names so the CLI can render a clear
// "unknown record type" with the input string in the message.
func ParseDNSType(s string) (uint16, error) {
	if s == "" {
		return dns.TypeA, nil
	}
	upper := strings.ToUpper(strings.TrimSpace(s))
	if t, ok := dns.StringToType[upper]; ok {
		return t, nil
	}
	return 0, fmt.Errorf("unknown DNS record type %q (try A, AAAA, CNAME, MX, NS, TXT, SRV, SOA, PTR, CAA, DS, DNSKEY, ANY)", s)
}

// CompareDNSVantages stitches a DNSCompareResult from a list of single-
// vantage results and computes the gslb_divergence boolean + a human-
// readable summary. Divergence is defined as: distinct answer sets
// across vantages, where an answer set is the *sorted* {type, rdata}
// tuple list (TTL/name are excluded — TTLs vary across resolvers even
// for identical GSLB answers; the FQDN is the same query target).
//
// PRD 03 §"DNS probe" §"GSLB use case": divergence is *expected* in a
// healthy GSLB; --require-divergence flips the exit code so CI can
// assert the GSLB is doing something.
func CompareDNSVantages(target string, qtype uint16, vantages []DNSProbeResult) DNSCompareResult {
	out := DNSCompareResult{
		Schema:   DNSSchemaVersion,
		Target:   target,
		Type:     dns.TypeToString[qtype],
		Vantages: vantages,
	}
	if len(vantages) <= 1 {
		return out
	}

	// Build a canonical "answer set fingerprint" per vantage: sorted
	// "TYPE rdata" tuples joined by "|". Skip vantages that errored —
	// their answers slice is empty, and counting them as "different"
	// would confuse the user (the GSLB probably is working; one
	// vantage just couldn't reach its resolver).
	fingerprint := make([]string, 0, len(vantages))
	for _, v := range vantages {
		if v.Err != "" {
			continue
		}
		entries := make([]string, 0, len(v.Answers))
		for _, a := range v.Answers {
			entries = append(entries, a.Type+" "+a.RData)
		}
		sort.Strings(entries)
		fingerprint = append(fingerprint, strings.Join(entries, "|"))
	}

	if len(fingerprint) <= 1 {
		return out
	}
	first := fingerprint[0]
	for _, f := range fingerprint[1:] {
		if f != first {
			out.GSLBDivergence = true
			out.GSLBDivergenceSummary = summariseDivergence(vantages)
			return out
		}
	}
	return out
}

// summariseDivergence renders a one-line human summary of which
// vantages disagreed. We keep it short — JSON consumers parse the
// `vantages` array for the full detail.
func summariseDivergence(vantages []DNSProbeResult) string {
	parts := make([]string, 0, len(vantages))
	for _, v := range vantages {
		if v.Err != "" {
			parts = append(parts, fmt.Sprintf("%s (error: %s)", v.Backend, v.Err))
			continue
		}
		rdatas := make([]string, 0, len(v.Answers))
		for _, a := range v.Answers {
			rdatas = append(rdatas, a.RData)
		}
		if len(rdatas) == 0 {
			parts = append(parts, fmt.Sprintf("%s (no answers)", v.Backend))
		} else {
			parts = append(parts, fmt.Sprintf("%s [%s]", v.Backend, strings.Join(rdatas, ",")))
		}
	}
	return "answers differ across vantages: " + strings.Join(parts, " vs ")
}

// ── legacy connectivity-suite path (kept verbatim) ──────────────────

// RunDNS is the workspace-config-driven path used by `roksbnkctl test`
// (the umbrella command) and `roksbnkctl test dns` when no flags are
// passed. Probes each `extra_hosts` entry with the std-lib resolver —
// preserving Sprint 0/1/2/3 behaviour byte-for-byte.
//
// PRD 03 §"DNS probe (GSLB-aware)" §"CLI surface" §"backwards-
// compatible path": flag-driven Probe activates only when one of
// --target/--type/--server/--gslb-compare is set on the command line.
func RunDNS(ctx context.Context, hosts []string) SuiteRun {
	start := time.Now()
	probes := make([]ProbeResult, 0, len(hosts))
	for _, h := range hosts {
		probes = append(probes, dnsProbe(ctx, hostOnly(h)))
	}
	return SuiteRun{
		Schema:     SchemaVersion,
		Command:    "test",
		Suite:      "dns",
		Timestamp:  start,
		DurationMS: time.Since(start).Milliseconds(),
		Results:    probes,
		Overall:    Aggregate(probes),
	}
}

func dnsProbe(ctx context.Context, host string) ProbeResult {
	start := time.Now()
	p := ProbeResult{Suite: "dns", Name: host, Status: StatusPass}
	if host == "" {
		p.Status = StatusFail
		p.Detail = "empty host"
		return p
	}

	resolver := net.Resolver{}
	ips, err := resolver.LookupHost(ctx, host)
	p.DurationMS = time.Since(start).Milliseconds()

	if err != nil {
		p.Status = StatusFail
		p.Detail = err.Error()
		return p
	}
	if len(ips) == 0 {
		p.Status = StatusFail
		p.Detail = "no addresses returned"
		return p
	}
	p.Detail = fmt.Sprintf("resolved %d address(es)", len(ips))
	p.Extra = map[string]any{"addresses": ips}
	return p
}

// hostOnly extracts the hostname from a URL or "host:port". Returns the
// input unchanged for plain hostnames.
func hostOnly(h string) string {
	h = strings.TrimSpace(h)
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	// Strip :port — but only if not part of an IPv6 literal.
	if !strings.Contains(h, "[") {
		if i := strings.LastIndex(h, ":"); i > 0 {
			h = h[:i]
		}
	}
	return h
}
