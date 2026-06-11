package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

var flagStateMigrateForce bool

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Manage where terraform state lives (local vs COS/S3 remote backend)",
}

var stateMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Copy each phase's local terraform state into the configured COS/S3 backend",
	Long: `Migrates a workspace's terraform state from local files into the COS/S3
remote backend declared in config.yaml's state: block (PRD 16).

Pre-requisite: set state.backend = "s3" (+ state.s3.{endpoint,bucket,region})
and export the COS HMAC keys (ROKSBNKCTL_COS_HMAC_ACCESS_KEY /
ROKSBNKCTL_COS_HMAC_SECRET_KEY, or AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY)
before running.

For each deployed phase (cluster / bnk / testing / gateway), this runs
terraform init -migrate-state to copy its local state to the per-phase key
in the bucket. It refuses to overwrite a key that already holds state (pass
--force to override). The local state files are left in place; verify the
remote read-back before deleting them.`,
	Args: cobra.NoArgs,
	RunE: runStateMigrate,
}

func init() {
	stateMigrateCmd.Flags().BoolVar(&flagStateMigrateForce, "force", false,
		"migrate even if the remote key already holds state (overwrites it)")
	stateCmd.AddCommand(stateMigrateCmd)
	rootCmd.AddCommand(stateCmd)
}

type migratePhase struct {
	name    string
	present bool
	dir     string
}

func runStateMigrate(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}
	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}
	ws := cctx.Workspace
	if ws.State.Backend != "s3" || ws.State.S3 == nil {
		return fmt.Errorf("state.backend is not \"s3\" — set the state: block in config.yaml first (PRD 16), then re-run `roksbnkctl state migrate`")
	}

	// HMAC keys for the remote-key clobber check (Open re-resolves them for
	// the terraform env). Resolving here also fails fast if they're unset.
	access, secret, err := tf.ResolveCOSHMAC(ws.State.S3)
	if err != nil {
		return err
	}

	pres, err := config.DetectPresence(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	if !pres.Any() {
		fmt.Fprintln(os.Stderr, "Nothing to migrate — no deployed phases in local state.")
		return nil
	}

	clusterDir, _ := config.WorkspaceClusterStateDir(cctx.WorkspaceName)
	bnkDir, _ := config.WorkspaceStateDir(cctx.WorkspaceName)
	testingDir, _ := config.WorkspaceTestingStateDir(cctx.WorkspaceName)
	gatewayDir, _ := config.WorkspaceGatewayStateDir(cctx.WorkspaceName)
	phases := []migratePhase{
		{"cluster", pres.Cluster, clusterDir},
		{"bnk", pres.BNK, bnkDir},
		{"testing", pres.Testing, testingDir},
		{"gateway", pres.Gateway, gatewayDir},
	}

	var migrated, skipped, failed int
	for _, p := range phases {
		if !p.present {
			continue
		}
		key, kerr := tf.S3StateKey(ws.State, cctx.WorkspaceName, p.dir)
		if kerr != nil {
			return kerr
		}

		if !flagStateMigrateForce {
			exists, herr := tf.RemoteS3StateExists(cmd.Context(), *ws.State.S3, key, access, secret)
			if herr != nil {
				fmt.Fprintf(os.Stderr, "✗ %s: %v\n", p.name, herr)
				failed++
				continue
			}
			if exists {
				fmt.Fprintf(os.Stderr, "· %s: remote key %s already holds state — skipping (use --force to overwrite)\n", p.name, key)
				skipped++
				continue
			}
		}

		fmt.Fprintf(os.Stderr, "→ migrating %s → s3://%s/%s\n", p.name, ws.State.S3.Bucket, key)
		// Open writes the s3 backend override + sets AWS_* env; apiKey "" is
		// fine here — init -migrate-state only reconfigures the backend, it
		// makes no IBM-authenticated provider calls.
		tfws, oerr := tf.Open(cmd.Context(), cctx.WorkspaceName, ws, p.dir, "", os.Stdout, os.Stderr)
		if oerr != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", p.name, oerr)
			failed++
			continue
		}
		if merr := tfws.InitMigrate(cmd.Context(), os.Stdout, os.Stderr); merr != nil {
			fmt.Fprintf(os.Stderr, "✗ %s: %v\n", p.name, merr)
			failed++
			continue
		}
		fmt.Fprintf(os.Stderr, "✓ %s migrated\n", p.name)
		migrated++
	}

	fmt.Fprintf(os.Stderr, "\nstate migrate: %d migrated, %d skipped, %d failed\n", migrated, skipped, failed)
	if failed > 0 {
		return fmt.Errorf("%d phase(s) failed to migrate", failed)
	}
	fmt.Fprintln(os.Stderr, "Local state files left in place — verify the remote read-back, then remove them when satisfied.")
	return nil
}
