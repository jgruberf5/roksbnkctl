package phases

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/yaml"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

const (
	floReleaseName    = "f5-lifecycle-operator"
	floChartRef       = "oci://repo.f5.com/charts/f5-lifecycle-operator"
	floRegistryHost   = "repo.f5.com"
	floNamespace      = "f5-cne-core"
	floFieldManager   = "awsbnkctl-phase14"
	floValuesYAMLPath = "shared/flo-values.yaml.tmpl"

	floHelmInstallTimeout = 15 * time.Minute
	cneCRDWaitTimeout     = 5 * time.Minute
	floDeployTimeout      = 60 * time.Second

	cneCRDName = "cneinstances.k8s.f5.com"
	// floDeployName is the primary FLO Deployment name produced by the
	// f5-lifecycle-operator Helm chart. Verified live on 2026-05-21
	// against chart v2.21.13-0.0.28: the Deployment is named
	// "f5-lifecycle-operator" (matches the Helm release name; no
	// chart-subchart suffix). Builder's initial guess included a
	// "-f5-spk-cnf-flo" suffix which produced a not-found error in
	// Phase 13 postflight.
	floDeployName = "f5-lifecycle-operator"
)

// helmInstaller is the testability interface wrapping the four Helm SDK actions
// used by Phase 14. Production injects realHelmInstaller; tests inject a fake.
type helmInstaller interface {
	// List returns releases matching the filter in the given namespace.
	List(namespace, filter string) ([]*release.Release, error)
	// Install installs a new Helm release.
	Install(releaseName, namespace string, chart *chart.Chart, values map[string]interface{}) (*release.Release, error)
	// Upgrade upgrades an existing Helm release.
	Upgrade(releaseName, namespace string, chart *chart.Chart, values map[string]interface{}) (*release.Release, error)
	// Uninstall removes a Helm release. Returns nil if release not found.
	Uninstall(releaseName, namespace string) error
	// PullAndLoad pulls an OCI chart to a temp dir and returns the loaded chart.
	PullAndLoad(chartRef, version string) (*chart.Chart, error)
}

// realHelmInstaller implements helmInstaller using the real Helm SDK.
// It is constructed by newRealHelmInstaller in Phase14FLOHelm after OCI login.
type realHelmInstaller struct {
	actionConfig *action.Configuration
	settings     *cli.EnvSettings
}

func (r *realHelmInstaller) List(namespace, filter string) ([]*release.Release, error) {
	l := action.NewList(r.actionConfig)
	l.Filter = filter
	l.AllNamespaces = false
	l.SetStateMask() // set to all states
	return l.Run()
}

func (r *realHelmInstaller) Install(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	inst := action.NewInstall(r.actionConfig)
	inst.ReleaseName = releaseName
	inst.Namespace = namespace
	inst.Wait = true
	inst.Timeout = floHelmInstallTimeout
	inst.CreateNamespace = false
	return inst.Run(ch, values)
}

func (r *realHelmInstaller) Upgrade(releaseName, namespace string, ch *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	upg := action.NewUpgrade(r.actionConfig)
	upg.Namespace = namespace
	upg.Wait = true
	upg.Timeout = floHelmInstallTimeout
	rel, err := upg.Run(releaseName, ch, values)
	if err != nil {
		return nil, err
	}
	return rel, nil
}

func (r *realHelmInstaller) Uninstall(releaseName, _ string) error {
	uns := action.NewUninstall(r.actionConfig)
	uns.IgnoreNotFound = true
	_, err := uns.Run(releaseName)
	return err
}

func (r *realHelmInstaller) PullAndLoad(chartRef, version string) (*chart.Chart, error) {
	tmpDir, err := os.MkdirTemp("", "flo-chart-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for chart pull: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pull := action.NewPullWithOpts(action.WithConfig(r.actionConfig))
	pull.Settings = r.settings
	pull.Version = version
	pull.DestDir = tmpDir
	pull.Untar = true
	if _, err := pull.Run(chartRef); err != nil {
		return nil, fmt.Errorf("pull chart %s@%s: %w", chartRef, version, err)
	}

	chartDir := tmpDir + "/f5-lifecycle-operator"
	ch, err := loader.Load(chartDir)
	if err != nil {
		return nil, fmt.Errorf("load chart from %s: %w", chartDir, err)
	}
	return ch, nil
}

// Phase14FLOHelm installs the FLO (F5 Lifecycle Operator) Helm chart into the
// f5-cne-core namespace.
//
// Steps:
//  1. Validate bnk: block + addons.flo enabled.
//  2. Read FAR archive + JWT files.
//  3. Render flo-values.yaml.tmpl → values map.
//  4. (live) OCI registry login → install or upgrade (idempotent).
//  5. Wait for cneinstances.k8s.f5.com CRD (up to 5 min).
//  6. Set state keys + Save.
//
// D-005: CheckAuthOrDie is called at entry.
func Phase14FLOHelm(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 14] FLO Helm install: cluster=%s\n", name)

	// Validate bnk: block.
	if cl.Bnk == nil {
		return fmt.Errorf("phase14: cluster.yaml must include a 'bnk:' block (required for FLO install)")
	}

	// Check FLO enabled.
	var floSpec *intent.FloSpec
	if cl.Addons != nil {
		floSpec = cl.Addons.Flo
	}
	if !floSpec.FloEnabled() {
		fmt.Fprintln(os.Stderr, "[phase 14] FLO disabled (addons.flo.enabled: false), skipping")
		return nil
	}

	floVersion := floSpec.FLOVersion()
	caIssuer := name + "-ca-cluster-issuer"

	farPath, jwtPath, err := resolveBnkFilePaths(cl)
	if err != nil {
		return fmt.Errorf("phase14: %w", err)
	}

	// Read FAR + JWT (surface ENOENT even in dry-run).
	farData, err := os.ReadFile(farPath) // #nosec G304 -- operator-supplied path via cluster.yaml
	if err != nil {
		return fmt.Errorf("phase14: reading FAR archive %s: %w", farPath, err)
	}
	if len(farData) == 0 {
		return fmt.Errorf("phase14: FAR archive %s is empty", farPath)
	}

	jwtData, err := os.ReadFile(jwtPath) // #nosec G304 -- operator-supplied path via cluster.yaml
	if err != nil {
		return fmt.Errorf("phase14: reading JWT %s: %w", jwtPath, err)
	}
	if len(jwtData) == 0 {
		return fmt.Errorf("phase14: JWT file %s is empty", jwtPath)
	}

	// Render flo-values.yaml.tmpl.
	valsTmpl, err := k8smanifests.FS.ReadFile(floValuesYAMLPath)
	if err != nil {
		return fmt.Errorf("phase14: reading embedded flo-values template: %w", err)
	}
	jwtStr := strings.TrimSpace(string(jwtData))
	renderedVals, err := render.RenderFLOValues(valsTmpl, cl, jwtStr)
	if err != nil {
		return fmt.Errorf("phase14: rendering flo-values template: %w", err)
	}
	var valuesMap map[string]interface{}
	if err := yaml.Unmarshal(renderedVals, &valuesMap); err != nil {
		return fmt.Errorf("phase14: parsing rendered flo-values as YAML: %w", err)
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 14] dry-run: would helm install %s %s in %s (ca-issuer=%s)\n",
			floReleaseName, floVersion, floNamespace, caIssuer)
		fmt.Fprintln(os.Stderr, "[phase 14] dry-run: would check existing releases (skipped in dry-run)")
		st.Set("FLO_RELEASE_NAME", floReleaseName)
		st.Set("FLO_VERSION", floVersion)
		st.Set("FLO_NAMESPACE", "dry-run")
		st.Set("FLO_INSTALLED_AT", "dry-run")
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase14: Clients.K8s is nil — call clients.AttachK8s(kubeconfigPath) after phase 11")
	}

	// Build Helm action configuration with OCI registry client.
	farKeyB64 := strings.TrimSpace(string(farData))
	helmInstaller, err := buildHelmInstaller(st, farKeyB64)
	if err != nil {
		return fmt.Errorf("phase14: building helm installer: %w", err)
	}

	return runFLOHelmInstall(ctx, helmInstaller, cl, st, floVersion, valuesMap, clients)
}

// helmValuesEqual reports whether two Helm values maps are semantically equal by
// comparing their canonical JSON representations. Canonical-JSON compare avoids
// int-vs-float64 skew between Helm's JSON-stored Config and the rendered values.
// Returns false (treat as changed → upgrade) if either side fails to marshal.
func helmValuesEqual(a, b map[string]interface{}) bool {
	aj, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bj, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(aj, bj)
}

// runFLOHelmInstall handles the list→install-or-upgrade→CRD-wait sequence.
// Extracted for testability (accepts helmInstaller interface).
func runFLOHelmInstall(ctx context.Context, h helmInstaller, cl *intent.Cluster, st *state.State, floVersion string, valuesMap map[string]interface{}, clients *Clients) error {
	// Pull the chart.
	fmt.Fprintf(os.Stderr, "[phase 14] pulling chart %s@%s\n", floChartRef, floVersion)
	ch, err := h.PullAndLoad(floChartRef, floVersion)
	if err != nil {
		return fmt.Errorf("phase14: pulling FLO chart: %w", err)
	}

	// Check if release already exists.
	releases, err := h.List(floNamespace, "^"+floReleaseName+"$")
	if err != nil {
		return fmt.Errorf("phase14: listing helm releases: %w", err)
	}

	if len(releases) == 0 {
		fmt.Fprintf(os.Stderr, "[phase 14] installing %s v%s in namespace %s\n", floReleaseName, floVersion, floNamespace)
		if _, err = h.Install(floReleaseName, floNamespace, ch, valuesMap); err != nil {
			return fmt.Errorf("phase14: helm install %s: %w", floReleaseName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 14] helm install %s complete\n", floReleaseName)
	} else {
		existing := releases[0]
		deployedVersion := ""
		if existing.Chart != nil && existing.Chart.Metadata != nil {
			deployedVersion = existing.Chart.Metadata.Version
		}
		alreadyDeployed := existing.Info != nil && existing.Info.Status == release.StatusDeployed
		valuesUnchanged := helmValuesEqual(existing.Config, valuesMap)

		if alreadyDeployed && deployedVersion == floVersion && valuesUnchanged {
			// Skip upgrade: release is healthy, at the correct version, with identical values.
			// Canonical-JSON compare avoids int-vs-float64 skew between Helm's JSON-stored Config and the rendered values.
			// Proceeding to CRD-wait + state writes (idempotent).
			fmt.Fprintf(os.Stderr, "[phase 14] release %s already at v%s with unchanged values — skipping upgrade\n", floReleaseName, floVersion)
		} else {
			fmt.Fprintf(os.Stderr, "[phase 14] release %s exists but needs upgrade (deployedVersion=%q desiredVersion=%q deployed=%v valuesMatch=%v) — upgrading\n",
				floReleaseName, deployedVersion, floVersion, alreadyDeployed, valuesUnchanged)
			if _, err = h.Upgrade(floReleaseName, floNamespace, ch, valuesMap); err != nil {
				return fmt.Errorf("phase14: helm upgrade %s: %w", floReleaseName, err)
			}
			fmt.Fprintf(os.Stderr, "[phase 14] helm upgrade %s complete\n", floReleaseName)
		}
	}

	// Wait for cneinstances.k8s.f5.com CRD (FLO installs it during reconciliation).
	fmt.Fprintf(os.Stderr, "[phase 14] waiting for CRD %s (up to %s)\n", cneCRDName, cneCRDWaitTimeout)
	if err := k8swait.WaitForCRDExists(ctx, clients.Dynamic, cneCRDName, cneCRDWaitTimeout); err != nil {
		return fmt.Errorf("phase14: CRD %s not established within %s: %w", cneCRDName, cneCRDWaitTimeout, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 14] CRD %s is established\n", cneCRDName)

	// Persist state.
	st.Set("FLO_RELEASE_NAME", floReleaseName)
	st.Set("FLO_VERSION", floVersion)
	st.Set("FLO_NAMESPACE", floNamespace)
	st.Set("FLO_INSTALLED_AT", time.Now().UTC().Format(time.RFC3339))
	return st.Save()
}

// buildHelmInstaller creates an OCI-authenticated Helm installer using the
// kubeconfig path from state and the FAR key for registry login.
func buildHelmInstaller(st *state.State, farKeyB64 string) (helmInstaller, error) {
	kubeconfigPath := st.Get("KUBECONFIG_PATH")

	regClient, err := registry.NewClient()
	if err != nil {
		return nil, fmt.Errorf("create helm registry client: %w", err)
	}

	if err := regClient.Login(floRegistryHost,
		registry.LoginOptBasicAuth("_json_key_base64", farKeyB64),
	); err != nil {
		return nil, fmt.Errorf("helm registry login %s: %w", floRegistryHost, err)
	}

	settings := cli.New()
	if kubeconfigPath != "" {
		settings.KubeConfig = kubeconfigPath
	}

	actionConfig := new(action.Configuration)
	logFn := func(format string, v ...interface{}) {
		fmt.Fprintf(os.Stderr, "[phase 14][helm] "+format+"\n", v...)
	}
	if err := actionConfig.Init(settings.RESTClientGetter(), floNamespace, "secret", logFn); err != nil {
		return nil, fmt.Errorf("init helm action config: %w", err)
	}
	actionConfig.RegistryClient = regClient

	return &realHelmInstaller{
		actionConfig: actionConfig,
		settings:     settings,
	}, nil
}

// Phase14FLOHelmDown uninstalls the FLO Helm release.
// Tolerates "release not found". CRDs may linger (slice 7+ tightens).
func Phase14FLOHelmDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 14 down] FLO helm uninstall: cluster=%s\n", name)

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 14 down] warning: K8s client not available, skipping FLO helm uninstall")
		clearPhase14State(st)
		return st.Save()
	}

	farPath := ""
	farKeyB64 := ""
	if cl.Bnk != nil {
		farData, err := os.ReadFile(cl.Bnk.FARArchive) // #nosec G304
		if err == nil && len(farData) > 0 {
			farPath = cl.Bnk.FARArchive
			farKeyB64 = strings.TrimSpace(string(farData))
		}
	}
	if farPath == "" {
		fmt.Fprintln(os.Stderr, "[phase 14 down] warning: FAR archive not readable, skipping FLO helm uninstall")
		clearPhase14State(st)
		return st.Save()
	}

	h, err := buildHelmInstaller(st, farKeyB64)
	if err != nil {
		// Non-fatal: log and continue so teardown doesn't block.
		fmt.Fprintf(os.Stderr, "[phase 14 down] warning: helm installer build error: %v\n", err)
		clearPhase14State(st)
		return st.Save()
	}

	fmt.Fprintf(os.Stderr, "[phase 14 down] helm uninstall %s\n", floReleaseName)
	if err := h.Uninstall(floReleaseName, floNamespace); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 14 down] warning: helm uninstall: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 14 down] helm uninstall %s complete\n", floReleaseName)
	}

	// Wait briefly for FLO Deployment to terminate (~60s).
	fmt.Fprintf(os.Stderr, "[phase 14 down] waiting for FLO deployment to terminate (up to %s)\n", floDeployTimeout)
	waitCtx, cancel := context.WithTimeout(ctx, floDeployTimeout)
	defer cancel()
	_ = waitForDeploymentGone(waitCtx, clients, floNamespace, floDeployName)

	clearPhase14State(st)
	return st.Save()
}

// waitForDeploymentGone polls until the named deployment no longer exists.
// Best-effort — errors are swallowed (deployment may already be gone).
func waitForDeploymentGone(ctx context.Context, clients *Clients, ns, name string) error {
	for {
		select {
		case <-ctx.Done():
			return nil // best-effort, don't block teardown
		default:
		}
		_, _, err := k8swait.DeploymentReplicaStatus(ctx, clients.K8s, ns, name)
		if err != nil {
			// deployment gone or unreachable
			return nil
		}
		// Deployment still exists — wait 2s before next poll.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
			// poll again
		}
	}
}

// clearPhase14State zeroes all phase 14 state keys.
func clearPhase14State(st *state.State) {
	for _, k := range []string{"FLO_RELEASE_NAME", "FLO_VERSION", "FLO_NAMESPACE", "FLO_INSTALLED_AT"} {
		st.Set(k, "")
	}
}
