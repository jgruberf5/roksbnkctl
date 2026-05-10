package exec

import (
	_ "embed"
)

// k8sInstallYAML is the multi-document YAML template applied by
// `roksbnkctl ops install`. Embedded at build time so the binary is
// self-contained — no need to ship a separate manifests directory.
//
// Template placeholders substituted at apply-time:
//
//	${IBMCLOUD_API_KEY_B64} — base64-encoded IBM Cloud API key from the
//	                          workspace's resolved cred (the Secret's
//	                          data field expects b64).
//	${ROTATED_AT}           — RFC3339 timestamp of the apply, stamped on
//	                          the Secret as an annotation so `ops show`
//	                          can render rotation.
//	${OPS_IMAGE}            — the roksbnkctl-tools-ibmcloud image ref;
//	                          version-pinned to internal/cli.Version per
//	                          the toolImages migration (no more :dev hard-
//	                          code on prod-tag builds).
//
//go:embed k8s_install.yaml
var k8sInstallYAML string

// K8sInstallYAML returns the embedded install manifest template. The
// CLI layer (internal/cli/ops.go) substitutes ${IBMCLOUD_API_KEY_B64},
// ${ROTATED_AT}, and ${OPS_IMAGE} before applying.
//
// Exported as a function (not a var) so callers can't accidentally
// mutate the embedded copy at runtime — the substitution happens on
// a strings.NewReplacer.Replace return value, which is a fresh string.
func K8sInstallYAML() string { return k8sInstallYAML }
