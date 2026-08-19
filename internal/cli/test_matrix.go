package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	execbackend "github.com/jgruberf5/roksbnkctl/internal/exec"
	"github.com/jgruberf5/roksbnkctl/internal/test"
)

var (
	flagMatrixFile   string
	flagMatrixOnly   string
	flagMatrixDryRun bool
)

var testMatrixCmd = &cobra.Command{
	Use:   "matrix",
	Short: "Run the declarative BNK-on-ROKS performance grid (iperf3 L4 + h2load L7)",
	Long: `roksbnkctl test matrix runs a declarative performance grid against an
already-deployed cluster + BNK. It reads a matrix.yaml describing cells
(endpoint-pair × test-family) and runs each, emitting a report shaped
like the BNK-on-ROKS perf plan.

Two families:
  iperf3  — L4 TCP throughput over an L4Route VIP, with content-size knobs
            (length: "128" vs "512K") — the L4 analog of the plan's
            128 B / 512 KB payload axis.
  l7      — h2load against an HTTPRoute, http and https (TLS terminate at
            TMM); cps / tps / throughput modes.

The locality axis (same-zone / different-zone / different-VPC) is implicit
in which jumphost ("vsi") endpoint a cell names as its client, so the
per-AZ jumphost targets the Testing phase auto-registers (jumphost,
jumphost-<zone>) are the traffic-source fleet. The Testing phase
preinstalls both generators (iperf3 + h2load/nghttp2-client) on every
jumphost, so the ssh runs need no --bootstrap.

This command provisions only ephemeral, runner-owned fixtures (an iperf3
server, an HTTP file backend, and optional L4Route/HTTPRoute/TLS objects
that attach to the EXISTING Gateway by name) and tears them down after
(unless --keep). It never touches Terraform or the gateway-phase objects.

  roksbnkctl test matrix --dry-run         expand + print the plan, no cluster calls
  roksbnkctl test matrix --only 'L7*'      run a subset (glob on cell name)
  roksbnkctl test matrix -o md             Markdown grid report

Honors -o json|text|md with the roksbnkctl.v1 schema. Exit code is
non-zero if any cell fails.`,
	Args: cobra.NoArgs,
	RunE: runTestMatrixCmd,
}

func init() {
	testMatrixCmd.Flags().StringVar(&flagMatrixFile, "file", "", "path to matrix.yaml (default: <workspace>/matrix.yaml, then ./matrix.yaml)")
	testMatrixCmd.Flags().StringVar(&flagMatrixOnly, "only", "", "glob on cell name; run only matching cells")
	testMatrixCmd.Flags().BoolVar(&flagMatrixDryRun, "dry-run", false, "expand the grid and print the plan + fixtures; no cluster calls")
	testMatrixCmd.Flags().BoolVar(&flagKeepFixtures, "keep", false, "leave fixtures running after the run")
	testCmd.AddCommand(testMatrixCmd)
}

func runTestMatrixCmd(cmd *cobra.Command, _ []string) error {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return err
	}

	path, err := resolveMatrixFile(cctx)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading matrix file: %w", err)
	}
	spec, err := test.ParseMatrix(raw)
	if err != nil {
		return err
	}
	cells, err := spec.Expand(flagMatrixOnly)
	if err != nil {
		return err
	}

	// Dry-run: print the resolved plan (and the fixtures we would apply)
	// without a single cluster call. Mirrors the --dry-run discipline of
	// scripts/e2e-three-phase.sh.
	if flagMatrixDryRun {
		fmt.Fprintln(os.Stderr, "→ matrix file:", path)
		fmt.Fprint(os.Stdout, test.PlanString(cells))
		if manifest := renderMatrixFixtures(spec); manifest != "" {
			fmt.Fprintln(os.Stdout, "\n--- fixtures (would apply) ---")
			fmt.Fprintln(os.Stdout, manifest)
		}
		return nil
	}

	if cctx.Workspace == nil {
		return config.WorkspaceNotReady(cctx.WorkspaceName)
	}

	// Deploy fixtures, then schedule teardown unless --keep.
	if err := deployMatrixFixtures(cmd.Context(), spec); err != nil {
		return err
	}
	if !flagKeepFixtures {
		defer teardownMatrixFixtures(spec)
	}

	start := time.Now()
	probes := make([]test.ProbeResult, 0, len(cells))
	for i, c := range cells {
		fmt.Fprintf(os.Stderr, "→ [%d/%d] %s (%s, client %s → %s)\n", i+1, len(cells), c.Name, c.Family, c.ClientLabel, c.ServerLabel)
		probes = append(probes, runMatrixCell(cmd.Context(), cctx, c))
	}
	run := test.NewMatrixRun(start, probes)
	return outputMatrix(run)
}

// resolveMatrixFile picks the matrix.yaml: explicit --file, else the
// workspace dir, else ./matrix.yaml.
func resolveMatrixFile(cctx *config.Context) (string, error) {
	if flagMatrixFile != "" {
		return flagMatrixFile, nil
	}
	candidates := []string{}
	if cctx != nil && cctx.WorkspaceName != "" {
		if dir, err := config.WorkspaceDir(cctx.WorkspaceName); err == nil && dir != "" {
			candidates = append(candidates, filepath.Join(dir, "matrix.yaml"))
		}
	}
	candidates = append(candidates, "matrix.yaml")
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no matrix.yaml found (looked in %s); pass --file", strings.Join(candidates, ", "))
}

// runMatrixCell dispatches one cell's tool over its resolved backend and
// folds the parsed result into a ProbeResult.
func runMatrixCell(ctx context.Context, cctx *config.Context, c test.ResolvedCell) test.ProbeResult {
	p := test.ProbeResult{Suite: c.Family, Name: c.Name}
	args, err := c.Argv()
	if err != nil {
		p.Status = test.StatusFail
		p.Detail = err.Error()
		return p
	}

	start := time.Now()
	stdout, rc, runErr := dispatchMatrixTool(ctx, cctx, c.BackendSpec, c.Tool(), args)
	p.DurationMS = time.Since(start).Milliseconds()

	if runErr != nil && rc == 0 {
		p.Status = test.StatusFail
		p.Detail = runErr.Error()
		return p
	}
	if rc != 0 {
		p.Status = test.StatusFail
		p.Detail = fmt.Sprintf("%s exited %d", c.Tool(), rc)
		return p
	}

	switch c.Family {
	case test.FamilyIperf3:
		gbps, rtx, perr := test.ParseIperf3JSON([]byte(stdout))
		if perr != nil {
			p.Status = test.StatusFail
			p.Detail = "parsing iperf3 output: " + perr.Error()
			return p
		}
		p.Status = test.StatusPass
		p.Detail = fmt.Sprintf("%.2f Gbit/s (%d retransmits)", gbps, rtx)
		p.Extra = map[string]any{"throughput_gbps": gbps, "retransmits": rtx}
		if c.Iperf3.Length != "" {
			p.Extra["length"] = c.Iperf3.Length
		}
	case test.FamilyL7:
		res, perr := test.ParseH2load(stdout)
		if perr != nil {
			p.Status = test.StatusFail
			p.Detail = "parsing h2load output: " + perr.Error()
			return p
		}
		p.Status = test.StatusPass
		p.Detail = fmt.Sprintf("%.0f req/s, %.1f Mbit/s, %.2fms mean", res.ReqPerSec, res.BytesPerSec*8/1e6, res.ReqTimeMeanS*1e3)
		p.Extra = res.Extra()
	}
	if p.Extra == nil {
		p.Extra = map[string]any{}
	}
	p.Extra["client"] = c.ClientLabel
	p.Extra["server"] = c.ServerLabel
	return p
}

// dispatchMatrixTool runs tool+args over the named backend (""=local,
// ssh:<target>, or k8s) and returns its stdout. Mirrors the iperf3
// client dispatchers in test.go.
func dispatchMatrixTool(ctx context.Context, cctx *config.Context, backendSpec, tool string, args []string) (string, int, error) {
	be, err := execbackend.ResolveBackend(backendSpec)
	if err != nil {
		return "", 0, err
	}
	var env []string
	if strings.HasPrefix(backendSpec, "ssh:") {
		target := execbackend.SpecTarget(backendSpec)
		wsName := ""
		if cctx != nil {
			wsName = cctx.WorkspaceName
		}
		execbackend.SetSSHOpts(execbackend.SSHBackendOpts{
			Workspace:       wsName,
			Bootstrap:       flagBootstrap,
			InsecureHostKey: flagInsecureHostKey,
		})
		env = []string{"ROKSBNKCTL_SSH_TARGET=" + target}
	}
	argv := append([]string{tool}, args...)
	var stdout strings.Builder
	rc, runErr := be.Run(ctx, argv, execbackend.RunOpts{
		Env:    env,
		Stdout: &stdout,
		Stderr: os.Stderr,
	})
	return stdout.String(), rc, runErr
}

func outputMatrix(run test.MatrixRun) error {
	switch flagOutput {
	case "json":
		if err := test.WriteJSON(os.Stdout, run); err != nil {
			return err
		}
	case "md":
		test.WriteMatrixMarkdown(os.Stdout, run)
	default:
		test.WriteMatrixMarkdown(os.Stderr, run)
	}
	if run.Overall == test.StatusFail {
		os.Exit(1)
	}
	return nil
}
