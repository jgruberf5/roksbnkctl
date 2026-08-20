package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// `roksbnkctl test hosts {list,add,remove,clear}` — Sprint 24 CLI surface
// for managing the workspace's `test.connectivity.extra_hosts` slice.
//
// Mirrors the `roksbnkctl targets {list,add,remove}` ergonomic precedent:
// idempotent add/remove (no-op + log on already-present / absent),
// hermetic persistence through the existing workspace marshaller.
//
// Surface decisions:
//   - Tight scope: only `test.connectivity.extra_hosts`. DNS / throughput
//     config fields keep their flag-driven equivalents.
//   - `list` empty output emits zero bytes + exit 0 (NOT an error);
//     JSON form emits `[]` for empty.
//   - `add` validates each arg via `url.Parse`.
//   - `clear` confirmation prompt defaults to No; `--auto` skips.
//
// Argv strictness (Sprint 21 contract): `list` and `clear` are
// `cobra.NoArgs`; `add` and `remove` are `cobra.MinimumNArgs(1)`.

// flagTestHostsClearAuto is the `--auto` flag local to
// `roksbnkctl test hosts clear`. Scoped local (rather than reusing the
// global `flagAuto`) so the lifecycle command group's flag namespace
// stays distinct from this UX-only command's namespace.
var flagTestHostsClearAuto bool

var testHostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Manage test.connectivity.extra_hosts in the workspace config",
	Long: `Test hosts are URLs probed by ` + "`roksbnkctl test connectivity`" + ` and
(in workspace-driven mode) ` + "`roksbnkctl test dns`" + `. They are stored under
` + "`test.connectivity.extra_hosts`" + ` in the workspace's config.yaml.

This command group is the first-class CLI for managing that slice —
without it, the only path is hand-editing
~/.roksbnkctl/<workspace>/config.yaml. Mirrors ` + "`roksbnkctl targets`" + `'
ergonomics: idempotent add/remove, ` + "`-o json`" + ` on list.`,
	Args: cobra.NoArgs,
}

var testHostsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List configured test hosts (one per line; -o json for array)",
	Long: `Prints the workspace's test.connectivity.extra_hosts one per line on
stdout. Empty list emits zero bytes + exit 0 (NOT an error;
distinguishes "nothing configured" from "command failed" by exit code).

With ` + "`-o json`" + `, emits the slice as a JSON array (` + "`[]`" + ` when empty).`,
	Args: cobra.NoArgs,
	RunE: runTestHostsList,
}

var testHostsAddCmd = &cobra.Command{
	Use:   "add <url> [<url> ...]",
	Short: "Append URLs to test.connectivity.extra_hosts (idempotent)",
	Long: `Appends each <url> to test.connectivity.extra_hosts. Idempotent —
adding an already-present URL is a no-op (logs to stderr; exit 0).

Each <url> is validated via the std-lib url.Parse; non-URLs are
rejected with an actionable error naming the offending arg. Insertion
order is preserved.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTestHostsAdd,
}

var testHostsRemoveCmd = &cobra.Command{
	Use:   "remove <url> [<url> ...]",
	Short: "Remove URLs from test.connectivity.extra_hosts (idempotent)",
	Long: `Removes each <url> from test.connectivity.extra_hosts. Idempotent —
removing an absent URL is a no-op (logs to stderr; exit 0). Preserves
the order of remaining entries.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTestHostsRemove,
}

var testHostsClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove ALL entries from test.connectivity.extra_hosts",
	Long: `Clears test.connectivity.extra_hosts. Confirmation prompt defaults to
No; pass ` + "`--auto`" + ` to skip the prompt (matches the
` + "`roksbnkctl down`" + ` / ` + "`cluster down`" + ` confirmation pattern).`,
	Args: cobra.NoArgs,
	RunE: runTestHostsClear,
}

func init() {
	testHostsClearCmd.Flags().BoolVar(&flagTestHostsClearAuto, "auto", false, "skip the confirmation prompt")

	testHostsCmd.AddCommand(testHostsListCmd, testHostsAddCmd, testHostsRemoveCmd, testHostsClearCmd)
	// testCmd.AddCommand(testHostsCmd) is wired in internal/cli/test.go's
	// init() block — kept there to centralise testCmd's child list.
}

// mutateExtraHosts is the load → mutate → save helper that all four
// RunEs route through. fn receives the current ExtraHosts slice and
// returns the new slice; mutateExtraHosts writes it back via the
// existing workspace marshaller. Keeps the RunEs short and pins the
// persistence path to ONE place for easier audit.
//
// Comment-preservation note: the underlying gopkg.in/yaml.v3 marshaller
// does NOT preserve YAML comments — round-tripping a config.yaml that
// includes operator comments will drop them. Documented in the sprint
// closure as a future-sprint candidate; not blocking this UX surface.
func mutateExtraHosts(workspace string, fn func([]string) []string) error {
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return err
	}
	ws.Test.Connectivity.ExtraHosts = fn(ws.Test.Connectivity.ExtraHosts)
	return config.SaveWorkspace(workspace, ws)
}

// loadExtraHosts is the read-only counterpart used by `list`. Returns
// the slice as it stands on disk; nil/empty handled by the caller.
func loadExtraHosts(workspace string) ([]string, error) {
	ws, err := config.LoadWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return ws.Test.Connectivity.ExtraHosts, nil
}

func runTestHostsList(_ *cobra.Command, _ []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	hosts, err := loadExtraHosts(cctx.WorkspaceName)
	if err != nil {
		return err
	}
	if flagOutput == "json" {
		// Emit `[]` (not `null`) for an empty list so the JSON
		// output is always a valid array shape — easier for jq /
		// CI parsers.
		if hosts == nil {
			hosts = []string{}
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(hosts)
	}
	// Text mode: empty list emits zero bytes + exit 0 (NOT an error).
	for _, h := range hosts {
		fmt.Fprintln(os.Stdout, h)
	}
	return nil
}

func runTestHostsAdd(_ *cobra.Command, args []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	// Validate every arg up front — refuse to mutate anything if any
	// arg is bad. Matches `targets add`'s fail-before-write discipline.
	for _, raw := range args {
		if err := validateHostURL(raw); err != nil {
			return err
		}
	}
	return mutateExtraHosts(cctx.WorkspaceName, func(cur []string) []string {
		out := append([]string(nil), cur...)
		for _, raw := range args {
			if containsString(out, raw) {
				fmt.Fprintf(os.Stderr, "test hosts: %q already present; no-op\n", raw)
				continue
			}
			out = append(out, raw)
			fmt.Fprintf(os.Stderr, "✓ added %q to test.connectivity.extra_hosts\n", raw)
		}
		return out
	})
}

func runTestHostsRemove(_ *cobra.Command, args []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	return mutateExtraHosts(cctx.WorkspaceName, func(cur []string) []string {
		// Build a removal set so multiple args dedupe naturally.
		toRemove := make(map[string]struct{}, len(args))
		for _, raw := range args {
			toRemove[raw] = struct{}{}
			if !containsString(cur, raw) {
				fmt.Fprintf(os.Stderr, "test hosts: %q not present; no-op\n", raw)
			}
		}
		out := make([]string, 0, len(cur))
		for _, h := range cur {
			if _, drop := toRemove[h]; drop {
				continue
			}
			out = append(out, h)
		}
		for _, raw := range args {
			if containsString(cur, raw) {
				fmt.Fprintf(os.Stderr, "✓ removed %q from test.connectivity.extra_hosts\n", raw)
			}
		}
		return out
	})
}

func runTestHostsClear(_ *cobra.Command, _ []string) error {
	cctx, err := requireWorkspace()
	if err != nil {
		return err
	}
	if !flagTestHostsClearAuto && !promptYesNo("Clear ALL test.connectivity.extra_hosts?", false) {
		fmt.Fprintln(os.Stderr, "test hosts clear: declined; no changes")
		return nil
	}
	return mutateExtraHosts(cctx.WorkspaceName, func(_ []string) []string {
		// Set to nil (not empty slice) so YAML output omits the key
		// entirely (the field is tagged `omitempty`). Distinguishes
		// "explicitly cleared" from "never set" only for code that
		// cares; most callers treat nil == empty.
		fmt.Fprintln(os.Stderr, "✓ cleared test.connectivity.extra_hosts")
		return nil
	})
}

// validateHostURL rejects strings that don't parse as URLs. Uses
// url.Parse (the same lenient parser the std-lib http.Client uses) so
// any URL that downstream `test connectivity` would accept passes here
// too. Empty input and scheme-less inputs are rejected — both indicate
// operator fat-finger and would silently no-op at probe time.
func validateHostURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("invalid host URL: empty string")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid host URL %q: %w (expected e.g. https://example.com)", raw, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid host URL %q: missing scheme or host (expected e.g. https://example.com)", raw)
	}
	return nil
}

// containsString is a tiny linear scan; the extra_hosts slice is
// expected to be small (~10s of entries max), so a map allocation
// would be wasted overhead.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
