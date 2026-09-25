package ibm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IBM/go-sdk-core/v5/core"
)

// Mutation K1 for #302 — deleting the markProtected call from FindOrphans —
// SURVIVED every other test in this package, because they all call
// markProtected directly. A helper is not the product: the product is
// FindOrphans, and an adopted resource is only safe if the protection is
// applied on the path cleanup actually takes.
//
// So this drives FindOrphans itself through a canned transport. It is also the
// only coverage FindOrphans has.

type cannedTransport struct{ body map[string]string }

func (ct cannedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	reply := func(s string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewBufferString(s)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	}
	// IAM token exchange, so authedGET never reaches the network.
	if strings.Contains(r.URL.Host, "iam.cloud.ibm.com") {
		return reply(`{"access_token":"tok","token_type":"Bearer","expires_in":3600,"expiration":` +
			strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + `}`)
	}
	for suffix, body := range ct.body {
		if strings.Contains(r.URL.Path, suffix) {
			return reply(body)
		}
	}
	// Every other collection: an empty page. listVPCCollection looks the items
	// up by key, so a missing key yields none and the cursor loop ends.
	return reply(`{}`)
}

func newCannedClient(t *testing.T, bodies map[string]string) *Client {
	t.Helper()
	rt := cannedTransport{body: bodies}
	hc := &http.Client{Transport: rt}
	return &Client{
		apiKey:     "test-api-key",
		region:     "us-east",
		auth:       &core.IamAuthenticator{ApiKey: "test-api-key", Client: hc},
		httpClient: hc,
		identity:   &Identity{IAMID: "IBMid-TEST", AccountID: "acct-12345"},
	}
}

// The adopted VPC is discovered — it matches the prefix — and comes back marked
// Protected, from FindOrphans, not from a helper.
func TestFindOrphansMarksAdoptedResources(t *testing.T) {
	c := newCannedClient(t, map[string]string{
		"/v1/vpcs": `{"vpcs":[
			{"id":"r014-adopted","name":"sm-cli-client-vpc","crn":"crn:vpc:adopted"},
			{"id":"r014-ours","name":"sm-cli-cluster-vpc","crn":"crn:vpc:ours"}
		]}`,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := c.FindOrphans(ctx, SweepScope{
		Prefix:  "sm-cli",
		Regions: []string{"us-east"},
		Adopted: []AdoptedRef{{Kind: "vpc", Value: "sm-cli-client-vpc", Source: "resources.client_vpc.existing"}},
	})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}

	var adopted, ours *OrphanResource
	for i := range got {
		switch got[i].Name {
		case "sm-cli-client-vpc":
			adopted = &got[i]
		case "sm-cli-cluster-vpc":
			ours = &got[i]
		}
	}
	if adopted == nil || ours == nil {
		t.Fatalf("expected both VPCs discovered, got %+v", got)
	}
	if !adopted.Protected {
		t.Error("FindOrphans returned the ADOPTED VPC unprotected — cleanup would delete a VPC roksbnkctl never created. " +
			"The protection helper can be correct and still not be applied on this path.")
	}
	if adopted.ProtectedBy != "resources.client_vpc.existing" {
		t.Errorf("ProtectedBy = %q, want the config key", adopted.ProtectedBy)
	}
	if ours.Protected {
		t.Error("FindOrphans protected a VPC that is NOT adopted; the sweep would stop cleaning up after itself")
	}
}

// With no adoptions, FindOrphans must protect nothing — the fix must not turn
// cleanup into a no-op.
func TestFindOrphansProtectsNothingWithoutAdoptions(t *testing.T) {
	c := newCannedClient(t, map[string]string{
		"/v1/vpcs": `{"vpcs":[{"id":"r014-ours","name":"sm-cli-vpc","crn":"crn:vpc:ours"}]}`,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	got, err := c.FindOrphans(ctx, SweepScope{Prefix: "sm-cli", Regions: []string{"us-east"}})
	if err != nil {
		t.Fatalf("FindOrphans: %v", err)
	}
	found := false
	for _, o := range got {
		if o.Name == "sm-cli-vpc" {
			found = true
			if o.Protected {
				t.Error("protected a resource with an empty Adopted list")
			}
		}
	}
	if !found {
		t.Fatal("the unprotected VPC was not discovered at all; this guard would pass vacuously")
	}
}
