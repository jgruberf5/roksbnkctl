package config

import "strings"

// The Gateway API bundle's two questions (#185): does this install need it, and
// which release is it. Both live here rather than at their call sites because
// each has more than one caller — the BOM build, the install-time apply and the
// admission-policy sweep — and a second copy of either is a second answer that
// can disagree with the first.

// DefaultGatewayAPIVersion mirrors the cneinstance_gateway_api_version terraform
// default. An empty bnk.gateway_api_version renders no tfvar, so the CNE
// controller is told this version — and the bundle roksbnkctl installs has to be
// the SAME release, or the controller is configured for a Gateway API the
// cluster does not carry.
const DefaultGatewayAPIVersion = "1.5.0"

// GatewayAPIBundleNeeded reports whether this install must have the upstream
// Gateway API standard-install bundle applied to its cluster.
//
// Exactly two things make it necessary, and both must hold:
//
//   - The BNK line is 2.4. On 2.3 the FLO crd-installer forces its own Gateway
//     API CRDs, so a second copy applied over the top is at best redundant.
//   - bnk.gateway_api_mtls is on. On 2.4 the crd-installer logs a graceful skip
//     and leaves the cluster on whatever bundle OpenShift ships, which is right
//     for a base install; only mTLS needs the newer standard channel.
//
// The same pair gates the admission-policy sweep on 2.4, and deliberately so:
// installing the bundle means winning a race against the OpenShift ingress
// operator, so a build that installs the bundle without sweeping, or sweeps
// without installing the bundle, is a build where one half does nothing.
func (w *Workspace) GatewayAPIBundleNeeded() bool {
	if w == nil {
		return false
	}
	return w.BNKLineOrEmpty() == "2.4" && w.BNK.GatewayAPIMTLS
}

// GatewayAPIBundleVersion is the Gateway API release this workspace installs AND
// tells the CNE controller to expect (GATEWAY_API_VERSION). One accessor for
// both, so the bundle on the cluster and the version the controller was
// configured with cannot be resolved from different places and disagree.
func (w *Workspace) GatewayAPIBundleVersion() string {
	if w == nil || strings.TrimSpace(w.BNK.GatewayAPIVersion) == "" {
		return DefaultGatewayAPIVersion
	}
	return strings.TrimSpace(w.BNK.GatewayAPIVersion)
}
