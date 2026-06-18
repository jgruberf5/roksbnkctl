package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

var flagStateMigrateForce bool

var (
	flagStateEndpoint  string
	flagStateBucket    string
	flagStateRegion    string
	flagStateKeyPrefix string
	flagStateAccessSrc string
	flagStateSecretSrc string
)

var stateCmd = &cobra.Command{
	Use:   "state",
	Short: "Manage where terraform state lives (local vs COS/S3 remote backend)",
}

// loadWorkspaceForEdit resolves the selected workspace and loads it for an
// in-place mutate → SaveWorkspace edit. Shared by the config-writing commands
// (state, backend, bnkforge) so turning a feature on never needs a hand-edit of
// config.yaml.
func loadWorkspaceForEdit() (string, *config.Workspace, error) {
	name := resolvedWorkspaceName()
	if name == "" {
		return "", nil, fmt.Errorf("no workspace selected (use -w <name> or `roksbnkctl init`)")
	}
	ws, err := config.LoadWorkspace(name)
	if err != nil {
		return "", nil, err
	}
	return name, ws, nil
}

var stateShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the workspace's terraform-state backend config",
	Args:  cobra.NoArgs,
	RunE:  runStateShow,
}

var stateLocalCmd = &cobra.Command{
	Use:   "local",
	Short: "Use local per-phase terraform state (the default) — writes config.yaml for you",
	Args:  cobra.NoArgs,
	RunE:  runStateLocal,
}

var stateS3Cmd = &cobra.Command{
	Use:   "s3",
	Short: "Use the COS/S3 remote terraform-state backend — writes the state: block for you",
	Long: `Switch this workspace to the COS/S3 remote state backend (PRD 16) by writing
its state: block to config.yaml — no hand-edit.

You still provision the bucket + HMAC credentials yourself: the HMAC access/
secret keys are NEVER written to config.yaml — only the names of the env vars
they come from are (--access-key-source / --secret-key-source, defaulting to
ROKSBNKCTL_COS_HMAC_ACCESS_KEY / ROKSBNKCTL_COS_HMAC_SECRET_KEY). After this,
export those env vars and run ` + "`roksbnkctl state migrate`" + ` to move existing state.`,
	Args: cobra.NoArgs,
	RunE: runStateS3,
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

	stateS3Cmd.Flags().StringVar(&flagStateEndpoint, "endpoint", "", "COS S3 endpoint URL (required)")
	stateS3Cmd.Flags().StringVar(&flagStateBucket, "bucket", "", "pre-provisioned bucket name (required)")
	stateS3Cmd.Flags().StringVar(&flagStateRegion, "region", "", "COS location / region (required)")
	stateS3Cmd.Flags().StringVar(&flagStateKeyPrefix, "key-prefix", "", "state key prefix (default: the workspace name)")
	stateS3Cmd.Flags().StringVar(&flagStateAccessSrc, "access-key-source", "", "env var holding the HMAC access key (default ROKSBNKCTL_COS_HMAC_ACCESS_KEY)")
	stateS3Cmd.Flags().StringVar(&flagStateSecretSrc, "secret-key-source", "", "env var holding the HMAC secret key (default ROKSBNKCTL_COS_HMAC_SECRET_KEY)")

	stateCmd.AddCommand(stateShowCmd, stateLocalCmd, stateS3Cmd, stateMigrateCmd)
	rootCmd.AddCommand(stateCmd)
}

func runStateShow(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	backend := ws.State.Backend
	if backend == "" {
		backend = "local"
	}
	fmt.Printf("workspace:   %s\n", name)
	fmt.Printf("backend:     %s\n", backend)
	if ws.State.S3 != nil {
		s3 := ws.State.S3
		fmt.Printf("s3.endpoint: %s\n", s3.Endpoint)
		fmt.Printf("s3.bucket:   %s\n", s3.Bucket)
		fmt.Printf("s3.region:   %s\n", s3.Region)
		kp := s3.KeyPrefix
		if kp == "" {
			kp = name + " (default: workspace name)"
		}
		fmt.Printf("s3.key_prefix: %s\n", kp)
		acc := s3.AccessKeySource
		if acc == "" {
			acc = "ROKSBNKCTL_COS_HMAC_ACCESS_KEY (default)"
		}
		sec := s3.SecretKeySource
		if sec == "" {
			sec = "ROKSBNKCTL_COS_HMAC_SECRET_KEY (default)"
		}
		fmt.Printf("s3.access_key_source: %s\n", acc)
		fmt.Printf("s3.secret_key_source: %s\n", sec)
	}
	return nil
}

func runStateLocal(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	ws.State.Backend = "local"
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %q now uses local terraform state.\n", name)
	if ws.State.S3 != nil {
		fmt.Fprintln(os.Stderr, "  note: the s3: block is kept (inert while backend=local) so `state s3` can switch back without re-entering it.")
	}
	return nil
}

func runStateS3(_ *cobra.Command, _ []string) error {
	if flagStateEndpoint == "" || flagStateBucket == "" || flagStateRegion == "" {
		return fmt.Errorf("--endpoint, --bucket, and --region are all required")
	}
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	if ws.State.S3 == nil {
		ws.State.S3 = &config.StateS3Cfg{}
	}
	ws.State.Backend = "s3"
	ws.State.S3.Endpoint = flagStateEndpoint
	ws.State.S3.Bucket = flagStateBucket
	ws.State.S3.Region = flagStateRegion
	if flagStateKeyPrefix != "" {
		ws.State.S3.KeyPrefix = flagStateKeyPrefix
	}
	if flagStateAccessSrc != "" {
		ws.State.S3.AccessKeySource = flagStateAccessSrc
	}
	if flagStateSecretSrc != "" {
		ws.State.S3.SecretKeySource = flagStateSecretSrc
	}
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %q now uses the COS/S3 remote state backend (s3://%s).\n", name, ws.State.S3.Bucket)
	acc := ws.State.S3.AccessKeySource
	if acc == "" {
		acc = "ROKSBNKCTL_COS_HMAC_ACCESS_KEY"
	}
	sec := ws.State.S3.SecretKeySource
	if sec == "" {
		sec = "ROKSBNKCTL_COS_HMAC_SECRET_KEY"
	}
	fmt.Fprintf(os.Stderr, "  Next: export the HMAC keys (%s / %s), then run `roksbnkctl state migrate` to move existing state.\n", acc, sec)
	return nil
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
