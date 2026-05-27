package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Layout constants. Filenames inside a workspace dir match the global
// filename so users can mentally substitute one for the other.
const (
	defaultBaseDirName  = ".awsbnkctl"
	globalConfigFile    = "config.yaml"
	workspaceConfigFile = "config.yaml"
	stateSubdir         = "state"
	clusterStateSubdir  = "state-cluster"
	clusterOutputsFile  = "cluster-outputs.json"

	// AWSBNKCTLHomeEnv overrides the default ~/.awsbnkctl base. Used by tests
	// (and power users who want non-home-dir state).
	// The legacy name ROKSBNKCTL_HOME is still honoured for back-compat.
	AWSBNKCTLHomeEnv = "AWSBNKCTL_HOME"

	// ROKSBNKCTLHomeEnv is the legacy alias for AWSBNKCTLHomeEnv, kept for
	// back-compat with scripts and CI that pre-date the rename.
	ROKSBNKCTLHomeEnv = "ROKSBNKCTL_HOME"
)

// BaseDir returns the awsbnkctl root directory.
//
//  1. $AWSBNKCTL_HOME if set (no expansion — used as-is)
//  2. $ROKSBNKCTL_HOME if set (legacy back-compat alias)
//  3. $HOME/.awsbnkctl otherwise
func BaseDir() (string, error) {
	if v := os.Getenv(AWSBNKCTLHomeEnv); v != "" {
		return v, nil
	}
	if v := os.Getenv(ROKSBNKCTLHomeEnv); v != "" { // back-compat
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, defaultBaseDirName), nil
}

// GlobalConfigPath: ~/.awsbnkctl/config.yaml
func GlobalConfigPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, globalConfigFile), nil
}

// WorkspaceDir: ~/.awsbnkctl/<name>/
func WorkspaceDir(name string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, name), nil
}

// WorkspaceConfigPath: ~/.awsbnkctl/<name>/config.yaml
func WorkspaceConfigPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, workspaceConfigFile), nil
}

// WorkspaceStateDir: ~/.awsbnkctl/<name>/state/  (state.env IDs cache, kubeconfig, scratch/)
func WorkspaceStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, stateSubdir), nil
}

// WorkspaceClusterStateDir: ~/.awsbnkctl/<name>/state-cluster/ — separate
// state directory for the `awsbnkctl cluster up/down` phase so it doesn't
// tangle with the BNK-trial state at WorkspaceStateDir.
func WorkspaceClusterStateDir(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterStateSubdir), nil
}

// WorkspaceClusterOutputsPath: ~/.awsbnkctl/<name>/cluster-outputs.json —
// persisted cluster identity (created by `awsbnkctl cluster up` or
// `awsbnkctl cluster register`, consumed by `awsbnkctl up` for BNK-only runs).
func WorkspaceClusterOutputsPath(name string) (string, error) {
	dir, err := WorkspaceDir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, clusterOutputsFile), nil
}
