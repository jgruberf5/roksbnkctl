package bigipcis

// This file is a deliberate copy of internal/demo/ingressmigration/helm_real.go
// (only the log-prefix string differs). Per the F2-C scope boundary the two demo
// use-case packages must remain independently deletable — deleting either must
// not leave a dangling shared helper — so the Helm SDK wrapper is copied here
// rather than factored into a shared package. The action.Pull + RepoURL pull
// mechanism is identical to the ingress-migration template it mirrors.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
)

const helmInstallTimeout = 10 * time.Minute

// realHelmRunner implements helmRunner using the Helm v3 SDK.
// It builds one action.Configuration per namespace (Helm stores release
// secrets per namespace) and uses action.Pull with RepoURL for HTTP repos.
type realHelmRunner struct {
	kubeconfigPath string
	logFn          func(format string, v ...interface{})
}

func newRealHelmRunner(kubeconfigPath string) *realHelmRunner {
	return &realHelmRunner{
		kubeconfigPath: kubeconfigPath,
		logFn: func(format string, v ...interface{}) {
			fmt.Fprintf(os.Stderr, "[demo/bigip-cis][helm] "+format+"\n", v...)
		},
	}
}

func (r *realHelmRunner) actionConfig(namespace string) (*action.Configuration, *cli.EnvSettings, error) {
	settings := cli.New()
	if r.kubeconfigPath != "" {
		settings.KubeConfig = r.kubeconfigPath
	}
	cfg := new(action.Configuration)
	if err := cfg.Init(settings.RESTClientGetter(), namespace, "secret", r.logFn); err != nil {
		return nil, nil, fmt.Errorf("helm action config for namespace %s: %w", namespace, err)
	}
	return cfg, settings, nil
}

func (r *realHelmRunner) EnsureRelease(rel helmRelease) error {
	cfg, settings, err := r.actionConfig(rel.Namespace)
	if err != nil {
		return err
	}

	// Pull the chart from the HTTP repo into a temp dir and load it.
	tmpDir, err := os.MkdirTemp("", "helm-chart-*")
	if err != nil {
		return fmt.Errorf("create temp dir for helm pull: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pull := action.NewPullWithOpts(action.WithConfig(cfg))
	pull.Settings = settings
	pull.RepoURL = rel.RepoURL
	pull.Version = rel.Version
	pull.DestDir = tmpDir
	pull.Untar = true
	if _, err := pull.Run(rel.Chart); err != nil {
		return fmt.Errorf("helm pull %s from %s@%s: %w", rel.Chart, rel.RepoURL, rel.Version, err)
	}
	chartDir := tmpDir + "/" + rel.Chart
	ch, err := loader.Load(chartDir)
	if err != nil {
		return fmt.Errorf("helm load chart %s: %w", chartDir, err)
	}

	// Check if release already exists (all states, including pending-install/
	// pending-upgrade from a previously timed-out attempt).
	lister := action.NewList(cfg)
	lister.Filter = "^" + rel.ReleaseName + "$"
	lister.All = true
	lister.SetStateMask()
	releases, err := lister.Run()
	if err != nil {
		return fmt.Errorf("helm list releases: %w", err)
	}

	if len(releases) == 0 {
		r.logFn("installing %s v%s in namespace %s", rel.ReleaseName, rel.Version, rel.Namespace)
		inst := action.NewInstall(cfg)
		inst.ReleaseName = rel.ReleaseName
		inst.Namespace = rel.Namespace
		inst.Wait = true
		inst.Timeout = helmInstallTimeout
		inst.CreateNamespace = true
		if _, err := inst.Run(ch, rel.Values); err != nil {
			return fmt.Errorf("helm install %s: %w", rel.ReleaseName, err)
		}
		r.logFn("helm install %s complete", rel.ReleaseName)
		return nil
	}

	existing := releases[0]
	deployedVersion := ""
	if existing.Chart != nil && existing.Chart.Metadata != nil {
		deployedVersion = existing.Chart.Metadata.Version
	}
	alreadyDeployed := existing.Info != nil && existing.Info.Status == release.StatusDeployed
	valuesUnchanged := helmValuesEqual(existing.Config, rel.Values)

	if alreadyDeployed && deployedVersion == rel.Version && valuesUnchanged {
		r.logFn("release %s already at v%s with unchanged values — skipping upgrade", rel.ReleaseName, rel.Version)
		return nil
	}

	r.logFn("upgrading %s (deployedVersion=%q desiredVersion=%q deployed=%v valuesMatch=%v)",
		rel.ReleaseName, deployedVersion, rel.Version, alreadyDeployed, valuesUnchanged)
	upg := action.NewUpgrade(cfg)
	upg.Namespace = rel.Namespace
	upg.Wait = true
	upg.Timeout = helmInstallTimeout
	if _, err := upg.Run(rel.ReleaseName, ch, rel.Values); err != nil {
		return fmt.Errorf("helm upgrade %s: %w", rel.ReleaseName, err)
	}
	r.logFn("helm upgrade %s complete", rel.ReleaseName)
	return nil
}

func (r *realHelmRunner) UninstallRelease(releaseName, namespace string) error {
	cfg, _, err := r.actionConfig(namespace)
	if err != nil {
		return err
	}
	uns := action.NewUninstall(cfg)
	uns.IgnoreNotFound = true
	if _, err := uns.Run(releaseName); err != nil {
		return fmt.Errorf("helm uninstall %s: %w", releaseName, err)
	}
	return nil
}

// helmValuesEqual reports whether two Helm values maps are semantically equal
// by comparing their canonical JSON representations. Matches the same logic
// used in phase14 to avoid int-vs-float64 skew.
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
