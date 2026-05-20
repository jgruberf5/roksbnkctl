package cos

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/IBM/ibm-cos-sdk-go/aws"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials"
	"github.com/IBM/ibm-cos-sdk-go/aws/credentials/ibmiam"
	"github.com/IBM/ibm-cos-sdk-go/aws/session"
	"github.com/IBM/ibm-cos-sdk-go/service/s3"
)

// iamTokenURL is IBM Cloud's IAM token exchange endpoint. Public; not
// region-specific. The COS S3 SDK uses this to swap an API key for a
// short-lived bearer token on each call.
const iamTokenURL = "https://iam.cloud.ibm.com/identity/token"

// BucketRegionResolver looks up the IBM Cloud region the named bucket
// physically lives in (e.g. "us-south") for a given COS instance CRN.
// Sprint 18 Issue 3 introduces this seam so the S3 client used for
// per-bucket operations targets the bucket's actual regional endpoint,
// not the workspace cluster's region (which 404s when the bucket lives
// elsewhere).
//
// Production callers wire this to a real IBM Cloud lookup (HeadBucket
// against the home region + 301/Location parse, or a probe across the
// public region list). Tests wire it to an in-memory fake. nil is
// acceptable on the Client and falls back to the workspace's home
// region (preserves pre-Sprint-18 behaviour for same-region buckets).
type BucketRegionResolver func(ctx context.Context, instanceCRN, bucket string) (string, error)

// newCallCount is incremented every time New / NewWithResolver build a
// *Client. Sprint 18 Issue 2 acceptance criterion #2 asserts that a
// single roksbnkctl invocation constructs the COS client exactly once
// (no re-construction per verb / per object-iteration page); the
// hermetic test snapshots the counter, runs a representative call
// chain, and re-snapshots — delta must be 1.
//
// Exposed via NewCallCount so additive tests can read it without
// touching internal fields.
var newCallCount atomic.Int64

// NewCallCount returns the running tally of *Client constructions.
// Used by Sprint 18 hermetic perf tests; not exported for runtime use.
func NewCallCount() int64 { return newCallCount.Load() }

// Client wraps an IBM Cloud Object Storage S3 client. Bound to a single
// COS instance via IAM-scoped credentials; the instance's buckets may
// live in different regions, so the Client carries one home-region S3
// handle (used for instance-level operations: ListBuckets, CreateBucket
// in a default region, etc.) plus a per-region S3-handle cache built
// lazily as `s3ForBucket` resolves bucket→region pairs.
//
// One Client per command invocation (Sprint 18 Issue 2 — IAM
// authenticator is shared across all per-region handles so a single
// token serves the whole run).
type Client struct {
	region      string // "home" region: workspace cluster region; used for instance-level ops
	instanceCRN string

	// creds is the shared IBM-IAM credentials object. Every per-region
	// S3 handle the Client builds is constructed against this same
	// `*credentials.Credentials`, so the underlying IAM bearer token
	// is fetched once and re-used (until expiry, via the SDK's built-in
	// token caching) across every bucket-region the Client touches.
	// This is the per-invocation reuse Sprint 18 Issue 2 demands.
	creds *credentials.Credentials

	// s3 is the home-region S3 handle. Sprint 18 keeps this field name
	// stable because internal/cos/bucket.go calls into it for
	// instance-level operations (ListBuckets, CreateBucket, DeleteBucket)
	// and that file is out of scope for this fix per the round-2 prompt.
	s3 *s3.S3

	// regionalS3 caches per-region S3 handles. Keyed on region string
	// ("us-south", "ca-tor", …). Built lazily by s3ForBucket the first
	// time the Client needs to talk to a bucket in that region; reused
	// thereafter so the recursive `cos bucket get` over N objects in
	// us-south makes exactly one regional client, not N of them.
	regionalS3 sync.Map // map[string]*s3.S3

	// bucketRegions caches the resolved region for each bucket name
	// (within this Client's instance scope). Sprint 18 Issue 3: the
	// per-bucket region lookup runs once per CLI invocation, not once
	// per object iteration. Keyed on bucket name (single-instance scope
	// means name uniqueness is sufficient).
	bucketRegions sync.Map // map[string]string

	// resolver is the seam that maps (instanceCRN, bucket) → region.
	// nil resolver = fall back to home region (preserves pre-Sprint-18
	// behaviour for workspaces where the bucket and the cluster share
	// a region). Tests inject a fake.
	resolver BucketRegionResolver
}

// New constructs a COS S3 client with a default region resolver. apiKey +
// instanceCRN authenticate via IBM IAM; region picks the regional S3
// endpoint that instance-level ops (ListBuckets, CreateBucket) hit by
// default. The client uses path-style addressing (S3ForcePathStyle)
// which IBM COS prefers.
//
// instanceCRN is required: COS S3 operations (including ListBuckets)
// scope by instance via the IAM credentials. A bare API key without a
// service instance ID would get "no buckets" with no way to discover them.
//
// The returned Client carries a default BucketRegionResolver that falls
// back to the home region when no better lookup is wired. Cross-region
// buckets get the proper endpoint as soon as a non-nil resolver is set
// via WithResolver (the production CLI wires this; tests inject a fake).
func New(apiKey, region, instanceCRN string) (*Client, error) {
	return NewWithResolver(apiKey, region, instanceCRN, nil)
}

// NewWithResolver is the explicit-resolver variant of New. Sprint 18
// Issue 3 hermetic tests use this to inject a fake region resolver so
// the test can prove the per-bucket S3 client is built against the
// resolved region, not the workspace's home region.
//
// resolver may be nil; nil → home-region fallback (same shape as
// pre-Sprint-18 behaviour).
func NewWithResolver(apiKey, region, instanceCRN string, resolver BucketRegionResolver) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("api key is empty")
	}
	if region == "" {
		return nil, fmt.Errorf("region is empty")
	}
	if instanceCRN == "" {
		return nil, fmt.Errorf("COS instance CRN is empty")
	}

	// Build the IAM credentials object once. Every per-region S3 handle
	// the Client constructs (home + lazy regional cache) is wired
	// against this same `*credentials.Credentials` value, so the
	// underlying IAM bearer token is fetched once and cached by the
	// SDK's IAM provider for the lifetime of the Client. Sprint 18
	// Issue 2: the perf gap vs `ibmcloud cos` was IAM round-trips per
	// op; sharing creds across regions closes it.
	creds := ibmiam.NewStaticCredentials(aws.NewConfig(), iamTokenURL, apiKey, instanceCRN)

	sess, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("creating COS session: %w", err)
	}

	homeConf := aws.NewConfig().
		WithRegion(region).
		WithEndpoint(EndpointForRegion(region)).
		WithCredentials(creds).
		WithS3ForcePathStyle(true)

	c := &Client{
		region:      region,
		instanceCRN: instanceCRN,
		creds:       creds,
		s3:          s3.New(sess, homeConf),
		resolver:    resolver,
	}
	// Pre-seed the regional cache with the home-region handle so
	// `s3ForBucket` short-circuits for the common same-region case.
	c.regionalS3.Store(region, c.s3)

	newCallCount.Add(1)
	return c, nil
}

// WithResolver replaces the Client's BucketRegionResolver. Returns the
// Client for chaining. Useful when the CLI wants to construct the
// Client first (cheap — no IAM call), then attach a real-network
// resolver built from the same IBM Cloud handles the rest of the
// command uses.
func (c *Client) WithResolver(r BucketRegionResolver) *Client {
	c.resolver = r
	return c
}

// Region returns the home region the Client was constructed with.
// Sprint 18: kept stable so the existing bucket.go callsite
// (LocationConstraint composition in CreateBucket) keeps compiling
// without an edit to that file.
func (c *Client) Region() string { return c.region }

// InstanceCRN returns the COS instance CRN the Client is scoped to.
// Exposed so the bucket-region resolver (which needs the CRN to phrase
// its lookup) can pull it off the Client without an extra arg.
func (c *Client) InstanceCRN() string { return c.instanceCRN }

// s3ForBucket returns the S3 handle whose endpoint targets the region
// where `bucket` physically lives. The first call for a given bucket
// consults the resolver (and caches the region); subsequent calls hit
// the in-memory cache and never make a lookup round-trip.
//
// If the resolver is nil OR returns "" without an error, the home
// region is used as a safe fallback (preserves pre-Sprint-18 behaviour
// for workspaces where bucket and cluster share a region).
//
// Errors from the resolver are wrapped with a hint that names the
// bucket (Issue 3 acceptance criterion #3: the operator should be able
// to distinguish "wrong region" from "wrong bucket name").
func (c *Client) s3ForBucket(ctx context.Context, bucket string) (*s3.S3, error) {
	if bucket == "" {
		return nil, fmt.Errorf("bucket name is empty")
	}

	region, err := c.regionFor(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if region == "" {
		region = c.region
	}

	if v, ok := c.regionalS3.Load(region); ok {
		return v.(*s3.S3), nil
	}

	conf := aws.NewConfig().
		WithRegion(region).
		WithEndpoint(EndpointForRegion(region)).
		WithCredentials(c.creds).
		WithS3ForcePathStyle(true)

	sess, err := session.NewSession()
	if err != nil {
		return nil, fmt.Errorf("creating COS session for region %s: %w", region, err)
	}
	cli := s3.New(sess, conf)
	// LoadOrStore so concurrent first-time lookups for the same region
	// converge on a single handle (sync.Map race-safe; the loser's
	// just-built handle gets GC'd).
	actual, _ := c.regionalS3.LoadOrStore(region, cli)
	return actual.(*s3.S3), nil
}

// regionFor consults the bucket→region cache, falling through to the
// resolver on a cache miss. Returns "" + nil error when no resolver is
// wired and the home-region fallback should apply.
func (c *Client) regionFor(ctx context.Context, bucket string) (string, error) {
	if v, ok := c.bucketRegions.Load(bucket); ok {
		return v.(string), nil
	}
	if c.resolver == nil {
		// No resolver → home-region fallback. Cache the empty result so
		// subsequent calls don't re-enter this branch.
		c.bucketRegions.Store(bucket, c.region)
		return c.region, nil
	}
	region, err := c.resolver(ctx, c.instanceCRN, bucket)
	if err != nil {
		// Surface the bucket name in the error so the operator can
		// distinguish a region-lookup failure from a wrong-bucket-name
		// (Issue 3 acceptance criterion #3).
		return "", fmt.Errorf("resolving region for bucket %q in instance %s: %w", bucket, c.instanceCRN, err)
	}
	if region == "" {
		region = c.region
	}
	c.bucketRegions.Store(bucket, region)
	return region, nil
}

// EndpointForRegion returns the cross-region public S3 endpoint URL for
// IBM Cloud Object Storage in a given region.
//
// For private (VPC-only) endpoints, the format is
// https://s3.private.{region}.cloud-object-storage.appdomain.cloud.
// Public is the v1 default.
func EndpointForRegion(region string) string {
	return fmt.Sprintf("https://s3.%s.cloud-object-storage.appdomain.cloud", region)
}

// LocationConstraint composes region + storage class into the
// LocationConstraint string IBM COS expects on bucket create.
//
//	us-south + standard → "us-south-standard"
//	ca-tor + smart      → "ca-tor-smart"
func LocationConstraint(region, class string) string {
	if class == "" {
		class = "standard"
	}
	return fmt.Sprintf("%s-%s", region, class)
}

// DefaultBucketRegionResolver is the production region resolver. It
// probes a small ordered list of candidate regions via the
// always-cheap HeadBucket S3 op, starting with the home region; the
// first region whose endpoint returns success (or returns a
// region-specific 301 redirect whose Location header names the actual
// region) wins.
//
// The Sprint 18 round-2 cut ships a minimal probe — home-region first,
// then a fixed fallback list of public IBM COS regions. The CLI wires
// this resolver at openCOSClient time; tests inject simpler fakes.
//
// The probe uses HeadBucket because (a) it's the cheapest S3 op that
// actually exercises the bucket's existence, (b) IBM COS returns the
// bucket's real region in `x-amz-bucket-region` even on a misdirected
// request, and (c) it requires no special permissions beyond what
// `cos object list` already needs.
func DefaultBucketRegionResolver(c *Client) BucketRegionResolver {
	return func(ctx context.Context, _ string, bucket string) (string, error) {
		// Try the home region first (single-region workspaces are the
		// majority case and avoid the cross-region probe entirely).
		if r, ok := probeBucketRegion(ctx, c, c.region, bucket); ok {
			return r, nil
		}
		// Fall back to a fixed ordered list of public IBM COS regions.
		// Order is rough popularity: us-south then us-east cover the
		// bulk of BNK installs; eu-* and ap-* round out the list.
		for _, candidate := range fallbackRegions {
			if candidate == c.region {
				continue
			}
			if r, ok := probeBucketRegion(ctx, c, candidate, bucket); ok {
				return r, nil
			}
		}
		return "", fmt.Errorf("bucket %q not found in any probed IBM COS region (instance %s)", bucket, c.instanceCRN)
	}
}

// fallbackRegions is the ordered probe list for the default resolver.
// Not exported because it's a tuning knob, not a public API.
var fallbackRegions = []string{
	"us-south", "us-east", "eu-de", "eu-gb", "ca-tor", "jp-tok", "au-syd", "br-sao",
}

// probeBucketRegion does a single HeadBucket against the region's
// endpoint. Returns the resolved region (which may differ from the
// probed region if IBM COS responded with an `x-amz-bucket-region`
// hint) and a boolean indicating success.
//
// The implementation is deliberately defensive: a 200 says "yes, here";
// a 301/400/403 with `x-amz-bucket-region` says "wrong region, try
// this one"; any other failure says "skip this candidate".
func probeBucketRegion(ctx context.Context, c *Client, probeRegion, bucket string) (string, bool) {
	// Build (or fetch from cache) the per-region S3 handle for the
	// probe. We intentionally bypass s3ForBucket here to avoid an
	// infinite loop (s3ForBucket calls regionFor calls resolver calls
	// probeBucketRegion).
	var cli *s3.S3
	if v, ok := c.regionalS3.Load(probeRegion); ok {
		cli = v.(*s3.S3)
	} else {
		conf := aws.NewConfig().
			WithRegion(probeRegion).
			WithEndpoint(EndpointForRegion(probeRegion)).
			WithCredentials(c.creds).
			WithS3ForcePathStyle(true)
		sess, err := session.NewSession()
		if err != nil {
			return "", false
		}
		cli = s3.New(sess, conf)
		actual, _ := c.regionalS3.LoadOrStore(probeRegion, cli)
		cli = actual.(*s3.S3)
	}

	req, _ := cli.HeadBucketRequest(&s3.HeadBucketInput{Bucket: aws.String(bucket)})
	req.SetContext(ctx)
	if err := req.Send(); err != nil {
		// Inspect the response for IBM COS's region hint. The header
		// is set by COS even on a 301/400 misdirection response.
		if req.HTTPResponse != nil {
			if hint := req.HTTPResponse.Header.Get("x-amz-bucket-region"); hint != "" {
				return strings.TrimSpace(hint), true
			}
		}
		return "", false
	}
	// HeadBucket 200 → the bucket exists in this region.
	return probeRegion, true
}
