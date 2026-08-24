package bnkbom

import (
	"fmt"
	"sort"
	"strings"
)

// The Gateway API bundle (#185).
//
// BNK 2.4's FLO crd-installer no longer forces its own Gateway API CRDs — it
// logs a graceful skip and leaves the cluster on whatever bundle OpenShift
// ships. That is correct for a base install. An mTLS deployment is the
// exception: it needs the upstream STANDARD channel at the version the CNE
// controller is told to expect (GATEWAY_API_VERSION), and nothing on the
// cluster installs it.
//
// So roksbnkctl installs it — and carries it through the mirror rather than
// beside it. A disconnected cluster can only reach the mirror, and in the CI
// path roksbnkctl runs as an Argo pod IN that cluster, so its egress is the
// cluster's: github.com is unreachable for exactly the estates that want mTLS.
// In the BOM it gets the same three guarantees every chart and image gets —
// `registry bom` lists it, `registry replicate` copies it, `registry verify`
// proves it arrived. A file staged by hand has none of them.

const (
	// OriginGatewayAPI marks the upstream kubernetes-sigs/gateway-api bundle.
	// Its own origin rather than OriginManifest: the F5 manifest does not
	// enumerate it, and it is not one of the two non-F5 deps --include-deps
	// toggles either — it is unioned on a different condition entirely (2.4 +
	// mTLS), so folding it into an existing origin would make `registry bom`
	// attribute it to something that never mentioned it.
	OriginGatewayAPI Origin = "gateway-api"

	// GatewayAPIBundleHost is the artifact's source registry host. A KindFile is
	// fetched over HTTPS from SourceURL rather than pulled from a registry, but
	// every artifact still reports where it came from — `registry bom` prints a
	// SOURCE column, and a blank one for the single row an operator is least
	// likely to recognise is the wrong thing to print.
	GatewayAPIBundleHost = "github.com"

	// GatewayAPIBundleName is the repository path the bundle occupies in the
	// mirror. "files/" parallels the FAR categories (charts/, images/, utils/)
	// so a mirror listing groups it with its kind rather than scattering it.
	GatewayAPIBundleName = "files/gateway-api-standard-install"

	// GatewayAPIBundleFile is the upstream asset's basename. copyFile stores the
	// layer's single file under it, so the pull side finds it without a
	// side-channel convention.
	GatewayAPIBundleFile = "standard-install.yaml"
)

// gatewayAPIBundleSHA256 pins each bundle version roksbnkctl can install, by
// version WITHOUT a leading "v".
//
// The pin lives in CODE, not in configuration, and that is deliberate. This
// file is ~1 MB of CustomResourceDefinitions and a ValidatingAdmissionPolicy
// applied with --force-conflicts to a cluster; a pin an operator can retype is
// a pin an attacker upstream can talk them out of. Adding a version is a
// one-line, reviewed change — which is exactly the amount of ceremony a new
// megabyte of cluster-scoped CRDs deserves.
//
// v1.5.0: 1023753 bytes — 8 CustomResourceDefinitions, one
// ValidatingAdmissionPolicy and its binding, and no container images at all
// (nothing to pull at runtime; the file itself is the whole artifact).
var gatewayAPIBundleSHA256 = map[string]string{
	"1.5.0": "510338cf6709f84410efcce5269268f4c7c5067efdc5d04c75aa2fd2f8380c96",
}

// NormalizeGatewayAPIVersion strips a leading "v" and surrounding space.
//
// bnk.gateway_api_version is rendered verbatim into GATEWAY_API_VERSION for the
// CNE controller, where F5's reference spells it "1.5.0"; the upstream release
// tag spells the same release "v1.5.0". One value, two spellings, so every
// lookup normalises rather than each caller remembering which form it holds.
func NormalizeGatewayAPIVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}

// GatewayAPIBundleURL is the upstream release asset for a bundle version.
func GatewayAPIBundleURL(version string) string {
	return fmt.Sprintf(
		"https://github.com/kubernetes-sigs/gateway-api/releases/download/v%s/%s",
		NormalizeGatewayAPIVersion(version), GatewayAPIBundleFile)
}

// PinnedGatewayAPIVersions lists the versions carrying a pin, sorted, for error
// messages. Derived from the table so a message can never name a version the
// table has lost.
func PinnedGatewayAPIVersions() []string {
	out := make([]string, 0, len(gatewayAPIBundleSHA256))
	for v := range gatewayAPIBundleSHA256 {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// GatewayAPIBundle returns the BOM artifact for the Gateway API standard-install
// bundle at version, fetched from sourceURL when that is non-empty and from the
// upstream release otherwise.
//
// sourceURL overrides only WHERE the bytes come from, never WHAT they must be:
// the sha256 is still the pin for the configured VERSION. An internal proxy of
// the upstream release serves byte-identical content, so the override costs
// nothing in safety and is what makes the bundle reachable from an estate that
// blocks github.com but has not (yet) replicated a mirror.
//
// An unpinned version is refused rather than fetched unverified. That refusal is
// the whole reason the pin table exists — see gatewayAPIBundleSHA256.
func GatewayAPIBundle(version, sourceURL string) (Artifact, error) {
	v := NormalizeGatewayAPIVersion(version)
	if v == "" {
		return Artifact{}, fmt.Errorf("gateway API bundle: no version given")
	}
	sum, ok := gatewayAPIBundleSHA256[v]
	if !ok {
		return Artifact{}, fmt.Errorf(
			"gateway API bundle %s has no sha256 pin in this build of roksbnkctl, so it will not be "+
				"fetched or installed (pinned: %s).\n"+
				"  set bnk.gateway_api_version to a pinned release, or add the pin — it is deliberately "+
				"code rather than configuration, because this bundle is applied to the cluster with "+
				"--force-conflicts",
			v, strings.Join(PinnedGatewayAPIVersions(), ", "))
	}
	url := strings.TrimSpace(sourceURL)
	if url == "" {
		url = GatewayAPIBundleURL(v)
	}
	return Artifact{
		Kind:       KindFile,
		SourceHost: GatewayAPIBundleHost,
		Name:       GatewayAPIBundleName,
		Tag:        "v" + v,
		Origin:     OriginGatewayAPI,
		SourceURL:  url,
		SHA256:     sum,
	}, nil
}
