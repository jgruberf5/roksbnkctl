package ibm

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// tgwFake serves the Transit Gateway connection + gateway endpoints, recording
// the ORDER of calls. The order is the whole point: #85 was not a missing
// delete, it was a gateway DELETE fired while the connections were still
// clearing.
type tgwFake struct {
	mu sync.Mutex
	// calls is every non-IAM request as "METHOD path".
	calls []string
	// remaining is how many more connection listings still report the
	// connection before it finally disappears — the async detach IBM performs.
	remaining int
	connsJSON string
}

func (f *tgwFake) roundTrip(r *http.Request) (*http.Response, error) {
	if isIAM(r) {
		return jsonResp(200, iamToken), nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connections"):
		if f.remaining > 0 {
			f.remaining--
			return jsonResp(200, f.connsJSON), nil
		}
		return jsonResp(200, `{"connections":[]}`), nil

	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/connections/"):
		return jsonResp(204, ""), nil

	case r.Method == http.MethodDelete:
		// The gateway itself. IBM refuses while anything is still attached —
		// reproduce that, so a premature delete fails the test the way it
		// failed the operator.
		if f.remaining > 0 {
			return jsonResp(412, `{"errors":[{"code":"precondition_failed","message":"Before you can delete this gateway, you must delete all attached connections."}]}`), nil
		}
		return jsonResp(204, ""), nil
	}
	return jsonResp(404, `{}`), nil
}

func (f *tgwFake) sequence() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// The regression test for #85: the gateway DELETE must not be issued until the
// connections have actually cleared. With the old fire-and-forget code this
// fails with the same 412 the operator saw.
func TestDeleteTransitGateway_WaitsForConnectionsToClear(t *testing.T) {
	prev := tgwConnectionPollInterval
	tgwConnectionPollInterval = time.Millisecond
	defer func() { tgwConnectionPollInterval = prev }()

	f := &tgwFake{
		// Still listed on the delete-time read and on the first two polls.
		remaining: 3,
		connsJSON: `{"connections":[{"id":"c1","name":"conn-swept","network_type":"vpc","network_id":"` + sweptVPCCRN + `","status":"attached"}]}`,
	}
	c := newTestClient(f.roundTrip)

	err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	seq := f.sequence()
	var connDelete, gwDelete = -1, -1
	for i, call := range seq {
		switch {
		case strings.HasPrefix(call, "DELETE") && strings.Contains(call, "/connections/"):
			connDelete = i
		case strings.HasPrefix(call, "DELETE"):
			gwDelete = i
		}
	}
	if connDelete < 0 || gwDelete < 0 {
		t.Fatalf("expected both a connection delete and a gateway delete, got %v", seq)
	}
	if gwDelete < connDelete {
		t.Fatalf("gateway deleted before its connection: %v", seq)
	}
	// The gateway DELETE must be the LAST call: anything after it means we
	// deleted first and polled afterwards, which is the bug.
	if gwDelete != len(seq)-1 {
		t.Fatalf("gateway delete was not the final call: %v", seq)
	}
	// And it must have waited — at least one poll after the connection delete.
	polls := 0
	for _, call := range seq[connDelete+1:] {
		if strings.HasPrefix(call, "GET") {
			polls++
		}
	}
	if polls < 1 {
		t.Fatalf("no re-check between detaching and deleting the gateway: %v", seq)
	}
}

// A foreign attachment must stop the whole thing before ANY delete is issued —
// refusing after detaching half a shared gateway would be worse than the bug.
func TestDeleteTransitGateway_ForeignConnectionDetachesNothing(t *testing.T) {
	f := &tgwFake{
		remaining: 99,
		connsJSON: `{"connections":[{"id":"c2","name":"conn-shared","network_type":"vpc","network_id":"` + foreignVPCCRN + `","status":"attached"}]}`,
	}
	c := newTestClient(f.roundTrip)

	err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if err == nil {
		t.Fatal("a gateway attached to a network outside the sweep must be refused")
	}
	for _, call := range f.sequence() {
		if strings.HasPrefix(call, "DELETE") {
			t.Fatalf("refusal must issue no deletes at all, got %v", f.sequence())
		}
	}
}

// The wait is bounded. A connection that never clears has to surface as a clear
// timeout rather than hanging a cleanup run forever.
func TestDeleteTransitGateway_BoundedWait(t *testing.T) {
	prevPoll, prevTimeout := tgwConnectionPollInterval, tgwConnectionSettleTimeout
	tgwConnectionPollInterval, tgwConnectionSettleTimeout = time.Millisecond, 20*time.Millisecond
	defer func() { tgwConnectionPollInterval, tgwConnectionSettleTimeout = prevPoll, prevTimeout }()

	f := &tgwFake{
		remaining: 1 << 30, // never clears
		connsJSON: `{"connections":[{"id":"c1","network_type":"vpc","network_id":"` + sweptVPCCRN + `","status":"deleting"}]}`,
	}
	c := newTestClient(f.roundTrip)

	err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("a connection that never clears must time out, got %v", err)
	}
}

// A gateway with nothing attached is deleted directly — the common re-run case.
func TestDeleteTransitGateway_NoConnections(t *testing.T) {
	f := &tgwFake{remaining: 0}
	c := newTestClient(f.roundTrip)
	if err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", nil); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	for _, call := range f.sequence() {
		if strings.Contains(call, "/connections/") {
			t.Fatalf("nothing to detach, yet a connection delete was issued: %v", f.sequence())
		}
	}
}
