package config

// Sprint 30 (PRD 13 Issue 4) — `roksbnkctl init --override-from-env`. After a
// workspace config.yaml is assembled (from --config-file and/or the interview),
// overlay a FIXED set of fields from environment variables. This lets a single
// committed config.yaml template carry placeholders (e.g. api_key_b64: "") and
// have a CI pipeline inject the real values from the environment — no secret in
// version control. Env values WIN over whatever the seed/interview produced.
//
// This is a fixed field map, NOT arbitrary interpolation (a non-goal): each
// supported variable maps to exactly one config field.

import (
	"encoding/base64"
	"os"
	"strings"
)

// OverrideFromEnv overlays config.yaml fields from environment variables that
// are set + non-empty, mutating ws in place. It returns a list of human-
// readable field labels that were overridden (for logging) — NEVER the values,
// so a secret like the API key is never printed.
//
// Supported variables (env → field):
//
//	IBMCLOUD_API_KEY                 → ibmcloud.api_key_b64   (raw key, base64-encoded)
//	ROKSBNKCTL_API_KEY_B64          → ibmcloud.api_key_b64   (verbatim; pre-encoded)
//	ROKSBNKCTL_PREFIX               → prefix
//	ROKSBNKCTL_REGION               → ibmcloud.region
//	ROKSBNKCTL_RESOURCE_GROUP       → ibmcloud.resource_group
//	ROKSBNKCTL_TESTING_SSH_KEY_NAME → resources.testing_ssh_key_name
//	ROKSBNKCTL_GENERIC_PASSWORD     → registry.generic_password_b64 (raw, base64-encoded)
//
// ROKSBNKCTL_API_KEY_B64 takes precedence over IBMCLOUD_API_KEY when both are
// set (an explicit pre-encoded value beats the raw-key convenience path).
func OverrideFromEnv(ws *Workspace) []string {
	var applied []string

	// API key — pre-encoded escape hatch first, else the raw-key convenience.
	if v := envValue("ROKSBNKCTL_API_KEY_B64"); v != "" {
		ws.IBMCloud.APIKeyB64 = v
		applied = append(applied, "ibmcloud.api_key_b64 (ROKSBNKCTL_API_KEY_B64)")
	} else if v := envValue("IBMCLOUD_API_KEY"); v != "" {
		ws.IBMCloud.APIKeyB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "ibmcloud.api_key_b64 (IBMCLOUD_API_KEY)")
	}

	if v := envValue("ROKSBNKCTL_PREFIX"); v != "" {
		ws.Prefix = v
		applied = append(applied, "prefix (ROKSBNKCTL_PREFIX)")
	}
	if v := envValue("ROKSBNKCTL_REGION"); v != "" {
		ws.IBMCloud.Region = v
		applied = append(applied, "ibmcloud.region (ROKSBNKCTL_REGION)")
	}
	if v := envValue("ROKSBNKCTL_RESOURCE_GROUP"); v != "" {
		ws.IBMCloud.ResourceGroup = v
		applied = append(applied, "ibmcloud.resource_group (ROKSBNKCTL_RESOURCE_GROUP)")
	}
	if v := envValue("ROKSBNKCTL_TESTING_SSH_KEY_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.TestingSSHKeyName = v
		applied = append(applied, "resources.testing_ssh_key_name (ROKSBNKCTL_TESTING_SSH_KEY_NAME)")
	}

	// Generic OCI registry password (e.g. an Artifactory access token) — raw in
	// the env, base64-encoded into the config like the API key.
	if v := envValue("ROKSBNKCTL_GENERIC_PASSWORD"); v != "" {
		if ws.Registry == nil {
			ws.Registry = &RegistryCfg{}
		}
		ws.Registry.GenericPasswordB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "registry.generic_password_b64 (ROKSBNKCTL_GENERIC_PASSWORD)")
	}

	return applied
}

// envValue returns the trimmed value of an environment variable, or "" when
// unset or whitespace-only.
func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
