package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// The `backend` command group persists the per-tool execution-backend default
// (the `exec:` block) from the CLI — so changing a workspace's default never
// requires hand-editing config.yaml. The per-invocation `--backend` flag still
// overrides whatever is persisted here.
//
//	roksbnkctl backend show                 # effective backend per tool (+ source)
//	roksbnkctl backend set <tool> <backend> # persist exec.<tool>.backend
//	roksbnkctl backend unset <tool>         # drop the override (revert to the built-in default)

// backendTools is the canonical set of tools that dispatch through a backend —
// the keys of the built-in per-tool default table.
var backendTools = orchestration.PerToolDefaultBackends()

var backendCmd = &cobra.Command{
	Use:   "backend",
	Short: "Configure the per-tool execution backend default (writes config.yaml for you)",
	Long: `Set or inspect the per-tool execution-backend default — the exec: block — from
the CLI, instead of hand-editing config.yaml.

  show              effective backend for each tool, and where it came from
  set <tool> <be>   persist exec.<tool>.backend (be: local | docker | k8s | ssh:<target>)
  unset <tool>      remove the override; the tool reverts to its built-in default

The per-invocation ` + "`--backend`" + ` flag still wins over whatever is persisted here.`,
}

var backendShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the effective execution backend for each tool",
	Args:  cobra.NoArgs,
	RunE:  runBackendShow,
}

var backendSetCmd = &cobra.Command{
	Use:   "set <tool> <backend>",
	Short: "Persist a per-tool execution-backend default",
	Args:  cobra.ExactArgs(2),
	RunE:  runBackendSet,
}

var backendUnsetCmd = &cobra.Command{
	Use:   "unset <tool>",
	Short: "Remove a per-tool backend override (revert to the built-in default)",
	Args:  cobra.ExactArgs(1),
	RunE:  runBackendUnset,
}

func init() {
	backendCmd.AddCommand(backendShowCmd, backendSetCmd, backendUnsetCmd)
	rootCmd.AddCommand(backendCmd)
}

// validBackendSpec syntactically validates a backend spec without requiring the
// backend to be registered in this build (k8s/ssh resolve lazily at run time).
func validBackendSpec(spec string) error {
	name := spec
	target := ""
	if i := strings.IndexByte(spec, ':'); i >= 0 {
		name, target = spec[:i], spec[i+1:]
	}
	switch name {
	case "local", "docker", "k8s":
		if target != "" {
			return fmt.Errorf("backend %q takes no :<target>", name)
		}
		return nil
	case "ssh":
		if target == "" {
			return fmt.Errorf("the ssh backend needs a target: ssh:<target>")
		}
		return nil
	default:
		return fmt.Errorf("unknown backend %q (want local | docker | k8s | ssh:<target>)", spec)
	}
}

func knownTool(tool string) bool {
	_, ok := backendTools[tool]
	return ok
}

func sortedTools() []string {
	ts := make([]string, 0, len(backendTools))
	for t := range backendTools {
		ts = append(ts, t)
	}
	sort.Strings(ts)
	return ts
}

func runBackendShow(_ *cobra.Command, _ []string) error {
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	fmt.Printf("workspace: %s\n", name)
	for _, tool := range sortedTools() {
		def := backendTools[tool]
		if entry, ok := ws.Exec[tool]; ok && entry.Backend != "" {
			fmt.Printf("  %-10s %s  (workspace exec: override; default %s)\n", tool+":", entry.Backend, def)
		} else {
			fmt.Printf("  %-10s %s  (built-in default)\n", tool+":", def)
		}
	}
	return nil
}

func runBackendSet(_ *cobra.Command, args []string) error {
	tool, spec := args[0], args[1]
	if !knownTool(tool) {
		return fmt.Errorf("unknown tool %q (want one of: %s)", tool, strings.Join(sortedTools(), " "))
	}
	if err := validBackendSpec(spec); err != nil {
		return err
	}
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	if ws.Exec == nil {
		ws.Exec = map[string]config.ExecToolCfg{}
	}
	ws.Exec[tool] = config.ExecToolCfg{Backend: spec}
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %s now uses the %s backend in %q.\n", tool, spec, name)
	return nil
}

func runBackendUnset(_ *cobra.Command, args []string) error {
	tool := args[0]
	if !knownTool(tool) {
		return fmt.Errorf("unknown tool %q (want one of: %s)", tool, strings.Join(sortedTools(), " "))
	}
	name, ws, err := loadWorkspaceForEdit()
	if err != nil {
		return err
	}
	if _, ok := ws.Exec[tool]; !ok {
		fmt.Fprintf(os.Stderr, "✓ %s has no override in %q (already at the built-in default %s).\n", tool, name, backendTools[tool])
		return nil
	}
	delete(ws.Exec, tool)
	if len(ws.Exec) == 0 {
		ws.Exec = nil // keep an emptied exec: block out of the file
	}
	if err := config.SaveWorkspace(name, ws); err != nil {
		return fmt.Errorf("saving workspace: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ %s reverted to its built-in default (%s) in %q.\n", tool, backendTools[tool], name)
	return nil
}
