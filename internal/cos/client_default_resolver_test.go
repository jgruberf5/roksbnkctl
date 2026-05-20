package cos

// Sprint 18 Issue 2 round-3 hermetic tests — pin the GetBucketLocation
// single-call replacement of the round-2 HeadBucket probe sweep in
// `DefaultBucketRegionResolver`.
//
// Background: round-2 (commit 5a1b13b) shipped the COS client
// architecture fix but live verify on `cos object list
// bnk-schematics-resources --instance bnk-orchestration` measured ~89s
// wall-clock (vs ~1.4s for `ibmcloud cos objects` against the same
// bucket). The cost was in `DefaultBucketRegionResolver`'s
// HeadBucket-probe sweep — each missed candidate cost a full
// round-trip plus the SDK's retry/backoff. Round-3 swaps that for a
// single `s3:GetBucketLocation` call against the home-region S3
// endpoint (IBM COS answers from the home endpoint regardless of the
// bucket's true region — the call is instance-scoped via IAM).
//
// These tests are ADDITIVE and DO NOT edit any pre-existing _test.go
// in this package (round-1 / round-2 parity discipline still in force).
// They pin three invariants:
//
//  1. The new resolver issues `GetBucketLocation` exactly once per
//     bucket per Client invocation (the bucketRegions cache in
//     `regionFor` should subsume repeat lookups).
//  2. The `LocationConstraint` payload parser strips the
//     `-{standard,smart,vault,cold}` storage-class suffix to yield the
//     canonical IBM Cloud region.
//  3. A genuine `NoSuchBucket` from the home-region endpoint surfaces
//     an error that names BOTH the bucket and the instance CRN — so
//     the operator can distinguish "wrong bucket name" from "wrong
//     --instance" (Issue 3 AC #3's clarity goal stays satisfied).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

// newTestClientWithHomeEndpoint builds a *Client whose home-region S3
// handle points at the given URL (a test server). It bypasses the
// public `New` constructor because we need to redirect the home-region
// endpoint to the test server (which `New` hard-wires to the real IBM
// Cloud endpoint via `EndpointForRegion`).
//
// The Client is otherwise wired identically to a production-shape
// instance — shared *credentials.Credentials, regionalS3 cache
// pre-seeded with the home handle, bucketRegions empty — so the round-2
// `regionFor` cache + `s3ForBucket` flow exercise the new resolver
// exactly the way the real CLI does.
func newTestClientWithHomeEndpoint(t *testing.T, homeRegion, instanceCRN, endpointURL string) *Client {
	t.Helper()
	// Use static (non-IAM) creds so the SDK doesn't try to mint a real
	// IAM token against IBM Cloud during the test.
	creds := credentials.NewStaticCredentials("test-ak", "test-sk", "test-token")
	sess, err := session.NewSession()
	if err != nil {
		t.Fatalf("session.NewSession: %v", err)
	}
	conf := aws.NewConfig().
		WithRegion(homeRegion).
		WithEndpoint(endpointURL).
		WithCredentials(creds).
		WithS3ForcePathStyle(true).
		WithDisableSSL(true).
		WithMaxRetries(0) // fail-fast on any unexpected response from the test server
	c := &Client{
		region:      homeRegion,
		instanceCRN: instanceCRN,
		creds:       creds,
		s3:          s3.New(sess, conf),
	}
	c.regionalS3.Store(homeRegion, c.s3)
	return c
}

// locationXML composes the GetBucketLocation success-response body in
// the shape IBM COS / S3 returns. The SDK's bucket_location unmarshal
// pulls the value out via a `>...</Location` regex, so the exact tag
// suffix matters but the namespace declaration can be omitted in tests.
func locationXML(constraint string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">` +
		constraint +
		`</LocationConstraint>`
}

// noSuchBucketXML composes a NoSuchBucket error-response body that the
// SDK's restxml unmarshal recognises (sets awserr.Error.Code() ==
// s3.ErrCodeNoSuchBucket on the returned error).
func noSuchBucketXML(bucket string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Error>` +
		`<Code>NoSuchBucket</Code>` +
		`<Message>The specified bucket does not exist.</Message>` +
		`<BucketName>` + bucket + `</BucketName>` +
		`<RequestId>test-req-id</RequestId>` +
		`</Error>`
}

// TestDefaultResolver_GetBucketLocation_CalledOncePerBucket pins the
// central round-3 perf claim: the new resolver issues exactly ONE
// GetBucketLocation round-trip per bucket per Client invocation, even
// across many s3ForBucket calls for the same bucket. This is the
// hermetic gate that prevents a regression back to the round-2
// N-call HeadBucket sweep behaviour.
func TestDefaultResolver_GetBucketLocation_CalledOncePerBucket(t *testing.T) {
	const (
		bucketName   = "bnk-schematics-resources"
		bucketRegion = "us-south"
		homeRegion   = "ca-tor"
	)
	var locationCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetBucketLocation is GET /<bucket>?location
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s %s", r.Method, r.URL.String())
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if _, ok := r.URL.Query()["location"]; !ok {
			t.Errorf("expected ?location query; got %s", r.URL.RawQuery)
			http.Error(w, "missing ?location", http.StatusBadRequest)
			return
		}
		if !strings.Contains(r.URL.Path, bucketName) {
			t.Errorf("expected bucket %q in path; got %q", bucketName, r.URL.Path)
		}
		atomic.AddInt32(&locationCalls, 1)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(locationXML(bucketRegion + "-standard")))
	}))
	defer srv.Close()

	c := newTestClientWithHomeEndpoint(t, homeRegion, "crn:fake:instance", srv.URL)
	c.resolver = DefaultBucketRegionResolver(c)

	// Drive the resolver via the production code path (s3ForBucket →
	// regionFor → resolver). Call it 20 times for the same bucket; the
	// bucketRegions cache should subsume calls 2..20.
	for i := 0; i < 20; i++ {
		sv, err := c.s3ForBucket(context.Background(), bucketName)
		if err != nil {
			t.Fatalf("s3ForBucket iter %d: %v", i, err)
		}
		if got := aws.StringValue(sv.Client.Config.Region); got != bucketRegion {
			t.Fatalf("iter %d: resolved region = %q; want %q (LocationConstraint suffix not stripped?)",
				i, got, bucketRegion)
		}
	}

	if got := atomic.LoadInt32(&locationCalls); got != 1 {
		t.Fatalf("GetBucketLocation hit the wire %d times across 20 s3ForBucket calls; want exactly 1 (round-3 single-call resolver + round-2 bucketRegions cache)", got)
	}
}

// TestDefaultResolver_ParseLocationConstraint_StripsStorageClassSuffix
// pins the LocationConstraint parsing rule: IBM COS returns shapes
// like "us-south-standard" / "us-south-smart" / "us-south-vault" /
// "us-south-cold"; the parser must strip the storage-class suffix to
// yield the canonical IBM Cloud region the rest of the Client wires
// into endpoint URLs.
func TestDefaultResolver_ParseLocationConstraint_StripsStorageClassSuffix(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"us-south-standard", "us-south"},
		{"us-south-smart", "us-south"},
		{"us-south-vault", "us-south"},
		{"us-south-cold", "us-south"},
		{"ca-tor-standard", "ca-tor"},
		{"eu-de-smart", "eu-de"},
		{"jp-tok-vault", "jp-tok"},
		// Bare region — no storage-class suffix; passes through.
		{"us-south", "us-south"},
		{"eu-gb", "eu-gb"},
		// Defensive: whitespace trimmed.
		{"  us-south-standard  ", "us-south"},
		{"\tca-tor\n", "ca-tor"},
		// Empty payload — return empty so the caller can fall back to
		// the home region.
		{"", ""},
	}
	for _, tc := range cases {
		got := parseLocationConstraint(tc.raw)
		if got != tc.want {
			t.Errorf("parseLocationConstraint(%q) = %q; want %q", tc.raw, got, tc.want)
		}
	}
}

// TestDefaultResolver_NoSuchBucket_ErrorNamesBucket pins Issue 3 AC
// #3's clarity goal for the round-3 resolver: when IBM COS answers
// `NoSuchBucket` (404) the surfaced error MUST name the bucket the
// operator typed AND the instance CRN, so they can distinguish
// "wrong bucket name" from "wrong --instance". Without this the
// switch from HeadBucket-sweep ("not found in any probed region") to
// GetBucketLocation-single-call ("404") could regress the error
// message quality the round-2 hermetic tests already pin in
// client_region_test.go.
func TestDefaultResolver_NoSuchBucket_ErrorNamesBucket(t *testing.T) {
	const (
		bucketName  = "does-not-exist-anywhere"
		homeRegion  = "ca-tor"
		instanceCRN = "crn:v1:bluemix:public:cloud-object-storage:global:a/x:fake-instance::"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(noSuchBucketXML(bucketName)))
	}))
	defer srv.Close()

	c := newTestClientWithHomeEndpoint(t, homeRegion, instanceCRN, srv.URL)
	resolver := DefaultBucketRegionResolver(c)

	_, err := resolver(context.Background(), instanceCRN, bucketName)
	if err == nil {
		t.Fatal("expected an error from NoSuchBucket; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, bucketName) {
		t.Errorf("NoSuchBucket error must name the bucket (Issue 3 AC #3); got %q", msg)
	}
	if !strings.Contains(msg, instanceCRN) {
		t.Errorf("NoSuchBucket error must name the instance CRN so wrong-bucket vs wrong-instance is distinguishable; got %q", msg)
	}
}

// TestDefaultResolver_BareRegionConstraint_PassesThrough pins the
// other production payload shape: cross-region buckets (and some older
// IBM COS buckets) return a bare region string with no storage-class
// suffix in LocationConstraint. The resolver must pass that through
// unchanged so the per-region S3-handle cache wires the correct
// endpoint.
func TestDefaultResolver_BareRegionConstraint_PassesThrough(t *testing.T) {
	const (
		bucketName   = "older-bucket"
		bucketRegion = "eu-de"
		homeRegion   = "ca-tor"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(locationXML(bucketRegion))) // bare region, no -standard suffix
	}))
	defer srv.Close()

	c := newTestClientWithHomeEndpoint(t, homeRegion, "crn:fake", srv.URL)
	resolver := DefaultBucketRegionResolver(c)

	got, err := resolver(context.Background(), "crn:fake", bucketName)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got != bucketRegion {
		t.Fatalf("bare-region LocationConstraint: resolver returned %q; want %q", got, bucketRegion)
	}
}

// Compile-time guard that the resolver returned by
// DefaultBucketRegionResolver still satisfies the BucketRegionResolver
// signature the round-2 Client struct (and its hermetic tests) pin.
// Defensive — keeps a refactor that changes the function shape from
// silently breaking the round-2 NewWithResolver wiring.
var _ BucketRegionResolver = DefaultBucketRegionResolver(&Client{})
