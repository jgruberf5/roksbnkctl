package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	awspkg "github.com/JLCode-tech/awsbnkctl/internal/aws"
	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/forge"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

var (
	flagForgeMCPURL    string
	flagForgeProject   string
	flagForgeScan      bool
	flagForgePurge     bool
	flagForgeKubeconf  string
	flagForgeClusterNm string
	flagForgeConfig    string
)

var forgeCmd = &cobra.Command{
	Use:   "forge",
	Short: "Register the current workspace with a BNK-Forge instance over MCP",
	Long: `forge wires this workspace into a running BNK-Forge instance so
forge can manage and observe the AWS infra + EKS + BNK deployment
awsbnkctl provisioned.

Talks to forge's MCP server (Streamable HTTP / JSON-RPC) at
http://localhost:8081/mcp/ by default. Override with --forge-mcp-url or
the AWSBNKCTL_FORGE_MCP_URL environment variable.`,
}

var forgeRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register the workspace's EKS cluster with forge (idempotent)",
	Long: `Creates a forge project (awsbnkctl-<workspace>) and registers
the workspace's EKS cluster under it. Idempotent — re-running on an
already-registered workspace reuses the existing project/cluster.

By default, awsbnkctl uses its in-process EKS auth flow (presigned
sts:GetCallerIdentity URL) to generate a kubeconfig and hand it to forge.
Pass --kubeconfig <path> to upload a pre-existing kubeconfig file instead.

Use --scan to also trigger forge's scan_cluster + bnk_health after
registration as a smoke check.`,
	RunE: runForgeRegister,
}

var forgeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show this workspace's forge registration state",
	RunE:  runForgeStatus,
}

var forgeUnregisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Remove this workspace's forge registration",
	Long: `Deletes the cluster registration from forge and removes the
local forge_link.json. Pass --purge to also delete the forge project
(use with caution — irreversible from awsbnkctl's side).`,
	RunE: runForgeUnregister,
}

func init() {
	forgeCmd.PersistentFlags().StringVar(&flagForgeMCPURL, "forge-mcp-url", "",
		"forge MCP endpoint (default $AWSBNKCTL_FORGE_MCP_URL, fallback "+forge.DefaultMCPURL+")")
	forgeCmd.PersistentFlags().StringVarP(&flagForgeConfig, "config", "f", "",
		"path to cluster.yaml (intent mode); when set, forge targets the cluster.yaml's metadata.name/region and stores the link in the cluster's state dir instead of the legacy workspace")

	forgeRegisterCmd.Flags().StringVar(&flagForgeProject, "project-name", "",
		"forge project name (default awsbnkctl-<workspace>)")
	forgeRegisterCmd.Flags().StringVar(&flagForgeClusterNm, "cluster-name", "",
		"EKS cluster name to register (default: workspace cluster.name)")
	forgeRegisterCmd.Flags().StringVar(&flagForgeKubeconf, "kubeconfig", "",
		"path to a pre-existing kubeconfig (default: generate in-process via EKS auth)")
	forgeRegisterCmd.Flags().BoolVar(&flagForgeScan, "scan", false,
		"after register, call scan_cluster + bnk_health as a smoke check")

	forgeUnregisterCmd.Flags().BoolVar(&flagForgePurge, "purge", false,
		"also delete the forge project (default: cluster only; project preserved)")

	forgeCmd.AddCommand(forgeRegisterCmd, forgeStatusCmd, forgeUnregisterCmd)
	rootCmd.AddCommand(forgeCmd)
}

// forgeTarget carries the resolved identity for any forge sub-command.
// It is populated either from a cluster.yaml (intent mode, --config) or from
// the legacy workspace config (~/.awsbnkctl/<ws>/config.yaml).
type forgeTarget struct {
	clusterName string // EKS cluster name
	region      string // AWS region
	profile     string // AWS named profile ("" → use AWS_PROFILE env / default chain)
	linkDir     string // directory where forge_link.json lives
	label       string // human-facing identity label (workspace name or cluster name)
	mcpURL      string // preferred MCP URL from intent ("" → fall through to flag/env/default)
}

// resolveForgeTarget returns a forgeTarget from either:
//   - intent mode (--config <cluster.yaml>), or
//   - legacy workspace mode (~/.awsbnkctl/<ws>/config.yaml).
func resolveForgeTarget() (*forgeTarget, error) {
	if flagForgeConfig != "" {
		cl, err := intent.Load(flagForgeConfig)
		if err != nil {
			return nil, fmt.Errorf("loading --config %s: %w", flagForgeConfig, err)
		}
		if cl.Metadata.Name == "" {
			return nil, fmt.Errorf("cluster.yaml %s: metadata.name is required", flagForgeConfig)
		}
		if cl.Metadata.Region == "" {
			return nil, fmt.Errorf("cluster.yaml %s: metadata.region is required", flagForgeConfig)
		}
		t := &forgeTarget{
			clusterName: cl.Metadata.Name,
			region:      cl.Metadata.Region,
			profile:     os.Getenv("AWS_PROFILE"),
			linkDir:     cl.StateDir(),
			label:       cl.Metadata.Name,
		}
		if cl.Forge != nil && cl.Forge.MCPURL != "" {
			t.mcpURL = cl.Forge.MCPURL
		}
		return t, nil
	}

	// Legacy workspace path.
	cctx, err := requireWorkspace()
	if err != nil {
		return nil, err
	}
	wsDir, err := config.WorkspaceDir(cctx.WorkspaceName)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace dir: %w", err)
	}
	return &forgeTarget{
		clusterName: cctx.Workspace.Cluster.Name,
		region:      cctx.Workspace.AWS.Region,
		profile:     cctx.Workspace.AWS.Profile,
		linkDir:     wsDir,
		label:       cctx.WorkspaceName,
		mcpURL:      "",
	}, nil
}

// pickMCPURL returns the MCP URL to use, in priority order:
//  1. explicit --forge-mcp-url flag
//  2. mcpURL from the intent cluster.yaml forge.mcpUrl field
//  3. "" (forge.NewClient falls back to $AWSBNKCTL_FORGE_MCP_URL / DefaultMCPURL)
func pickMCPURL(intentMCP string) string {
	if flagForgeMCPURL != "" {
		return flagForgeMCPURL
	}
	return intentMCP
}

func runForgeRegister(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	// --cluster-name overrides the resolved cluster name in both modes.
	clusterName := t.clusterName
	if flagForgeClusterNm != "" {
		clusterName = flagForgeClusterNm
	}
	if clusterName == "" {
		return errors.New("no cluster name — set cluster.name in the workspace or pass --cluster-name")
	}

	if t.region == "" {
		return errors.New("workspace AWS.region is empty — run `awsbnkctl init` first")
	}

	// 1) build kubeconfig: either read --kubeconfig <path> or generate
	// in-process via the EKS presigned-URL flow (PRD 07).
	kubeconfigYAML, err := buildKubeconfig(cmd.Context(), t.profile, clusterName, t.region)
	if err != nil {
		return err
	}

	// 2) talk to forge over MCP
	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	if !flagQuiet {
		fmt.Fprintf(os.Stderr, "→ forge MCP: %s\n", fc.URL())
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
	defer cancel()

	res, err := forge.Register(ctx, fc, forge.RegisterRequest{
		WorkspaceName:    t.label,
		WorkspaceDir:     t.linkDir,
		ProjectName:      flagForgeProject,
		ClusterName:      clusterName,
		Region:           t.region,
		Kubeconfig:       kubeconfigYAML,
		PostRegisterScan: flagForgeScan,
	})
	if err != nil {
		return err
	}

	// 3) report
	fmt.Printf("✓ registered with forge\n")
	fmt.Printf("  project:   %s (id=%d)\n", res.Link.ProjectName, res.Link.ProjectID)
	fmt.Printf("  cluster:   %s (id=%d)\n", res.Link.ClusterName, res.Link.ClusterID)
	fmt.Printf("  link:      %s\n", forge.LinkPath(t.linkDir))
	fmt.Printf("  mcp:       %s\n", res.ForgeURL)
	if flagForgeScan {
		fmt.Printf("  scan:      %s\n", oneLine(res.ScanOutput))
		fmt.Printf("  health:    %s\n", oneLine(res.HealthCheck))
	}
	return nil
}

func runForgeStatus(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	st, err := forge.Status(ctx, fc, t.linkDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "workspace %q has no forge link — run `awsbnkctl forge register`\n", t.label)
			return nil
		}
		return err
	}

	fmt.Printf("workspace:    %s\n", st.Link.Workspace)
	fmt.Printf("project:      %s (id=%d)\n", st.Link.ProjectName, st.Link.ProjectID)
	fmt.Printf("cluster:      %s (id=%d)\n", st.Link.ClusterName, st.Link.ClusterID)
	fmt.Printf("registered:   %s\n", st.Link.RegisteredAt.Format(time.RFC3339))
	fmt.Printf("forge mcp:    %s\n", st.Link.ForgeMCPURL)
	fmt.Printf("reachable:    %v\n", st.Reachable)
	if st.ForgeVersion != "" {
		fmt.Printf("forge version: %s\n", oneLine(st.ForgeVersion))
	}
	if st.Err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v\n", st.Err)
	}
	return nil
}

func runForgeUnregister(cmd *cobra.Command, _ []string) error {
	t, err := resolveForgeTarget()
	if err != nil {
		return err
	}

	fc := forge.NewClient(pickMCPURL(t.mcpURL))
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	if err := forge.Unregister(ctx, fc, t.linkDir, flagForgePurge); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "workspace %q has no forge link — nothing to do\n", t.label)
			return nil
		}
		return err
	}
	fmt.Printf("✓ workspace unregistered from forge%s\n",
		map[bool]string{true: " (project purged)", false: ""}[flagForgePurge])
	return nil
}

// buildKubeconfig returns the kubeconfig bytes for an EKS cluster —
// either from --kubeconfig <path> or generated in-process via the EKS
// presigned-URL auth flow. profile is the AWS named profile to use
// ("" → use the default credential chain / AWS_PROFILE env).
func buildKubeconfig(ctx context.Context, profile, clusterName, region string) ([]byte, error) {
	if flagForgeKubeconf != "" {
		b, err := os.ReadFile(flagForgeKubeconf) // #nosec G304 -- explicit operator-supplied --kubeconfig path; matches kubectl's own UX
		if err != nil {
			return nil, fmt.Errorf("read --kubeconfig %s: %w", flagForgeKubeconf, err)
		}
		return b, nil
	}

	clients, err := awspkg.NewClients(ctx, awspkg.Options{
		Region:  region,
		Profile: profile,
	})
	if err != nil {
		return nil, fmt.Errorf("aws clients: %w", err)
	}

	ci, err := clients.DescribeCluster(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("eks describe-cluster %s: %w", clusterName, err)
	}

	yaml, err := clients.KubeconfigFromCluster(ci)
	if err != nil {
		return nil, fmt.Errorf("generate kubeconfig: %w", err)
	}
	return []byte(yaml), nil
}

// oneLine collapses multi-line tool output to a single line for
// status-line display. Long JSON payloads get truncated. Uses a
// strings.Builder so multi-byte runes (e.g. the ellipsis we append on
// truncation) survive intact.
func oneLine(s string) string {
	const max = 160
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' {
			b.WriteByte(' ')
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > max {
		// Walk back to the last rune boundary so we don't slice through
		// a multi-byte sequence.
		cut := max
		for cut > 0 && (out[cut]&0xC0) == 0x80 {
			cut--
		}
		return out[:cut] + "…"
	}
	return out
}
