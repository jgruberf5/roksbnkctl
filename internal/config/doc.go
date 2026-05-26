// Package config loads workspace and global configuration for awsbnkctl.
//
// File layout:
//
//	~/.awsbnkctl/config.yaml             — global preferences, current_workspace
//	~/.awsbnkctl/<workspace>/config.yaml — per-workspace inputs
//	~/.awsbnkctl/<workspace>/state/      — state.env IDs cache, kubeconfig, scratch/
//
// Override the base directory via $ROKSBNKCTL_HOME (used by tests;
// advanced users with non-home-dir state).
//
// Secrets policy: workspace config.yaml is rejected at load time if it
// contains plaintext credentials (api_key, password, token,
// secret_access_key, etc.). AWS credentials resolve via the SDK chain
// in internal/aws (env / shared config / profile / instance role /
// SSO / web identity), never via the workspace config.
package config
