// Package source is the FAR (F5 Artifact Repository, repo.f5.com) OCI source
// client for the Sprint 29 registry mirror (PRD 11 §3). It does two jobs:
//
//   - FetchManifest pulls the f5-bigip-k8s-manifest OCI artifact for a BNK
//     release (the same chart the terraform FLO module pulls to discover the
//     FLO/CIS versions), extracts the embedded
//     bigip-k8s-manifest-<version>.yaml, and returns its bytes — the seed the
//     bnkbom.Build parser turns into the BOM.
//   - SourceAuth resolves a go-containerregistry authenticator for a source
//     registry host: the FAR `_json_key_base64` service-account credential for
//     repo.f5.com, anonymous everywhere else. This feeds mirror.Engine.SourceAuth.
//
// FetchManifest shells the helm binary (`helm registry login` + `helm pull
// oci://…`) rather than reimplementing the OCI pull, mirroring how the
// classic-helm copy path in internal/registry/mirror already drives helm.
package source

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
)

// FARHost is the F5 Artifact Repository registry host the manifest + the F5
// charts/images live under.
const FARHost = "repo.f5.com"

// manifestChart is the OCI repository path (under the FAR host's release/
// namespace) of the f5-bigip-k8s-manifest chart that carries the BOM document.
const manifestChart = "release/f5-bigip-k8s-manifest"

// jsonKeyUser is the fixed username FAR's `_json_key_base64` auth scheme
// expects; the password is the base64 service-account JSON itself.
const jsonKeyUser = "_json_key_base64"

// FetchManifest pulls the f5-bigip-k8s-manifest OCI artifact for `version` from
// the FAR host (default repo.f5.com when farRepoURL is empty), extracts it, and
// returns the bytes of the inner bigip-k8s-manifest-<version>.yaml.
//
// It shells helm: `helm registry login <host> -u _json_key_base64 -p <sa>` (only
// when a service-account credential is supplied), then `helm pull
// oci://<host>/release/f5-bigip-k8s-manifest --version <version> -d <scratch>`.
// The resulting tarball is f5-bigip-k8s-manifest-<version>.tgz, which unpacks to
// a directory f5-bigip-k8s-manifest-<version>/ containing
// bigip-k8s-manifest-<version>.yaml (confirmed against the real 2.3.0 artifact).
//
// helmBin may be "" → "helm". scratchDir may be "" → an os.MkdirTemp cleaned up
// on return. farServiceAccountB64 may be "" → an anonymous pull (helm registry
// login is skipped).
func FetchManifest(ctx context.Context, farRepoURL, version, helmBin, scratchDir, farServiceAccountB64 string) ([]byte, error) {
	if version == "" {
		return nil, fmt.Errorf("manifest version is required")
	}
	host := farHost(farRepoURL)
	bin := helmBin
	if bin == "" {
		bin = "helm"
	}

	scratch := scratchDir
	if scratch == "" {
		d, err := os.MkdirTemp("", "bnk-far-manifest-*")
		if err != nil {
			return nil, fmt.Errorf("scratch dir: %w", err)
		}
		defer os.RemoveAll(d)
		scratch = d
	} else if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, fmt.Errorf("scratch dir %s: %w", scratch, err)
	}

	if farServiceAccountB64 != "" {
		login := exec.CommandContext(ctx, bin, "registry", "login", host,
			"-u", jsonKeyUser, "-p", farServiceAccountB64)
		if out, err := login.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("helm registry login %s: %w: %s", host, err, strings.TrimSpace(string(out)))
		}
	}

	ref := fmt.Sprintf("oci://%s/%s", host, manifestChart)
	pull := exec.CommandContext(ctx, bin, "pull", ref, "--version", version, "-d", scratch)
	if out, err := pull.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("helm pull %s --version %s: %w: %s", ref, version, err, strings.TrimSpace(string(out)))
	}

	tgz := filepath.Join(scratch, fmt.Sprintf("f5-bigip-k8s-manifest-%s.tgz", version))
	return extractManifestYAML(tgz, version)
}

// farHost extracts the registry host from a far_repo_url that may carry an
// oci:// scheme and/or a trailing path. Empty → the default FARHost.
func farHost(farRepoURL string) string {
	s := strings.TrimSpace(farRepoURL)
	if s == "" {
		return FARHost
	}
	s = strings.TrimPrefix(s, "oci://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	// keep only the host (drop any /path the user pinned).
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return FARHost
	}
	return s
}

// SourceAuth returns a mirror.SourceAuth-compatible resolver: the FAR
// `_json_key_base64` credential for the FAR host (when a service account is
// supplied), anonymous for every other host. farServiceAccountB64 is the
// base64-encoded service-account JSON (the password half of the FAR
// `_json_key_base64` scheme). farRepoURL selects which host the credential
// applies to (default repo.f5.com).
//
// Returning nil for a host means "anonymous" (the engine's keychain treats a nil
// authenticator as authn.Anonymous).
func SourceAuth(farRepoURL, farServiceAccountB64 string) func(host string) authn.Authenticator {
	target := farHost(farRepoURL)
	return func(host string) authn.Authenticator {
		if host == target && farServiceAccountB64 != "" {
			return &authn.Basic{Username: jsonKeyUser, Password: farServiceAccountB64}
		}
		return nil
	}
}

// DecodeServiceAccount decodes the base64 service-account JSON, returning a
// helpful error on a malformed value (the credential is otherwise an opaque blob
// the helm login / authn.Basic password carries verbatim — this is a validation
// convenience, not a transform the callers must apply).
func DecodeServiceAccount(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decoding FAR service-account base64: %w", err)
	}
	return raw, nil
}
