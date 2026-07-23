package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// `tfx read-json` replaces the modules' `data.external` sites — e.g. flp_vsi's
// `printf '{"v":"%s"}' "$(cat flp-version.txt)"`. data.external's contract is: emit
// a flat JSON object (map of string→string) on stdout; terraform reads it as the
// data source's `result`. This reads a file and emits {<key>: <trimmed contents>}.

var (
	flagReadJSONFile string
	flagReadJSONKey  string
	flagReadJSONRaw  bool
)

var tfxReadJSONCmd = &cobra.Command{
	Use:   "read-json",
	Short: "Emit {key: file-contents} as JSON for terraform's data.external (internal)",
	Long: `Reads --file and prints a flat JSON object {"<key>": "<contents>"} to stdout,
in the shape terraform's data.external expects. Contents are whitespace-trimmed
unless --raw.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXReadJSONCmd,
}

func init() {
	f := tfxReadJSONCmd.Flags()
	f.StringVar(&flagReadJSONFile, "file", "", "file whose contents become the value (required)")
	f.StringVar(&flagReadJSONKey, "key", "v", "JSON key to emit the contents under")
	f.BoolVar(&flagReadJSONRaw, "raw", false, "do not trim surrounding whitespace")
	tfxCmd.AddCommand(tfxReadJSONCmd)
}

func runTFXReadJSONCmd(cmd *cobra.Command, _ []string) error {
	if flagReadJSONFile == "" {
		return fmt.Errorf("--file is required")
	}
	b, err := os.ReadFile(flagReadJSONFile)
	if err != nil {
		return fmt.Errorf("reading %s: %w", flagReadJSONFile, err)
	}
	return writeReadJSON(cmd.OutOrStdout(), flagReadJSONKey, string(b), flagReadJSONRaw)
}

// writeReadJSON emits the flat {key: value} object. data.external requires a
// map[string]string, so a single key/value is exactly the shape it consumes.
func writeReadJSON(w io.Writer, key, value string, raw bool) error {
	if key == "" {
		key = "v"
	}
	if !raw {
		value = strings.TrimSpace(value)
	}
	enc := json.NewEncoder(w)
	return enc.Encode(map[string]string{key: value})
}
