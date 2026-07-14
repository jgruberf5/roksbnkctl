package k8s

import (
	"os"
	"strings"

	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"k8s.io/client-go/tools/clientcmd"
)

// ContextForCluster returns the context in a kubeconfig that addresses the
// ROKS cluster with the given id, or "" when the file cannot usefully talk
// to it. Callers pass the result to clientcmd as an explicit context
// override, so a file whose current-context selects a DIFFERENT cluster
// still gets addressed correctly instead of silently talking to the wrong
// one.
//
// Selection has to be both identity-aware and credential-aware, because
// IBM's kubeconfigs are neither as simple nor as consistent as they look:
//
//   - Identity is the 20-char cluster id, not the server URL: every cluster
//     in a region shares the API host (c100-e.<region>.containers.cloud.ibm.com)
//     and differs only by PORT. But only SOME entries carry the id in their
//     name — IBM writes a "<name>/<id>" cluster entry AND a host-derived one
//     ("c100-e-...-com:32394") for the same server.
//
//   - The id-named context is a decoy: IBM leaves its user EMPTY, so
//     selecting it authenticates as system:anonymous and every request is
//     forbidden. The credentialed context is the host-named one — whose
//     cluster entry does NOT carry the id.
//
// So: resolve the target cluster's SERVER from whichever entry does name the
// id, then take a context pointing at that server whose user actually has
// credentials. Within one file, server equality is a sound identity — the
// port disambiguates the cluster.
func ContextForCluster(data []byte, clusterID string) string {
	if clusterID == "" {
		return ""
	}
	cfg, err := clientcmd.Load(data)
	if err != nil {
		return ""
	}
	return contextForCluster(cfg, clusterID)
}

func contextForCluster(cfg *clientcmdapi.Config, clusterID string) string {
	// 1. Servers belonging to the target cluster (from the id-named entries).
	servers := map[string]bool{}
	for name, c := range cfg.Clusters {
		if c != nil && strings.Contains(name, clusterID) && c.Server != "" {
			servers[c.Server] = true
		}
	}
	if len(servers) == 0 {
		return ""
	}

	// 2. Contexts that point at one of those servers AND carry credentials.
	usable := func(ctxName string) bool {
		kctx := cfg.Contexts[ctxName]
		if kctx == nil {
			return false
		}
		c := cfg.Clusters[kctx.Cluster]
		if c == nil || !servers[c.Server] {
			return false
		}
		return hasCredentials(cfg.AuthInfos[kctx.AuthInfo])
	}

	// Prefer the file's own current-context when it is usable — that keeps us
	// byte-identical with what kubectl would do for the same file.
	if usable(cfg.CurrentContext) {
		return cfg.CurrentContext
	}
	for name := range cfg.Contexts {
		if usable(name) {
			return name
		}
	}
	return ""
}

// hasCredentials reports whether an authinfo can actually authenticate. An
// empty/absent user means system:anonymous — IBM writes exactly that for the
// id-named context, so this is what stops us from picking the decoy.
func hasCredentials(a *clientcmdapi.AuthInfo) bool {
	if a == nil {
		return false
	}
	return a.Token != "" ||
		a.TokenFile != "" ||
		a.Username != "" ||
		a.Password != "" ||
		len(a.ClientCertificateData) > 0 ||
		a.ClientCertificate != "" ||
		len(a.ClientKeyData) > 0 ||
		a.ClientKey != "" ||
		a.AuthProvider != nil ||
		a.Exec != nil
}

// CanAddressCluster reports whether a kubeconfig can authenticate to the
// given cluster — i.e. ContextForCluster finds a usable context.
func CanAddressCluster(data []byte, clusterID string) bool {
	return ContextForCluster(data, clusterID) != ""
}

// KubeconfigCanAddressCluster is CanAddressCluster over a file path. A
// missing or unreadable file is simply "no" — callers walk a candidate list.
func KubeconfigCanAddressCluster(path, clusterID string) bool {
	if path == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return CanAddressCluster(data, clusterID)
}
