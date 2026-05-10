// Unit tests for `roksbnkctl k port-forward` (`internal/k8s/port_forward.go`).
//
// Port-forward needs a real SPDY upgrade — not fakeable. We test the
// option-validation surface and the Ports slice round-trip; end-to-end
// is in the live golden tests.

package k8s

import (
	"context"
	"strings"
	"testing"
)

// TestPortForwardOptions_RequiresPod: no pod → error.
func TestPortForwardOptions_RequiresPod(t *testing.T) {
	o := &PortForwardOptions{Ports: []string{"8080:80"}}
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty PodName; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "pod") {
		t.Errorf("expected 'pod' in err; got: %v", err)
	}
}

// TestPortForwardOptions_RequiresPorts: no ports → error.
func TestPortForwardOptions_RequiresPorts(t *testing.T) {
	o := &PortForwardOptions{PodName: "p"}
	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty Ports; got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "port") {
		t.Errorf("expected 'port' in err; got: %v", err)
	}
}

// TestPortForwardOptions_PortsRoundTrip is a drift guard against future
// option struct changes. The Ports slice is passed verbatim to
// portforward.New (which parses kubectl-style "L:R" strings); we just
// confirm the field accepts the canonical forms.
func TestPortForwardOptions_PortsRoundTrip(t *testing.T) {
	cases := [][]string{
		{"8080:80"},
		{"5000"},
		{"8080:80", "9090:90"},
		{":80"}, // random local port (kubectl shorthand)
	}
	for _, c := range cases {
		o := &PortForwardOptions{PodName: "p", Ports: c}
		if len(o.Ports) != len(c) {
			t.Errorf("Ports round-trip lost entries: in=%v out=%v", c, o.Ports)
		}
	}
}
