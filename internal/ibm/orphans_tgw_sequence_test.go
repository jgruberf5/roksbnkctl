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

// Regression for a bug introduced by the #85 fix itself and caught in review.
//
// Surfacing connection-delete errors (right) combined with re-deleting a
// connection already in `deleting` (wrong) made a self-healing case fatal: IBM
// rejects the redundant DELETE, authedDELETE forgives only 404, and the whole
// gateway delete aborts — for a connection that was about to vanish anyway.
// The old fire-and-forget code discarded that error, so this would have been a
// REGRESSION shipped inside a fix.
func TestDeleteTransitGateway_DoesNotRedeleteASettlingConnection(t *testing.T) {
	prev := tgwConnectionPollInterval
	tgwConnectionPollInterval = time.Millisecond
	defer func() { tgwConnectionPollInterval = prev }()

	f := &tgwFake{
		remaining: 2,
		connsJSON: `{"connections":[{"id":"c1","name":"already-going","network_type":"vpc","network_id":"` + sweptVPCCRN + `","status":"deleting"}]}`,
	}
	// Reject any DELETE aimed at a connection the way IBM rejects a redundant
	// one: a 409, which is NOT forgiven.
	base := f.roundTrip
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/connections/") {
			return jsonResp(409, `{"errors":[{"code":"conflict","message":"connection is already being deleted"}]}`), nil
		}
		return base(r)
	})

	if err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN))); err != nil {
		t.Fatalf("a connection already deleting must be waited for, not deleted again: %v", err)
	}
	for _, call := range f.sequence() {
		if strings.HasPrefix(call, "DELETE") && strings.Contains(call, "/connections/") {
			t.Fatalf("issued a redundant connection DELETE: %v", f.sequence())
		}
	}
}

// A gateway with nothing attached must not be polled at all — the wait exists
// to cover an async detach, and there was none.
func TestDeleteTransitGateway_NoConnectionsSkipsTheWait(t *testing.T) {
	f := &tgwFake{remaining: 0}
	c := newTestClient(f.roundTrip)
	if err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", nil); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	gets := 0
	for _, call := range f.sequence() {
		if strings.HasPrefix(call, "GET") {
			gets++
		}
	}
	if gets != 1 {
		t.Errorf("expected the single initial listing and no polling, got %v", f.sequence())
	}
}

// cleanup scans only the workspace's cluster and client regions by default, so
// the likeliest reason a VPC looks foreign is that it IS yours and simply was
// not scanned. Without this hint the operator is told to go detach their own
// network by hand.
func TestForeignRefusalSuggestsWideningTheRegionSweep(t *testing.T) {
	f := &tgwFake{
		remaining: 99,
		connsJSON: `{"connections":[{"id":"c2","name":"conn-shared","network_type":"vpc","network_id":"` + foreignVPCCRN + `","status":"attached"}]}`,
	}
	c := newTestClient(f.roundTrip)
	err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN)))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"--all-regions", "--region", "ibmcloud tg connection-delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q; got: %v", want, err)
		}
	}
}

// DeleteOrphan is documented idempotent. A gateway already gone — a re-run, or
// a concurrent sweep — makes the connection listing fail; that must still
// resolve to success via the delete's own 404, not surface as an error. The
// code this replaced got this right by swallowing the listing error, so
// tightening the error handling could have quietly regressed it.
func TestDeleteTransitGateway_AlreadyGoneIsSuccess(t *testing.T) {
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		return jsonResp(404, `{"errors":[{"code":"not_found","message":"gateway not found"}]}`), nil
	})
	if err := c.deleteTransitGateway(context.Background(), "gw-gone", "f5orph-tgw", nil); err != nil {
		t.Fatalf("an already-deleted gateway must be a no-op, got %v", err)
	}
}

// But an unreadable listing must not become a silent success when the gateway
// is genuinely still there and still attached: both failures get reported.
func TestDeleteTransitGateway_UnreadableListingAndFailedDeleteReportsBoth(t *testing.T) {
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		if r.Method == http.MethodGet {
			return jsonResp(503, `{"errors":[{"code":"service_unavailable"}]}`), nil
		}
		return jsonResp(412, `{"errors":[{"code":"precondition_failed","message":"Before you can delete this gateway, you must delete all attached connections."}]}`), nil
	})
	err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", nil)
	if err == nil {
		t.Fatal("a gateway that could not be listed OR deleted must not report success")
	}
	for _, want := range []string{"could not list connections", "503", "412"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should surface %q; got: %v", want, err)
		}
	}
}

// Regression for #87, reproducing the reported failure: a connection still
// `pending` at sweep time. IBM rejects a DELETE from that state with
// 409 invalid_state, which is what the reporter saw. The sweep must WAIT for it
// to finish attaching, then detach — not fire immediately and tell the operator
// to re-run into the identical failure.
func TestDeleteTransitGateway_WaitsOutAPendingConnection(t *testing.T) {
	prev := tgwConnectionPollInterval
	tgwConnectionPollInterval = time.Millisecond
	defer func() { tgwConnectionPollInterval = prev }()

	pending := `{"connections":[{"id":"c1","name":"f5orph-conn","network_type":"vpc","network_id":"` + sweptVPCCRN + `","status":"pending"}]}`
	attached := `{"connections":[{"id":"c1","name":"f5orph-conn","network_type":"vpc","network_id":"` + sweptVPCCRN + `","status":"attached"}]}`

	var mu sync.Mutex
	gets := 0
	calls := []string{}
	c := newTestClient(func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/connections"):
			gets++
			switch {
			case gets <= 2:
				return jsonResp(200, pending), nil // still attaching
			case gets <= 4:
				return jsonResp(200, attached), nil // settled — now deletable
			}
			return jsonResp(200, `{"connections":[]}`), nil // detached
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/connections/"):
			mu.Unlock()
			mu.Lock()
			// IBM's actual refusal while pending — if the fix regresses and the
			// DELETE fires early, this is what comes back.
			if gets <= 2 {
				return jsonResp(409, `{"errors":[{"code":"invalid_state","message":"You cannot delete a connection that shows 'pending' status."}]}`), nil
			}
			return jsonResp(204, ""), nil
		case r.Method == http.MethodDelete:
			return jsonResp(204, ""), nil
		}
		return jsonResp(404, `{}`), nil
	})

	if err := c.deleteTransitGateway(context.Background(), "gw-1", "f5orph-tgw", sweptVPCCRNs(sweepWithVPC(sweptVPCCRN))); err != nil {
		t.Fatalf("a pending connection must be waited out, not deleted early: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	// The connection DELETE must come after at least one re-read, i.e. after the
	// wait actually observed the transition out of `pending`.
	firstDelete := -1
	getsBefore := 0
	for i, call := range calls {
		if strings.HasPrefix(call, "GET") && firstDelete < 0 {
			getsBefore++
		}
		if strings.HasPrefix(call, "DELETE") && strings.Contains(call, "/connections/") && firstDelete < 0 {
			firstDelete = i
		}
	}
	if firstDelete < 0 {
		t.Fatalf("the connection was never detached: %v", calls)
	}
	if getsBefore < 2 {
		t.Fatalf("detached without waiting for the pending transition (%d listings before the delete): %v", getsBefore, calls)
	}
}
