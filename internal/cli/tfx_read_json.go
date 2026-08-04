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
	flagReadJSONFile  string
	flagReadJSONKey   string
	flagReadJSONRaw   bool
	flagReadJSONPairs []string
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
	f.StringVar(&flagReadJSONFile, "file", "", "file whose contents become the value (single-key mode)")
	f.StringVar(&flagReadJSONKey, "key", "v", "JSON key to emit the contents under (single-key mode)")
	f.BoolVar(&flagReadJSONRaw, "raw", false, "do not trim surrounding whitespace")
	f.StringArrayVar(&flagReadJSONPairs, "pair", nil, "key=file mapping, repeatable — emit {key: file-contents, ...} (multi-key mode)")
	tfxCmd.AddCommand(tfxReadJSONCmd)
}

func runTFXReadJSONCmd(cmd *cobra.Command, _ []string) error {
	// Multi-key mode: one --pair key=file per output key. A missing file yields an
	// empty string (like the modules' `cat … 2>/dev/null`) so a destroy-phase
	// refresh in a fresh container still emits a well-formed object.
	if len(flagReadJSONPairs) > 0 {
		out := make(map[string]string, len(flagReadJSONPairs))
		for _, p := range flagReadJSONPairs {
			key, path, ok := strings.Cut(p, "=")
			if !ok || key == "" {
				return fmt.Errorf("--pair %q must be key=file", p)
			}
			v := ""
			if b, err := os.ReadFile(path); err == nil {
				v = string(b)
				if !flagReadJSONRaw {
					v = strings.TrimSpace(v)
				}
			}
			out[key] = v
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}
	if flagReadJSONFile == "" {
		return fmt.Errorf("--file (or --pair) is required")
	}
	b, err := os.ReadFile(flagReadJSONFile)
	if err != nil {
		// A missing file emits an empty value (like the modules' `cat … 2>/dev/null`)
		// so a destroy-phase data.external refresh in a fresh container still emits a
		// well-formed object instead of aborting the plan.
		if os.IsNotExist(err) {
			return writeReadJSON(cmd.OutOrStdout(), flagReadJSONKey, "", flagReadJSONRaw)
		}
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
