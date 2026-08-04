package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/registry/source"
)

// `tfx far-extract` replaces the modules' `tar -xzf … | grep '\.json$'` FAR-auth
// extraction: it reads the downloaded FAR auth tarball and writes the single
// `_json_key_base64` service-account JSON to --out, reusing the same Go extractor
// the local-file supply chain already uses (registry/source). No tar/grep on the
// host, so it runs on native Windows.

var (
	flagFarTarball string
	flagFarOut     string
)

var tfxFarExtractCmd = &cobra.Command{
	Use:   "far-extract",
	Short: "Extract the FAR service-account JSON from the auth tarball (internal)",
	Long: `Reads the FAR auth tarball (--tarball) and writes the single _json_key_base64
service-account JSON it contains to --out.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXFarExtract,
}

func init() {
	f := tfxFarExtractCmd.Flags()
	f.StringVar(&flagFarTarball, "tarball", "", "path to the FAR auth tarball (required)")
	f.StringVar(&flagFarOut, "out", "", "path to write the service-account JSON (required)")
	tfxCmd.AddCommand(tfxFarExtractCmd)
}

func runTFXFarExtract(_ *cobra.Command, _ []string) error {
	if flagFarTarball == "" || flagFarOut == "" {
		return fmt.Errorf("--tarball and --out are required")
	}
	sa, err := source.ExtractServiceAccountFromTarball(flagFarTarball)
	if err != nil {
		return fmt.Errorf("extracting FAR service account: %w", err)
	}
	if dir := filepath.Dir(flagFarOut); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(flagFarOut, []byte(sa), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", flagFarOut, err)
	}
	fmt.Fprintf(os.Stderr, "tfx far-extract: %s -> %s\n", flagFarTarball, flagFarOut)
	return nil
}
