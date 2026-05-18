package orchestration

import (
	"context"
	"fmt"
	"os"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// WorkspaceEnvCore composes the machine-portable subset of the
// workspace env — values, not local filesystem paths: IBMCLOUD_API_KEY
// / IC_API_KEY / IBMCLOUD_REGION / IBMCLOUD_VERSION_CHECK, on top of
// the host env with every local-path-valued var (LocalOnlyEnvKeys)
// already scrubbed. This is the ONLY workspace-derived env that is safe
// to cross the --on SSH boundary; it deliberately omits KUBECONFIG (a
// local path) — including any KUBECONFIG the user exported in their
// shell, which os.Environ() would otherwise re-leak across the boundary
// (Sprint 13 Issue 1: correctness is "never send a local path", which
// includes inherited ones).
//
// Callers that dispatch remotely source this; callers that exec locally
// use WorkspaceEnv which adds the KUBECONFIG addendum on top.
func WorkspaceEnvCore(workspace string) (*config.Context, []string, error) {
	cctx, err := config.New(workspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `roksbnkctl init` first", cctx.WorkspaceName)
	}

	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    cctx.Workspace.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("resolving API key: %w", err)
	}

	// Start from the host env MINUS any local-path-valued var (the
	// single LocalOnlyEnvKeys classification — same one the remote
	// boundary assertion uses).
	env := ScrubLocalOnly(os.Environ())
	env = append(env, "IBMCLOUD_API_KEY="+apiKey)
	env = append(env, "IC_API_KEY="+apiKey)
	if r := cctx.Workspace.IBMCloud.Region; r != "" {
		env = append(env, "IBMCLOUD_REGION="+r)
	}
	// Silence the "New plug-in version available" / "TIP: --check-version"
	// banner the ibmcloud CLI prints on every invocation.
	env = append(env, "IBMCLOUD_VERSION_CHECK=false")
	return cctx, env, nil
}

// WorkspaceEnv composes the env a child process should inherit when run
// LOCALLY: the machine-portable core (WorkspaceEnvCore) plus the
// local-only addendum (KUBECONFIG, resolved from the host's lookup
// chain).
//
// IMPORTANT: this slice is correct for LOCAL exec only. KUBECONFIG is a
// local filesystem path that is meaningless on an SSH target —
// forwarding it across the --on boundary points remote kubectl/oc at a
// nonexistent file and shadows the target's cloud-init kubeconfig
// (Sprint 13 Issue 1). Callers that cross the SSH boundary MUST use
// WorkspaceEnvCore.
func WorkspaceEnv(workspace string) (*config.Context, []string, error) {
	cctx, core, err := WorkspaceEnvCore(workspace)
	if err != nil {
		return nil, nil, err
	}
	env := core
	if path := k8s.DefaultKubeconfigPath(); path != "" {
		env = append(env, "KUBECONFIG="+path)
	}
	return cctx, env, nil
}
