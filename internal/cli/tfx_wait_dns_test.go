package cli

import (
	"errors"
	"fmt"
	"net"
	"testing"
)

// A host that does not resolve is permanent, and retrying it for the full --timeout is
// how "licensing hangs for 15 minutes" happens when the real fault was a wrong or empty
// --kube-host. Classification has to be exact: mistaking a refused connection or a
// timeout for a permanent failure would abort waits that would have succeeded.
func TestIsDNSUnresolvable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"go DNSError not-found", &net.DNSError{Err: "no such host", Name: "flp.example", IsNotFound: true}, true},
		{"DNSError by message", &net.DNSError{Err: "no such host", Name: "flp.example"}, true},
		{"wrapped DNSError", fmt.Errorf("get bnk-license: %w", &net.DNSError{Err: "no such host", IsNotFound: true}), true},
		{"the shifted-arg shape", errors.New(`Get "http://--insecure/apis/...": dial tcp: lookup --insecure: no such host`), true},

		// Everything below may clear on its own — these MUST keep retrying.
		{"connection refused", errors.New("dial tcp 10.0.0.1:443: connect: connection refused"), false},
		{"i/o timeout", errors.New("dial tcp 10.0.0.1:443: i/o timeout"), false},
		{"TLS handshake", errors.New("net/http: TLS handshake timeout"), false},
		{"DNS server temporary failure", &net.DNSError{Err: "server misbehaving", IsTemporary: true}, false},
		{"not found (k8s object)", errors.New(`licenses.k8s.f5net.com "bnk-license" not found`), false},
	}
	for _, tc := range cases {
		if got := isDNSUnresolvable(tc.err); got != tc.want {
			t.Errorf("%s: isDNSUnresolvable = %v, want %v (err: %v)", tc.name, got, tc.want, tc.err)
		}
	}
}

// The tolerance must be small but non-zero: real DNS blips, and aborting on the first
// one would be its own flakiness.
func TestDNSFailFastAttemptsIsSane(t *testing.T) {
	if dnsFailFastAttempts < 2 {
		t.Errorf("too eager (%d): a single DNS blip would abort a wait that would have succeeded", dnsFailFastAttempts)
	}
	if dnsFailFastAttempts > 10 {
		t.Errorf("too patient (%d): the point is to not burn the whole --timeout on a name that will never resolve", dnsFailFastAttempts)
	}
}
