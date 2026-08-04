package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	flagHelmManifestFile  string
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

var tfxHelmProdJWKSCmd = &cobra.Command{
	Use:           "prod-jwks",
	Short:         "Extract and base64-decode the prod_jwks keyset bundled in a chart (internal)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXHelmProdJWKS,
}

var tfxHelmPullChartCmd = &cobra.Command{
	Use:           "pull-chart",
	Short:         "Pull a chart archive (.tgz) to --out (internal)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runTFXHelmPullChart,
}

func init() {
	for _, c := range []*cobra.Command{tfxHelmChartVersionCmd, tfxHelmPullFileCmd, tfxHelmProdJWKSCmd, tfxHelmPullChartCmd} {
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
	tfxHelmChartVersionCmd.Flags().StringVar(&flagHelmManifestFile, "manifest-file", "", "read the version from this local manifest file instead of pulling the chart (no-pull mode)")
	tfxHelmValueCmd.AddCommand(tfxHelmChartVersionCmd, tfxHelmPullFileCmd, tfxHelmProdJWKSCmd, tfxHelmPullChartCmd)
	tfxCmd.AddCommand(tfxHelmValueCmd)
}

// runTFXHelmPullChart stages a chart archive (.tgz) on disk at --out, using the
// same inline-registry-config auth as every other tfx pull. A helm_release then
// installs from that LOCAL path (chart = the .tgz) instead of an oci:// repository,
// which makes the terraform helm PROVIDER do NO OCI login at all. That is the whole
// point: the provider's own OCI login stores the credential via a docker credential
// helper, and on Windows the multi-KB FAR password overflows the Credential Manager
// blob ("The stub received bad data"); dropping the provider's creds instead made it
// pull anonymously (403). Pulling the archive ourselves sidesteps both.
func runTFXHelmPullChart(cmd *cobra.Command, _ []string) error {
	if flagHelmChart == "" || flagHelmOut == "" {
		return fmt.Errorf("--chart and --out are required")
	}
	tgz, cleanup, err := tfxHelmPullArchive(cmd.Context())
	if err != nil {
		return err
	}
	defer cleanup()
	if err := os.MkdirAll(filepath.Dir(flagHelmOut), 0o755); err != nil {
		return fmt.Errorf("creating out dir: %w", err)
	}
	if err := copyFileContents(tgz, flagHelmOut); err != nil {
		return fmt.Errorf("staging chart archive to %s: %w", flagHelmOut, err)
	}
	fmt.Fprintf(os.Stderr, "tfx helm-value: pulled chart -> %s\n", flagHelmOut)
	return nil
}

func runTFXHelmChartVersion(cmd *cobra.Command, _ []string) error {
	if flagHelmSubchart == "" {
		return fmt.Errorf("--subchart is required")
	}
	var manifest []byte
	if flagHelmManifestFile != "" {
		// No-pull mode: read a manifest already on disk (e.g. left by a prior
		// `pull-file`). Lets one chart pull feed multiple sub-chart version reads —
		// the FLO phase resolves both the operator and the CIS version this way.
		b, err := os.ReadFile(flagHelmManifestFile)
		if err != nil {
			return fmt.Errorf("reading manifest file %s: %w", flagHelmManifestFile, err)
		}
		manifest = b
	} else {
		if flagHelmChart == "" || flagHelmFile == "" {
			return fmt.Errorf("--chart and --file are required (or use --manifest-file)")
		}
		dir, cleanup, err := tfxHelmPull(cmd.Context())
		if err != nil {
			return err
		}
		defer cleanup()
		manifestPath, err := findChartFile(dir, flagHelmFile)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(manifestPath)
		if err != nil {
			return fmt.Errorf("reading manifest %s in the chart: %w", flagHelmFile, err)
		}
		manifest = b
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
	src, err := findChartFile(dir, flagHelmFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(flagHelmOut), 0o755); err != nil {
		return fmt.Errorf("creating out dir: %w", err)
	}
	if err := copyFileContents(src, flagHelmOut); err != nil {
		return fmt.Errorf("extracting %s from chart: %w", flagHelmFile, err)
	}
	fmt.Fprintf(os.Stderr, "tfx helm-value: extracted %s -> %s\n", flagHelmFile, flagHelmOut)
	return nil
}

// prodJWKSRe matches the `prod_jwks.txt: <base64>` entry F5 embeds in the license
// proxy chart's template YAML (a ConfigMap/Secret value carrying the public
// signature-verification keyset).
var prodJWKSRe = regexp.MustCompile(`prod_jwks\.txt:\s*([A-Za-z0-9+/=]+)`)

func runTFXHelmProdJWKS(cmd *cobra.Command, _ []string) error {
	if flagHelmChart == "" || flagHelmOut == "" {
		return fmt.Errorf("--chart and --out are required")
	}
	dir, cleanup, err := tfxHelmPull(cmd.Context())
	if err != nil {
		return err
	}
	defer cleanup()
	decoded, err := extractProdJWKS(dir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(flagHelmOut), 0o755); err != nil {
		return fmt.Errorf("creating out dir: %w", err)
	}
	if err := os.WriteFile(flagHelmOut, decoded, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", flagHelmOut, err)
	}
	fmt.Fprintf(os.Stderr, "tfx helm-value: prod_jwks -> %s\n", flagHelmOut)
	return nil
}

// extractProdJWKS walks the chart's template YAMLs for a `prod_jwks.txt: <base64>`
// entry and returns the decoded bytes — the Go form of the module's
// `grep -ohE 'prod_jwks.txt: <b64>' templates/*.yaml | awk '{print $2}' | base64 -d`.
func extractProdJWKS(root string) ([]byte, error) {
	var b64 string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || b64 != "" {
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext != ".yaml" && ext != ".yml" {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if m := prodJWKSRe.FindSubmatch(data); m != nil {
			b64 = string(m[1])
			return filepath.SkipAll
		}
		return nil
	})
	if b64 == "" {
		return nil, fmt.Errorf("no prod_jwks.txt entry found in the chart templates")
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decoding prod_jwks base64: %w", err)
	}
	return decoded, nil
}

// findChartFile resolves want within the untarred chart tree tolerantly. helm's
// `pull --untar` names the top-level directory after the chart, which for the F5
// manifest charts can be versioned (f5-bigip-k8s-manifest-<v>/) or not depending
// on how the artifact was packaged — the one path detail we can't pin without the
// real OCI chart in hand. So try the literal join first, then fall back to matching
// the basename anywhere in the tree rather than hardcoding the top-dir shape.
func findChartFile(root, want string) (string, error) {
	if p := filepath.Join(root, want); fileExists(p) {
		return p, nil
	}
	base := filepath.Base(want)
	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		if d.Name() == base {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("file %q not found anywhere in the pulled chart", want)
	}
	return found, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// resolveHelmBin returns the helm binary to shell to: --helm-bin if set, else helm
// on PATH.
func resolveHelmBin() (string, error) {
	if flagHelmBin != "" {
		return flagHelmBin, nil
	}
	p, err := exec.LookPath("helm")
	if err != nil {
		return "", fmt.Errorf("helm not found on PATH (required for tfx helm-value)")
	}
	return p, nil
}

// tfxHelmPullArchive runs `helm pull` (NO --untar) into a temp dir and returns the
// produced .tgz path + a cleanup func. Lets a helm_release install from a local
// archive so the terraform helm PROVIDER performs no OCI login (see
// runTFXHelmPullChart). Auth is the same inline registry-config file as tfxHelmPull.
func tfxHelmPullArchive(ctx context.Context) (string, func(), error) {
	helmBin, err := resolveHelmBin()
	if err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "tfx-helm-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	args := []string{"pull", flagHelmChart, "--destination", dir}
	if flagHelmVersion != "" {
		args = append(args, "--version", flagHelmVersion)
	}
	if flagHelmRegistryLogin != "" {
		regConf, werr := writeHelmRegistryConfig(dir, flagHelmRegistryLogin, flagHelmUsername, os.Getenv(flagHelmPasswordEnv))
		if werr != nil {
			cleanup()
			return "", func() {}, werr
		}
		args = append(args, "--registry-config", regConf)
	}

	pull := exec.CommandContext(ctx, helmBin, args...)
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("helm pull %s: %w", flagHelmChart, err)
	}

	// helm pull writes exactly one <name>-<version>.tgz into the destination dir.
	entries, rerr := os.ReadDir(dir)
	if rerr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("reading pull dir: %w", rerr)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tgz") {
			return filepath.Join(dir, e.Name()), cleanup, nil
		}
	}
	cleanup()
	return "", func() {}, fmt.Errorf("helm pull %s produced no .tgz archive", flagHelmChart)
}

// tfxHelmPull runs `helm pull --untar` into a temp dir (after an optional registry
// login) and returns the untar dir + a cleanup func.
func tfxHelmPull(ctx context.Context) (string, func(), error) {
	helmBin, err := resolveHelmBin()
	if err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp("", "tfx-helm-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	args := []string{"pull", flagHelmChart, "--untar", "--untardir", dir}
	if flagHelmVersion != "" {
		args = append(args, "--version", flagHelmVersion)
	}

	// Authenticate the OCI pull via a temp registry-config file rather than
	// `helm registry login`. On Windows, `helm registry login` writes the credential
	// to the Windows Credential Manager, whose blob is capped (~2.5 KB) — the FAR
	// `_json_key_base64` password is a multi-KB base64 blob, so the store fails with
	// "The stub received bad data". A docker-style config file has no such limit, is
	// what `helm pull --registry-config` reads for auth, keeps the password off the
	// command line (no insecure-`--password` warning), and works identically on
	// Linux. The file lives in the same temp dir the cleanup func removes.
	if flagHelmRegistryLogin != "" {
		regConf, werr := writeHelmRegistryConfig(dir, flagHelmRegistryLogin, flagHelmUsername, os.Getenv(flagHelmPasswordEnv))
		if werr != nil {
			cleanup()
			return "", func() {}, werr
		}
		args = append(args, "--registry-config", regConf)
	}

	pull := exec.CommandContext(ctx, helmBin, args...)
	pull.Stderr = os.Stderr
	if err := pull.Run(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("helm pull %s: %w", flagHelmChart, err)
	}
	return dir, cleanup, nil
}

// writeHelmRegistryConfig writes a docker-style registry auth file (the format
// `helm pull --registry-config` consumes) for host, and returns its path.
func writeHelmRegistryConfig(dir, host, user, pw string) (string, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pw))
	conf := map[string]any{
		"auths": map[string]any{
			host: map[string]string{"auth": auth},
		},
	}
	b, err := json.Marshal(conf)
	if err != nil {
		return "", fmt.Errorf("building helm registry config: %w", err)
	}
	p := filepath.Join(dir, "registry-config.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		return "", fmt.Errorf("writing helm registry config: %w", err)
	}
	return p, nil
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
