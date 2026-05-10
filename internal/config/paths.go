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
