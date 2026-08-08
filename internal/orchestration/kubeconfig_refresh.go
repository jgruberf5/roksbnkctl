package orchestration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/k8s"
)

// Staleness skews — how close to expiry a credential may get before
// ensureFreshKubeconfig refreshes it. Tokens live ~1h, so a 5-minute skew
// keeps a comfortable margin while almost always no-op'ing; admin certs
// live ~30d, so a 2-day skew avoids a needless (expensive) re-fetch.
const (
	tokenRefreshSkew = 5 * time.Minute
	certRefreshSkew  = 48 * time.Hour
)

// EnsureFreshKubeconfig is the self-healing gate every kubeconfig consumer
// calls before exec'ing the underlying tool. It operates on the token-based
// forge kubeconfig (config.ForgeKubeconfigPath) and returns the path the
// caller should point KUBECONFIG at, or "" to fall back to the existing
// admin/default config.
//
// Cheap by design: when the embedded credential is still well within its
// life it's a pure local parse — no network, no IBM client. Only when the
// credential is within the skew (or force is set, or it can't be parsed)
// does it mint a fresh IAM token and rewrite the file atomically.
//
// Graceful degradation: if there's no forge kubeconfig, no API key, or
// we're offline, it logs a warning (when it actually tried and failed) and
// returns "" so the caller uses the existing config rather than hard-failing.
func EnsureFreshKubeconfig(ctx context.Context, in *ClusterInputs, force bool) string {
	path, err := config.ForgeKubeconfigPath()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// No forge kubeconfig (workspace predates the feature, or cluster
		// not up yet) — silently fall back to the admin/default config.
		return ""
	}

	class := k8s.Classify(data)
	skew := tokenRefreshSkew
	if class == k8s.ClassCert {
		skew = certRefreshSkew
	}

	expiry, perr := k8s.MinExpiry(data)
	fresh := perr == nil && time.Until(expiry) > skew
	if fresh && !force {
		return path // still good — no network
	}

	if err := refreshKubeconfig(ctx, in, path, data, class); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not refresh kubeconfig (%v); using existing config\n", err)
		// If the credential is unexpired (just inside the skew), the stale
		// file still works — prefer it. If it's actually expired/unparseable,
		// fall back to the admin/default config.
		if perr == nil && time.Until(expiry) > 0 {
			return path
		}
		return ""
	}
	return path
}

// refreshKubeconfig re-mints the credential in the forge kubeconfig in
// place and writes it back atomically. Token-based (the common path) is
// cheap — a single IAM-token exchange, no cluster round-trip. Cert-based
// is expensive — re-fetch the admin kubeconfig — and only happens if the
// gate is ever pointed at an admin config.
func refreshKubeconfig(ctx context.Context, in *ClusterInputs, path string, data []byte, class k8s.KubeconfigClass) error {
	if in == nil || in.OpenIBMClient == nil {
		return fmt.Errorf("no IBM client available")
	}
	cctx, ic, err := in.OpenIBMClient()
	if err != nil {
		return err
	}

	switch class {
	case k8s.ClassCert:
		// Expensive path: re-fetch the admin kubeconfig.
		cluster := ClusterFromTFOutput(ctx, cctx)
		if cluster == "" && cctx != nil && cctx.Workspace != nil {
			cluster = cctx.Workspace.Cluster.Name
		}
		if cluster == "" {
			return fmt.Errorf("cannot resolve cluster to re-fetch admin kubeconfig")
		}
		body, ferr := ic.FetchClusterConfig(ctx, cluster)
		if ferr != nil {
			return ferr
		}
		return writeFileAtomic(path, body, 0o600)
	default:
		// Token (and unknown — try a token) path: cheap re-mint.
		token, terr := ic.IAMToken()
		if terr != nil {
			return terr
		}
		out, rerr := k8s.RewriteTokens(data, token)
		if rerr != nil {
			return rerr
		}
		return writeFileAtomic(path, out, 0o600)
	}
}

// The old preferForgeKubeconfig lived here: it ran the freshness gate and repointed
// KUBECONFIG at the SHARED forge kubeconfig. That substitution is what made two
// workspaces retarget each other (issue #55), so it now belongs to
// pinLocalKubeconfig in kubetarget.go, which prefers the WORKSPACE's own kubeconfig
// and falls back to the forge file only when there is no workspace-specific answer.
// The freshness gate itself is unchanged and still runs on both paths.

// setEnvKV returns env with key set to val: it replaces an existing
// key=... entry in place, or appends one if absent.
func setEnvKV(env []string, key, val string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		if len(kv) >= len(prefix) && kv[:len(prefix)] == prefix {
			out = append(out, prefix+val)
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, prefix+val)
	}
	return out
}

// writeFileAtomic writes data to path via a temp file in the same dir +
// os.Rename, so a concurrent reader never sees a half-written kubeconfig.
// Two simultaneous refreshes both produce a valid file; last rename wins
// (benign — both carry a valid fresh credential).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".kubeconfig-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
