package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/cos"
)

// `tfx cos-get` replaces the modules' COS S3 downloads (far_download, the JWT
// fetch) — the `curl`/`data.http` pulls of the FAR auth tarball + subscription JWT
// out of the supply-chain bucket. Backed by internal/cos (the IBM COS S3 client),
// so it needs no aws/ibmcloud CLI on the host.

var (
	flagCosInstanceCRN string
	flagCosBucket      string
	flagCosKey         string
	flagCosOut         string
	flagCosRegion      string
	flagCosAPIKeyEnv   string
)

var tfxCosGetCmd = &cobra.Command{
	Use:   "cos-get",
	Short: "Download an object from IBM COS to a local file (internal)",
	Long: `Downloads bucket/key from the given COS instance to --out. The API key is
read from an env var (default IBMCLOUD_API_KEY), never the command line.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXCosGetCmd,
}

func init() {
	f := tfxCosGetCmd.Flags()
	f.StringVar(&flagCosInstanceCRN, "instance-crn", "", "COS instance CRN (required)")
	f.StringVar(&flagCosBucket, "bucket", "", "bucket name (required)")
	f.StringVar(&flagCosKey, "key", "", "object key to download (required)")
	f.StringVar(&flagCosOut, "out", "", "local file path to write (required)")
	f.StringVar(&flagCosRegion, "region", "us-south", "bucket region")
	f.StringVar(&flagCosAPIKeyEnv, "api-key-env", "IBMCLOUD_API_KEY", "env var holding the IBM Cloud API key")
	tfxCmd.AddCommand(tfxCosGetCmd)
}

func runTFXCosGetCmd(cmd *cobra.Command, _ []string) error {
	if flagCosInstanceCRN == "" || flagCosBucket == "" || flagCosKey == "" || flagCosOut == "" {
		return fmt.Errorf("--instance-crn, --bucket, --key and --out are all required")
	}
	env := flagCosAPIKeyEnv
	if env == "" {
		env = "IBMCLOUD_API_KEY"
	}
	apiKey := os.Getenv(env)
	if apiKey == "" {
		return fmt.Errorf("no API key in $%s", env)
	}
	cl, err := cos.New(apiKey, flagCosRegion, flagCosInstanceCRN)
	if err != nil {
		return fmt.Errorf("creating COS client: %w", err)
	}
	return runTFXCosGet(cmd.Context(), cl, flagCosBucket, flagCosKey, flagCosOut, os.Stderr)
}

// cosObjectGetter is the slice of the COS client tfx cos-get needs — so the core
// is testable with a fake getter.
type cosObjectGetter interface {
	GetObjectToFile(ctx context.Context, bucket, key, localPath string) error
}

// runTFXCosGet is the testable core: ensure the out dir exists, then download.
func runTFXCosGet(ctx context.Context, g cosObjectGetter, bucket, key, out string, logw io.Writer) error {
	if dir := filepath.Dir(out); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := g.GetObjectToFile(ctx, bucket, key, out); err != nil {
		return fmt.Errorf("downloading %s/%s: %w", bucket, key, err)
	}
	fmt.Fprintf(logw, "tfx cos-get: %s/%s -> %s\n", bucket, key, out)
	return nil
}
