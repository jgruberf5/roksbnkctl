package cli

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/JLCode-tech/awsbnkctl/internal/config"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s"
)

// Cross-verb helpers shared by test.go + doctor_backend.go.
//
// Origin: Sprint 0 split-out from the deleted IBM lifecycle verbs.
// Sprint 3 trims the file to the helpers that still have live
// callers in test.go / doctor_backend.go (workspaceEnv,
// resolveBackendSpecWith, podReady). The IBM-cred
// silencer + context import dropped alongside the PRD 04 retarget;
// the cred package no longer threads an IBM Cloud API key through
// the execution backends — AWS credentials resolve via the SDK chain
// in internal/aws and IRSA in-cluster, so there's nothing for these
// helpers to inject.

// workspaceEnv composes a child-process env for inherited tool
// passthroughs. Returns the host env plus KUBECONFIG if a kubeconfig
// is on disk. AWS credentials are resolved by the SDK chain (env /
// profile / instance role / SSO) — no cred-shaped env vars are
// injected here.
func workspaceEnv() (*config.Context, []string, error) {
	cctx, err := config.New(flagWorkspace)
	if err != nil {
		return nil, nil, err
	}
	if cctx.Workspace == nil {
		return nil, nil, fmt.Errorf("workspace %q is not initialised; run `awsbnkctl init` first", cctx.WorkspaceName)
	}
	env := os.Environ()
	if path := k8s.DefaultKubeconfigPath(); path != "" {
		env = append(env, "KUBECONFIG="+path)
	}
	return cctx, env, nil
}

// resolveBackendSpecWith picks the execution backend for tool. Order:
//
//  1. flagOverride (the explicit per-invocation flag)
//  2. workspace's exec.<tool>.backend
//  3. perToolDefaultBackend[tool]
//  4. "local" default
func resolveBackendSpecWith(cctx *config.Context, tool, flagOverride string) string {
	if flagOverride != "" {
		return flagOverride
	}
	if cctx != nil && cctx.Workspace != nil {
		if entry, ok := cctx.Workspace.Exec[tool]; ok && entry.Backend != "" {
			return entry.Backend
		}
	}
	if def, ok := perToolDefaultBackend[tool]; ok {
		return def
	}
	return "local"
}

// perToolDefaultBackend is the per-tool default backend table (PRD 03
// §"Tool migration plan"). AWS doesn't ship a CLI passthrough — the
// binary uses internal/aws SDK directly per PRD 00 § "Inheritance map".
var perToolDefaultBackend = map[string]string{
	"iperf3": "k8s",
}

// podReady reports whether a pod's ContainerStatuses agree that it is
// Ready. Lifted verbatim from the deleted ops.go so doctor_backend.go's
// ops-pod check keeps compiling.
func podReady(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
