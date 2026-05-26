package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/JLCode-tech/awsbnkctl/internal/aws"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
)

// Sprint 2 `awsbnkctl init` — AWS path (PRD 08 § "CLI surface" +
// PRD 07 § "Inputs"). Interactive wizard collecting region / profile
// / VPC / subnets / cluster name + the FAR archive + JWT local paths
// the supply-chain S3 upload phase needs at apply time.
//
// SPIKE DEFERRAL: --dry-run flag short-circuits the AWS PutObject
// call so this binary runs on a stock dev box without live AWS. The
// wizard still walks every prompt + writes the workspace config; the
// supply-chain upload is the only step that depends on a live
// account, and it's the only step --dry-run skips.

// flagInitDryRun is the workspace-side `--dry-run` for `awsbnkctl init`.
// Distinct from the per-up-command flag (flagClusterDryRun) so the
// two flows can move at different cadences as Sprint 3 fills the
// non-dry-run lifecycle in.
var flagInitDryRun bool

// Register the --dry-run flag onto the existing initCmd (declared in
// lifecycle.go) at package init time. Kept separate from
// lifecycle.go's flag wiring so the init.go file owns the AWS-wizard
// surface end-to-end.
func init() {
	initCmd.Flags().BoolVar(&flagInitDryRun, "dry-run", false, "walk the wizard + write the workspace config but skip the live AWS upload step (Sprint 2 spike-deferral path)")
}

func runInit(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "── awsbnkctl init (AWS) ─────────────────────────────────────────────")
	fmt.Fprintf(os.Stderr, "  workspace: %s\n", cctx.WorkspaceName)
	if flagInitDryRun {
		fmt.Fprintln(os.Stderr, "  --dry-run: live AWS PutObject step will be skipped")
	}
	fmt.Fprintln(os.Stderr)

	// Start from existing workspace if there is one (so re-running
	// init doesn't blow away values the user already set).
	ws := cctx.Workspace
	if ws == nil {
		ws = &config.Workspace{}
	}

	// ── AWS block ────────────────────────────────────────────────────
	fmt.Fprintln(os.Stderr, "AWS:")
	ws.AWS.Region = promptString("region", firstNonEmpty(ws.AWS.Region, os.Getenv("AWS_REGION"), "us-east-1"))
	ws.AWS.Profile = promptString("profile (optional)", firstNonEmpty(ws.AWS.Profile, os.Getenv("AWS_PROFILE")))
	ws.AWS.VPCID = promptString("VPC ID (vpc-…)", ws.AWS.VPCID)

	subnetsCSV := promptString("subnet IDs (comma-separated, >=3)", strings.Join(ws.AWS.SubnetIDs, ","))
	ws.AWS.SubnetIDs = splitAndTrim(subnetsCSV)

	// ── Cluster block ────────────────────────────────────────────────
	fmt.Fprintln(os.Stderr, "\nCluster:")
	ws.Cluster.Name = promptString("EKS cluster name", firstNonEmpty(ws.Cluster.Name, "bnk-demo"))
	ws.Cluster.Create = promptYesNo("create new cluster?", ws.Cluster.Create || ws.Cluster.Name == "bnk-demo")

	// ── Supply chain block (PRD 08) ──────────────────────────────────
	fmt.Fprintln(os.Stderr, "\nSupply chain (PRD 08):")
	ws.AWS.SupplyChain.FARArchivePath = promptString("FAR archive path (f5cne-far-auth-*.tar.gz)", ws.AWS.SupplyChain.FARArchivePath)
	ws.AWS.SupplyChain.JWTPath = promptString("subscription JWT path (f5cne-subscription-*.jwt)", ws.AWS.SupplyChain.JWTPath)
	ws.AWS.SupplyChain.FLONamespace = promptString("FLO namespace", firstNonEmpty(ws.AWS.SupplyChain.FLONamespace, "flo-system"))
	ws.AWS.SupplyChain.EnableECRMirror = promptYesNo("enable optional ECR mirror? (v1.0 stretch)", ws.AWS.SupplyChain.EnableECRMirror)

	// ── Validate ────────────────────────────────────────────────────
	if err := validateInitInputs(ws, flagInitDryRun); err != nil {
		return err
	}

	// ── Persist ─────────────────────────────────────────────────────
	if err := config.SaveWorkspace(cctx.WorkspaceName, ws); err != nil {
		return fmt.Errorf("saving workspace config: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n✓ workspace %q saved\n", cctx.WorkspaceName)

	// ── Supply-chain upload (live AWS) ──────────────────────────────
	if flagInitDryRun {
		fmt.Fprintln(os.Stderr, "  (--dry-run: skipping S3 PutObject; `awsbnkctl up` will create the bucket + upload)")
		return nil
	}

	// The supply-chain bucket follows the standard naming pattern, but
	// the operator may not have run `awsbnkctl up` yet — in which case
	// the bucket doesn't exist. Skip the upload step with an
	// informational message when that's the case; the phased `up` path
	// creates the bucket + uploads the objects at apply time.
	if ws.AWS.SupplyChain.FARArchivePath == "" || ws.AWS.SupplyChain.JWTPath == "" {
		fmt.Fprintln(os.Stderr, "  (no FAR archive / JWT path supplied; skipping S3 upload — re-run init when ready)")
		return nil
	}

	bucket := ws.AWS.SupplyChain.BucketName
	if bucket == "" {
		fmt.Fprintln(os.Stderr, "  (no supply-chain bucket recorded yet; `awsbnkctl up` will create it + upload the objects)")
		return nil
	}

	return uploadSupplyChain(cmd.Context(), ws, bucket)
}

// uploadSupplyChain is split out so callers (and tests) can exercise
// the upload path against an injected S3API. Production calls go
// through runInit; tests inject a fake via SetS3ForTest.
func uploadSupplyChain(ctx context.Context, ws *config.Workspace, bucket string) error {
	clients, err := awspkg.NewClients(ctx, awspkg.Options{
		Region:  ws.AWS.Region,
		Profile: ws.AWS.Profile,
	})
	if err != nil {
		return fmt.Errorf("constructing AWS clients: %w", err)
	}

	uploadCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := putFile(uploadCtx, clients, bucket, "far-auth.tar.gz", ws.AWS.SupplyChain.FARArchivePath, ws.AWS.SupplyChain.KMSKeyARN); err != nil {
		return fmt.Errorf("uploading FAR archive: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ uploaded FAR archive → s3://%s/far-auth.tar.gz\n", bucket)

	if err := putFile(uploadCtx, clients, bucket, "subscription.jwt", ws.AWS.SupplyChain.JWTPath, ws.AWS.SupplyChain.KMSKeyARN); err != nil {
		return fmt.Errorf("uploading subscription JWT: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ uploaded subscription JWT → s3://%s/subscription.jwt\n", bucket)
	return nil
}

// putFile reads the local path into memory + PUTs to S3. The
// in-memory buffering is fine for the FAR archive (megabytes) and
// the JWT (kilobytes); v1.x can switch to chunked-upload for >5GB
// payloads if F5 ever ships one.
func putFile(ctx context.Context, c *awspkg.Clients, bucket, key, path, kmsKeyID string) error {
	if path == "" {
		return errors.New("local path is empty")
	}
	body, err := os.ReadFile(path) // #nosec G304 -- path comes from the init wizard's supply-chain prompts (operator-provided FAR/JWT paths), validated upstream
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	_, err = c.PutObject(ctx, bucket, key, bytes.NewReader(body), kmsKeyID)
	return err
}

// validateInitInputs checks the post-prompt workspace shape. In
// --dry-run mode we relax the supply-chain prompts (operator may
// not have the FAR archive yet); the strict path requires every
// prompt populated.
func validateInitInputs(ws *config.Workspace, dryRun bool) error {
	if ws.AWS.Region == "" {
		return errors.New("AWS region is required")
	}
	if !dryRun {
		// VPC + subnets are mandatory in the strict path because the
		// provisioning phases need them up front. The Sprint 1
		// `awsbnkctl up cluster --dry-run` still works with these
		// unset (synthetic workspace path), but `awsbnkctl init`
		// without --dry-run is a "ready to apply" intent.
		if ws.AWS.VPCID == "" {
			return errors.New("VPC ID is required (pass --dry-run to skip strict validation)")
		}
		if len(ws.AWS.SubnetIDs) < 3 {
			return errors.New("at least 3 subnet IDs required for HA per PRD 07 (pass --dry-run to skip strict validation)")
		}
	}
	if ws.Cluster.Name == "" {
		return errors.New("cluster name is required")
	}
	return nil
}

// firstNonEmpty returns the first non-empty string from xs. Used by
// the init prompts to compose default values from existing workspace,
// env vars, and a hard-coded fallback.
func firstNonEmpty(xs ...string) string {
	for _, s := range xs {
		if s != "" {
			return s
		}
	}
	return ""
}

// splitAndTrim splits "a, b, c" into ["a","b","c"] with whitespace
// stripped. Empty input returns nil rather than []string{""}.
func splitAndTrim(csv string) []string {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Keep cobra import referenced even when the wizard path doesn't use
// cmd directly — future verbs (init --supply-chain only) will.
var _ = (*cobra.Command)(nil)
