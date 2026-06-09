package ingressmigration

// helmRelease describes a single Helm release to install.
type helmRelease struct {
	// ReleaseName is the Helm release name.
	ReleaseName string
	// Namespace is the target namespace.
	Namespace string
	// RepoURL is the HTTP chart repository URL.
	RepoURL string
	// Chart is the chart name within the repository.
	Chart string
	// Version is the pinned chart version.
	Version string
	// Values is the set of --set overrides applied at install/upgrade time.
	Values map[string]interface{}
}

// helmRunner is the testability seam for Helm operations. Production code
// uses newRealHelmRunner; tests inject noopHelmRunner to skip network calls.
type helmRunner interface {
	// EnsureRelease installs the chart if the release does not exist,
	// upgrades it if it already exists with a different version or values.
	// Idempotent: no-ops when the release is already at the desired state.
	EnsureRelease(rel helmRelease) error
	// UninstallRelease removes the Helm release. Tolerates not-found.
	UninstallRelease(releaseName, namespace string) error
}

// noopHelmRunner is a helmRunner that does nothing. Used by tests to skip
// real Helm network calls without affecting the scenario call-order logic.
type noopHelmRunner struct{}

func (n *noopHelmRunner) EnsureRelease(_ helmRelease) error  { return nil }
func (n *noopHelmRunner) UninstallRelease(_, _ string) error { return nil }
