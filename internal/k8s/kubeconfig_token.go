package k8s

import (
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// BuildTokenKubeconfig produces a portable, token-based kubeconfig from an
// admin (cert-based) kubeconfig. It keeps the cluster's public `server` URL
// and embedded `certificate-authority-data`, and replaces the user with a
// single token user named userName carrying the supplied IAM bearer token.
//
// The result is fully self-contained: NO file references, NO client
// certificate/key. This is the form BNK Forge registers from and refreshes
// (it re-mints the token from the project credential template), and the one
// ensureFreshKubeconfig refreshes locally.
//
// adminKubeconfig must already embed certificate-authority-data (the IBM
// admin fetch inlines it — see ibm.buildSelfContainedKubeconfig); a cluster
// with only a CA file ref is rejected, since the output would not be
// portable.
func BuildTokenKubeconfig(adminKubeconfig []byte, token, userName string) ([]byte, error) {
	if token == "" {
		return nil, errors.New("token is empty")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(adminKubeconfig, &doc); err != nil {
		return nil, fmt.Errorf("parsing admin kubeconfig: %w", err)
	}

	clusters, _ := doc["clusters"].([]any)
	if len(clusters) == 0 {
		return nil, errors.New("admin kubeconfig has no clusters")
	}
	c0, _ := clusters[0].(map[string]any)
	if c0 == nil {
		return nil, errors.New("malformed cluster entry")
	}
	clusterName, _ := c0["name"].(string)
	if clusterName == "" {
		clusterName = "cluster"
	}
	inner, _ := c0["cluster"].(map[string]any)
	if inner == nil {
		return nil, errors.New("cluster entry has no `cluster` block")
	}
	server, _ := inner["server"].(string)
	if server == "" {
		return nil, errors.New("cluster has no server URL")
	}
	caData, _ := inner["certificate-authority-data"].(string)
	if caData == "" {
		return nil, errors.New("cluster has no certificate-authority-data (admin kubeconfig is not self-contained)")
	}

	if userName == "" {
		userName = clusterName + "-token"
	}
	// Preserve the current context's namespace if the admin config set one.
	ns := currentContextNamespace(doc)
	if ns == "" {
		ns = "default"
	}

	out := map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []any{
			map[string]any{
				"name": clusterName,
				"cluster": map[string]any{
					"server":                     server,
					"certificate-authority-data": caData,
				},
			},
		},
		"contexts": []any{
			map[string]any{
				"name": clusterName,
				"context": map[string]any{
					"cluster":   clusterName,
					"user":      userName,
					"namespace": ns,
				},
			},
		},
		"current-context": clusterName,
		"users": []any{
			map[string]any{
				"name": userName,
				"user": map[string]any{"token": token},
			},
		},
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("emitting token kubeconfig: %w", err)
	}
	return b, nil
}

// RewriteTokens replaces the bearer token on every token-bearing user in a
// kubeconfig, leaving clusters[].cluster (server + CA) and everything else
// untouched. Used by the refresh gate: only the credential changes. Returns
// an error if there are no token users to refresh (so the caller can fall
// back rather than silently no-op).
func RewriteTokens(kubeconfig []byte, token string) ([]byte, error) {
	if token == "" {
		return nil, errors.New("token is empty")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return nil, fmt.Errorf("parsing kubeconfig: %w", err)
	}
	users, _ := doc["users"].([]any)
	n := 0
	for _, u := range users {
		um, _ := u.(map[string]any)
		if um == nil {
			continue
		}
		inner, _ := um["user"].(map[string]any)
		if inner == nil {
			continue
		}
		if _, ok := inner["token"]; ok {
			inner["token"] = token
			n++
		}
	}
	if n == 0 {
		return nil, errors.New("kubeconfig has no token users to refresh")
	}
	b, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("re-emitting kubeconfig: %w", err)
	}
	return b, nil
}

// currentContextNamespace returns the namespace of the kubeconfig's
// current-context, or "" if none is set / resolvable.
func currentContextNamespace(doc map[string]any) string {
	cur, _ := doc["current-context"].(string)
	if cur == "" {
		return ""
	}
	contexts, _ := doc["contexts"].([]any)
	for _, c := range contexts {
		cm, _ := c.(map[string]any)
		if cm == nil {
			continue
		}
		if name, _ := cm["name"].(string); name != cur {
			continue
		}
		inner, _ := cm["context"].(map[string]any)
		if inner == nil {
			return ""
		}
		ns, _ := inner["namespace"].(string)
		return ns
	}
	return ""
}
