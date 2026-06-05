package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout constants. Filenames inside a workspace dir match the global
// filename so users can mentally substitute one for the other.
const (
	defaultBaseDirName  = ".roksbnkctl"
	globalConfigFile    = "config.yaml"
	workspaceConfigFile = "config.yaml"
	stateSubdir         = "state"
	clusterStateSubdir  = "state-cluster"
	testingStateSubdir  = "state-testing"
	clusterOutputsFile  = "cluster-outputs.json"

	// ROKSBNKCTLHomeEnv overrides the default ~/.roksbnkctl base. Used by tests
	// (and power users who want non-home-dir state).
	ROKSBNKCTLHomeEnv = "ROKSBNKCTL_HOME"
)

// BaseDir returns the roksbnkctl root directory.
//
//  1. $ROKSBNKCTL_HOME if set (no expansion — used as-is)
//  2. $HOME/.roksbnkctl otherwise
func BaseDir() (string, error) {
	if v := os.Getenv(ROKSBNKCTLHomeEnv); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, defaultBaseDirName), nil
}

// PluginCacheDir: ~/.roksbnkctl/plugin-cache/ — the shared terraform
// provider plugin cache (TF_PLUGIN_CACHE_DIR). One cache across every
// workspace and phase so each provider downloads ONCE and is linked from
// the cache thereafter, instead of re-downloading ~440 MB of providers per
// phase/workspace.
func PluginCacheDir() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "plugin-cache"), nil
}

// GlobalConfigPath: ~/.roksbnkctl/config.yaml
func GlobalConfigPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, globalConfigFile), nil
}

// WorkspaceDir: ~/.roksbnkctl/<name>/
func WorkspaceDir(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name), nil
}

// WorkspaceConfigPath: ~/.roksbnkctl/<name>/config.yaml
func WorkspaceConfigPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceConfigFile), nil
}

// WorkspaceStateDir: ~/.roksbnkctl/<name>/state/  (terraform.tfstate, kubeconfig, scratch/)
func WorkspaceStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateSubdir), nil
}

// WorkspaceClusterStateDir: ~/.roksbnkctl/<name>/state-cluster/ — separate TF
// state tree for the `roksbnkctl cluster up/down` phase so it doesn't tangle
// with the BNK-trial state at WorkspaceStateDir.
func WorkspaceClusterStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterStateSubdir), nil
}

// WorkspaceTestingStateDir: ~/.roksbnkctl/<name>/state-testing/ — separate TF
// state tree for the `roksbnkctl testing up/down` phase (Sprint 28 three-phase
// split). The testing jumphosts (pure IBM VPC) live here, decoupled from both
// the cluster phase (state-cluster/) and the BNK phase (state/), so `bnk down`
// leaves the jumphosts and `testing down` leaves BNK.
//
// Note the naming asymmetry: the BNK phase keeps the original WorkspaceStateDir
// (state/) — see the architect's Sprint 28 design §1a (BNK keeps state/, zero
// migration; only the jumphosts move out).
func WorkspaceTestingStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, testingStateSubdir), nil
}

// WorkspaceClusterOutputsPath: ~/.roksbnkctl/<name>/cluster-outputs.json —
// persisted cluster identity (created by `roksbnkctl cluster up` or
// `roksbnkctl cluster register`, consumed by `roksbnkctl up` for BNK-only runs).
func WorkspaceClusterOutputsPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterOutputsFile), nil
}
