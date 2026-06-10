package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	awspkg "github.com/JLCode-tech/awsbnkctl/internal/aws"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
)

// serviceQuotasFeatureFlagEnv gates the optional Service Quotas probe
// in the doctor. Off by default — the operator opts in by exporting
// AWSBNKCTL_DOCTOR_SERVICE_QUOTAS=1 (or `true`/`yes`/`on`) before
// invoking `awsbnkctl doctor`. Once the operator-run spike validates
// the live signal, v0.x flips the default and retires the flag.
const serviceQuotasFeatureFlagEnv = "AWSBNKCTL_DOCTOR_SERVICE_QUOTAS"

// serviceQuotasEnabled reports whether the operator opted in to the
// Service Quotas probe via the feature-flag env var. Accepts any of
// `1`, `true`, `yes`, `on` (case-insensitive) — keeps the toggle
// ergonomic without parsing nuance.
func serviceQuotasEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(serviceQuotasFeatureFlagEnv)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// awsChecks runs the AWS-shaped pre-flight checks.
//
// Six checks, ordered by failure cost (cheapest → most informative):
//
//  1. credentials configured — no API call.
//  2. STS GetCallerIdentity — one API call; validates the resolved key.
//  3. EKS DescribeCluster permission probe — bogus cluster name.
//  4. EC2 vCPU quota probe — probes ec2:DescribeAccountAttributes (the
//     cheapest EC2 read that answers "is the cred allowed to talk to
//     EC2 at all?"); the actual running-on-demand quota lives in
//     Service Quotas, which this row points the operator at.
//  5. S3 PutObject feasibility probe — HeadBucket against the
//     workspace's supply-chain bucket name; NotFound is OK (bucket not
//     created yet, normal pre-`up`), AccessDenied surfaces the IAM gap.
//  6. IAM:GetOpenIDConnectProvider permission probe — GetRole against
//     the FLO IRSA role; NoSuchEntity is informational (role not yet
//     created), other errors are actionable.
func awsChecks(ctx context.Context, cctx *config.Context) []withWhy {
	var out []withWhy

	// Short timeout — doctor must not block a fresh dev box that
	// happens to be offline. The six checks total resolve in <5s on
	// a good connection; 15s is generous slack.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	region := awsRegionFromContext(cctx)
	profile := awsProfileFromContext(cctx)

	// Check 1: credentials configured (cheap, no API call).
	credSource, credErr := awspkg.CredentialsConfigured(ctx, awspkg.Options{Region: region, Profile: profile})
	if credErr != nil {
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws credentials",
				Status: StatusWarning,
				Detail: "no credentials resolved via env / profile / instance role / SSO — set AWS_PROFILE or AWS_ACCESS_KEY_ID before `awsbnkctl up`",
			},
			Why: "every AWS-side operation needs valid credentials",
		})
		// No point running the live checks without creds.
		for _, name := range []string{
			"aws sts caller-identity",
			"aws eks:DescribeCluster permission",
			"aws ec2 vCPU quota",
			"aws s3:PutObject feasibility",
			"aws iam:GetRole (FLO IRSA)",
		} {
			out = append(out, withWhy{
				Check: Check{Name: name, Status: StatusSkipped, Detail: "skipped (no credentials)"},
				Why:   "skipped because earlier credential check failed",
			})
		}
		return out
	}
	out = append(out, withWhy{
		Check: Check{Name: "aws credentials", Status: StatusOK, Detail: credSource},
		Why:   "every AWS-side operation needs valid credentials",
	})

	// Build a shared Clients for the remaining live checks.
	clients, err := awspkg.NewClients(ctx, awspkg.Options{Region: region, Profile: profile})
	if err != nil {
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws sts caller-identity",
				Status: StatusError,
				Detail: fmt.Sprintf("client construction failed: %v", err),
			},
			Why: "validates credentials are accepted by AWS",
		})
		return out
	}

	// Check 2: STS GetCallerIdentity.
	id, err := clients.CallerIdentity(ctx)
	if err != nil {
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws sts caller-identity",
				Status: StatusError,
				Detail: fmt.Sprintf("AccessDenied or transport error: %v", err),
			},
			Why: "validates credentials are accepted by AWS",
		})
		// No point probing downstream if STS already says no.
		for _, name := range []string{
			"aws eks:DescribeCluster permission",
			"aws ec2 vCPU quota",
			"aws s3:PutObject feasibility",
			"aws iam:GetRole (FLO IRSA)",
		} {
			out = append(out, withWhy{
				Check: Check{Name: name, Status: StatusSkipped, Detail: "skipped (STS failed)"},
				Why:   "skipped because credentials probe failed",
			})
		}
		return out
	}
	out = append(out, withWhy{
		Check: Check{Name: "aws sts caller-identity", Status: StatusOK, Detail: fmt.Sprintf("account=%s arn=%s", id.Account, id.ARN)},
		Why:   "validates credentials are accepted by AWS",
	})

	// Check 3: EKS DescribeCluster permission probe against a bogus
	// cluster name. ResourceNotFound = permission OK.
	_, err = clients.DescribeCluster(ctx, "awsbnkctl-doctor-permission-probe-does-not-exist")
	switch {
	case err == nil:
		out = append(out, withWhy{
			Check: Check{Name: "aws eks:DescribeCluster permission", Status: StatusOK, Detail: "probe returned (unexpected: a cluster matched the probe name)"},
			Why:   "validates `awsbnkctl up cluster` will have permission to read the cluster post-apply",
		})
	case awspkg.IsResourceNotFound(err):
		out = append(out, withWhy{
			Check: Check{Name: "aws eks:DescribeCluster permission", Status: StatusOK, Detail: "probe returned ResourceNotFound (permission OK)"},
			Why:   "validates `awsbnkctl up cluster` will have permission to read the cluster post-apply",
		})
	default:
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws eks:DescribeCluster permission",
				Status: StatusWarning,
				Detail: fmt.Sprintf("probe failed: %v (may be AccessDenied — verify IAM policy attaches eks:DescribeCluster)", err),
			},
			Why: "validates `awsbnkctl up cluster` will have permission to read the cluster post-apply",
		})
	}

	// Check 4: EC2 vCPU quota probe. The real "running on-demand" quota
	// lives in Service Quotas; for the doctor row we probe
	// ec2:DescribeAccountAttributes as the cheapest "EC2 read works at
	// all" signal and surface a pointer at where the operator finds the
	// live quota.
	quota, qerr := clients.VCPUQuotaAttribute(ctx)
	switch {
	case qerr == nil:
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws ec2 vCPU quota",
				Status: StatusOK,
				Detail: fmt.Sprintf("ec2 read OK (default-vpc=%s); check Service Quotas for the Running On-Demand c5n quota (c5n.4xlarge target = 16 vCPU/node, default 5 instances = 80 vCPU)", quota),
			},
			Why: "The self-managed node group needs ≥3 c5n.4xlarge (48 vCPU). Many accounts default to 5-instance / 80-vCPU running-on-demand quota — enough for the default node count but flagging the path to lift it.",
		})
	default:
		out = append(out, withWhy{
			Check: Check{
				Name:   "aws ec2 vCPU quota",
				Status: StatusWarning,
				Detail: fmt.Sprintf("ec2:DescribeAccountAttributes failed: %v (verify ec2:Describe* IAM permissions)", qerr),
			},
			Why: "The self-managed node group needs EC2 quota headroom; this probe validates the IAM permission path.",
		})
	}

	// Check 4b (optional, feature-flagged): Service Quotas
	// GetServiceQuota for L-1216C47A (Running On-Demand Standard
	// instances). Off by default — operator opts in via
	// AWSBNKCTL_DOCTOR_SERVICE_QUOTAS=1. When the IAM permits the
	// servicequotas:GetServiceQuota call we surface the live quota
	// value (e.g. "256 vCPU"); when AccessDenied we surface that as a
	// Warning row pointing at the IAM gap. Brief §"Optional Service
	// Quotas check" — gated by an internal feature flag (off by
	// default) until v0.x validates the signal on live AWS.
	if serviceQuotasEnabled() {
		val, sqErr := clients.RunningOnDemandVCPUQuota(ctx)
		switch {
		case sqErr == nil:
			out = append(out, withWhy{
				Check: Check{
					Name:   "aws servicequotas:RunningOnDemand-vCPU",
					Status: StatusOK,
					Detail: fmt.Sprintf("L-1216C47A = %.0f vCPU (live Service Quotas value)", val),
				},
				Why: "The self-managed node group needs ≥48 vCPU (3× c5n.4xlarge). The live quota answers whether the account can host that without a limit-increase ticket.",
			})
		default:
			out = append(out, withWhy{
				Check: Check{
					Name:   "aws servicequotas:RunningOnDemand-vCPU",
					Status: StatusWarning,
					Detail: fmt.Sprintf("probe failed: %v (verify IAM attaches servicequotas:GetServiceQuota; falling back to the default 5-instance / 80-vCPU pointer above)", sqErr),
				},
				Why: "optional probe surfaces the live running-on-demand vCPU quota; AccessDenied is expected when the operator's IAM doesn't permit it",
			})
		}
	}

	// Check 5: S3 PutObject feasibility probe. HeadBucket against the
	// workspace's expected supply-chain bucket. We don't
	// know the exact bucket name until `awsbnkctl up` runs (the
	// random suffix is generated by the Go-SDK provisioning phase),
	// so the probe uses the workspace-recorded bucket (if any) or
	// falls back to probing the s3 service's general reachability via
	// the public AWS bucket-name behaviour: HeadBucket on a
	// known-nonexistent bucket returns NotFound + 404 when permission
	// is OK, AccessDenied + 403 when the cred can't talk to S3 at all.
	bucket := supplyChainBucketFromContext(cctx)
	if bucket == "" {
		bucket = "awsbnkctl-doctor-s3-probe-does-not-exist"
	}
	if herr := clients.HeadBucket(ctx, bucket); herr != nil {
		switch {
		case awspkg.IsS3NotFound(herr):
			out = append(out, withWhy{
				Check: Check{Name: "aws s3:PutObject feasibility", Status: StatusOK, Detail: "HeadBucket returned NotFound (s3 permission OK; bucket not yet created)"},
				Why:   "the supply-chain bucket holds the FAR archive + JWT; validates `awsbnkctl init` will be able to PutObject when the bucket exists",
			})
		case awspkg.IsS3AccessDenied(herr):
			out = append(out, withWhy{
				Check: Check{Name: "aws s3:PutObject feasibility", Status: StatusWarning, Detail: "HeadBucket returned AccessDenied — verify the IAM policy attaches s3:HeadBucket + s3:PutObject"},
				Why:   "the supply-chain bucket holds the FAR archive + JWT",
			})
		default:
			out = append(out, withWhy{
				Check: Check{
					Name:   "aws s3:PutObject feasibility",
					Status: StatusWarning,
					Detail: fmt.Sprintf("probe failed: %v", herr),
				},
				Why: "the supply-chain bucket holds the FAR archive + JWT",
			})
		}
	} else {
		out = append(out, withWhy{
			Check: Check{Name: "aws s3:PutObject feasibility", Status: StatusOK, Detail: "HeadBucket OK (s3 permission + bucket reachable)"},
			Why:   "the supply-chain bucket holds the FAR archive + JWT",
		})
	}

	// Check 6: iam:GetRole probe for the FLO IRSA role. NoSuchEntity
	// is informational (role not yet created by the Go-SDK IAM phase);
	// other errors surface IAM-permission gaps.
	roleName := awspkg.IRSARoleNameForCluster(clusterNameFromContext(cctx))
	if cctx != nil && cctx.Workspace != nil && cctx.Workspace.Cluster.Name == "" {
		// No cluster name yet — surface as informational, don't
		// fail the row.
		out = append(out, withWhy{
			Check: Check{Name: "aws iam:GetRole (FLO IRSA)", Status: StatusOK, Detail: "no cluster name in workspace yet — probe skipped"},
			Why:   "validates the FLO IRSA role landed (Go-SDK IAM phase)",
		})
	} else {
		info, rerr := clients.HasIRSARole(ctx, roleName)
		switch {
		case rerr == nil && info != nil:
			out = append(out, withWhy{
				Check: Check{Name: "aws iam:GetRole (FLO IRSA)", Status: StatusOK, Detail: fmt.Sprintf("role exists: %s", info.ARN)},
				Why:   "validates the FLO IRSA role landed (Go-SDK IAM phase)",
			})
		case rerr == nil && info == nil:
			out = append(out, withWhy{
				Check: Check{Name: "aws iam:GetRole (FLO IRSA)", Status: StatusOK, Detail: fmt.Sprintf("role %s not found (normal pre-`awsbnkctl up`)", roleName)},
				Why:   "validates the FLO IRSA role landed (Go-SDK IAM phase)",
			})
		default:
			out = append(out, withWhy{
				Check: Check{
					Name:   "aws iam:GetRole (FLO IRSA)",
					Status: StatusWarning,
					Detail: fmt.Sprintf("probe failed: %v (verify IAM policy attaches iam:GetRole)", rerr),
				},
				Why: "validates the FLO IRSA role landed (Go-SDK IAM phase)",
			})
		}
	}

	return out
}

// awsRegionFromContext extracts the AWS region from the workspace
// config if present. AWS is the only first-class cloud block.
//
// Empty return falls through to the SDK's default chain, which reads
// AWS_REGION env / shared config. internal/aws's NewClients surfaces
// a clear "AWS region is empty" error in that case.
func awsRegionFromContext(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	return cctx.Workspace.AWS.Region
}

// awsProfileFromContext mirrors awsRegionFromContext for AWS_PROFILE.
// Empty = use the chain's default profile.
func awsProfileFromContext(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	return cctx.Workspace.AWS.Profile
}

// supplyChainBucketFromContext returns the workspace-recorded
// supply-chain bucket name, or empty if not yet set. The doctor's S3
// probe falls back to a known-nonexistent bucket name when this is
// empty so the IAM-permission check still runs.
func supplyChainBucketFromContext(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	return cctx.Workspace.AWS.SupplyChain.BucketName
}

// clusterNameFromContext returns the workspace-recorded cluster name.
// Used to derive the FLO IRSA role name for the doctor's iam:GetRole
// probe.
func clusterNameFromContext(cctx *config.Context) string {
	if cctx == nil || cctx.Workspace == nil {
		return ""
	}
	return cctx.Workspace.Cluster.Name
}
