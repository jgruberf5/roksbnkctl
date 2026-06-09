package bigipcis

// This file exports internal constructors/seams for the external _test package.
// It is compiled only during `go test`.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

// ScenarioTestConfig bundles the seams a test wants to inject.
type ScenarioTestConfig struct {
	VDeps          VerifyDeps
	ApplyRoutesFn  func(ctx context.Context, sctx *scenarios.Context, cidrs []string) error
	RemoveRoutesFn func(ctx context.Context, sctx *scenarios.Context, cidrs []string) error
	CreateSecretFn func(ctx context.Context, sctx *scenarios.Context, password string) error
	// EnsureReleaseFn, when non-nil, replaces the no-op Helm runner's
	// EnsureRelease so a test can record when the Helm install fires relative to
	// the secret create (the Apply ordering invariant).
	EnsureReleaseFn func() error
}

// recordingHelmRunner wraps the no-op runner but lets a test observe the
// EnsureRelease call via an injected hook (used to assert Apply call order).
type recordingHelmRunner struct {
	ensureFn func() error
}

func (r *recordingHelmRunner) EnsureRelease(_ helmRelease) error {
	if r.ensureFn != nil {
		return r.ensureFn()
	}
	return nil
}
func (r *recordingHelmRunner) UninstallRelease(_, _ string) error { return nil }

// NewScenarioForTest returns a scenario with the supplied seams injected and a
// no-op (or recording) helmRunner so unit tests never make Helm network calls.
// The SSA apply is stubbed to a no-op so Apply can run without a live kubeconfig.
func NewScenarioForTest(cfg ScenarioTestConfig) scenarios.Scenario {
	d := cfg.VDeps
	var hr helmRunner = &noopHelmRunner{}
	if cfg.EnsureReleaseFn != nil {
		hr = &recordingHelmRunner{ensureFn: cfg.EnsureReleaseFn}
	}
	return &scenario{
		vDeps:            &d,
		helm:             hr,
		applyRoutesFn:    cfg.ApplyRoutesFn,
		removeRoutesFn:   cfg.RemoveRoutesFn,
		createSecretFn:   cfg.CreateSecretFn,
		applyManifestsFn: func(_ *scenarios.Context, _ string) error { return nil },
	}
}

// NewScenarioForApplyTest is an alias of NewScenarioForTest used by Apply tests
// for readability.
func NewScenarioForApplyTest(cfg ScenarioTestConfig) scenarios.Scenario {
	return NewScenarioForTest(cfg)
}

// SetGetBigIPPassword replaces the package-level env-password reader and returns
// an undo func. Lets tests drive the env-var-required path deterministically.
func SetGetBigIPPassword(fn func() string) func() {
	orig := getBigIPPassword
	getBigIPPassword = fn
	return func() { getBigIPPassword = orig }
}

// CISHelmValues exposes the rendered CIS Helm values for a given mgmt IP so a
// test can assert the key args without a live cluster.
func CISHelmValues(mgmtIP string) map[string]interface{} {
	return cisHelmRelease(mgmtIP).Values
}

// GatewayFromCIDR exposes the gateway-derivation helper for unit testing.
func GatewayFromCIDR(cidr string) string { return gatewayFromCIDR(cidr) }

// WalkFiles walks dir and calls fn(path, content) for each regular file.
func WalkFiles(dir string, fn func(path string, content []byte) error) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, readErr := os.ReadFile(p) // #nosec G304 — test helper
		if readErr != nil {
			return readErr
		}
		return fn(p, b)
	})
}
