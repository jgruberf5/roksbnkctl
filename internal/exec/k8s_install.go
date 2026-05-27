package exec

import (
	_ "embed"
)

// k8sInstallYAML is the multi-document YAML template applied by
// `awsbnkctl ops install`. Embedded at build time so the binary is
// self-contained — no need to ship a separate manifests directory.
//
// The ops ServiceAccount carries an `eks.amazonaws.com/role-arn`
// annotation; the EKS pod-identity webhook injects `AWS_ROLE_ARN` +
// `AWS_WEB_IDENTITY_TOKEN_FILE` + a projected SA token, and
// aws-sdk-go-v2 inside the pod assumes the role via
// `sts:AssumeRoleWithWebIdentity`. No static credential lands in
// any Secret. PRD 04 §"In-cluster identity".
//
// Template placeholders substituted at apply-time:
//
//	${OPS_IRSA_ROLE_ARN} — the IAM role ARN provisioned for the
//	                       ops pod's ServiceAccount by the
//	                       Go-SDK IAM phase (PRD 08); resolved
//	                       from the workspace state by the CLI
//	                       layer at install time.
//	${OPS_IMAGE}         — the awsbnkctl tools image ref;
//	                       version-pinned to internal/cli.Version.
//
//go:embed k8s_install.yaml
var k8sInstallYAML string

// K8sInstallYAML returns the embedded install manifest template. The
// CLI layer substitutes ${OPS_IRSA_ROLE_ARN} and ${OPS_IMAGE} before
// applying.
//
// Exported as a function (not a var) so callers can't accidentally
// mutate the embedded copy at runtime — the substitution happens on
// a strings.NewReplacer.Replace return value, which is a fresh string.
func K8sInstallYAML() string { return k8sInstallYAML }
