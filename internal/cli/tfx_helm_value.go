package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// `tfx helm-value` replaces the modules' `helm pull` + `grep` sites — resolving a
// sub-chart version out of the BNK manifest chart (extract_flp_version, the FLO/CIS
// version reads) and extracting a bundled file (extract_prod_jwks). It shells to
// the `helm` binary (required anyway; not linked as an SDK) for the pull, then does
// the extraction in Go — no `grep`/`tar` on the host path.

var (
	flagHelmChart         string
	flagHelmVersion       string
	flagHelmFile          string
	flagHelmSubchart      string
	flagHelmOut           string
	flagHelmBin           string
	flagHelmRegistryLogin string
	flagHelmUsername      string
	flagHelmPasswordEnv   string
)

var tfxHelmValueCmd = &cobra.Command{
	Use:   "helm-value",
	Short: "Pull a chart and extract a value or file from it (internal)",
	Long: `Shells to the helm binary to pull a chart, then extracts data from it in Go.

Subcommands:
  chart-version   read a sub-chart's version out of the pulled manifest YAML
  pull-file       copy a file bundled in the chart to --out`,
}

var tfxHelmChartVersionCmd = &cobra.Command{
	Use:           "chart-version",
	Short:         "Resolve a sub-chart version from the pulled manifest (internal)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXHelmChartVersion,
}

var tfxHelmPullFileCmd = &cobra.Command{
	Use:           "pull-file",
	Short:         "Extract a bundled file from a pulled chart to --out (internal)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXHelmPullFile,
}

func init() {
	for _, c := range []*cobra.Command{tfxHelmChartVersionCmd, tfxHelmPullFileCmd} {
		f := c.Flags()
		f.StringVar(&flagHelmChart, "chart", "", "chart ref (OCI url or repo/chart) (required)")
		f.StringVar(&flagHelmVersion, "version", "", "chart version to pull")
		f.StringVar(&flagHelmFile, "file", "", "path WITHIN the untarred chart (the manifest YAML, or the file to extract) (required)")
		f.StringVar(&flagHelmOut, "out", "", "output path (default: stdout for chart-version)")
		f.StringVar(&flagHelmBin, "helm-bin", "", "helm binary (default: helm on PATH)")
		f.StringVar(&flagHelmRegistryLogin, "registry-login", "", "OCI registry host to `helm registry login` before the pull")
		f.StringVar(&flagHelmUsername, "username", "", "registry username (with --registry-login)")
		f.StringVar(&flagHelmPasswordEnv, "password-env", "", "env var holding the registry password (with --registry-login)")
	}
	tfxHelmChartVersionCmd.Flags().StringVar(&flagHelmSubchart, "subchart", "", "sub-chart path to resolve the version for (required)")
	tfxHelmValueCmd.AddCommand(tfxHelmChartVersionCmd, tfxHelmPullFileCmd)
	tfxCmd.AddCommand(tfxHelmValueCmd)
}

func runTFXHelmChartVersion(cmd *cobra.Command, _ []string) error {
	if flagHelmChart == "" || flagHelmFile == "" || flagHelmSubchart == "" {
		return fmt.Errorf("--chart, --file and --subchart are required")
	}
	dir, cleanup, err := tfxHelmPull(cmd.Context())
	if err != nil {
		return err
	}
	defer cleanup()
	manifest, err := os.ReadFile(filepath.Join(dir, flagHelmFile))
	if err != nil {
		return fmt.Errorf("reading manifest %s in the chart: %w", flagHelmFile, err)
	}
	version, err := extractChartVersion(manifest, flagHelmSubchart)
	if err != nil {
		return err
	}
	if flagHelmOut == "" || flagHelmOut == "-" {
		fmt.Fprintln(cmd.OutOrStdout(), version)
		return nil
	}
	return os.WriteFile(flagHelmOut, []byte(version+"\n"), 0o644)
}

func runTFXHelmPullFile(cmd *cobra.Command, _ []string) error {
	if flagHelmChart == "" || flagHelmFile == "" || flagHelmOut == "" {
		return fmt.Errorf("--chart, --file and --out are required")
	}
	dir, cleanup, err := tfxHelmPull(cmd.Context())
	if err != nil {
		return err
	}
	defer cleanup()
	src := filepath.Join(dir, flagHelmFile)
	if err := os.MkdirAll(filepath.Dir(flagHelmOut), 0o755); err != nil {
		return fmt.Errorf("creating out dir: %w", err)
	}
	if err := copyFileContents(src, flagHelmOut); err != nil {
		return fmt.Errorf("extracting %s from chart: %w", flagHelmFile, err)
	}
	fmt.Fprintf(os.Stderr, "tfx helm-value: extracted %s -> %s\n", flagHelmFile, flagHelmOut)
	return nil
}

// tfxHelmPull runs `helm pull --untar` into a temp dir (after an optional registry
// login) and returns the untar dir + a cleanup func.
func tfxHelmPull(ctx context.Context) (string, func(), error) {
	helmBin := flagHelmBin
	if helmBin == "" {
		p, err := exec.LookPath("helm")
		if err != nil {
			return "", func() {}, fmt.Errorf("helm not found on PATH (required for tfx helm-value)")
		}
		helmBin = p
	}
	dir, err := os.MkdirTemp("", "tfx-helm-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	if flagHelmRegistryLogin != "" {
		pw := os.Getenv(flagHelmPasswordEnv)
		login := exec.CommandContext(ctx, helmBin, "registry", "login", flagHelmRegistryLogin,
			"--username", flagHelmUsername, "--password", pw)
		login.Stderr = os.Stderr
		if err := login.Run(); err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("helm registry login %s: %w", flagHelmRegistryLogin, err)
		}
	}

	args := []string{"pull", flagHelmChart, "--untar", "--untardir", dir}
	if flagHelmVersion != "" {
		args = append(args, "--version", flagHelmVersion)
	}
	pull := exec.CommandContext(ctx, helmBin, args...)
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("helm pull %s: %w", flagHelmChart, err)
	}
	return dir, cleanup, nil
}

// extractChartVersion finds the version associated with subchart in the manifest
// YAML — the Go equivalent of the modules' `grep -A1 "<subchart>" | grep version`:
// locate the line naming the sub-chart, then the nearest following `version:` line.
func extractChartVersion(manifest []byte, subchart string) (string, error) {
	lines := strings.Split(string(manifest), "\n")
	for i, ln := range lines {
		if !strings.Contains(ln, subchart) {
			continue
		}
		for j := i; j < i+4 && j < len(lines); j++ {
			if idx := strings.Index(lines[j], "version:"); idx >= 0 {
				// Take just the version token: strip leading quotes/space, then cut
				// at the first quote/comma/brace/space (a version has none of these,
				// so this survives inline maps like {..., version: "9.9.9"}).
				v := strings.TrimLeft(strings.TrimSpace(lines[j][idx+len("version:"):]), `"' `)
				if k := strings.IndexAny(v, "\"',} \t"); k >= 0 {
					v = v[:k]
				}
				if v != "" {
					return v, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no version found for sub-chart %q in the manifest", subchart)
}

func copyFileContents(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
