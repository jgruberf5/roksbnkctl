//go:build integration

package test

// Sprint 5 / PRD 03 §"DNS probe" — integration-tier coverage.
//
// Two cases:
//
//  1. Real-world resolver: a Probe against `8.8.8.8` for
//     `www.cloudflare.com` A records. Skips on networkless runners.
//
//  2. Concurrent in-process probes against a local stub server.
//     Validates the probe's concurrency story without external
//     network: two probes run in parallel goroutines, both record
//     RTTs, no cross-talk between their answer lists.
//
// Run with:
//
//	go test -tags 'integration dnsprobe' -timeout 5m -run 'IntegrationProbe' ./internal/test/...
//
// CI: piggybacks on the existing integration job in ci.yml — the
// `8.8.8.8` test is the only one that needs external network and is
// the one most likely to skip on a corporate/firewalled runner.

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// hasNetwork is a cheap reachability probe — TCP connect to 1.1.1.1:53
// with a 2s timeout. Skips the calling test if it fails.
func hasNetwork(t *testing.T) {
	t.Helper()
	c, err := net.DialTimeout("tcp", "1.1.1.1:53", 2*time.Second)
	if err != nil {
		t.Skipf("no external network available: %v", err)
	}
	_ = c.Close()
}

// TestIntegrationProbe_Real_Cloudflare runs a real Probe against
// `8.8.8.8`, asking for `www.cloudflare.com` A records. The pass
// criteria are loose because anycast + caching means the exact answer
// varies; we just need a non-empty answer set with NOERROR + a
// positive RTT.
func TestIntegrationProbe_Real_Cloudflare(t *testing.T) {
	hasNetwork(t)

	p := &Probe{
		Target:     "www.cloudflare.com.",
		Type:       dns.TypeA,
		Server:     "8.8.8.8:53",
		Iterations: 1,
		Timeout:    5 * time.Second,
	}
	result, err := p.Run(context.Background())
	if err != nil {
		t.Skipf("Probe.Run against 8.8.8.8 failed (firewall? rate-limited?): %v", err)
	}
	if result.Rcode != "NOERROR" {
		t.Fatalf("Rcode: got %q, want NOERROR", result.Rcode)
	}
	if len(result.Answers) == 0 {
		t.Fatalf("no answers returned")
	}
	if result.RTTMs.P50 <= 0 {
		t.Errorf("RTT p50 must be positive; got %v", result.RTTMs.P50)
	}
}

// TestIntegrationProbe_LocalStub_Concurrent spins a local stub server
// and runs two probes in parallel against it. Asserts both complete,
// both have RTTs > 0, and their answer lists are independent (no
// shared mutable state slipping through).
func TestIntegrationProbe_LocalStub_Concurrent(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer pc.Close()

	srv := &dns.Server{
		PacketConn: pc,
		Net:        "udp",
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			m := new(dns.Msg)
			m.SetReply(r)
			for _, q := range r.Question {
				if q.Qtype == dns.TypeA {
					rr, _ := dns.NewRR(q.Name + " 60 IN A 192.0.2.42")
					m.Answer = append(m.Answer, rr)
				}
			}
			_ = w.WriteMsg(m)
		}),
	}
	ready := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(ready) }
	go func() { _ = srv.ActivateAndServe() }()
	defer srv.Shutdown()
	<-ready

	addr := pc.LocalAddr().String()

	type probeOutcome struct {
		result *DNSProbeResult
		err    error
	}
	wg := sync.WaitGroup{}
	results := make([]probeOutcome, 2)
	targets := []string{"alpha.example.com.", "beta.example.com."}
	for i, target := range targets {
		i, target := i, target
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := &Probe{
				Target:     target,
				Type:       dns.TypeA,
				Server:     addr,
				Iterations: 5,
				Timeout:    2 * time.Second,
			}
			result, err := p.Run(context.Background())
			results[i] = probeOutcome{result: result, err: err}
		}()
	}
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Errorf("probe %d (%s): err=%v", i, targets[i], r.err)
			continue
		}
		if len(r.result.Answers) == 0 {
			t.Errorf("probe %d (%s): no answers", i, targets[i])
			continue
		}
		// The stub echoed the query name into the answer — verify each
		// probe got its own target back, no cross-talk.
		got := r.result.Answers[0].Name
		if got != targets[i] {
			t.Errorf("probe %d cross-talk: answer.Name=%q want %q", i, got, targets[i])
		}
		if r.result.RTTMs.P50 <= 0 {
			t.Errorf("probe %d RTT p50 not positive: %v", i, r.result.RTTMs.P50)
		}
	}
}
