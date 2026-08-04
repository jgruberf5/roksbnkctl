package ibm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IBM/go-sdk-core/v5/core"
)

// fakeRT serves canned responses for both the IAM token exchange and the raw-REST
// calls, so the REST layer is exercised without touching the network.
type fakeRT struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fakeRT) RoundTrip(r *http.Request) (*http.Response, error) { return f.fn(r) }

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

// iamToken is a canned IAM token response with a far-future expiry, so the
// authenticator caches it and a second call reuses it.
const iamToken = `{"access_token":"test-token","token_type":"Bearer","expires_in":3600,"expiration":9999999999}`

func newTestClient(fn func(*http.Request) (*http.Response, error)) *Client {
	hc := &http.Client{Transport: fakeRT{fn}}
	auth := &core.IamAuthenticator{ApiKey: "test-key"}
	auth.Client = hc // route the token exchange through the fake too
	return &Client{apiKey: "test-key", region: "us-south", auth: auth, httpClient: hc}
}

func isIAM(r *http.Request) bool { return strings.Contains(r.URL.Host, "iam.cloud.ibm.com") }

func TestListVPCs_PaginatesAndReusesToken(t *testing.T) {
	var tokenHits int32
	fn := func(r *http.Request) (*http.Response, error) {
		switch {
		case isIAM(r):
			atomic.AddInt32(&tokenHits, 1)
			return jsonResp(200, iamToken), nil
		case strings.Contains(r.URL.RawQuery, "start=page2"):
			return jsonResp(200, `{"vpcs":[{"id":"v3","name":"c"}]}`), nil
		case strings.Contains(r.URL.Path, "/v1/vpcs"):
			return jsonResp(200, `{"vpcs":[{"id":"v1","name":"a"},{"id":"v2","name":"b"}],`+
				`"next":{"href":"https://us-south.iaas.cloud.ibm.com/v1/vpcs?start=page2"}}`), nil
		}
		return jsonResp(404, `{}`), nil
	}
	c := newTestClient(fn)

	vpcs, err := c.ListVPCs(context.Background(), "us-south")
	if err != nil {
		t.Fatal(err)
	}
	if len(vpcs) != 3 {
		t.Fatalf("want 3 VPCs across 2 pages, got %d (%+v)", len(vpcs), vpcs)
	}
	if vpcs[2].Name != "c" {
		t.Errorf("page-2 VPC missing/wrong: %+v", vpcs)
	}
	// The IAM-reuse fix: two API calls (2 pages) must share ONE token exchange.
	if got := atomic.LoadInt32(&tokenHits); got != 1 {
		t.Errorf("expected 1 IAM token exchange across the paginated calls, got %d", got)
	}
}

func TestAuthToken_ReusesCachedToken(t *testing.T) {
	var hits int32
	fn := func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			atomic.AddInt32(&hits, 1)
			return jsonResp(200, iamToken), nil
		}
		return jsonResp(404, `{}`), nil
	}
	c := newTestClient(fn)
	for i := 0; i < 3; i++ {
		if _, err := c.authToken(); err != nil {
			t.Fatal(err)
		}
	}
	if hits != 1 {
		t.Errorf("3 authToken calls should share 1 IAM exchange, got %d", hits)
	}
}

func TestAuthedGET_Non2xxSurfacesStatusAndBody(t *testing.T) {
	fn := func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		return jsonResp(403, `{"message":"forbidden: quota"}`), nil
	}
	c := newTestClient(fn)
	_, err := c.authedGET(context.Background(), "https://us-south.iaas.cloud.ibm.com/v1/vpcs")
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("want a 403 error carrying the body, got %v", err)
	}
}

func TestAuthedDELETE_404IsIdempotent(t *testing.T) {
	fn := func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		return jsonResp(404, `{}`), nil
	}
	c := newTestClient(fn)
	if err := c.authedDELETE(context.Background(), "https://us-south.iaas.cloud.ibm.com/v1/subnets/x"); err != nil {
		t.Errorf("a 404 DELETE must be idempotent (nil error), got %v", err)
	}
}

func TestListSubnets_FiltersByVPCViaParsing(t *testing.T) {
	fn := func(r *http.Request) (*http.Response, error) {
		if isIAM(r) {
			return jsonResp(200, iamToken), nil
		}
		return jsonResp(200, `{"subnets":[`+
			`{"id":"s1","name":"a-subnet-zone1","vpc":{"id":"vpc-1"}},`+
			`{"id":"s2","name":"b-subnet-zone1","vpc":{"id":"vpc-2"}}]}`), nil
	}
	c := newTestClient(fn)
	subs, err := c.ListSubnets(context.Background(), "us-south")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 || subs[0].VPC.ID != "vpc-1" || subs[1].VPC.ID != "vpc-2" {
		t.Fatalf("subnet VPC ids not parsed: %+v", subs)
	}
}
