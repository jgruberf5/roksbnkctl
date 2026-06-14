package cli

// forge proxy prepare-envoy — idempotent pre-seed of EnvoyProxy + GatewayClass
// resources so that forge's Envoy helm deploy produces a NodePort Service
// (not LoadBalancer) that is reachable from the jumphost in the shootout.
//
// Sequence (T1 in the architect's deploy→ready→discover flow):
//  1. SSA-apply the EnvoyProxy CRD (so the RESTMapper knows the type before
//     forge's gateway-helm install).
//  2. SSA-apply the EnvoyProxy CR (NodePort + externalTrafficPolicy:Cluster)
//     in namespace perf-proxies.
//  3. SSA-apply the GatewayClass "eg" with spec.parametersRef pointing to the
//     EnvoyProxy above — forge's later apply only sets spec.controllerName
//     (same value, shared-owner, no conflict) and omits parametersRef, so the
//     field survives per the SSA spec.
//
// --dry-run: prints the would-be manifest diff (previews SSA patch), no mutation.
//
// See: .agent/tasks/active/prd11-envoy-shootout-connectivity/work/architect-design.md §2-§3

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericiooptions"

	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
)

var (
	flagPrepareEnvoyConfig string
	flagPrepareEnvoyDryRun bool
	flagPrepareEnvoyForce  bool
)

const (
	prepareEnvoyManifestCRD  = "envoy-gateway/envoyproxy-crd.yaml"
	prepareEnvoyManifestSeed = "envoy-gateway/prepare-envoy.yaml"
)

var forgeProxyPrepareEnvoyCmd = &cobra.Command{
	Use:   "prepare-envoy",
	Short: "Pre-seed EnvoyProxy (NodePort) + GatewayClass 'eg' for the proxy shootout (idempotent)",
	Long: `forge proxy prepare-envoy applies the Envoy Gateway pre-seed manifests to the
cluster referenced by --config. Must be run BEFORE forge's proxy deploy so the
Envoy Gateway controller picks up the NodePort intent on first reconcile.

Resources applied (all idempotent via server-side apply):
  1. envoyproxies CRD (gateway.envoyproxy.io/v1alpha1) — so the RESTMapper
     resolves EnvoyProxy before forge's gateway-helm install.
  2. Namespace perf-proxies (forge also creates this; idempotent).
  3. EnvoyProxy perf-proxies/awsbnkctl-nodeport:
       spec.provider.kubernetes.envoyService.type: NodePort
       spec.provider.kubernetes.envoyService.externalTrafficPolicy: Cluster
  4. GatewayClass eg (spec.parametersRef → the EnvoyProxy above).
     awsbnkctl owns parametersRef; forge's later SSA apply only sets
     controllerName (same value) and omits parametersRef — field survives.

Pass --dry-run to preview the would-be SSA diff without mutating the cluster.`,
	RunE: runForgePrepareEnvoy,
}

func init() {
	f := forgeProxyPrepareEnvoyCmd.Flags()
	f.StringVarP(&flagPrepareEnvoyConfig, "config", "f", "",
		"path to cluster.yaml (resolves kubeconfig for the target cluster) [required]")
	f.BoolVar(&flagPrepareEnvoyDryRun, "dry-run", false,
		"print the manifests that would be applied without mutating the cluster")
	f.BoolVar(&flagPrepareEnvoyForce, "force", true,
		"pass force-conflicts to SSA (default true; set false to detect unexpected ownership conflicts)")
	// --config is required only for the live apply path; --dry-run does not
	// need a provisioned cluster, so we do NOT MarkFlagRequired here.
	// The non-dry-run path validates the flag explicitly in runForgePrepareEnvoy.

	// forgeProxyCmd is the "proxy" sub-command of "forge".
	// Create it if it doesn't exist yet, add prepare-envoy under it.
	forgeCmd.AddCommand(forgeProxyCmd)
	forgeProxyCmd.AddCommand(forgeProxyPrepareEnvoyCmd)
}

// forgeProxyCmd is the "awsbnkctl forge proxy" grouping command.
var forgeProxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage proxy deployments for the benchmark shootout",
}

func runForgePrepareEnvoy(cmd *cobra.Command, _ []string) error {
	// --dry-run: print manifests to stdout and return immediately.
	// No kubeconfig resolution or cluster-existence check needed — dry-run
	// never touches the cluster, so it works before 'awsbnkctl up'.
	if flagPrepareEnvoyDryRun {
		fmt.Fprintln(os.Stderr, "→ dry-run: would apply the following manifests (no cluster mutation)")
		return printEmbeddedManifests()
	}

	// Non-dry-run path: --config is required. Validate explicitly since we
	// removed MarkFlagRequired (which would block --dry-run without --config).
	if flagPrepareEnvoyConfig == "" {
		return fmt.Errorf("--config is required (path to cluster.yaml); use --dry-run to preview without a cluster")
	}

	// Resolve kubeconfig from cluster.yaml.
	kubeconfigPath, err := resolveKubeconfigFromConfig(flagPrepareEnvoyConfig)
	if err != nil {
		return fmt.Errorf("--config: %w", err)
	}
	// resolveKubeconfigFromConfig returns "" when the state has no KUBECONFIG_PATH yet
	// (e.g. cluster never brought up). Surface a clear error rather than silently
	// falling back to ~/.kube/config (which may point at the wrong cluster).
	if kubeconfigPath == "" {
		return fmt.Errorf("could not resolve kubeconfig from --config %s: run 'awsbnkctl up' first or set KUBECONFIG_PATH in state", flagPrepareEnvoyConfig)
	}

	fmt.Fprintf(os.Stderr, "→ forge proxy prepare-envoy: applying pre-seed manifests (kubeconfig=%s)\n", kubeconfigPath)

	// Apply CRD first so the RESTMapper knows EnvoyProxy before the CR.
	if err := applyEmbeddedManifest(cmd, kubeconfigPath, prepareEnvoyManifestCRD); err != nil {
		return fmt.Errorf("apply EnvoyProxy CRD: %w", err)
	}

	// Apply EnvoyProxy CR + GatewayClass with retry to handle the CRD Established
	// race: the RESTMapper cache may not yet reflect the newly-applied CRD, causing
	// "no matches for kind EnvoyProxy" on the first attempt. Retry with a short
	// backoff for up to ~15 s before failing.
	const (
		crApplyMaxAttempts = 10
		crApplyRetryDelay  = 1500 * time.Millisecond
	)
	var crApplyErr error
	for attempt := 1; attempt <= crApplyMaxAttempts; attempt++ {
		crApplyErr = applyEmbeddedManifest(cmd, kubeconfigPath, prepareEnvoyManifestSeed)
		if crApplyErr == nil {
			break
		}
		// Retry only on RESTMapper "no matches for kind" errors — these indicate the
		// CRD Established condition has not propagated to the API discovery cache yet.
		if !isNoMatchError(crApplyErr) {
			break
		}
		if attempt < crApplyMaxAttempts {
			fmt.Fprintf(os.Stderr, "⟳ EnvoyProxy CRD not yet established (attempt %d/%d); retrying in %.1fs\n",
				attempt, crApplyMaxAttempts, crApplyRetryDelay.Seconds())
			time.Sleep(crApplyRetryDelay)
		}
	}
	if crApplyErr != nil {
		return fmt.Errorf("apply EnvoyProxy CR + GatewayClass: %w", crApplyErr)
	}

	fmt.Fprintln(os.Stderr, "✓ pre-seed complete: EnvoyProxy(NodePort) + GatewayClass eg applied")
	fmt.Fprintln(os.Stderr, "  Next: trigger forge proxy deploy (POST /api/benchmarks/targets/{id}/proxies)")
	return nil
}

// applyEmbeddedManifest reads a manifest from the embedded FS, writes it to
// a temporary file, and SSA-applies it via ApplyOptions.Run.
// Using a tempfile lets us reuse the existing ApplyOptions path without
// modification (ApplyOptions.loadObjects reads from Filename).
func applyEmbeddedManifest(cmd *cobra.Command, kubeconfigPath, embeddedPath string) error {
	data, err := manifests.FS.ReadFile(embeddedPath)
	if err != nil {
		return fmt.Errorf("read embedded manifest %s: %w", embeddedPath, err)
	}

	tmp, err := os.CreateTemp("", "awsbnkctl-prepare-envoy-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmp.Name()) // #nosec G246 -- tempfile cleaned after SSA apply; path not user-controlled

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp manifest: %w", err)
	}

	opts := &k8s.ApplyOptions{
		Filename:       tmp.Name(),
		Force:          flagPrepareEnvoyForce,
		KubeconfigPath: kubeconfigPath,
		IOStreams: genericiooptions.IOStreams{
			In:     os.Stdin,
			Out:    os.Stdout,
			ErrOut: os.Stderr,
		},
	}
	return opts.Run(cmd.Context())
}

// isNoMatchError reports whether err is a RESTMapper "no matches for kind" error.
// The k8s.io/apimachinery meta.IsNoMatchError typed check is the canonical path,
// but ApplyOptions wraps the error in text — we also string-match as a fallback
// so the retry covers both the raw and wrapped forms.
func isNoMatchError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no matches for kind") ||
		strings.Contains(msg, "no match for kind") ||
		strings.Contains(msg, "no kind") ||
		strings.Contains(msg, "RESTMapping")
}

// printEmbeddedManifests writes the pre-seed manifests to stdout for dry-run preview.
func printEmbeddedManifests() error {
	for _, path := range []string{prepareEnvoyManifestCRD, prepareEnvoyManifestSeed} {
		data, err := manifests.FS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded manifest %s: %w", path, err)
		}
		fmt.Printf("# --- %s ---\n", path)
		fmt.Print(string(data))
		fmt.Println()
	}
	return nil
}
