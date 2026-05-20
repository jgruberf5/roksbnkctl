package cos

// Sprint 18 Issue 2 hermetic test — pins the "client constructed
// exactly once per invocation" claim that the manual-testing 10× perf
// gap surfaced. The root cause Issue 2 names is IAM token re-fetch per
// call AND fresh-client-per-call; the round-2 fix puts a shared
// `*credentials.Credentials` on the Client struct (one IAM token per
// invocation) and a per-region S3 handle cache (one regional client
// per region, not per object). This test pins both via the
// `newCallCount` atomic + the regional-handle map invariant.
//
// Additive — no edits to any pre-existing `_test.go` (Sprint 18
// round-1 acceptance criterion #1 parity discipline).

import (
	"context"
	"testing"
)

// TestClient_SingleConstruction_PerInvocation_Issue2AC2 pins Issue 2
// acceptance criterion #2 ("a single roksbnkctl invocation constructs
// the COS client exactly once — no re-construction per verb / per
// object-iteration page"). We snapshot newCallCount, simulate a
// representative recursive `cos bucket get` workflow that touches the
// same bucket many times across two regions, and re-snapshot — the
// delta must be exactly 1 (the one `New()` call), not N.
func TestClient_SingleConstruction_PerInvocation_Issue2AC2(t *testing.T) {
	resolver := func(_ context.Context, _, bucket string) (string, error) {
		// Two different buckets land in two different regions —
		// exercises the per-region S3-handle cache too.
		if bucket == "in-us-south" {
			return "us-south", nil
		}
		return "ca-tor", nil
	}

	before := NewCallCount()
	c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", resolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}

	// Simulate `cos bucket get` over a bucket with 50 objects, then a
	// follow-on `cos object list` against a same-instance bucket in
	// the home region. Both code paths funnel through s3ForBucket.
	for i := 0; i < 50; i++ {
		if _, err := c.s3ForBucket(context.Background(), "in-us-south"); err != nil {
			t.Fatalf("s3ForBucket(in-us-south) iter %d: %v", i, err)
		}
	}
	for i := 0; i < 50; i++ {
		if _, err := c.s3ForBucket(context.Background(), "at-home"); err != nil {
			t.Fatalf("s3ForBucket(at-home) iter %d: %v", i, err)
		}
	}

	after := NewCallCount()
	delta := after - before
	if delta != 1 {
		t.Fatalf("Issue 2 AC #2: across 100 s3ForBucket calls on one Client, %d Client constructions ran; want exactly 1 (only the New)", delta)
	}
}

// TestClient_RegionalCacheReuse_Issue2 pins the per-region handle
// reuse: across many s3ForBucket calls for buckets in the same
// region, the Client builds exactly one regional S3 handle for that
// region (not N). The cheap invariant — same pointer is returned —
// proves the SDK's `s3.New` (which builds a fresh session/handle pair)
// is not being called per call site.
func TestClient_RegionalCacheReuse_Issue2(t *testing.T) {
	resolver := func(_ context.Context, _, _ string) (string, error) { return "us-south", nil }
	c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", resolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}
	sv1, err := c.s3ForBucket(context.Background(), "bucket-one")
	if err != nil {
		t.Fatalf("s3ForBucket(bucket-one): %v", err)
	}
	sv2, err := c.s3ForBucket(context.Background(), "bucket-two")
	if err != nil {
		t.Fatalf("s3ForBucket(bucket-two): %v", err)
	}
	// Different buckets, both resolved to us-south → must share the
	// region's S3 handle (same pointer). Otherwise we are re-building
	// per-bucket and the perf claim is hollow.
	if sv1 != sv2 {
		t.Fatalf("Issue 2: two buckets resolved to the same region returned different S3 handles; the regional cache is not reusing entries")
	}
}

// TestClient_SharedCreds_OneIAMAuthenticator pins the cross-region
// IAM-credentials-sharing claim from Issue 2 (the underlying IAM
// bearer is fetched once and reused for the lifetime of the Client,
// even when buckets across different regions are touched). The cheap
// invariant — both regional handles point at the same
// `*credentials.Credentials` — proves the SDK's IAM authenticator is
// not being re-constructed per region.
func TestClient_SharedCreds_OneIAMAuthenticator(t *testing.T) {
	resolver := func(_ context.Context, _, bucket string) (string, error) {
		if bucket == "in-us-south" {
			return "us-south", nil
		}
		return "ca-tor", nil
	}
	c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", resolver)
	if err != nil {
		t.Fatalf("NewWithResolver: %v", err)
	}
	svUS, err := c.s3ForBucket(context.Background(), "in-us-south")
	if err != nil {
		t.Fatalf("s3ForBucket(in-us-south): %v", err)
	}
	svCA, err := c.s3ForBucket(context.Background(), "at-home")
	if err != nil {
		t.Fatalf("s3ForBucket(at-home): %v", err)
	}
	// Both handles must wire the SAME `*credentials.Credentials`
	// object (identity, not value). If they differ, the IAM
	// authenticator was rebuilt per region and the 10× perf claim
	// reopens.
	if svUS.Client.Config.Credentials != svCA.Client.Config.Credentials {
		t.Fatalf("Issue 2: regional S3 handles wire different *credentials.Credentials objects; the IAM authenticator is being re-constructed per region")
	}
	if svUS.Client.Config.Credentials != c.creds {
		t.Fatalf("Issue 2: per-region S3 handle's credentials object differs from the Client's shared creds")
	}
}
