package cos

// Sprint 18 Issue 2 round-4 hermetic test — pins the parallel-probe
// shape of DefaultBucketRegionResolver. Round-2 shipped a sequential
// HeadBucket sweep across the candidate list; that was correct but
// measured 89s wall-clock against a cross-region bucket (Issue 2 AC #1
// wants ≤2× baseline of ~1.4s). Round-3's "swap to GetBucketLocation
// against the home endpoint" was reverted at commit 39a9af5 after live
// verify RED: IBM COS's S3 API is endpoint-scoped, so a home-region
// GetBucketLocation 404s for a non-home bucket.
//
// Round-4 chose approach (d) — fan-out HeadBucket probes concurrently
// across every candidate region — mirroring the official
// `ibmcloud cos bucket-location-get` design
// (github.com/IBM/ibmcloud-cos-cli, functions/bucket_class_location.go:
// `getBucketLocationCoordinator` runs one goroutine per known region,
// first non-error wins).
//
// The test injects a fake probe via the unexported `bucketRegionProbeFn`
// seam so no network I/O is exercised. Additive — no edits to any
// pre-existing `_test.go` (round-1 / round-2 invariants stay
// byte-unchanged and keep passing).

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// withFakeProbe swaps in a fake probe for the duration of `body`,
// restoring the production probe afterward. Centralised here so each
// test below stays focused on what it actually asserts.
func withFakeProbe(t *testing.T, fake func(ctx context.Context, c *Client, probeRegion, bucket string) (string, bool), body func()) {
	t.Helper()
	orig := bucketRegionProbeFn
	bucketRegionProbeFn = fake
	defer func() { bucketRegionProbeFn = orig }()
	body()
}

// TestDefaultResolver_CrossRegion_SinglePerBucketCall pins the central
// claim of round-4's fix: when the bucket lives in a region other than
// the Client's home, the resolver fans out probes in parallel and
// reports the resolved region exactly once. The fake probe counts
// per-region invocations; for a single resolver call on bucket "b" we
// expect to see at most one probe per candidate region (concurrent
// fan-out is fine — but no goroutine re-issues a probe for the same
// region within a single call).
func TestDefaultResolver_CrossRegion_SinglePerBucketCall(t *testing.T) {
	const (
		homeRegion   = "ca-tor"
		bucketRegion = "us-south"
		bucketName   = "bnk-schematics-resources"
	)

	var (
		mu          sync.Mutex
		perRegion   = map[string]int{}
		totalProbes int32
	)
	fakeProbe := func(ctx context.Context, _ *Client, probeRegion, bucket string) (string, bool) {
		atomic.AddInt32(&totalProbes, 1)
		mu.Lock()
		perRegion[probeRegion]++
		mu.Unlock()
		if bucket != bucketName {
			return "", false
		}
		// Simulate the cross-region case: only the us-south goroutine
		// returns a successful match. Every other goroutine reports
		// "not here" (ok=false). The home-region (ca-tor) goroutine
		// MUST report ok=false too — that's the round-3 lesson.
		if probeRegion == bucketRegion {
			return bucketRegion, true
		}
		return "", false
	}

	withFakeProbe(t, fakeProbe, func() {
		c, err := NewWithResolver("test-apikey", homeRegion, "crn:v1:bluemix:public:cloud-object-storage:global:a/x:fake::", nil)
		if err != nil {
			t.Fatalf("NewWithResolver: %v", err)
		}
		// Attach the default resolver (under test) and route through the
		// normal s3ForBucket → regionFor → resolver path so this test
		// exercises the same call chain production hits.
		c.WithResolver(DefaultBucketRegionResolver(c))

		sv, err := c.s3ForBucket(context.Background(), bucketName)
		if err != nil {
			t.Fatalf("s3ForBucket(%q): %v", bucketName, err)
		}
		// Sanity: the resolved region is the cross-region one, and the
		// S3 handle's endpoint points at it (NOT the home region — that
		// would be the round-3 regression).
		gotRegion := *sv.Client.Config.Region
		if gotRegion != bucketRegion {
			t.Fatalf("resolved region = %q; want %q", gotRegion, bucketRegion)
		}
		gotEndpoint := *sv.Client.Config.Endpoint
		if !strings.Contains(gotEndpoint, "s3."+bucketRegion+".") {
			t.Fatalf("S3 handle endpoint = %q; want substring %q", gotEndpoint, "s3."+bucketRegion+".")
		}
		if strings.Contains(gotEndpoint, "s3."+homeRegion+".") {
			t.Fatalf("S3 handle endpoint still wires the home region %q (round-3 regression shape); got %q", homeRegion, gotEndpoint)
		}
	})

	// One probe per candidate region, no duplicates within a single
	// resolver call. (The candidate list is home + fallbackRegions
	// minus home-dup; we don't pin the exact count here because the
	// fallback list is a tuning knob, but we DO pin the "no region
	// probed twice" invariant.)
	mu.Lock()
	defer mu.Unlock()
	for region, n := range perRegion {
		if n > 1 {
			t.Fatalf("region %q probed %d times in a single resolver call; want at most 1 (parallel fan-out must not re-issue probes per region)", region, n)
		}
	}
	if perRegion[bucketRegion] != 1 {
		t.Fatalf("expected exactly 1 probe to the winning region %q; got %d", bucketRegion, perRegion[bucketRegion])
	}
	if perRegion[homeRegion] != 1 {
		t.Fatalf("expected exactly 1 probe to the home region %q (home is included in the candidate list); got %d", homeRegion, perRegion[homeRegion])
	}
}

// TestDefaultResolver_ParallelFastWallClock pins the latency claim that
// distinguishes round-4 (parallel) from round-2 (sequential). Each fake
// probe sleeps 100ms before responding; if the resolver fans out in
// parallel, the wall-clock for a resolver call is ~100ms, not
// ~N*100ms. We assert the resolver returns in under 400ms (generous
// CI-tolerant ceiling) — a sequential 8-candidate implementation
// would take ≥800ms and fail this bound.
func TestDefaultResolver_ParallelFastWallClock(t *testing.T) {
	const (
		homeRegion   = "ca-tor"
		bucketRegion = "us-south"
		probeLatency = 100 * time.Millisecond
	)
	fakeProbe := func(ctx context.Context, _ *Client, probeRegion, _ string) (string, bool) {
		select {
		case <-time.After(probeLatency):
		case <-ctx.Done():
			return "", false
		}
		if probeRegion == bucketRegion {
			return bucketRegion, true
		}
		return "", false
	}

	withFakeProbe(t, fakeProbe, func() {
		c, err := NewWithResolver("test-apikey", homeRegion, "crn:fake", nil)
		if err != nil {
			t.Fatalf("NewWithResolver: %v", err)
		}
		c.WithResolver(DefaultBucketRegionResolver(c))

		start := time.Now()
		if _, err := c.s3ForBucket(context.Background(), "anywhere"); err != nil {
			t.Fatalf("s3ForBucket: %v", err)
		}
		elapsed := time.Since(start)
		// Parallel ≈ max(per-region) = ~probeLatency. Allow 4× headroom
		// for scheduler jitter on a loaded CI box. A sequential
		// implementation across ~8 candidates would be ≥800ms.
		ceiling := 4 * probeLatency
		if elapsed > ceiling {
			t.Fatalf("resolver wall-clock = %v (probe latency = %v, %d-region fan-out); want ≤ %v — parallel fan-out is the load-bearing optimisation, sequential probing fails this bound", elapsed, probeLatency, len(candidateProbeRegions(homeRegion)), ceiling)
		}
	})
}

// TestDefaultResolver_NonexistentBucket_ErrorNamesBucket pins Issue 3
// AC #3 clarity preservation: when no candidate region resolves the
// bucket, the error message names the bucket so the operator can
// distinguish a region-lookup miss from a typo. The fake probe always
// returns ok=false; the resolver must surface the bucket name in the
// resulting error (wrapped through regionFor, the message also gets
// the instance CRN for additional context).
func TestDefaultResolver_NonexistentBucket_ErrorNamesBucket(t *testing.T) {
	fakeProbe := func(_ context.Context, _ *Client, _, _ string) (string, bool) {
		return "", false
	}
	withFakeProbe(t, fakeProbe, func() {
		c, err := NewWithResolver("test-apikey", "ca-tor", "crn:fake", nil)
		if err != nil {
			t.Fatalf("NewWithResolver: %v", err)
		}
		c.WithResolver(DefaultBucketRegionResolver(c))
		_, err = c.s3ForBucket(context.Background(), "ghost-bucket")
		if err == nil {
			t.Fatal("expected an error when no candidate region resolves the bucket; got nil")
		}
		msg := err.Error()
		if !strings.Contains(msg, "ghost-bucket") {
			t.Fatalf("Issue 3 AC #3: error must name the bucket so wrong-region vs wrong-bucket are distinguishable; got %q", msg)
		}
	})
}

// TestDefaultResolver_HintHeaderShortcut pins the best-effort
// `x-amz-bucket-region` shortcut: when IBM COS does volunteer the hint
// (rare in practice, but the production probe still honours it), the
// resolver should accept the hinted region even from a non-matching
// candidate probe. The fake here returns the hint from the *home*
// region probe, naming a remote region.
func TestDefaultResolver_HintHeaderShortcut(t *testing.T) {
	const (
		homeRegion = "ca-tor"
		hinted     = "eu-de"
	)
	fakeProbe := func(_ context.Context, _ *Client, probeRegion, _ string) (string, bool) {
		// Only the home-region probe returns ok=true, with the hinted
		// region (NOT its own region). Models the "COS returned
		// x-amz-bucket-region on the misdirected probe" code path.
		if probeRegion == homeRegion {
			return hinted, true
		}
		return "", false
	}
	withFakeProbe(t, fakeProbe, func() {
		c, err := NewWithResolver("test-apikey", homeRegion, "crn:fake", nil)
		if err != nil {
			t.Fatalf("NewWithResolver: %v", err)
		}
		c.WithResolver(DefaultBucketRegionResolver(c))
		sv, err := c.s3ForBucket(context.Background(), "any")
		if err != nil {
			t.Fatalf("s3ForBucket: %v", err)
		}
		if got := *sv.Client.Config.Region; got != hinted {
			t.Fatalf("expected resolved region to be the hinted %q; got %q", hinted, got)
		}
	})
}

// TestCandidateProbeRegions_HomeFirstNoDuplicates pins the candidate
// list shape: home region appears exactly once and is first. This is a
// small focused unit test on the helper because home-first dispatch
// affects the dispatch-ordering tail-latency claim in the closure.
func TestCandidateProbeRegions_HomeFirstNoDuplicates(t *testing.T) {
	got := candidateProbeRegions("us-south") // us-south is also in fallbackRegions
	if len(got) == 0 || got[0] != "us-south" {
		t.Fatalf("home region must be first in candidate list; got %v", got)
	}
	seen := map[string]int{}
	for _, r := range got {
		seen[r]++
	}
	for r, n := range seen {
		if n > 1 {
			t.Fatalf("region %q appears %d times in candidate list; want 1 (home must not be duplicated when also present in fallback list)", r, n)
		}
	}
}
