// Package ocireg implements the Sprint 30 RegistryTarget for any STANDARD OCI
// registry that nests the FAR category under a configured namespace/repository
// prefix: "<host>/<namespace>/images/<name>". One impl serves two backends:
//
//   - IBM Container Registry (the default) — "<region>.icr.io" + an ICR
//     namespace + iamapikey auth.
//   - a generic OCI registry — Artifactory / Harbor / registry:2 + a repo
//     prefix + static basic auth.
//
// push == image-pull == chart-pull on the single host (the easy case the
// mirror.Target contract was designed to also support). Contrast the OpenShift
// target, whose flat <project>/<name> registry forces the FAR category to be
// the top-level project and splits push/pull across a route + an in-cluster
// service.
package ocireg

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/authn"

	"github.com/jgruberf5/roksbnkctl/internal/bnkbom"
)

// Target is a nesting OCI RegistryTarget. Host is the registry host; Namespace
// is the repository prefix every FAR category nests under (an ICR namespace, an
// Artifactory repo path). Auth is the static push/pull credential.
type Target struct {
	Host      string
	Namespace string
	Auth      authn.Authenticator
}

// base is "<host>/<namespace>", or just "<host>" when Namespace is empty.
func (t *Target) base() string {
	if t.Namespace == "" {
		return t.Host
	}
	return t.Host + "/" + t.Namespace
}

// ── mirror.Target (push side) ───────────────────────────────────────────────

// PushHost is the registry host (keychain routing + `helm registry login`).
func (t *Target) PushHost() string { return t.Host }

// PushRef nests the artifact (a.Name is "<category>/<name>") under the
// namespace: "<host>/<namespace>/images/tmm-img:<tag>".
func (t *Target) PushRef(a bnkbom.Artifact) string {
	return fmt.Sprintf("%s/%s:%s", t.base(), a.Name, a.Tag)
}

// PushAuth authenticates pushes (nil → anonymous).
func (t *Target) PushAuth() authn.Authenticator { return t.Auth }

// ── pull-side endpoints (the install redirect consumes the host paths) ───────

// ImagePullRef — pods pull from the SAME host, by digest when known.
func (t *Target) ImagePullRef(a bnkbom.Artifact) string {
	ref := t.base() + "/" + a.Name
	if a.Digest != "" {
		return ref + "@" + a.Digest
	}
	return ref + ":" + a.Tag
}

// ChartPullRef — the host's helm provider pulls over the same host.
func (t *Target) ChartPullRef(a bnkbom.Artifact) string {
	return "oci://" + t.base() + "/" + a.Name
}

// ImageHostPath / ChartHostPath — the host+namespace root the install redirect
// points references at; the install appends "/<category>/<name>", so both are
// "<host>/<namespace>".
func (t *Target) ImageHostPath() string { return t.base() }
func (t *Target) ChartHostPath() string { return t.base() }

// MirrorNamespace is the repo prefix recorded in registry-mirror.json.
func (t *Target) MirrorNamespace() string { return t.Namespace }

// Prepare is a no-op for a standard OCI registry: the namespace/repo must
// already exist and auth is static — there is no live cluster bootstrap to do
// (unlike the OpenShift target). Present so the construction sites read
// uniformly; callers may skip it.
func (t *Target) Prepare() error { return nil }
