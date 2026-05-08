package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout constants. Filenames inside a workspace dir match the global
// filename so users can mentally substitute one for the other.
const (
	defaultBaseDirName  = ".roksctl"
	globalConfigFile    = "config.yaml"
	workspaceConfigFile = "config.yaml"
	stateSubdir         = "state"
	clusterStateSubdir  = "state-cluster"
	clusterOutputsFile  = "cluster-outputs.json"

	// ROKSCTLHomeEnv overrides the default ~/.roksctl base. Used by tests
	// (and power users who want non-home-dir state).
	ROKSCTLHomeEnv = "ROKSCTL_HOME"
)

// BaseDir returns the roksctl root directory.
//
//   1. $ROKSCTL_HOME if set (no expansion — used as-is)
//   2. $HOME/.roksctl otherwise
func BaseDir() (string, error) {
	if v := os.Getenv(ROKSCTLHomeEnv); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, defaultBaseDirName), nil
}

// GlobalConfigPath: ~/.roksctl/config.yaml
func GlobalConfigPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, globalConfigFile), nil
}

// WorkspaceDir: ~/.roksctl/<name>/
func WorkspaceDir(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name), nil
}

// WorkspaceConfigPath: ~/.roksctl/<name>/config.yaml
func WorkspaceConfigPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceConfigFile), nil
}

// WorkspaceStateDir: ~/.roksctl/<name>/state/  (terraform.tfstate, kubeconfig, scratch/)
func WorkspaceStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateSubdir), nil
}

// WorkspaceClusterStateDir: ~/.roksctl/<name>/state-cluster/ — separate TF
// state tree for the `roksctl cluster up/down` phase so it doesn't tangle
// with the BNK-trial state at WorkspaceStateDir.
func WorkspaceClusterStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterStateSubdir), nil
}

// WorkspaceClusterOutputsPath: ~/.roksctl/<name>/cluster-outputs.json —
// persisted cluster identity (created by `roksctl cluster up` or
// `roksctl cluster register`, consumed by `roksctl up` for BNK-only runs).
func WorkspaceClusterOutputsPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterOutputsFile), nil
}
