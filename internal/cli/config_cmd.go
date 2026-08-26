package cli

import (
	_ "embed"

	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// exampleEnvFile is the annotated .env template `config env` prints. GENERATED
// by tools/refgen/env-example -- run `go generate ./internal/cli/` after adding
// an override, and TestEnvExampleIsCurrent fails if it is stale.
//
//go:embed env.example
var exampleEnvFile []byte

//go:generate sh -c "go run ../../tools/refgen/env-example > env.example"

var (
	flagConfigFromYAML string
	flagConfigFromEnv  string
)

// `config` prints the workspace input in either of its two forms, so a template
// can be piped to a file and an existing configuration can be converted between
// them.
//
// Two forms exist because two things configure this tool: a human editing
// config.yaml, and a CI runner passing ROKSBNKCTL_* variables. They describe the
// same settings, and moving between them by hand means transcribing a hundred
// names — which is how a .env ends up naming a variable that does not exist.
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Print an example config.yaml or .env, or convert one into the other",
	Long: `Prints the workspace input in either form, for piping to a file.

With no --from flag it prints an ANNOTATED template — every setting with the
comment explaining it, which is what you want when starting from nothing:

  roksbnkctl config yaml > config.yaml
  roksbnkctl config env  > .env

With --from-yaml or --from-env it prints the POPULATED equivalent, without
comments — the same settings expressed in the other form:

  roksbnkctl config env  --from-yaml config.yaml > .env
  roksbnkctl config yaml --from-env  .env        > config.yaml

The conversion carries what the input actually sets. A setting left at its
default is omitted rather than written out, so the result stays a statement of
intent instead of a snapshot of every default at the moment it ran -- except the
handful of REQUIRED fields, which are emitted empty so a missing one is visible
rather than silently absent.`,
	Args: cobra.NoArgs,
}

var configYAMLCmd = &cobra.Command{
	Use:   "yaml",
	Short: "Print an annotated config.yaml, or the config.yaml equivalent of a .env",
	Args:  cobra.NoArgs,
	RunE:  runConfigYAML,
}

var configEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Print an annotated .env of ROKSBNKCTL_* overrides, or the .env equivalent of a config.yaml",
	Args:  cobra.NoArgs,
	RunE:  runConfigEnv,
}

func init() {
	for _, c := range []*cobra.Command{configYAMLCmd, configEnvCmd} {
		c.Flags().StringVar(&flagConfigFromYAML, "from-yaml", "", "read settings from a config.yaml and print the populated equivalent (no comments)")
		c.Flags().StringVar(&flagConfigFromEnv, "from-env", "", "read settings from a .env file and print the populated equivalent (no comments)")
	}
	configCmd.AddCommand(configYAMLCmd, configEnvCmd)
	rootCmd.AddCommand(configCmd)
}

func runConfigYAML(cmd *cobra.Command, _ []string) error {
	ws, annotated, err := loadConfigSource()
	if err != nil {
		return err
	}
	if annotated {
		_, err := cmd.OutOrStdout().Write(exampleConfigYAML)
		return err
	}
	b, err := yaml.Marshal(ws)
	if err != nil {
		return fmt.Errorf("rendering config.yaml: %w", err)
	}
	_, err = cmd.OutOrStdout().Write(b)
	return err
}

func runConfigEnv(cmd *cobra.Command, _ []string) error {
	ws, annotated, err := loadConfigSource()
	if err != nil {
		return err
	}
	if annotated {
		_, err := cmd.OutOrStdout().Write(exampleEnvFile)
		return err
	}
	lines, err := envLinesFor(ws)
	if err != nil {
		return err
	}
	for _, l := range lines {
		fmt.Fprintln(cmd.OutOrStdout(), l)
	}
	return nil
}

// loadConfigSource resolves the --from flags to a workspace. It returns
// annotated=true when neither is set, which is the "print the template" case.
func loadConfigSource() (*config.Workspace, bool, error) {
	switch {
	case flagConfigFromYAML != "" && flagConfigFromEnv != "":
		return nil, false, fmt.Errorf("--from-yaml and --from-env are alternatives; pass one")
	case flagConfigFromYAML != "":
		b, err := os.ReadFile(flagConfigFromYAML)
		if err != nil {
			return nil, false, fmt.Errorf("reading %s: %w", flagConfigFromYAML, err)
		}
		var ws config.Workspace
		if err := yaml.Unmarshal(b, &ws); err != nil {
			return nil, false, fmt.Errorf("parsing %s: %w", flagConfigFromYAML, err)
		}
		return &ws, false, nil
	case flagConfigFromEnv != "":
		env, err := readDotEnv(flagConfigFromEnv)
		if err != nil {
			return nil, false, err
		}
		var ws config.Workspace
		// The SAME override machinery `init --override-from-env` uses, so a value
		// lands where it would in a real run rather than where a second parser
		// thinks it should.
		config.OverrideFromMap(&ws, env)
		return &ws, false, nil
	default:
		return nil, true, nil
	}
}

// envLinesFor renders a workspace as ROKSBNKCTL_* assignments, using the derived
// name->path map so the names cannot drift from the ones the tool reads.
//
// Four things it deliberately does NOT do:
//
//   - print secrets. This output is piped to a file; writing an API key into it
//     puts a credential on disk from a command whose own template says to keep
//     them out of the file. Secret-bearing settings are named with an empty
//     value and a comment, so the reader learns the variable exists and supplies
//     it themselves.
//   - emit a setting the input did not set. Several fields marshal at their zero
//     value because they are not omitempty, so a config.yaml with no cluster
//     block still produced ROKSBNKCTL_CLUSTER_CREATE=false. Anything matching an
//     empty workspace is skipped.
//   - lose a list. A list path arrives as name[0]; the whole list is joined the
//     way the override parses it, rather than the first element being emitted
//     alone or the setting dropped.
//   - emit a value the shell would re-split. `a b` unquoted assigns `a` and then
//     tries to run `b`.
func envLinesFor(ws *config.Workspace) ([]string, error) {
	tree, err := marshalTree(ws)
	if err != nil {
		return nil, err
	}
	baseline, err := marshalTree(&config.Workspace{})
	if err != nil {
		return nil, err
	}

	var out []string
	for name, path := range config.OverridePaths() {
		if path == "" {
			continue
		}
		v, ok := lookupPath(tree, path)
		if !ok || v == "" {
			continue
		}
		// A field that is not omitempty marshals at its zero value whether or not
		// the input mentioned it.
		if b, ok := lookupPath(baseline, path); ok && b == v {
			continue
		}
		if isSecretPath(path) {
			out = append(out, "# "+name+"=            # secret — set it in the environment, not this file")
			continue
		}
		out = append(out, name+"="+shellQuote(v))
	}
	sort.Strings(out)
	return out, nil
}

func marshalTree(ws *config.Workspace) (map[string]any, error) {
	b, err := yaml.Marshal(ws)
	if err != nil {
		return nil, fmt.Errorf("rendering workspace: %w", err)
	}
	var tree map[string]any
	if err := yaml.Unmarshal(b, &tree); err != nil {
		return nil, fmt.Errorf("re-reading workspace: %w", err)
	}
	return tree, nil
}

// isSecretPath reports whether a config path carries a credential. Keyed on the
// path rather than a list of variable names: the naming convention (*_b64 for
// obfuscated secrets, plus the explicit password/token fields) is what the
// config schema already uses, so a new secret field is covered on arrival.
func isSecretPath(path string) bool {
	leaf := path
	if i := strings.LastIndex(path, "."); i >= 0 {
		leaf = path[i+1:]
	}
	switch {
	case path == "bnkforge.ca_b64":
		// A certificate is PUBLIC data. The config field says so in as many words:
		// "unlike the `_b64` credential fields — this is encoded only for
		// single-line YAML safety". Withholding it would drop a working setting
		// from the conversion to protect nothing.
		return false
	case strings.HasSuffix(leaf, "_b64"):
		return true
	case strings.Contains(leaf, "password"), strings.Contains(leaf, "secret"),
		strings.Contains(leaf, "token"), strings.Contains(leaf, "api_key"):
		return true
	}
	return false
}

// shellQuote wraps a value that a shell would otherwise re-split or interpret.
func shellQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\"'$`\\#&|;<>()*?[]{}!~") {
		return v
	}
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func lookupPath(tree map[string]any, path string) (string, bool) {
	// A path can index a list, and there are two distinct shapes:
	//
	//   a.b[0]            a scalar LIST whose whole contents are the setting
	//   a.b[1].c          one FIELD of one element -- the per-zone family, where
	//                     ZONE2_* is zones[1] and the index carries meaning
	//
	// Collapsing the second into the first was the earlier bug: every zone
	// resolved to zones[0], so a three-zone config emitted zone 1's values three
	// times. Multi-zone is the only shape this tool deploys, so that is the whole
	// network configuration silently wrong.
	cur := any(tree)
	for _, seg := range strings.Split(path, ".") {
		idx := -1
		if i := strings.Index(seg, "["); i >= 0 {
			n, err := strconv.Atoi(strings.TrimSuffix(seg[i+1:], "]"))
			if err != nil {
				return "", false
			}
			seg, idx = seg[:i], n
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[seg]
		if !ok {
			return "", false
		}
		if idx >= 0 {
			list, ok := cur.([]any)
			if !ok || idx >= len(list) {
				return "", false
			}
			// A list of scalars indexed at [0] means "the whole list"; a list of
			// maps means "this element", and the remaining segments address into it.
			if _, isMap := list[idx].(map[string]any); !isMap && idx == 0 {
				parts := make([]string, 0, len(list))
				for _, e := range list {
					parts = append(parts, fmt.Sprint(e))
				}
				return strings.Join(parts, ","), true
			}
			cur = list[idx]
		}
	}
	if cur == nil {
		return "", false
	}
	if list, ok := cur.([]any); ok {
		if len(list) == 0 {
			return "", false
		}
		parts := make([]string, 0, len(list))
		for _, e := range list {
			parts = append(parts, fmt.Sprint(e))
		}
		return strings.Join(parts, ","), true
	}
	if _, isMap := cur.(map[string]any); isMap {
		return "", false
	}
	return fmt.Sprint(cur), true
}

// readDotEnv parses KEY=value lines, ignoring blanks, comments and an optional
// leading `export`. Values keep their surrounding quotes stripped but are
// otherwise verbatim — the override parsers do their own validation.
func readDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()

	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		// A trailing comment on a value line is common in the templates this
		// command prints, so it must not become part of the value.
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		v = strings.Trim(v, `"'`)
		env[k] = v
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return env, nil
}
