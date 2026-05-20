package cos

// Sprint 18 Issue 3 hermetic test — pins the per-bucket region
// resolution that the manual-testing 404 surfaced (`cos object list
// <bucket-in-us-south> --instance <inst-in-default-rg>` hit the
// `ca-tor` S3 endpoint instead of the bucket's actual `us-south`
// home, returning 404 from a populated bucket).
//
// Additive — no edits to any pre-existing `_test.go` (Sprint 18
// round-1 acceptance criterion #1 parity discipline).

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/IBM/ibm-cos-sdk-go/aws"
)

// TestClient_PerBucketRegion_FakeResolver_Issue3AC2 pins the central
// claim of Issue 3's fix: when a `BucketRegionResolver` reports that a
// bucket lives in a region different from the Client's home region,
// the S3 handle the Client builds for that bucket targets the resolved
// region's endpoint (`s3.us-south.cloud-object-storage.appdomain.cloud`),
// NOT the home region's (`s3.ca-tor.cloud-object-storage.appdomain.cloud`).
//
// This is the assertion Issue 3 acceptance criterion #2 specifies
// ("given a fake resource-controller that reports the bucket lives in
// us-south, the COS S3 client is constructed against the us-south
// endpoint, not the workspace's cluster region").
func TestClient_PerBucketRegion_FakeResolver_Issue3AC2(t *testing.T) {
	const (
		homeRegion   = "ca-tor"
		bucketRegion = "us-south"
		bucketName   = "in-us-south"
	)

	fakeResolver := func(_ context.Context, instanceCRN, bucket string) (string, error) {
		if bucket != bucketName {
			return "", fmt.Errorf("unexpected bucket %q", bucket)
		}
		if instanceCRN == "" {
			return "", fmt.Errorf("resolver received empty instanceCRN")
		}
		return bucketRegion, nil
	}

	c, err := NewWithResolver("test-apikey", homeRegion, "crn:v1:bluemix:public:cloud-object-storage:global:a/x:fake::", fakeResolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}

	sv, err := c.s3ForBucket(context.Background(), bucketName)
	if err != nil {
		t.Fatalf("s3ForBucket(%q): %v", bucketName, err)
	}

	gotEndpoint := aws.StringValue(sv.Client.Config.Endpoint)
	wantInEndpoint := "s3." + bucketRegion + "."
	if !strings.Contains(gotEndpoint, wantInEndpoint) {
		t.Fatalf("Issue 3 AC #2: s3ForBucket(%q) endpoint = %q; want it to contain %q (the bucket's resolved region, not home %q)",
			bucketName, gotEndpoint, wantInEndpoint, homeRegion)
	}
	homeMarker := "s3." + homeRegion + "."
	if strings.Contains(gotEndpoint, homeMarker) {
		t.Fatalf("Issue 3 AC #2: s3ForBucket(%q) endpoint still contains the home region marker %q — the fix did not take effect; got %q",
			bucketName, homeMarker, gotEndpoint)
	}

	gotRegion := aws.StringValue(sv.Client.Config.Region)
	if gotRegion != bucketRegion {
		t.Fatalf("Issue 3: per-bucket S3 handle region = %q; want %q", gotRegion, bucketRegion)
	}
}

// TestClient_PerBucketRegion_NilResolver_HomeRegionFallback pins the
// pre-Sprint-18 behaviour preservation: when no resolver is wired
// (the round-1 code path, and the production path when the operator
// hasn't configured a real lookup), s3ForBucket falls back to the
// home region — so workspaces where the bucket and cluster share a
// region keep working byte-identically.
func TestClient_PerBucketRegion_NilResolver_HomeRegionFallback(t *testing.T) {
	const homeRegion = "ca-tor"
	c, err := New("test-apikey", homeRegion, "crn:v1:bluemix:public:cloud-object-storage:global:a/x:fake::")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sv, err := c.s3ForBucket(context.Background(), "any-bucket")
	if err != nil {
		t.Fatalf("s3ForBucket: %v", err)
	}
	if got := aws.StringValue(sv.Client.Config.Region); got != homeRegion {
		t.Fatalf("nil-resolver fallback: region = %q; want home %q", got, homeRegion)
	}
}

// TestClient_PerBucketRegion_ResolverErrorMessage_Issue3AC3 pins the
// error-message clarity AC: a region-lookup failure must surface the
// bucket name so the operator can distinguish "wrong region" from
// "wrong bucket name" (Issue 3 AC #3).
func TestClient_PerBucketRegion_ResolverErrorMessage_Issue3AC3(t *testing.T) {
	failResolver := func(_ context.Context, _, _ string) (string, error) {
		return "", fmt.Errorf("simulated lookup failure")
	}
	c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", failResolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}
	_, err = c.s3ForBucket(context.Background(), "elsewhere")
	if err == nil {
		t.Fatal("expected an error when the resolver fails; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "elsewhere") {
		t.Fatalf("Issue 3 AC #3: error message must name the bucket so wrong-region vs wrong-bucket are distinguishable; got %q", msg)
	}
}

// TestClient_PerBucketRegion_CacheHit pins the once-per-bucket lookup
// claim from Issue 3 ("the per-bucket region lookup runs once per CLI
// invocation, not once per object iteration"). A counter wrapped around
// the resolver verifies the second-and-subsequent s3ForBucket calls
// for the same bucket do not re-enter the resolver.
func TestClient_PerBucketRegion_CacheHit(t *testing.T) {
	var calls int
	countingResolver := func(_ context.Context, _, _ string) (string, error) {
		calls++
		return "us-south", nil
	}
	c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", countingResolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := c.s3ForBucket(context.Background(), "same-bucket"); err != nil {
			t.Fatalf("s3ForBucket iter %d: %v", i, err)
		}
	}
	if calls != 1 {
		t.Fatalf("resolver called %d times across 5 same-bucket lookups; want 1 (cache should hit after the first)", calls)
	}
}
