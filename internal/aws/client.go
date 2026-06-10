// Package aws wraps aws-sdk-go-v2 for awsbnkctl's cloud-side surface.
//
// One Clients struct per CLI invocation; constructed via NewClients with
// either a load-from-default-chain helper or an explicit region/profile.
// Tests inject mocks via the per-service interfaces (STSAPI, EC2API,
// EKSAPI, VPCAPI) — see *_test.go files for the patterns.
//
// The load-bearing contract: never log credentials. SDK constructors take
// `context.Context` so cancellation propagates from the cobra command
// surface; long-running listing calls (DescribeInstanceTypes etc.) honour
// it.
package aws

import (
	"context"
	"errors"
	"fmt"
	"os"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Clients bundles the per-service SDK handles awsbnkctl needs.
//
// Each field is an interface (defined alongside its consumer file —
// sts.go, ec2.go, eks.go, vpc.go) so tests inject a fake without
// requiring the real network-aware client. NewClients constructs the
// real clients via the standard credential resolution chain.
type Clients struct {
	// Region is the AWS region the clients were constructed for. Echoed
	// back in error messages so users debugging cross-region issues see
	// which region the SDK landed on.
	Region string

	// AWSConfig is the resolved aws-sdk-go-v2 Config — exposed so callers
	// that need a sub-client awsbnkctl doesn't wrap (e.g., S3)
	// can construct one without re-resolving credentials.
	AWSConfig awssdk.Config

	STS STSAPI
	EC2 EC2API
	EKS EKSAPI
	VPC VPCAPI

	// s3 / iam are lazily constructed on first use via EnsureS3 /
	// EnsureIAM. This keeps NewClients's cost low for the verbs that
	// don't touch S3 or IAM (most reads). Tests inject fakes by setting
	// these directly before invoking the consumer method.
	s3  S3API
	iam IAMAPI

	// serviceQuotas is the optional probe surface — lazily constructed
	// via EnsureServiceQuotas. Only the doctor's feature-flagged Service
	// Quotas row touches this, so the SDK's import cost is kept off
	// every other verb.
	serviceQuotas ServiceQuotasAPI
}

// SetS3ForTest lets tests inject a fake S3API. Production callers go
// through EnsureS3; this hook keeps test files independent of the
// (private) field name.
func (c *Clients) SetS3ForTest(api S3API) { c.s3 = api }

// SetIAMForTest mirrors SetS3ForTest for the IAM client.
func (c *Clients) SetIAMForTest(api IAMAPI) { c.iam = api }

// Options configures NewClients. Empty Options uses the standard chain:
// env vars, shared config (~/.aws/config + ~/.aws/credentials), profile
// (AWS_PROFILE), EC2 instance role / ECS task role, SSO.
type Options struct {
	// Region overrides AWS_REGION / shared-config region. Empty = use
	// the resolved chain's default. Region is a required CLI input —
	// callers normally pass it through.
	Region string

	// Profile overrides AWS_PROFILE. Empty = whatever the chain
	// resolves. Used by the workspace-driven init flow to pin a
	// specific profile per workspace.
	Profile string
}

// NewClients constructs Clients using the standard aws-sdk-go-v2 chain.
//
// Returns a clear error when credentials cannot be resolved at all
// (no env, no profile, no instance role) — the doctor command's
// caller-identity check surfaces this with actionable remediation.
func NewClients(ctx context.Context, opts Options) (*Clients, error) {
	loadOpts := []func(*config.LoadOptions) error{}
	if opts.Region != "" {
		loadOpts = append(loadOpts, config.WithRegion(opts.Region))
	}
	if opts.Profile != "" {
		loadOpts = append(loadOpts, config.WithSharedConfigProfile(opts.Profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	if cfg.Region == "" {
		return nil, errors.New("AWS region is empty; set AWS_REGION, --region, or configure a profile")
	}

	return &Clients{
		Region:    cfg.Region,
		AWSConfig: cfg,
		STS:       sts.NewFromConfig(cfg),
		EC2:       ec2.NewFromConfig(cfg),
		EKS:       eks.NewFromConfig(cfg),
		VPC:       ec2.NewFromConfig(cfg), // VPC API shares the EC2 client
	}, nil
}

// CredentialsConfigured is a fast probe answering "does the standard
// chain resolve to non-empty credentials?" without making a network
// call. Returns the source string (e.g. "Environment", "SharedConfig")
// when credentials resolve. Used by doctor to distinguish "no creds
// configured" from "creds configured but STS rejected them".
func CredentialsConfigured(ctx context.Context, opts Options) (string, error) {
	cfg, err := config.LoadDefaultConfig(ctx,
		buildLoadOpts(opts)...)
	if err != nil {
		return "", fmt.Errorf("loading AWS config: %w", err)
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return "", err
	}
	if creds.AccessKeyID == "" {
		return "", errors.New("credential source returned empty AccessKeyID")
	}
	return creds.Source, nil
}

// buildLoadOpts mirrors NewClients's chain so CredentialsConfigured
// resolves against the exact same view of env / profile the real
// clients will use.
func buildLoadOpts(opts Options) []func(*config.LoadOptions) error {
	out := []func(*config.LoadOptions) error{}
	if opts.Region != "" {
		out = append(out, config.WithRegion(opts.Region))
	}
	if opts.Profile != "" {
		out = append(out, config.WithSharedConfigProfile(opts.Profile))
	}
	return out
}

// HasEnvCredentials reports whether AWS_ACCESS_KEY_ID is set in the
// environment. Used by doctor for the "AWS credentials not detected"
// pre-flight message — surfaces the most-common-case answer without
// needing a context.
func HasEnvCredentials() bool {
	return os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != ""
}
